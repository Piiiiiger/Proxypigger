// Package agent is pigger-agent: it runs Xray for one panel node, applies the
// config the panel sends and reports traffic, online clients and load back.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
	"github.com/mhsanaei/3x-ui/v3/internal/agentproto"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/snell"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"github.com/gorilla/websocket"
)

const (
	defaultInterval = 5 * time.Second
	// resendAfter: a report still unacked this long after sending is sent again.
	resendAfter = 15 * time.Second
	// readTimeout outlasts several of the panel's 20s pings.
	readTimeout  = 90 * time.Second
	writeTimeout = 10 * time.Second
	maxBackoff   = 30 * time.Second
	maxMessage   = 64 << 20
)

var errNotConnected = errors.New("not connected to the panel")

type Agent struct {
	cfg      Config
	version  string
	interval time.Duration

	snell    *snellManager
	applied  savedConfig
	core     *xrayCore
	outbox   *outbox
	activity *activity
	abuse    *abuseWatch
	sampler  hostSampler

	// statsMu orders stats polls with core restarts, so counters are read
	// before a core goes away and counted from zero on the next one.
	statsMu sync.Mutex
	stats   *xray.XrayAPI
	ips     map[string][]agentproto.IPEntry

	connMu sync.Mutex
	conn   *websocket.Conn
	sent   sentReport

	writeMu sync.Mutex
}

type sentReport struct {
	instance string
	seq      int64
	at       time.Time
}

type savedConfig struct {
	Hash   string          `json:"hash"`
	Config json.RawMessage `json:"config"`
}

func New(cfg Config, version string) (*Agent, error) {
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, err
	}
	box, err := openOutbox(filepath.Join(cfg.StateDir, "outbox.json"))
	if err != nil {
		return nil, err
	}
	a := &Agent{
		cfg:      cfg,
		version:  version,
		interval: defaultInterval,
		core:     newXrayCore(),
		snell:    newSnellManager(cfg.StateDir),
		outbox:   box,
		activity: newActivity(onlineGrace),
		abuse:    newAbuseWatch(),
	}
	if cfg.AccessAuditLog != "" {
		audit, err := newAccessAudit(cfg.AccessAuditLog)
		if err != nil {
			// An audit disk/path failure must not stop proxy service.
			logger.Warning("agent: access audit unavailable:", err)
		} else {
			a.abuse.audit = audit
		}
	}
	a.core.onStarted = a.abuse.attach
	return a, nil
}

// Run serves until ctx ends, then queues the usage counted since the last poll
// so the next start reports it.
func (a *Agent) Run(ctx context.Context) {
	a.restoreConfig()
	var wg sync.WaitGroup
	wg.Go(func() { a.pollLoop(ctx) })
	a.connectLoop(ctx)
	wg.Wait()

	a.statsMu.Lock()
	a.pollLocked(time.Now())
	a.closeStatsLocked()
	a.statsMu.Unlock()
	a.core.stop()
	if a.abuse.audit != nil {
		a.abuse.audit.close()
	}
}

func (a *Agent) savedConfigPath() string { return filepath.Join(a.cfg.StateDir, "xray.json") }

// restoreConfig starts the last config the panel sent, so users keep working
// while the panel is unreachable.
func (a *Agent) restoreConfig() {
	raw, err := os.ReadFile(a.savedConfigPath())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.Warning("agent: reading the saved config failed:", err)
		}
		return
	}
	var saved savedConfig
	if err := json.Unmarshal(raw, &saved); err != nil || len(saved.Config) == 0 {
		logger.Warning("agent: the saved config is unreadable, waiting for the panel:", err)
		return
	}
	if result := a.applyConfig(agentproto.Apply{Config: saved.Config, Hash: saved.Hash}); !result.OK {
		logger.Warning("agent: saved config failed:", result.Error)
	}
}

func (a *Agent) applyConfig(apply agentproto.Apply) agentproto.Result {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	withoutAbuse, abuseRules, err := abuse.SplitConfig(apply.Config)
	if err != nil {
		return agentproto.Result{Error: err.Error(), ConfigHash: a.applied.Hash}
	}
	coreRaw, instances, err := snell.Split(withoutAbuse)
	if err != nil {
		return agentproto.Result{Error: err.Error(), ConfigHash: a.applied.Hash}
	}
	if err := a.snell.validate(instances); err != nil {
		return agentproto.Result{Error: err.Error(), ConfigHash: a.applied.Hash}
	}
	a.pollLocked(time.Now())
	previous := a.applied
	restarted, err := a.core.apply(coreRaw, apply.Hash, apply.RestartToDropUsers)
	if restarted {
		a.resetStatsLocked()
	}
	if err != nil {
		return agentproto.Result{Error: err.Error(), ConfigHash: previous.Hash, Restarted: restarted}
	}
	rollback := func(cause error, restoreSnell bool) agentproto.Result {
		previousCore, _, _ := abuse.SplitConfig(previous.Config)
		oldCore, oldInstances, splitErr := snell.Split(previousCore)
		if len(previous.Config) > 0 && splitErr == nil {
			if _, restoreErr := a.core.apply(oldCore, previous.Hash, false); restoreErr != nil {
				cause = fmt.Errorf("%w; Xray rollback failed: %w", cause, restoreErr)
			}
			a.resetStatsLocked()
		} else if len(previous.Config) == 0 {
			a.closeStatsLocked()
			a.core.stop()
		}
		if restoreSnell {
			if restoreErr := a.snell.apply(oldInstances); restoreErr != nil {
				cause = fmt.Errorf("%w; Snell rollback failed: %w", cause, restoreErr)
			}
		}
		return agentproto.Result{Error: cause.Error(), ConfigHash: previous.Hash, Restarted: restarted}
	}
	if err := a.snell.apply(instances); err != nil {
		return rollback(err, false)
	}
	saved := savedConfig{Hash: apply.Hash, Config: apply.Config}
	encoded, err := json.Marshal(saved)
	if err == nil {
		err = writeFileAtomic(a.savedConfigPath(), encoded, 0o600)
	}
	if err != nil {
		return rollback(fmt.Errorf("cannot persist applied configuration"), true)
	}
	a.applied = saved
	a.abuse.configure(abuseRules)
	return agentproto.Result{OK: true, ConfigHash: apply.Hash, Restarted: restarted}
}

func (a *Agent) restartCore() agentproto.Result {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	a.pollLocked(time.Now())
	err := a.core.restart()
	a.resetStatsLocked()
	if err != nil {
		return agentproto.Result{Error: err.Error()}
	}
	for _, id := range snellIDs(a.snell.known) {
		if err := a.snell.start(a.snell.known[id]); err != nil {
			return agentproto.Result{Error: err.Error()}
		}
	}
	return agentproto.Result{OK: true, ConfigHash: a.core.state().Hash, Restarted: true}
}

// resetStatsLocked binds the stats reader to the core now running, which starts
// with every counter at zero.
func (a *Agent) resetStatsLocked() {
	a.closeStatsLocked()
	port := a.core.state().APIPort
	if port <= 0 {
		return
	}
	api := &xray.XrayAPI{}
	if err := api.Init(port); err != nil {
		logger.Warning("agent: connecting to the core API failed:", err)
		return
	}
	api.CountFromZero()
	a.stats = api
}

func (a *Agent) closeStatsLocked() {
	if a.stats != nil {
		a.stats.Close()
		a.stats = nil
	}
}

// pollLocked moves the core's traffic since the last poll into the outbox and
// notes who is active and from where.
func (a *Agent) pollLocked(now time.Time) {
	var inbounds, users []agentproto.Counter
	var tags, emails []string
	for _, c := range a.snell.traffic() {
		inbounds = append(inbounds, c)
		tags = append(tags, c.Name)
	}
	if a.stats == nil {
		a.outbox.add(inbounds, nil)
		return
	}
	traffics, clients, err := a.stats.GetTraffic()
	if err != nil {
		logger.Debug("agent: reading traffic failed:", err)
		a.outbox.add(inbounds, nil)
		return
	}
	for _, t := range traffics {
		if t == nil || !t.IsInbound || t.Up+t.Down <= 0 {
			continue
		}
		inbounds = append(inbounds, agentproto.Counter{Name: t.Tag, Up: t.Up, Down: t.Down})
		tags = append(tags, t.Tag)
	}
	for _, c := range clients {
		if c == nil || c.Up+c.Down <= 0 {
			continue
		}
		users = append(users, agentproto.Counter{Name: c.Email, Up: c.Up, Down: c.Down})
		emails = append(emails, c.Email)
		a.abuse.traffic(c.Email, c.Up+c.Down, now)
	}
	a.outbox.add(inbounds, users)

	ips := map[string][]agentproto.IPEntry{}
	if online, err := a.stats.GetOnlineUsers(); err == nil {
		for _, u := range online {
			emails = append(emails, u.Email)
			for _, ip := range u.IPs {
				ips[u.Email] = append(ips[u.Email], agentproto.IPEntry{IP: ip.IP, Timestamp: ip.LastSeen})
			}
		}
	}
	a.activity.observe(now, emails, tags)
	a.ips = ips
}

func (a *Agent) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.statsMu.Lock()
			a.pollLocked(now)
			status := a.statusLocked(now)
			a.statsMu.Unlock()
			a.outbox.addSignals(a.abuse.collect(now))
			if a.send(agentproto.Message{Type: agentproto.TypeStatus, Status: &status}) == nil {
				a.sendTraffic(false)
			}
		}
	}
}

func (a *Agent) statusLocked(now time.Time) agentproto.Status {
	st := agentproto.Status{XrayVersion: xrayVersion()}
	a.sampler.sample(&st)
	cs := a.core.state()
	st.ConfigHash = a.applied.Hash
	st.XrayError = cs.Err
	switch {
	case cs.Running:
		st.XrayState = "running"
	case cs.Err != "":
		st.XrayState = "error"
	default:
		st.XrayState = "stop"
	}
	st.Online, st.ActiveInbounds = a.activity.current(now)
	st.IPs = a.ips
	st.Snell = a.snell.statuses(now)
	return st
}

// sendTraffic sends the outbox's report when it is new to this connection, or
// when its ack is overdue.
func (a *Agent) sendTraffic(force bool) {
	r := a.outbox.next()
	if r == nil {
		return
	}
	a.connMu.Lock()
	same := a.sent.instance == r.Instance && a.sent.seq == r.Seq
	if same && !force && time.Since(a.sent.at) < resendAfter {
		a.connMu.Unlock()
		return
	}
	a.sent = sentReport{instance: r.Instance, seq: r.Seq, at: time.Now()}
	a.connMu.Unlock()
	_ = a.send(agentproto.Message{Type: agentproto.TypeTraffic, Traffic: r})
}

func (a *Agent) send(msg agentproto.Message) error {
	a.connMu.Lock()
	conn := a.conn
	a.connMu.Unlock()
	if conn == nil {
		return errNotConnected
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return conn.WriteJSON(msg)
}

func (a *Agent) setConn(conn *websocket.Conn) {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	a.conn = conn
}

func (a *Agent) connectLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		logger.Warning("agent: lost the panel, reconnecting:", err)
		wait := backoff + rand.N(backoff/2+1)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (a *Agent) session(ctx context.Context) error {
	target, err := a.cfg.connectURL()
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	header := http.Header{"Authorization": []string{"Bearer " + a.cfg.Secret}}
	conn, resp, err := dialer.DialContext(ctx, target, header)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return errors.New("the panel refused this server's secret; mint a new one for its node")
		}
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	conn.SetReadLimit(maxMessage)
	extend := func() { _ = conn.SetReadDeadline(time.Now().Add(readTimeout)) }
	extend()
	conn.SetPingHandler(func(data string) error {
		extend()
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(writeTimeout))
	})
	a.setConn(conn)
	defer a.setConn(nil)

	a.statsMu.Lock()
	hello := agentproto.Hello{AgentVersion: a.version, XrayVersion: xrayVersion(), ConfigHash: a.applied.Hash}
	a.statsMu.Unlock()
	hello.Capabilities = []string{agentproto.ProbeCapability, abuse.Capability}
	if a.snell.ready {
		hello.Capabilities = append(hello.Capabilities, snell.Capability)
	}
	if err := a.send(agentproto.Message{Type: agentproto.TypeHello, Hello: &hello}); err != nil {
		return err
	}
	logger.Info("agent: connected to the panel")
	a.sendTraffic(true)
	for {
		var msg agentproto.Message
		if err := conn.ReadJSON(&msg); err != nil {
			return err
		}
		extend()
		a.handle(msg)
	}
}

func (a *Agent) handle(msg agentproto.Message) {
	switch msg.Type {
	case agentproto.TypeProbe:
		res := probeTCP(msg.Probe)
		_ = a.send(agentproto.Message{Type: agentproto.TypeResult, ID: msg.ID, Result: &res})
	case agentproto.TypeApply:
		if msg.Apply == nil {
			return
		}
		res := a.applyConfig(*msg.Apply)
		_ = a.send(agentproto.Message{Type: agentproto.TypeResult, ID: msg.ID, Result: &res})
	case agentproto.TypeRestart:
		res := a.restartCore()
		_ = a.send(agentproto.Message{Type: agentproto.TypeResult, ID: msg.ID, Result: &res})
	case agentproto.TypeAck:
		if msg.Ack == nil {
			return
		}
		a.outbox.ack(msg.Ack.Instance, msg.Ack.Seq)
		a.sendTraffic(false)
	}
}

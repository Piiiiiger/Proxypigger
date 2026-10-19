package agent

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	xlog "github.com/xtls/xray-core/common/log"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

const (
	auditFileBytes = 8 << 20
	auditBackups   = 7
)

// accessAudit is opt-in evidence for periodic review, independent of rule matches.
// The core never waits for disk: excess records are dropped and counted in health.
type accessAudit struct {
	path     string
	queue    chan auditRecord
	stop     chan struct{}
	done     chan struct{}
	dropped  atomic.Uint64
	errors   uint64 // writer goroutine only
	started  time.Time
	file     *os.File
	size     int64
	maxBytes int64
}

type auditRecord struct {
	At          time.Time `json:"at"`
	User        string    `json:"user"`
	SourceIP    string    `json:"source_ip,omitempty"`
	Destination string    `json:"destination"`
	Port        int       `json:"port"`
	Protocol    string    `json:"protocol"`
	Status      string    `json:"status"`
	Outbound    string    `json:"outbound"`
}

func newAccessAudit(path string) (*accessAudit, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("access audit path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	a := &accessAudit{path: path, queue: make(chan auditRecord, 4096), stop: make(chan struct{}), done: make(chan struct{}), started: time.Now().UTC(), maxBytes: auditFileBytes}
	if err := a.open(); err != nil {
		return nil, err
	}
	go a.run()
	return a, nil
}

func (a *accessAudit) open() error {
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return fmt.Errorf("access audit log must be a regular file")
	}
	a.file, a.size = f, info.Size()
	return nil
}

func (a *accessAudit) offer(e abuse.Event, m *xlog.AccessMessage) {
	if abuse.IsChainSocksHop(e.Tag) {
		return
	}
	protocol := "tcp"
	if e.UDP {
		protocol = "udp"
	}
	source := fmt.Sprint(m.From)
	for _, prefix := range []string{"tcp:", "udp:"} {
		source = strings.TrimPrefix(source, prefix)
	}
	host, _, err := net.SplitHostPort(source)
	if err != nil || net.ParseIP(host) == nil {
		host = ""
	}
	record := auditRecord{At: e.At.UTC(), User: e.Email, SourceIP: host, Destination: e.Host, Port: e.Port, Protocol: protocol, Status: "accepted", Outbound: e.Tag}
	select {
	case a.queue <- record:
	default:
		a.dropped.Add(1)
	}
}

func (a *accessAudit) write(record auditRecord) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > 16<<10 {
		return fmt.Errorf("oversized access audit record")
	}
	if a.file == nil {
		if err := a.open(); err != nil {
			return err
		}
	}
	if a.size+int64(len(raw)) > a.maxBytes {
		_ = a.file.Close()
		a.file = nil
		if err := os.Remove(fmt.Sprintf("%s.%d", a.path, auditBackups)); err != nil && !os.IsNotExist(err) {
			return err
		}
		for i := auditBackups - 1; i >= 1; i-- {
			if err := os.Rename(fmt.Sprintf("%s.%d", a.path, i), fmt.Sprintf("%s.%d", a.path, i+1)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := os.Rename(a.path, a.path+".1"); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := a.open(); err != nil {
			return err
		}
	}
	n, err := a.file.Write(raw)
	a.size += int64(n)
	return err
}

func (a *accessAudit) health() {
	raw, _ := json.Marshal(map[string]any{
		"started_at": a.started, "updated_at": time.Now().UTC(), "dropped_records": a.dropped.Load(),
		"write_errors": a.errors, "accepted_only": true, "max_retained_bytes": (auditBackups + 1) * a.maxBytes,
	})
	if err := writeFileAtomic(a.path+".health.json", raw, 0o640); err != nil {
		logger.Warning("agent: cannot write access audit health:", err)
	}
}

func (a *accessAudit) run() {
	defer close(a.done)
	defer func() {
		if a.file != nil {
			_ = a.file.Close()
		}
	}()
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	a.health()
	write := func(record auditRecord) {
		if err := a.write(record); err != nil {
			a.errors++
			if a.errors == 1 {
				logger.Warning("agent: access audit is losing records:", err)
			}
		}
	}
	for {
		select {
		case record := <-a.queue:
			write(record)
		case <-timer.C:
			a.health()
		case <-a.stop:
			// The core has stopped; drain what was queued, then record final health.
			for {
				select {
				case record := <-a.queue:
					write(record)
				default:
					a.health()
					return
				}
			}
		}
	}
}

func (a *accessAudit) close() {
	close(a.stop)
	<-a.done
}

package agent

import (
	"sync/atomic"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"

	applog "github.com/xtls/xray-core/app/log"
	xlog "github.com/xtls/xray-core/common/log"
	xnet "github.com/xtls/xray-core/common/net"
	xcore "github.com/xtls/xray-core/core"
)

// abuseQueue bounds the connections waiting for the detector: past it they are
// dropped and counted, never held up in front of a user's traffic.
const abuseQueue = 4096

// abuseRun is detection while it is on: its detector and the queue feeding it.
type abuseRun struct {
	detector *abuse.Detector
	events   chan abuse.Event
	stop     chan struct{}
}

// abuseWatch feeds the in-memory detector while detection is enabled, and the
// optional audit writer independently, from Xray's own connection records.
type abuseWatch struct {
	run     atomic.Pointer[abuseRun]
	dropped atomic.Int64
	audit   *accessAudit // immutable after Agent.New; independent of fixed-rule mode
}

func newAbuseWatch() *abuseWatch { return &abuseWatch{} }

// configure turns detection on with rules, retunes it, or turns it off (nil).
func (w *abuseWatch) configure(rules *abuse.Rules) {
	current := w.run.Load()
	switch {
	case rules == nil && current != nil:
		w.run.Store(nil)
		close(current.stop)
	case rules != nil && current == nil:
		r := &abuseRun{detector: abuse.NewDetector(*rules), events: make(chan abuse.Event, abuseQueue), stop: make(chan struct{})}
		go r.consume()
		w.run.Store(r)
	case rules != nil:
		current.detector.SetRules(*rules)
	}
}

func (r *abuseRun) consume() {
	for {
		select {
		case <-r.stop:
			return
		case e := <-r.events:
			r.detector.Observe(e)
		}
	}
}

// attach puts the watch between Xray and its own logger. Each new core
// registers its logger anew, so this runs after every start.
func (w *abuseWatch) attach(instance *xcore.Instance) {
	inner, ok := instance.GetFeature((*applog.Instance)(nil)).(xlog.Handler)
	if !ok {
		logger.Warning("agent: the core has no logger to watch; abuse detection sees no connections")
		return
	}
	xlog.RegisterHandler(&accessTee{next: inner, watch: w})
}

// offer queues one access record for the detector, if detection is on.
func (w *abuseWatch) offer(m *xlog.AccessMessage) {
	r := w.run.Load()
	if r == nil && w.audit == nil {
		return
	}
	e, ok := accessEvent(m, time.Now())
	if !ok {
		return
	}
	if w.audit != nil {
		w.audit.offer(e, m)
	}
	if r == nil {
		return
	}
	select {
	case r.events <- e:
	default:
		w.dropped.Add(1)
	}
}

func (w *abuseWatch) traffic(email string, bytes int64, at time.Time) {
	if r := w.run.Load(); r != nil {
		r.detector.ObserveTraffic(email, bytes, at)
	}
}

func (w *abuseWatch) collect(now time.Time) []abuse.Signal {
	r := w.run.Load()
	if r == nil {
		return nil
	}
	if n := w.dropped.Swap(0); n > 0 {
		logger.Warningf("agent: abuse detection skipped %d connections it could not keep up with", n)
	}
	return r.detector.Collect(now)
}

// accessTee hands every access record to the watch, then to Xray's logger.
type accessTee struct {
	next  xlog.Handler
	watch *abuseWatch
}

func (t *accessTee) Handle(msg xlog.Message) {
	if m, ok := msg.(*xlog.AccessMessage); ok {
		t.watch.offer(m)
	}
	t.next.Handle(msg)
}

func accessEvent(m *xlog.AccessMessage, at time.Time) (abuse.Event, bool) {
	if m.Status != xlog.AccessAccepted || m.Email == "" {
		return abuse.Event{}, false
	}
	var dest xnet.Destination
	switch to := m.To.(type) {
	case xnet.Destination:
		dest = to
	case *xnet.Destination:
		if to == nil {
			return abuse.Event{}, false
		}
		dest = *to
	default:
		return abuse.Event{}, false
	}
	if dest.Address == nil {
		return abuse.Event{}, false
	}
	var host string
	if dest.Address.Family().IsDomain() {
		host = dest.Address.Domain()
	} else {
		host = dest.Address.IP().String()
	}
	return abuse.NewEvent(at, m.Email, host, int(dest.Port), dest.Network == xnet.Network_UDP, m.Detour), true
}

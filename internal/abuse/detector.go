package abuse

import (
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Event is one connection a server routed for an account.
type Event struct {
	At    time.Time
	Email string
	Host  string
	IP    bool
	Port  int
	UDP   bool
	Tag   string
}

// NewEvent builds an Event from what Xray records: the destination's host and
// port, and the detour whose last part names the outbound that took it.
func NewEvent(at time.Time, email, host string, port int, udp bool, detour string) Event {
	return Event{At: at, Email: email, Host: host, IP: net.ParseIP(host) != nil, Port: port, UDP: udp, Tag: OutboundTag(detour)}
}

// OutboundTag reads the outbound out of a detour such as "in >> out".
func OutboundTag(detour string) string {
	for _, sep := range []string{" >> ", " -> ", " ==> "} {
		if i := strings.LastIndex(detour, sep); i >= 0 {
			return detour[i+len(sep):]
		}
	}
	return detour
}

// ParseAccessLine reads one line of Xray's access log, the way the panel's own
// core writes it; ok is false for any other line.
func ParseAccessLine(line string, at time.Time) (Event, bool) {
	_, rest, ok := strings.Cut(line, " accepted ")
	if !ok {
		return Event{}, false
	}
	to, rest, _ := strings.Cut(rest, " ")
	detour := ""
	if strings.HasPrefix(rest, "[") {
		if end := strings.Index(rest, "]"); end > 0 {
			detour, rest = rest[1:end], rest[end+1:]
		}
	}
	_, email, ok := strings.Cut(rest, "email: ")
	if !ok {
		return Event{}, false
	}
	network, hostport, _ := strings.Cut(to, ":")
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return Event{}, false
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return Event{}, false
	}
	return NewEvent(at, strings.TrimSpace(email), host, p, network == "udp", detour), true
}

// mapCap bounds each minute's sets: past it a minute still counts connections
// but stops remembering new destinations, far above any threshold.
const mapCap = 4096

// idleForget is how long an account with nothing pending stays remembered.
const idleForget = time.Hour

type bucket struct {
	minute    int64
	conns     int64
	smtp      int64
	bt        int64
	ips       map[string]struct{}
	sensitive map[string]struct{}
	hosts     map[string]struct{}
	ports     map[string]map[int]struct{}
	dests     map[string]int64
}

// speedStreak counts 5-minute blocks at full speed in a row; the block being
// filled counts once it closes.
type speedStreak struct {
	block    int64
	bytes    int64
	lastFull int64
	blocks   int
	warned   bool
	struck   bool
}

// busyMinutes are the minutes of the last day in which an account opened
// connections to one platform's sign-in server, oldest first.
type busyMinutes []int64

type account struct {
	buckets  [ringMinutes]bucket
	fired    map[string]int64
	tests    []int64
	lastTest int64
	signIns  map[string]busyMinutes
	speed    speedStreak
	lastSeen int64
}

// Detector keeps rolling per-account counters for one server and turns them
// into signals; it is safe for the connection hook and the reporter at once.
type Detector struct {
	mu       sync.Mutex
	rules    Rules
	accounts map[string]*account
}

func NewDetector(rules Rules) *Detector {
	return &Detector{rules: rules.Normalized(), accounts: map[string]*account{}}
}

func (d *Detector) SetRules(rules Rules) {
	d.mu.Lock()
	d.rules = rules.Normalized()
	d.mu.Unlock()
}

func (d *Detector) account(email string) *account {
	a := d.accounts[email]
	if a == nil {
		a = &account{fired: map[string]int64{}, signIns: map[string]busyMinutes{}}
		d.accounts[email] = a
	}
	return a
}

func (a *account) bucket(minute int64) *bucket {
	b := &a.buckets[minute%ringMinutes]
	if b.minute != minute {
		*b = bucket{minute: minute}
	}
	return b
}

// window returns the buckets of the n minutes ending at minute.
func (a *account) window(minute int64, n int) []*bucket {
	out := make([]*bucket, 0, n)
	for m := minute - int64(n) + 1; m <= minute; m++ {
		if m < 0 {
			continue
		}
		if b := &a.buckets[m%ringMinutes]; b.minute == m {
			out = append(out, b)
		}
	}
	return out
}

// ready lets one check fire once per cooldown, so a burst is one signal.
func (a *account) ready(check string, sec int64, cooldown int) bool {
	if last, ok := a.fired[check]; ok && sec-last < int64(cooldown) {
		return false
	}
	a.fired[check] = sec
	return true
}

func remember(set map[string]struct{}, key string) map[string]struct{} {
	if set == nil {
		set = map[string]struct{}{}
	}
	if len(set) < mapCap {
		set[key] = struct{}{}
	}
	return set
}

// notOwnTraffic are the outbounds whose connections Observe leaves out.
var notOwnTraffic = map[string]bool{
	TagBlock: true, TagIPLimitBlock: true, TagChainTransitDirect: true, TagChainTransitBlock: true,
}

// Observe counts one connection.
func (d *Detector) Observe(e Event) {
	if e.Email == "" || notOwnTraffic[e.Tag] || IsChainSocksHop(e.Tag) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	a := d.account(e.Email)
	sec := e.At.Unix()
	a.lastSeen = max(a.lastSeen, sec)
	b := a.bucket(sec / 60)
	b.conns++
	if e.Port == 25 || e.Tag == TagSMTP {
		b.smtp++
	}
	if e.Tag == TagBT {
		b.bt++
	}
	if e.Tag == TagSpeedTest {
		if a.lastTest == 0 || sec-a.lastTest > int64(d.rules.SpeedTestGapMin)*60 {
			a.tests = append(a.tests, sec)
		}
		a.lastTest = sec
	}
	if platform := signInServers[e.Host]; platform != "" {
		busy := a.signIns[platform]
		if n := len(busy); n == 0 || sec/60 > busy[n-1] {
			a.signIns[platform] = append(busy, sec/60)
		}
	}
	dest := net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
	if b.dests == nil {
		b.dests = map[string]int64{}
	}
	if _, ok := b.dests[dest]; ok || len(b.dests) < mapCap {
		b.dests[dest]++
	}
	b.hosts = remember(b.hosts, e.Host)
	if e.IP {
		b.ips = remember(b.ips, e.Host)
		if sensitivePorts[e.Port] {
			b.sensitive = remember(b.sensitive, e.Host)
		}
		if b.ports == nil {
			b.ports = map[string]map[int]struct{}{}
		}
		if ports := b.ports[e.Host]; ports != nil || len(b.ports) < mapCap {
			if ports == nil {
				ports = map[int]struct{}{}
				b.ports[e.Host] = ports
			}
			ports[e.Port] = struct{}{}
		}
	}
}

// ObserveTraffic counts bytes an account moved, as the traffic reports give them.
func (d *Detector) ObserveTraffic(email string, bytes int64, at time.Time) {
	if email == "" || bytes <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	a := d.account(email)
	sec := at.Unix()
	a.lastSeen = max(a.lastSeen, sec)
	s := &a.speed
	if block := sec / 300; block != s.block {
		full := int64(d.rules.FullSpeedMbps) * 1_000_000 / 8 * 300
		switch {
		case s.block == 0:
		case full > 0 && s.bytes >= full:
			// A block with no traffic never closes, so only the previous one
			// continues a run; after any gap a new run starts and may warn again.
			if s.lastFull == s.block-1 {
				s.blocks++
			} else {
				s.blocks, s.warned, s.struck = 1, false, false
			}
			s.lastFull = s.block
		default:
			s.blocks, s.warned, s.struck = 0, false, false
		}
		s.block, s.bytes = block, 0
	}
	s.bytes += bytes
}

// Collect checks every account against the rules and returns what tripped.
func (d *Detector) Collect(now time.Time) []Signal {
	d.mu.Lock()
	defer d.mu.Unlock()
	sec := now.Unix()
	var out []Signal
	for email, a := range d.accounts {
		day := sec - 24*3600
		for len(a.tests) > 0 && a.tests[0] <= day {
			a.tests = a.tests[1:]
		}
		for platform, busy := range a.signIns {
			for len(busy) > 0 && busy[0] <= day/60 {
				busy = busy[1:]
			}
			if len(busy) == 0 {
				delete(a.signIns, platform)
			} else {
				a.signIns[platform] = busy
			}
		}
		if sec-a.lastSeen > int64(idleForget/time.Second) && len(a.tests) == 0 && len(a.signIns) == 0 && a.speed.blocks == 0 {
			delete(d.accounts, email)
			continue
		}
		for _, s := range d.check(a, sec) {
			s.Email, s.At = email, sec
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Email != out[j].Email {
			return out[i].Email < out[j].Email
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

func (d *Detector) check(a *account, sec int64) []Signal {
	r := d.rules
	minute := sec / 60
	var out []Signal

	if r.SpamAttempts > 0 {
		var n int64
		window := a.window(minute, r.SpamWindowMin)
		for _, b := range window {
			n += b.smtp
		}
		if n >= int64(r.SpamAttempts) && a.ready(RuleSpam, sec, r.SpamWindowMin*60) {
			out = append(out, Signal{
				Rule: RuleSpam, Level: LevelStrike, Measure: MeasureAttempts, Count: n,
				Limit: int64(r.SpamAttempts), Window: r.SpamWindowMin * 60, Samples: destsOnPort(window, 25),
			})
		}
	}

	if r.BTAttempts > 0 {
		var n int64
		for _, b := range a.window(minute, r.BTWindowMin) {
			n += b.bt
		}
		if n >= int64(r.BTAttempts) && a.ready(RuleBT, sec, r.BTWindowMin*60) {
			out = append(out, Signal{
				Rule: RuleBT, Level: LevelStrike, Measure: MeasureAttempts, Count: n,
				Limit: int64(r.BTAttempts), Window: r.BTWindowMin * 60,
			})
		}
	}

	if s, ok := d.checkScan(a, sec); ok {
		out = append(out, s)
	}

	if r.FloodPerDest > 0 || r.FloodTotal > 0 {
		for _, b := range a.window(minute, 2) {
			dest, top := topCount(b.dests)
			var s Signal
			switch {
			case r.FloodPerDest > 0 && top >= int64(r.FloodPerDest):
				s = Signal{Measure: MeasurePerDest, Count: top, Limit: int64(r.FloodPerDest), Samples: []string{dest}}
			case r.FloodTotal > 0 && b.conns >= int64(r.FloodTotal):
				s = Signal{Measure: MeasureConns, Count: b.conns, Limit: int64(r.FloodTotal)}
			default:
				continue
			}
			if a.ready(RuleFlood, sec, 60) {
				s.Rule, s.Level, s.Window = RuleFlood, LevelStrike, 60
				out = append(out, s)
			}
			break
		}
	}

	if s, ok := d.checkCrawler(a, sec); ok {
		out = append(out, s)
	}

	if r.SpeedTestsPerHour > 0 || r.SpeedTestsPerDay > 0 {
		var hour int64
		for _, t := range a.tests {
			if t > sec-3600 {
				hour++
			}
		}
		day := int64(len(a.tests))
		switch {
		case r.SpeedTestsPerHour > 0 && hour > int64(r.SpeedTestsPerHour) && a.ready(MeasureTestsHour, sec, 3600):
			out = append(out, Signal{
				Rule: RuleSpeedTest, Level: LevelStrike, Measure: MeasureTestsHour, Count: hour,
				Limit: int64(r.SpeedTestsPerHour), Window: 3600,
			})
		case r.SpeedTestsPerDay > 0 && day > int64(r.SpeedTestsPerDay) && a.ready(MeasureTestsDay, sec, 24*3600):
			out = append(out, Signal{
				Rule: RuleSpeedTest, Level: LevelStrike, Measure: MeasureTestsDay, Count: day,
				Limit: int64(r.SpeedTestsPerDay), Window: 24 * 3600,
			})
		}
	}

	out = append(out, d.checkRegister(a, sec)...)

	if r.FullSpeedMbps > 0 {
		minutes := int64(a.speed.blocks) * 5
		switch {
		case r.FullSpeedStrikeMin > 0 && minutes >= int64(r.FullSpeedStrikeMin) && !a.speed.struck:
			a.speed.struck, a.speed.warned = true, true
			out = append(out, Signal{
				Rule: RuleFullSpeed, Level: LevelStrike, Measure: MeasureMinutes, Count: minutes,
				Limit: int64(r.FullSpeedStrikeMin), Window: int(minutes) * 60,
			})
		case r.FullSpeedWarnMin > 0 && minutes >= int64(r.FullSpeedWarnMin) && !a.speed.warned:
			a.speed.warned = true
			out = append(out, Signal{
				Rule: RuleFullSpeed, Level: LevelWarn, Measure: MeasureMinutes, Count: minutes,
				Limit: int64(r.FullSpeedWarnMin), Window: int(minutes) * 60,
			})
		}
	}
	return out
}

func (d *Detector) checkScan(a *account, sec int64) (Signal, bool) {
	r := d.rules
	if r.ScanIPs <= 0 && r.ScanPortsOnIP <= 0 && r.ScanSensitiveIPs <= 0 {
		return Signal{}, false
	}
	window := a.window(sec/60, r.ScanWindowMin)
	ips, sensitive := map[string]struct{}{}, map[string]struct{}{}
	ports := map[string]map[int]struct{}{}
	for _, b := range window {
		for ip := range b.ips {
			ips[ip] = struct{}{}
		}
		for ip := range b.sensitive {
			sensitive[ip] = struct{}{}
		}
		for ip, set := range b.ports {
			if ports[ip] == nil {
				ports[ip] = map[int]struct{}{}
			}
			for p := range set {
				ports[ip][p] = struct{}{}
			}
		}
	}
	var s Signal
	switch topIP, topPorts := widest(ports); {
	case r.ScanIPs > 0 && len(ips) >= r.ScanIPs:
		s = Signal{Measure: MeasureIPs, Count: int64(len(ips)), Limit: int64(r.ScanIPs), Samples: firstKeys(ips, 5)}
	case r.ScanSensitiveIPs > 0 && len(sensitive) >= r.ScanSensitiveIPs:
		s = Signal{Measure: MeasureSensitive, Count: int64(len(sensitive)), Limit: int64(r.ScanSensitiveIPs), Samples: firstKeys(sensitive, 5)}
	case r.ScanPortsOnIP > 0 && topPorts >= r.ScanPortsOnIP:
		s = Signal{Measure: MeasurePorts, Count: int64(topPorts), Limit: int64(r.ScanPortsOnIP), Samples: []string{topIP}}
	default:
		return Signal{}, false
	}
	if !a.ready(RuleScan, sec, r.ScanWindowMin*60) {
		return Signal{}, false
	}
	s.Rule, s.Level, s.Window = RuleScan, LevelStrike, r.ScanWindowMin*60
	return s, true
}

// checkRegister counts each platform's busy minutes: a person signs in for a
// minute or two now and then, while bulk sign-ups keep at it.
func (d *Detector) checkRegister(a *account, sec int64) []Signal {
	var out []Signal
	for _, platform := range platforms {
		busy := a.signIns[platform]
		if len(busy) == 0 {
			continue
		}
		perHour, perDay := d.rules.registerLimits(platform)
		var hour int64
		for _, m := range busy {
			if m > sec/60-60 {
				hour++
			}
		}
		day := int64(len(busy))
		s := Signal{Rule: RuleRegister, Level: LevelStrike, Samples: []string{platform}}
		switch {
		case perHour > 0 && hour > int64(perHour) && a.ready(MeasureRegisterHour+":"+platform, sec, 3600):
			s.Measure, s.Count, s.Limit, s.Window = MeasureRegisterHour, hour, int64(perHour), 3600
		case perDay > 0 && day > int64(perDay) && a.ready(MeasureRegisterDay+":"+platform, sec, 24*3600):
			s.Measure, s.Count, s.Limit, s.Window = MeasureRegisterDay, day, int64(perDay), 24*3600
		default:
			continue
		}
		out = append(out, s)
	}
	return out
}

// checkCrawler wants every one of the last few windows busy: a crawler keeps
// going, while a burst of browsing fills one window at most.
func (d *Detector) checkCrawler(a *account, sec int64) (Signal, bool) {
	r := d.rules
	if r.CrawlerConns <= 0 && r.CrawlerHosts <= 0 {
		return Signal{}, false
	}
	minute := sec / 60
	var conns, hosts int64
	for w := 0; w < r.CrawlerWindows; w++ {
		var c int64
		seen := map[string]struct{}{}
		for _, b := range a.window(minute-int64(w*r.CrawlerWindowMin), r.CrawlerWindowMin) {
			c += b.conns
			for h := range b.hosts {
				seen[h] = struct{}{}
			}
		}
		if (r.CrawlerConns <= 0 || c < int64(r.CrawlerConns)) && (r.CrawlerHosts <= 0 || len(seen) < r.CrawlerHosts) {
			return Signal{}, false
		}
		if w == 0 {
			conns, hosts = c, int64(len(seen))
		}
	}
	if !a.ready(RuleCrawler, sec, r.CrawlerWindowMin*60) {
		return Signal{}, false
	}
	s := Signal{Rule: RuleCrawler, Level: LevelStrike, Measure: MeasureConns, Count: conns, Limit: int64(r.CrawlerConns), Window: r.CrawlerWindowMin * 60}
	if r.CrawlerHosts > 0 && hosts >= int64(r.CrawlerHosts) {
		s.Measure, s.Count, s.Limit = MeasureHosts, hosts, int64(r.CrawlerHosts)
	}
	return s, true
}

func topCount(counts map[string]int64) (string, int64) {
	var best string
	var top int64
	for k, v := range counts {
		if v > top || (v == top && k < best) {
			best, top = k, v
		}
	}
	return best, top
}

func widest(ports map[string]map[int]struct{}) (string, int) {
	var best string
	top := 0
	for ip, set := range ports {
		if len(set) > top || (len(set) == top && ip < best) {
			best, top = ip, len(set)
		}
	}
	return best, top
}

func firstKeys(set map[string]struct{}, n int) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys[:min(n, len(keys))]
}

func destsOnPort(window []*bucket, port int) []string {
	suffix := ":" + strconv.Itoa(port)
	seen := map[string]struct{}{}
	for _, b := range window {
		for dest := range b.dests {
			if strings.HasSuffix(dest, suffix) {
				seen[dest] = struct{}{}
			}
		}
	}
	return firstKeys(seen, 5)
}

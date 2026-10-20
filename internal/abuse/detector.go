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
	bytes     int64
	smtp      int64
	bt        int64
	ips       map[string]struct{}
	sensitive map[string]struct{}
	hosts     map[string]struct{}
	ports     map[string]map[int]struct{}
	tcp       map[string]int64
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

// after counts the busy minutes later than minute.
func (b busyMinutes) after(minute int64) int64 {
	return int64(len(b) - sort.Search(len(b), func(i int) bool { return b[i] > minute }))
}

// hoursOver counts the clock hours holding more than limit busy minutes.
func (b busyMinutes) hoursOver(limit int) int64 {
	var hours, run int64
	for i, m := range b {
		if i > 0 && m/60 != b[i-1]/60 {
			run = 0
		}
		if run++; run == int64(limit)+1 {
			hours++
		}
	}
	return hours
}

// helperKeep bounds the helper sites an account is remembered by.
const helperKeep = 8

type account struct {
	buckets  [ringMinutes]bucket
	fired    map[string]int64
	tests    []int64
	lastTest int64
	signIns  map[string]busyMinutes
	helpers  map[string]int64
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
		a = &account{fired: map[string]int64{}, signIns: map[string]busyMinutes{}, helpers: map[string]int64{}}
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
	if a.cooling(check, sec, cooldown) {
		return false
	}
	a.fired[check] = sec
	return true
}

func (a *account) cooling(check string, sec int64, cooldown int) bool {
	last, ok := a.fired[check]
	return ok && sec-last < int64(cooldown)
}

// suspectEvery is how often one rule may report an account as suspect.
const suspectEvery = 3600

// suspect lets a rule report a suspect hit once an hour, and not within the hour
// after it struck the account, so each episode reaches the admin once.
func (a *account) suspect(rule string, sec int64) bool {
	return !a.cooling(rule, sec, suspectEvery) && a.ready(rule+":suspect", sec, suspectEvery)
}

// half is the suspect line of a limit: half of it, rounded up.
func half(limit int) int64 { return int64((limit + 1) / 2) }

// grade says whether n strikes against limit, or is suspect at half of it.
func (a *account) grade(rule string, n int64, limit int, sec int64, cooldown int) (level string, line int64) {
	switch {
	case limit <= 0:
	case n >= int64(limit) && a.ready(rule, sec, cooldown):
		return LevelStrike, int64(limit)
	case n >= half(limit) && a.suspect(rule, sec):
		return LevelSuspect, half(limit)
	}
	return "", 0
}

// helperSites lists the helper services the account reached in the last day.
func (a *account) helperSites() []string {
	sites := make([]string, 0, len(a.helpers))
	for site := range a.helpers {
		sites = append(sites, site)
	}
	sort.Strings(sites)
	return sites[:min(3, len(sites))]
}

// hourBytes is the traffic of the hour ending at minute.
func (a *account) hourBytes(minute int64) int64 {
	var n int64
	for _, b := range a.window(minute, 60) {
		n += b.bytes
	}
	return n
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
	if site := helperSite(e.Host); site != "" {
		if _, ok := a.helpers[site]; ok || len(a.helpers) < helperKeep {
			a.helpers[site] = sec / 60
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
		if !e.UDP {
			if b.tcp == nil {
				b.tcp = map[string]int64{}
			}
			if _, ok := b.tcp[e.Host]; ok || len(b.tcp) < mapCap {
				b.tcp[e.Host]++
			}
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
	a.bucket(sec / 60).bytes += bytes
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
		for site, minute := range a.helpers {
			if minute <= day/60 {
				delete(a.helpers, site)
			}
		}
		if sec-a.lastSeen > int64(idleForget/time.Second) && len(a.tests) == 0 && len(a.signIns) == 0 && len(a.helpers) == 0 && a.speed.blocks == 0 {
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
		if level, line := a.grade(RuleSpam, n, r.SpamAttempts, sec, r.SpamWindowMin*60); level != "" {
			out = append(out, Signal{
				Rule: RuleSpam, Level: level, Measure: MeasureAttempts, Count: n,
				Limit: line, Window: r.SpamWindowMin * 60, Samples: destsOnPort(window, 25),
			})
		}
	}

	if r.BTAttempts > 0 {
		var n int64
		for _, b := range a.window(minute, r.BTWindowMin) {
			n += b.bt
		}
		if level, line := a.grade(RuleBT, n, r.BTAttempts, sec, r.BTWindowMin*60); level != "" {
			out = append(out, Signal{
				Rule: RuleBT, Level: level, Measure: MeasureAttempts, Count: n,
				Limit: line, Window: r.BTWindowMin * 60,
			})
		}
	}

	if s, ok := d.checkScan(a, sec); ok {
		out = append(out, s)
	}

	if s, ok := d.checkFlood(a, sec); ok {
		out = append(out, s)
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

// A TCP address reached at most probeTouches times in the scan window was probed,
// not used; scanRangeIPs TCP addresses in one IPv4 /24 sweep that network.
const (
	probeTouches = 2
	scanRangeIPs = 32
)

// checkScan strikes only on clear scans; volume without TCP probing, as peer-to-peer
// and chat apps produce, and the half-way marks are suspect.
func (d *Detector) checkScan(a *account, sec int64) (Signal, bool) {
	r := d.rules
	if r.ScanIPs <= 0 && r.ScanPortsOnIP <= 0 && r.ScanSensitiveIPs <= 0 {
		return Signal{}, false
	}
	window := a.window(sec/60, r.ScanWindowMin)
	ips, sensitive := map[string]struct{}{}, map[string]struct{}{}
	ports := map[string]map[int]struct{}{}
	tcp := map[string]int64{}
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
		for ip, n := range b.tcp {
			tcp[ip] += n
		}
	}
	probes := map[string]struct{}{}
	ranges := map[string]int{}
	for ip, n := range tcp {
		if n <= probeTouches {
			probes[ip] = struct{}{}
		}
		if v4 := net.ParseIP(ip).To4(); v4 != nil {
			ranges[net.IPv4(v4[0], v4[1], v4[2], 0).String()+"/24"]++
		}
	}
	topIP, topPorts := widest(ports)
	rangeNet, inRange := topCount(intCounts(ranges))
	var s Signal
	switch {
	case r.ScanSensitiveIPs > 0 && len(sensitive) >= r.ScanSensitiveIPs:
		s = Signal{Measure: MeasureSensitive, Count: int64(len(sensitive)), Limit: int64(r.ScanSensitiveIPs), Samples: firstKeys(sensitive, 5)}
	case r.ScanPortsOnIP > 0 && topPorts >= r.ScanPortsOnIP:
		s = Signal{Measure: MeasurePorts, Count: int64(topPorts), Limit: int64(r.ScanPortsOnIP), Samples: []string{topIP}}
	case r.ScanIPs > 0 && len(probes) >= r.ScanIPs:
		s = Signal{Measure: MeasureProbes, Count: int64(len(probes)), Limit: int64(r.ScanIPs), Samples: firstKeys(probes, 5)}
	}
	if s.Measure != "" && a.ready(RuleScan, sec, r.ScanWindowMin*60) {
		s.Rule, s.Level, s.Window = RuleScan, LevelStrike, r.ScanWindowMin*60
		return s, true
	}
	switch {
	case r.ScanIPs > 0 && len(ips) >= r.ScanIPs:
		s = Signal{Measure: MeasureIPs, Count: int64(len(ips)), Limit: int64(r.ScanIPs), Samples: firstKeys(ips, 5)}
	case r.ScanIPs > 0 && int64(len(probes)) >= half(r.ScanIPs):
		s = Signal{Measure: MeasureProbes, Count: int64(len(probes)), Limit: half(r.ScanIPs), Samples: firstKeys(probes, 5)}
	case r.ScanIPs > 0 && inRange >= scanRangeIPs:
		s = Signal{Measure: MeasureRange, Count: inRange, Limit: scanRangeIPs, Samples: []string{rangeNet}}
	case r.ScanSensitiveIPs > 0 && int64(len(sensitive)) >= half(r.ScanSensitiveIPs):
		s = Signal{Measure: MeasureSensitive, Count: int64(len(sensitive)), Limit: half(r.ScanSensitiveIPs), Samples: firstKeys(sensitive, 5)}
	case r.ScanPortsOnIP > 0 && int64(topPorts) >= half(r.ScanPortsOnIP):
		s = Signal{Measure: MeasurePorts, Count: int64(topPorts), Limit: half(r.ScanPortsOnIP), Samples: []string{topIP}}
	default:
		return Signal{}, false
	}
	if !a.suspect(RuleScan, sec) {
		return Signal{}, false
	}
	s.Rule, s.Level, s.Window = RuleScan, LevelSuspect, r.ScanWindowMin*60
	return s, true
}

// checkFlood looks at the busier of the last two minutes, one destination first.
func (d *Detector) checkFlood(a *account, sec int64) (Signal, bool) {
	r := d.rules
	if r.FloodPerDest <= 0 && r.FloodTotal <= 0 {
		return Signal{}, false
	}
	var dest string
	var top, conns int64
	for _, b := range a.window(sec/60, 2) {
		if to, n := topCount(b.dests); n > top {
			dest, top = to, n
		}
		conns = max(conns, b.conns)
	}
	reaches := func(n int64, limit int, line func(int) int64) bool { return limit > 0 && n >= line(limit) }
	full := func(limit int) int64 { return int64(limit) }
	var level string
	line := full
	switch {
	case (reaches(top, r.FloodPerDest, full) || reaches(conns, r.FloodTotal, full)) && a.ready(RuleFlood, sec, 60):
		level = LevelStrike
	case (reaches(top, r.FloodPerDest, half) || reaches(conns, r.FloodTotal, half)) && a.suspect(RuleFlood, sec):
		level, line = LevelSuspect, half
	default:
		return Signal{}, false
	}
	s := Signal{Rule: RuleFlood, Level: level, Window: 60, Measure: MeasureConns, Count: conns, Limit: line(r.FloodTotal)}
	if reaches(top, r.FloodPerDest, line) {
		s.Measure, s.Count, s.Limit, s.Samples = MeasurePerDest, top, line(r.FloodPerDest), []string{dest}
	}
	return s, true
}

// registerSuspect cools a platform's suspect hits apart from its strikes, so a
// strike later in the same hour still fires.
const registerSuspect = "register-suspect"

// checkRegister counts each platform's busy minutes. Going over the hourly limit
// strikes unless the platform needs it held for hours; until then it is suspect.
func (d *Detector) checkRegister(a *account, sec int64) []Signal {
	if len(a.signIns) == 0 {
		return nil
	}
	minute := sec / 60
	helpers := a.helperSites()
	var out []Signal
	for _, platform := range platforms {
		busy := a.signIns[platform]
		if len(busy) == 0 {
			continue
		}
		perHour, perDay, hours := d.rules.registerLimits(platform)
		hour := busy.after(minute - 60)
		over := perHour > 0 && hour > int64(perHour)
		hourly := MeasureRegisterHour + ":" + platform
		s := Signal{Rule: RuleRegister, Level: LevelStrike, Count: hour, Limit: int64(perHour), Window: 3600}
		switch {
		case perHour > 0 && len(helpers) > 0 && hour > int64(perHour/2) && a.ready(hourly, sec, 3600):
			s.Measure, s.Limit = MeasureRegisterHelped, int64(perHour/2)
		case over && hours <= 1 && a.ready(hourly, sec, 3600):
			s.Measure = MeasureRegisterHour
		case perHour > 0 && hour > int64(perHour*3/2) && a.ready(hourly, sec, 3600):
			s.Measure, s.Limit = MeasureRegisterBurst, int64(perHour*3/2)
		case over && busy.hoursOver(perHour) >= int64(hours) && a.ready(hourly, sec, 3600):
			s.Measure, s.Count, s.Window = MeasureRegisterHours, busy.hoursOver(perHour), 24*3600
		case perDay > 0 && len(busy) > perDay && a.ready(MeasureRegisterDay+":"+platform, sec, 24*3600):
			s.Measure, s.Count, s.Limit, s.Window = MeasureRegisterDay, int64(len(busy)), int64(perDay), 24*3600
		case over && !a.cooling(hourly, sec, 3600) && a.ready(registerSuspect+":"+platform, sec, 3600):
			s.Level, s.Measure = LevelSuspect, MeasureRegisterHour
		default:
			continue
		}
		s.Samples = append([]string{platform}, helpers...)
		s.Bytes = a.hourBytes(minute)
		out = append(out, s)
	}
	return out
}

// checkCrawler wants every one of the last few windows busy. Half the hosts in each
// is suspect; half the connections is not, as heavy API use alone reaches it.
func (d *Detector) checkCrawler(a *account, sec int64) (Signal, bool) {
	r := d.rules
	if r.CrawlerConns <= 0 && r.CrawlerHosts <= 0 {
		return Signal{}, false
	}
	minute := sec / 60
	var conns, hosts int64
	over, busy := true, r.CrawlerHosts > 0
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
			over = false
		}
		if int64(len(seen)) < half(r.CrawlerHosts) {
			busy = false
		}
		if w == 0 {
			conns, hosts = c, int64(len(seen))
		}
	}
	window := r.CrawlerWindowMin * 60
	switch {
	case over && a.ready(RuleCrawler, sec, window):
		s := Signal{Rule: RuleCrawler, Level: LevelStrike, Measure: MeasureConns, Count: conns, Limit: int64(r.CrawlerConns), Window: window}
		if r.CrawlerHosts > 0 && hosts >= int64(r.CrawlerHosts) {
			s.Measure, s.Count, s.Limit = MeasureHosts, hosts, int64(r.CrawlerHosts)
		}
		return s, true
	case busy && a.suspect(RuleCrawler, sec):
		return Signal{Rule: RuleCrawler, Level: LevelSuspect, Measure: MeasureHosts, Count: hosts, Limit: half(r.CrawlerHosts), Window: window}, true
	}
	return Signal{}, false
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

func intCounts(counts map[string]int) map[string]int64 {
	out := make(map[string]int64, len(counts))
	for k, v := range counts {
		out[k] = int64(v)
	}
	return out
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

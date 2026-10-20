package abuse

import (
	"fmt"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func connect(d *Detector, at time.Time, email, host string, port int, outbound string) {
	d.Observe(NewEvent(at, email, host, port, false, "in-443 >> "+outbound))
}

// only returns the one signal of a rule, failing on none or several.
func only(t *testing.T, signals []Signal, rule string) Signal {
	t.Helper()
	var hits []Signal
	for _, s := range signals {
		if s.Rule == rule {
			hits = append(hits, s)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("%s signals = %d (%+v), want 1", rule, len(hits), signals)
	}
	return hits[0]
}

func none(t *testing.T, signals []Signal, rule string) {
	t.Helper()
	for _, s := range signals {
		if s.Rule == rule {
			t.Fatalf("unexpected %s signal %+v", rule, s)
		}
	}
}

// A mail app may try port 25 once or twice; a spammer keeps at it. Half the limit
// is only suspect, and one run of attempts is one strike, not one per check.
func TestSpamNeedsRepeatedPort25AttemptsAndStrikesOnce(t *testing.T) {
	d := NewDetector(DefaultRules())
	for i := range 9 {
		connect(d, t0.Add(time.Duration(i)*time.Minute), "alice", "203.0.113.25", 25, TagSMTP)
	}
	if s := only(t, d.Collect(t0.Add(9*time.Minute)), RuleSpam); s.Level != LevelSuspect || s.Count != 9 || s.Limit != 5 {
		t.Fatalf("nine attempts gave %+v, want a suspect over half the limit", s)
	}

	connect(d, t0.Add(9*time.Minute), "alice", "203.0.113.26", 25, TagSMTP)
	s := only(t, d.Collect(t0.Add(9*time.Minute)), RuleSpam)
	if s.Count != 10 || s.Level != LevelStrike || len(s.Samples) != 2 {
		t.Errorf("spam signal = %+v, want 10 attempts to 2 servers as a strike", s)
	}
	connect(d, t0.Add(9*time.Minute+30*time.Second), "alice", "203.0.113.27", 25, TagSMTP)
	none(t, d.Collect(t0.Add(9*time.Minute+30*time.Second)), RuleSpam)
}

// Browsing reaches hundreds of sites by name; a scan reaches bare addresses.
func TestScanCountsBareIPsNotNamedSites(t *testing.T) {
	d := NewDetector(DefaultRules())
	for i := range 500 {
		connect(d, t0.Add(time.Duration(i%5)*time.Minute), "alice", fmt.Sprintf("site%d.example.com", i), 443, "direct")
	}
	none(t, d.Collect(t0.Add(5*time.Minute)), RuleScan)

	for i := range 150 {
		connect(d, t0.Add(5*time.Minute), "bob", fmt.Sprintf("198.51.%d.%d", i/250, i%250+1), 80, "direct")
	}
	s := only(t, d.Collect(t0.Add(5*time.Minute)), RuleScan)
	if s.Email != "bob" || s.Level != LevelStrike || s.Measure != MeasureProbes || s.Count != 150 {
		t.Errorf("scan signal = %+v, want bob probing 150 bare IPs", s)
	}
}

func TestScanCatchesPortsOnOneIPAndLoginPortsAcrossIPs(t *testing.T) {
	d := NewDetector(DefaultRules())
	for p := range 50 {
		connect(d, t0, "alice", "192.0.2.10", 1000+p, "direct")
	}
	if s := only(t, d.Collect(t0), RuleScan); s.Measure != MeasurePorts || s.Samples[0] != "192.0.2.10" {
		t.Errorf("port sweep signal = %+v", s)
	}

	d = NewDetector(DefaultRules())
	for i := range 20 {
		connect(d, t0, "bob", fmt.Sprintf("203.0.113.%d", i+1), 22, "direct")
	}
	if s := only(t, d.Collect(t0), RuleScan); s.Measure != MeasureSensitive || s.Count != 20 {
		t.Errorf("login-port sweep signal = %+v", s)
	}
}

// reach opens n connections to each of count bare IPs within a minute.
func reach(d *Detector, email string, count, n int, udp bool, ip func(int) string, port int) {
	for i := range count {
		for j := range n {
			d.Observe(NewEvent(t0.Add(time.Duration(i*n+j)*time.Millisecond), email, ip(i), port, udp, "in-443 >> direct"))
		}
	}
}

func spread(i int) string { return fmt.Sprintf("198.51.%d.%d", i/200, i%200+1) }

// Peer-to-peer apps and chat apps reach many addresses too, over UDP or again and
// again (a WeChat user hit 70 in five minutes); only one-off TCP probes are a scan.
func TestScanStrikesOnlyOnTCPProbes(t *testing.T) {
	d := NewDetector(DefaultRules())
	reach(d, "p2p", 160, 1, true, spread, 51413)
	reach(d, "app", 160, 3, false, spread, 443)
	got := d.Collect(t0.Add(time.Minute))
	for _, email := range []string{"p2p", "app"} {
		var mine []Signal
		for _, s := range got {
			if s.Email == email && s.Rule == RuleScan {
				mine = append(mine, s)
			}
		}
		if len(mine) != 1 || mine[0].Level != LevelSuspect || mine[0].Measure != MeasureIPs || mine[0].Count != 160 {
			t.Errorf("%s reaching 160 addresses gave %+v, want one suspect", email, mine)
		}
	}
}

// Under the scan limit, half of it in probes, a sweep through one /24, or half the
// login-port or one-address limits are recorded as suspect, never struck.
func TestScanEarlyWarningsAreSuspect(t *testing.T) {
	cases := []struct {
		name    string
		feed    func(d *Detector)
		measure string
		count   int64
		limit   int64
	}{
		{"probes", func(d *Detector) { reach(d, "eve", 80, 1, false, spread, 443) }, MeasureProbes, 80, 75},
		{"range", func(d *Detector) {
			reach(d, "eve", 40, 1, false, func(i int) string { return fmt.Sprintf("203.0.113.%d", i+1) }, 443)
		}, MeasureRange, 40, 32},
		{"login ports", func(d *Detector) { reach(d, "eve", 12, 1, false, spread, 22) }, MeasureSensitive, 12, 10},
		{"ports on one address", func(d *Detector) {
			for p := range 30 {
				connect(d, t0, "eve", "192.0.2.10", 1000+p, "direct")
			}
		}, MeasurePorts, 30, 25},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := NewDetector(DefaultRules())
			c.feed(d)
			s := only(t, d.Collect(t0.Add(time.Minute)), RuleScan)
			if s.Level != LevelSuspect || s.Measure != c.measure || s.Count != c.count || s.Limit != c.limit {
				t.Errorf("signal = %+v, want a suspect %s of %d over %d", s, c.measure, c.count, c.limit)
			}
		})
	}
}

// Half a rule's limit is worth a look: floods, crawling and BitTorrent are suspect
// there, while the strike lines stay where they were.
func TestHalfTheLimitIsSuspect(t *testing.T) {
	d := NewDetector(DefaultRules())
	end := t0.Add(19 * time.Minute)
	for range 600 {
		connect(d, end, "flood", "203.0.113.80", 80, "direct")
	}
	for i := range 16 {
		connect(d, end, "bt", fmt.Sprintf("198.51.100.%d", i+1), 51413, TagBT)
	}
	for w := range 2 {
		for i := range 320 {
			connect(d, t0.Add(time.Duration(w*10+i%10)*time.Minute), "crawl", fmt.Sprintf("p%d-%d.example.org", w, i), 443, "direct")
		}
	}
	got := d.Collect(end)
	want := map[string]string{"flood": MeasurePerDest, "bt": MeasureAttempts, "crawl": MeasureHosts}
	for _, s := range got {
		if want[s.Email] != s.Measure || s.Level != LevelSuspect {
			t.Errorf("unexpected signal %+v", s)
		}
		delete(want, s.Email)
	}
	if len(want) != 0 {
		t.Errorf("no suspect for %v in %+v", want, got)
	}
}

// A suspect is said once an hour, and not at all within the hour after a strike
// of the same rule, so the admin reads each episode once.
func TestSuspectsComeHourlyAndYieldToStrikes(t *testing.T) {
	d := NewDetector(DefaultRules())
	burst := func(at time.Time, n int) []Signal {
		for range n {
			connect(d, at, "alice", "203.0.113.80", 80, "direct")
		}
		return d.Collect(at)
	}
	if s := only(t, burst(t0, 600), RuleFlood); s.Level != LevelSuspect {
		t.Fatalf("first burst gave %+v, want a suspect", s)
	}
	none(t, burst(t0.Add(5*time.Minute), 600), RuleFlood)
	if s := only(t, burst(t0.Add(10*time.Minute), 1000), RuleFlood); s.Level != LevelStrike {
		t.Fatalf("a full flood gave %+v, want a strike", s)
	}
	none(t, burst(t0.Add(65*time.Minute), 600), RuleFlood)
	if s := only(t, burst(t0.Add(71*time.Minute), 600), RuleFlood); s.Level != LevelSuspect {
		t.Fatalf("an hour after the strike gave %+v, want a suspect again", s)
	}
}

// A download manager opens a few dozen connections to one site; a flood opens
// a thousand a minute.
func TestFloodTripsOnABurstNotOnADownloadManager(t *testing.T) {
	d := NewDetector(DefaultRules())
	for range 32 {
		connect(d, t0, "alice", "cdn.example.com", 443, "direct")
	}
	none(t, d.Collect(t0.Add(30*time.Second)), RuleFlood)

	for range 1000 {
		connect(d, t0, "bob", "203.0.113.80", 80, "direct")
	}
	s := only(t, d.Collect(t0.Add(30*time.Second)), RuleFlood)
	if s.Email != "bob" || s.Measure != MeasurePerDest || s.Samples[0] != "203.0.113.80:80" {
		t.Errorf("flood signal = %+v", s)
	}
}

// A banned device's retries and a relay's hops (transit identities, SOCKS5 relays)
// never leave for anyone else: the exit judges that traffic under the account, once.
func TestBansAndRelayHopsAreNotTheAccountsTraffic(t *testing.T) {
	for _, outbound := range []string{TagBlock, TagIPLimitBlock, TagChainTransitDirect, TagChainTransitBlock, ChainSocksTag(7, "carol")} {
		d := NewDetector(DefaultRules())
		for range 3000 {
			connect(d, t0, "carol", "192.0.2.73", 443, outbound)
		}
		if signals := d.Collect(t0.Add(30 * time.Second)); len(signals) != 0 {
			t.Errorf("via %s: signals = %+v, want none", outbound, signals)
		}
	}
}

// One busy stretch is browsing; a crawler keeps every window busy.
func TestCrawlerNeedsBusyWindowsInARow(t *testing.T) {
	d := NewDetector(DefaultRules())
	for i := range 700 {
		connect(d, t0.Add(time.Duration(i%10)*time.Minute), "alice", fmt.Sprintf("page%d.example.org", i), 443, "direct")
	}
	none(t, d.Collect(t0.Add(9*time.Minute)), RuleCrawler)

	for i := range 700 {
		connect(d, t0.Add(time.Duration(10+i%10)*time.Minute), "alice", fmt.Sprintf("next%d.example.org", i), 443, "direct")
	}
	if s := only(t, d.Collect(t0.Add(19*time.Minute)), RuleCrawler); s.Measure != MeasureHosts || s.Count != 700 {
		t.Errorf("crawler signal = %+v", s)
	}
}

func speedTest(d *Detector, start time.Time) {
	for i := range 20 {
		connect(d, start.Add(time.Duration(i)*2*time.Second), "alice", "speed.example.net", 443, TagSpeedTest)
	}
}

// A test opens many connections and still counts once; the sixth in an hour trips.
func TestSpeedTestsCountTestsNotConnections(t *testing.T) {
	d := NewDetector(DefaultRules())
	for i := range 5 {
		speedTest(d, t0.Add(time.Duration(i)*5*time.Minute))
	}
	none(t, d.Collect(t0.Add(25*time.Minute)), RuleSpeedTest)

	speedTest(d, t0.Add(30*time.Minute))
	if s := only(t, d.Collect(t0.Add(31*time.Minute)), RuleSpeedTest); s.Measure != MeasureTestsHour || s.Count != 6 {
		t.Errorf("speed test signal = %+v", s)
	}
}

// busy connects once a minute to host for the given minutes, from at.
func busy(d *Detector, at time.Time, minutes int, email, host string) {
	for i := range minutes {
		connect(d, at.Add(time.Duration(i)*time.Minute), email, host, 443, "direct")
	}
}

// A login takes a couple of minutes, and a few of them in an hour pass; being
// at OpenAI's sign-in servers in 11 minutes of one hour trips, either server.
func TestRegisterCountsMinutesAtAPlatformsSignInServer(t *testing.T) {
	d := NewDetector(DefaultRules())
	for i := range 4 {
		busy(d, t0.Add(time.Duration(i)*12*time.Minute), 2, "alice", "auth.openai.com")
	}
	none(t, d.Collect(t0.Add(50*time.Minute)), RuleRegister)

	busy(d, t0.Add(50*time.Minute), 3, "alice", "auth0.openai.com")
	s := only(t, d.Collect(t0.Add(53*time.Minute)), RuleRegister)
	if s.Measure != MeasureRegisterHour || s.Count != 11 || s.Limit != 10 || len(s.Samples) != 1 || s.Samples[0] != "auth.openai.com" {
		t.Errorf("register signal = %+v, want 11 minutes at OpenAI's sign-in in an hour", s)
	}
	busy(d, t0.Add(53*time.Minute), 1, "alice", "auth.openai.com")
	none(t, d.Collect(t0.Add(54*time.Minute)), RuleRegister)
}

// A bot signing up one account after another never pauses; the faster it goes,
// the more minutes it fills.
func TestRegisterCatchesSignUpsBackToBack(t *testing.T) {
	d := NewDetector(DefaultRules())
	for i := range 30 {
		connect(d, t0.Add(time.Duration(i)*30*time.Second), "bob", "auth.openai.com", 443, "direct")
	}
	if s := only(t, d.Collect(t0.Add(15*time.Minute)), RuleRegister); s.Email != "bob" || s.Count != 15 {
		t.Errorf("register signal = %+v, want bob's 15 busy minutes", s)
	}
}

// However many connections a page opens in one minute, that minute counts once.
func TestRegisterCountsAMinuteOnceWhateverItsConnections(t *testing.T) {
	d := NewDetector(DefaultRules())
	for i := range 200 {
		connect(d, t0.Add(time.Duration(i)*250*time.Millisecond), "alice", "signup.live.com", 443, "direct")
	}
	none(t, d.Collect(t0.Add(time.Minute)), RuleRegister)
}

// Using ChatGPT, Gemini, Gmail or Outlook all hour is not signing in to them.
func TestRegisterIgnoresThePlatformsOtherSites(t *testing.T) {
	d := NewDetector(DefaultRules())
	for _, host := range []string{"chatgpt.com", "gemini.google.com", "mail.google.com", "login.live.com", "outlook.live.com"} {
		busy(d, t0, 60, "alice", host)
	}
	none(t, d.Collect(t0.Add(time.Hour)), RuleRegister)
}

// Google's server also gets browsers' background account checks, so its limit
// is wide; Microsoft's only creates accounts, so its limit is tight.
func TestRegisterUsesEachPlatformsOwnLimit(t *testing.T) {
	d := NewDetector(DefaultRules())
	busy(d, t0, 25, "alice", "accounts.google.com")
	busy(d, t0, 11, "bob", "signup.live.com")
	s := only(t, d.Collect(t0.Add(30*time.Minute)), RuleRegister)
	if s.Email != "bob" || s.Samples[0] != "signup.live.com" || s.Count != 11 || s.Limit != 10 {
		t.Errorf("register signal = %+v, want bob's 11 minutes at Microsoft's sign-up over a limit of 10", s)
	}
}

// Sign-ups spread over a day trip the daily limit even with idle hours between
// them, and Google, with no daily limit, never does.
func TestRegisterDailyLimitOutlastsQuietHours(t *testing.T) {
	d := NewDetector(DefaultRules())
	var got []Signal
	for i := range 7 {
		at := t0.Add(time.Duration(i) * 3 * time.Hour)
		busy(d, at, 3, "bob", "signup.live.com")
		busy(d, at, 25, "alice", "accounts.google.com")
		got = append(got, d.Collect(at.Add(2*time.Hour))...)
	}
	s := only(t, got, RuleRegister)
	if s.Email != "bob" || s.Measure != MeasureRegisterDay || s.Count != 21 || s.Limit != 20 {
		t.Errorf("register signal = %+v, want bob's 21 minutes at Microsoft's sign-up in a day", s)
	}
}

// busyCollecting connects to host each minute from at and collects after each, as
// a server sees it, then keeps collecting quietly until the hour is over.
func busyCollecting(d *Detector, at time.Time, minutes int, email, host string) []Signal {
	var out []Signal
	for i := range 60 {
		if i < minutes {
			connect(d, at.Add(time.Duration(i)*time.Minute), email, host, 443, "direct")
		}
		out = append(out, d.Collect(at.Add(time.Duration(i)*time.Minute+30*time.Second))...)
	}
	return out
}

func registerAt(signals []Signal, level string) []Signal {
	var out []Signal
	for _, s := range signals {
		if s.Rule == RuleRegister && s.Level == level {
			out = append(out, s)
		}
	}
	return out
}

// A heavy evening at Google's sign-in server (a real one ran 31 and 35 minutes an
// hour) is only suspect, once an hour, with the hour's traffic to judge it by.
func TestRegisterGoogleOneBusyHourIsOnlySuspect(t *testing.T) {
	d := NewDetector(DefaultRules())
	d.ObserveTraffic("alice", 9_000_000_000, t0.Add(-2*time.Hour))
	d.ObserveTraffic("alice", 2_500_000_000, t0.Add(20*time.Minute))
	s := only(t, busyCollecting(d, t0, 35, "alice", "accounts.google.com"), RuleRegister)
	if s.Level != LevelSuspect || s.Measure != MeasureRegisterHour || s.Count != 31 || s.Limit != 30 || s.Bytes != 2_500_000_000 {
		t.Errorf("register signal = %+v, want a suspect 31 of 30 minutes with the hour's 2.5 GB", s)
	}
}

// Over the limit for three separate hours in a day is no evening's browsing; an
// hour at exactly the limit is not over it.
func TestRegisterGoogleStrikesWhenItLastsHours(t *testing.T) {
	d := NewDetector(DefaultRules())
	for i, minutes := range []int{40, 30, 40} {
		got := busyCollecting(d, t0.Add(time.Duration(i)*5*time.Hour), minutes, "alice", "accounts.google.com")
		if strikes := registerAt(got, LevelStrike); len(strikes) != 0 {
			t.Fatalf("hour %d (%d minutes) gave strikes %+v, want none", i+1, minutes, strikes)
		}
	}
	s := only(t, busyCollecting(d, t0.Add(15*time.Hour), 40, "alice", "accounts.google.com"), RuleRegister)
	if s.Level != LevelStrike || s.Measure != MeasureRegisterHours || s.Count != 3 || s.Limit != 30 || s.Window != 24*3600 {
		t.Errorf("the third hour over gave %+v, want only a strike: 3 hours over 30 minutes", s)
	}
}

// A bot running profiles side by side fills nearly every minute: no need to wait
// hours, and the suspect hit on the way there stays on record beside the strike.
func TestRegisterGoogleNearlyNonstopStrikesAtOnce(t *testing.T) {
	d := NewDetector(DefaultRules())
	got := busyCollecting(d, t0, 46, "alice", "accounts.google.com")
	strikes, suspects := registerAt(got, LevelStrike), registerAt(got, LevelSuspect)
	if len(strikes) != 1 || strikes[0].Measure != MeasureRegisterBurst || strikes[0].Count != 46 || strikes[0].Limit != 45 {
		t.Errorf("46 busy minutes gave strikes %+v, want one for 46 of 45 minutes", strikes)
	}
	if len(suspects) != 1 || suspects[0].Count != 31 {
		t.Errorf("46 busy minutes gave suspects %+v, want the one at 31 minutes", suspects)
	}
}

// An SMS-code, temp-mail or captcha site that day halves the limit and is named; a
// day old it is nothing, and neither is an anti-detect browser or a look-alike name.
func TestRegisterHelperSitesLowerTheLimit(t *testing.T) {
	d := NewDetector(DefaultRules())
	connect(d, t0, "bob", "api.5sim.net", 443, "direct")
	connect(d, t0.Add(time.Minute), "bob", "api.capsolver.com", 443, "direct")
	connect(d, t0, "dave", "www.yopmail.com", 443, "direct")
	connect(d, t0, "erin", "check.adspower.com", 443, "direct")
	connect(d, t0, "erin", "fakemail.tm", 443, "direct")
	var got []Signal
	for _, email := range []string{"alice", "bob", "erin"} {
		got = append(got, busyCollecting(d, t0.Add(3*time.Hour), 16, email, "accounts.google.com")...)
	}
	s := only(t, got, RuleRegister)
	if s.Email != "bob" || s.Level != LevelStrike || s.Measure != MeasureRegisterHelped || s.Count != 16 || s.Limit != 15 ||
		fmt.Sprint(s.Samples) != "[accounts.google.com 5sim.net capsolver.com]" {
		t.Errorf("register signal = %+v, want bob's 16 minutes over 15 with 5sim.net and capsolver.com", s)
	}
	none(t, busyCollecting(d, t0.Add(25*time.Hour), 16, "dave", "accounts.google.com"), RuleRegister)
}

// fullSpeed feeds traffic at mbps in 5-second reports, collecting as a server does.
func fullSpeed(d *Detector, from time.Time, minutes int, mbps int64) []Signal {
	var out []Signal
	per := mbps * 1_000_000 / 8 * 5
	for s := 0; s < minutes*60; s += 5 {
		at := from.Add(time.Duration(s) * time.Second)
		d.ObserveTraffic("alice", per, at)
		out = append(out, d.Collect(at)...)
	}
	return out
}

// A download of under two hours passes; two hours warns, four strikes, and a
// pause in between starts the count again.
func TestFullSpeedWarnsAtTwoHoursStrikesAtFour(t *testing.T) {
	d := NewDetector(DefaultRules())
	none(t, fullSpeed(d, t0, 115, 120), RuleFullSpeed)

	d = NewDetector(DefaultRules())
	got := fullSpeed(d, t0, 100, 120)
	got = append(got, fullSpeed(d, t0.Add(110*time.Minute), 100, 120)...)
	none(t, got, RuleFullSpeed)

	d = NewDetector(DefaultRules())
	warn := only(t, fullSpeed(d, t0, 126, 120), RuleFullSpeed)
	if warn.Level != LevelWarn || warn.Count < 120 {
		t.Errorf("two hours gave %+v, want a warning", warn)
	}
	strike := only(t, fullSpeed(d, t0.Add(126*time.Minute), 120, 120), RuleFullSpeed)
	if strike.Level != LevelStrike || strike.Count < 240 {
		t.Errorf("four hours gave %+v, want a strike", strike)
	}

	// A run that stops on a block boundary ends on a full block: the pause
	// must still start a new run that can warn again.
	d = NewDetector(DefaultRules())
	if first := only(t, fullSpeed(d, t0, 125, 120), RuleFullSpeed); first.Level != LevelWarn {
		t.Fatalf("the first run gave %+v, want a warning", first)
	}
	again := only(t, fullSpeed(d, t0.Add(135*time.Minute), 126, 120), RuleFullSpeed)
	if again.Level != LevelWarn {
		t.Errorf("a new two-hour run after a pause gave %+v, want a fresh warning", again)
	}
}

func TestBitTorrentAttemptsStrike(t *testing.T) {
	d := NewDetector(DefaultRules())
	for i := range 30 {
		connect(d, t0.Add(time.Duration(i%10)*time.Minute), "alice", fmt.Sprintf("198.51.100.%d", i+1), 51413, TagBT)
	}
	if s := only(t, d.Collect(t0.Add(9*time.Minute)), RuleBT); s.Count != 30 {
		t.Errorf("bt signal = %+v", s)
	}
}

func TestParseAccessLine(t *testing.T) {
	cases := []struct {
		line string
		want Event
	}{
		{
			"2026/10/05 14:40:59.123456 from tcp:198.51.100.7:51234 accepted tcp:www.example.com:443 [in-443 >> direct] email: alice",
			Event{Email: "alice", Host: "www.example.com", Port: 443, Tag: "direct"},
		},
		{
			"2026/10/05 14:40:59.123456 from 198.51.100.7:51234 accepted udp:[2001:db8::1]:3478 [in-443 -> abuse-bt] email: bob",
			Event{Email: "bob", Host: "2001:db8::1", IP: true, Port: 3478, UDP: true, Tag: "abuse-bt"},
		},
	}
	for _, c := range cases {
		got, ok := ParseAccessLine(c.line, t0)
		c.want.At = t0
		if !ok || got != c.want {
			t.Errorf("ParseAccessLine(%q) = %+v, %v; want %+v", c.line, got, ok, c.want)
		}
	}
	if _, ok := ParseAccessLine("2026/10/05 14:40:59.123456 [Warning] core: started", t0); ok {
		t.Error("an error line parsed as an access line")
	}
}

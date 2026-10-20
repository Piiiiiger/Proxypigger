// Package abuse spots accounts that misuse a proxy server, from their
// connections and traffic only, never their content.
package abuse

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Rule names, stored with every hit and shown to people.
const (
	RuleSpam      = "spam"
	RuleBT        = "bt"
	RuleScan      = "scan"
	RuleFlood     = "flood"
	RuleCrawler   = "crawler"
	RuleSpeedTest = "speedtest"
	RuleFullSpeed = "fullspeed"
	RuleRegister  = "register"
)

// Routing tags a server with detection on sends classified traffic to: the first
// two are blackholes, speed tests go out as usual and are only counted.
const (
	TagSMTP      = "abuse-smtp"
	TagBT        = "abuse-bt"
	TagSpeedTest = "abuse-speedtest"
	TagBlock     = "abuse-block"
)

// Routing tags of traffic that is not the account's own: an IP-limit ban's
// blackhole, and a relay identity's only way on, to its target Pigger server.
const (
	TagIPLimitBlock       = "iplimit-block"
	TagChainTransitDirect = "chain-transit-direct"
	TagChainTransitBlock  = "chain-transit-block"
)

// TagChainSocksPrefix starts the outbound a SOCKS5 relay carries one user on to
// its chain exit with; the exit judges that traffic under the user's own login.
const TagChainSocksPrefix = "chain-socks-"

// TagChainSocksBlock drops what a SOCKS5 relay user may not carry on to an exit.
const TagChainSocksBlock = TagChainSocksPrefix + "block"

// ChainSocksTag names that outbound; a hash keeps any user name safe in a tag.
func ChainSocksTag(chainID int, email string) string {
	sum := sha256.Sum256([]byte(email))
	return TagChainSocksPrefix + strconv.Itoa(chainID) + "-" + hex.EncodeToString(sum[:6])
}

// IsChainSocksHop reports whether an outbound is such a relay hop.
func IsChainSocksHop(tag string) bool {
	return strings.HasPrefix(tag, TagChainSocksPrefix)
}

// Levels say how far a hit went: a strike is a full hit, a warning the first
// stage of one, which never costs a strike whatever the rule is set to do.
const (
	LevelStrike  = "strike"
	LevelWarn    = "warn"
	LevelSuspect = "suspect"
)

// Measures name what a signal's Count counted, so a message can say it.
const (
	MeasureAttempts  = "attempts"
	MeasureIPs       = "ips"
	MeasurePorts     = "ports"
	MeasureSensitive = "sensitive"
	MeasurePerDest   = "perdest"
	MeasureConns     = "conns"
	MeasureHosts     = "hosts"
	MeasureTestsHour = "tests-hour"
	MeasureTestsDay  = "tests-day"
	MeasureMinutes   = "minutes"

	MeasureRegisterHour   = "register-hour"
	MeasureRegisterDay    = "register-day"
	MeasureRegisterHours  = "register-hours"
	MeasureRegisterBurst  = "register-burst"
	MeasureRegisterHelped = "register-helped"
)

// The platforms whose bulk sign-ups are watched, each named by the server it
// signs people in or up on; their other sites never count.
const (
	PlatformOpenAI    = "auth.openai.com"
	PlatformGoogle    = "accounts.google.com"
	PlatformMicrosoft = "signup.live.com"
)

var platforms = []string{PlatformOpenAI, PlatformGoogle, PlatformMicrosoft}

// helperSites are SMS-code, temp-mail and captcha-solving services, which bulk
// sign-ups lean on; anti-detect browsers are left out, as ordinary users run them.
var helperSites = map[string]bool{
	"sms-activate.org": true, "sms-activate.io": true, "hero-sms.com": true, "5sim.net": true,
	"smspva.com": true, "onlinesim.io": true, "onlinesim.ru": true, "grizzlysms.com": true,
	"sms-man.com": true, "smshub.org": true, "tiger-sms.com": true, "vak-sms.com": true,
	"smspool.net": true, "daisysms.com": true, "textverified.com": true, "receive-smss.com": true,
	"sms24.me": true, "quackr.io": true,
	"temp-mail.org": true, "temp-mail.io": true, "tempmail.lol": true, "tempmail.plus": true,
	"mail.tm": true, "mail.gw": true, "1secmail.com": true, "guerrillamail.com": true,
	"sharklasers.com": true, "mailinator.com": true, "10minutemail.com": true, "yopmail.com": true,
	"maildrop.cc": true, "dropmail.me": true, "emailnator.com": true, "mohmal.com": true,
	"linshiyouxiang.net": true, "22.do": true, "bccto.me": true,
	"2captcha.com": true, "rucaptcha.com": true, "anti-captcha.com": true, "capsolver.com": true,
	"capmonster.cloud": true, "yescaptcha.com": true, "nopecha.com": true, "deathbycaptcha.com": true,
	"ez-captcha.com": true, "cap.guru": true, "chaojiying.com": true, "chaojiying.net": true, "jfbym.com": true,
}

// helperSite names the helper service host belongs to, or "" for any other host.
func helperSite(host string) string {
	for h := host; ; {
		if helperSites[h] {
			return h
		}
		_, rest, ok := strings.Cut(h, ".")
		if !ok || !strings.Contains(rest, ".") {
			return ""
		}
		h = rest
	}
}

// signInServers maps every sign-in or sign-up server to its platform.
var signInServers = map[string]string{
	"auth.openai.com":     PlatformOpenAI,
	"auth0.openai.com":    PlatformOpenAI,
	"accounts.google.com": PlatformGoogle,
	"signup.live.com":     PlatformMicrosoft,
}

// Signal is one rule one account tripped, with what made it trip.
type Signal struct {
	Email   string   `json:"email"`
	Rule    string   `json:"rule"`
	Level   string   `json:"level"`
	Measure string   `json:"measure"`
	Count   int64    `json:"count"`
	Limit   int64    `json:"limit"`
	Window  int      `json:"window"`
	Samples []string `json:"samples,omitempty"`
	Bytes   int64    `json:"bytes,omitempty"`
	At      int64    `json:"at"`
}

// Rules are the thresholds, per account and per server; a zero turns a check
// off. Windows are minutes and stay within the hour the detector remembers.
type Rules struct {
	SpamAttempts  int `json:"spamAttempts" example:"10"`
	SpamWindowMin int `json:"spamWindowMin" example:"10"`

	BTAttempts  int `json:"btAttempts" example:"30"`
	BTWindowMin int `json:"btWindowMin" example:"10"`

	ScanIPs          int `json:"scanIps" example:"150"`
	ScanPortsOnIP    int `json:"scanPortsOnIp" example:"50"`
	ScanSensitiveIPs int `json:"scanSensitiveIps" example:"20"`
	ScanWindowMin    int `json:"scanWindowMin" example:"5"`

	FloodPerDest int `json:"floodPerDest" example:"1000"`
	FloodTotal   int `json:"floodTotal" example:"3000"`

	CrawlerConns     int `json:"crawlerConns" example:"4000"`
	CrawlerHosts     int `json:"crawlerHosts" example:"600"`
	CrawlerWindowMin int `json:"crawlerWindowMin" example:"10"`
	CrawlerWindows   int `json:"crawlerWindows" example:"2"`

	SpeedTestsPerHour int `json:"speedTestsPerHour" example:"5"`
	SpeedTestsPerDay  int `json:"speedTestsPerDay" example:"15"`
	SpeedTestGapMin   int `json:"speedTestGapMin" example:"3"`

	FullSpeedMbps      int `json:"fullSpeedMbps" example:"100"`
	FullSpeedWarnMin   int `json:"fullSpeedWarnMin" example:"120"`
	FullSpeedStrikeMin int `json:"fullSpeedStrikeMin" example:"240"`

	OpenAIAuthMinPerHour      int `json:"openaiAuthMinPerHour" example:"10"`
	OpenAIAuthMinPerDay       int `json:"openaiAuthMinPerDay" example:"30"`
	GoogleAuthMinPerHour      int `json:"googleAuthMinPerHour" example:"30"`
	GoogleAuthMinPerDay       int `json:"googleAuthMinPerDay" example:"0"`
	MicrosoftSignupMinPerHour int `json:"microsoftSignupMinPerHour" example:"10"`
	MicrosoftSignupMinPerDay  int `json:"microsoftSignupMinPerDay" example:"20"`
	GoogleAuthHours           int `json:"googleAuthHours" example:"3"`
}

// registerLimits are how many minutes at platform's sign-in server an hour and
// a day may hold, and in how many clock hours of a day the hourly limit must break.
func (r Rules) registerLimits(platform string) (perHour, perDay, hours int) {
	switch platform {
	case PlatformOpenAI:
		return r.OpenAIAuthMinPerHour, r.OpenAIAuthMinPerDay, 1
	case PlatformGoogle:
		return r.GoogleAuthMinPerHour, r.GoogleAuthMinPerDay, max(1, r.GoogleAuthHours)
	case PlatformMicrosoft:
		return r.MicrosoftSignupMinPerHour, r.MicrosoftSignupMinPerDay, 1
	}
	return 0, 0, 0
}

// DefaultRules are the starting thresholds: a week of observing on real traffic
// is meant to tune them before any server bans.
func DefaultRules() Rules {
	return Rules{
		SpamAttempts: 10, SpamWindowMin: 10,
		BTAttempts: 30, BTWindowMin: 10,
		ScanIPs: 150, ScanPortsOnIP: 50, ScanSensitiveIPs: 20, ScanWindowMin: 5,
		FloodPerDest: 1000, FloodTotal: 3000,
		CrawlerConns: 4000, CrawlerHosts: 600, CrawlerWindowMin: 10, CrawlerWindows: 2,
		SpeedTestsPerHour: 5, SpeedTestsPerDay: 15, SpeedTestGapMin: 3,
		FullSpeedMbps: 100, FullSpeedWarnMin: 120, FullSpeedStrikeMin: 240,
		OpenAIAuthMinPerHour: 10, OpenAIAuthMinPerDay: 30, GoogleAuthMinPerHour: 30, GoogleAuthHours: 3,
		MicrosoftSignupMinPerHour: 10, MicrosoftSignupMinPerDay: 20,
	}
}

// ringMinutes is how far back the detector remembers connections.
const ringMinutes = 60

// Normalized keeps each window inside what the detector remembers, so a stored
// setting can never ask for history it does not have.
func (r Rules) Normalized() Rules {
	window := func(v, fallback int) int {
		if v <= 0 {
			return fallback
		}
		return min(v, ringMinutes)
	}
	d := DefaultRules()
	r.SpamWindowMin = window(r.SpamWindowMin, d.SpamWindowMin)
	r.BTWindowMin = window(r.BTWindowMin, d.BTWindowMin)
	r.ScanWindowMin = window(r.ScanWindowMin, d.ScanWindowMin)
	r.CrawlerWindowMin = window(r.CrawlerWindowMin, d.CrawlerWindowMin)
	if r.CrawlerWindows <= 0 {
		r.CrawlerWindows = d.CrawlerWindows
	}
	r.CrawlerWindows = max(1, min(r.CrawlerWindows, ringMinutes/r.CrawlerWindowMin))
	if r.SpeedTestGapMin <= 0 {
		r.SpeedTestGapMin = d.SpeedTestGapMin
	}
	return r
}

// sensitivePorts are remote-login and database ports, the usual targets of a
// scan or a password-guessing run.
var sensitivePorts = map[int]bool{
	22: true, 23: true, 135: true, 139: true, 445: true, 1433: true, 1521: true, 3306: true,
	3389: true, 5432: true, 5900: true, 6379: true, 9200: true, 11211: true, 27017: true,
}

package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// The modes a server runs abuse detection in.
const (
	AbuseModeOff     = "off"
	AbuseModeObserve = "observe"
	AbuseModeEnforce = "enforce"
)

// What happened to a hit, as an event records it. Blocked is a network that may
// not sign up again that day.
const (
	AbuseActionObserved = "observed"
	AbuseActionWarned   = "warned"
	AbuseActionNoticed  = "noticed"
	AbuseActionBanned   = "banned"
	AbuseActionLocked   = "locked"
	AbuseActionHeld     = "held"
	AbuseActionBlocked  = "blocked"
)

// The sign-up guard's hits are the panel's own, from its user page.
const (
	AbuseRuleSignup     = "signup"
	abuseMeasureSignups = "signups"
)

// Ban kinds a BanRecord can hold.
const (
	BanKindAbuse   = "abuse"
	BanKindIPLimit = "iplimit"
)

// The policy decided for the abuse checks: each strike bans for 30 minutes, and
// a fourth within 30 days locks the account until an admin lifts it.
const (
	AbuseBanMinutes   = 30
	AbuseStrikeWindow = 30 * 24 * time.Hour
	AbuseStrikesLimit = 3
	abuseEventKeep    = 30 * 24 * time.Hour
)

// What an enforcing server does about a rule's hits.
const (
	AbuseActRecord = "record"
	AbuseActWarn   = "warn"
	AbuseActBan    = "ban"
)

var (
	errAbuseMode   = errors.New("the mode must be off, observe or enforce")
	errAbuseAction = errors.New("an action must be record, warn or ban")
)

// AbuseActions say what each rule's hits do on an enforcing server.
type AbuseActions struct {
	Spam      string `json:"spam" example:"ban"`
	BT        string `json:"bt" example:"ban"`
	Scan      string `json:"scan" example:"ban"`
	Flood     string `json:"flood" example:"ban"`
	Crawler   string `json:"crawler" example:"ban"`
	SpeedTest string `json:"speedtest" example:"ban"`
	FullSpeed string `json:"fullspeed" example:"ban"`
	Register  string `json:"register" example:"record"`
}

// For is what a hit of rule does; a rule this panel does not know only records.
func (a AbuseActions) For(rule string) string {
	switch rule {
	case abuse.RuleSpam:
		return a.Spam
	case abuse.RuleBT:
		return a.BT
	case abuse.RuleScan:
		return a.Scan
	case abuse.RuleFlood:
		return a.Flood
	case abuse.RuleCrawler:
		return a.Crawler
	case abuse.RuleSpeedTest:
		return a.SpeedTest
	case abuse.RuleFullSpeed:
		return a.FullSpeed
	case abuse.RuleRegister:
		return a.Register
	}
	return AbuseActRecord
}

func (a AbuseActions) valid() bool {
	for _, act := range []string{a.Spam, a.BT, a.Scan, a.Flood, a.Crawler, a.SpeedTest, a.FullSpeed, a.Register} {
		if act != AbuseActRecord && act != AbuseActWarn && act != AbuseActBan {
			return false
		}
	}
	return true
}

// SignupGuard watches sign-ups on the user page: a network reaching Limit in a
// day is reported, and while Action is ban refused until the day is over.
type SignupGuard struct {
	Limit  int    `json:"limit" example:"3"`
	Action string `json:"action" example:"record"`
}

// AbuseSettings are what the admin tunes: the thresholds every detecting server
// checks, what an enforcing server does about each rule, and the sign-up guard.
type AbuseSettings struct {
	Thresholds abuse.Rules  `json:"rules"`
	Actions    AbuseActions `json:"actions"`
	Signup     SignupGuard  `json:"signup"`
}

func DefaultAbuseSettings() AbuseSettings {
	return AbuseSettings{
		Thresholds: abuse.DefaultRules(),
		Actions: AbuseActions{
			Spam: AbuseActBan, BT: AbuseActBan, Scan: AbuseActBan, Flood: AbuseActBan,
			Crawler: AbuseActBan, SpeedTest: AbuseActBan, FullSpeed: AbuseActBan,
			// Bulk sign-ups are guessed from busy minutes, so they only record until tuned.
			Register: AbuseActRecord,
		},
		Signup: SignupGuard{Limit: 3, Action: AbuseActRecord},
	}
}

// AbuseService turns what the servers detect into warnings, strikes, bans and
// locks, and keeps the record people see afterwards.
type AbuseService struct {
	settingService SettingService
}

// Settings are the stored ones; anything missing from them keeps its default.
func (s *AbuseService) Settings() AbuseSettings {
	out := DefaultAbuseSettings()
	raw, err := s.settingService.getString("abuseSettings")
	if err != nil || strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		logger.Warning("abuse: the stored settings are unreadable, using the defaults:", err)
		return DefaultAbuseSettings()
	}
	out.Thresholds = out.Thresholds.Normalized()
	return out
}

func (s *AbuseService) SetSettings(settings AbuseSettings) error {
	signup := settings.Signup.Action
	if !settings.Actions.valid() || (signup != AbuseActRecord && signup != AbuseActBan) {
		return errAbuseAction
	}
	settings.Thresholds = settings.Thresholds.Normalized()
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.settingService.setString("abuseSettings", string(raw))
}

// Rules are the thresholds every detecting server uses.
func (s *AbuseService) Rules() abuse.Rules {
	return s.Settings().Thresholds
}

// Mode is how server nodeID runs detection; 0 is the panel's own core.
func (s *AbuseService) Mode(nodeID int) string {
	var mode string
	if nodeID == 0 {
		mode, _ = s.settingService.getString("abuseLocalMode")
	} else {
		var node model.Node
		if database.GetDB().Select("abuse_mode").First(&node, nodeID).Error == nil {
			mode = node.AbuseMode
		}
	}
	if mode != AbuseModeObserve && mode != AbuseModeEnforce {
		return AbuseModeOff
	}
	return mode
}

func (s *AbuseService) SetMode(nodeID int, mode string) error {
	if mode != AbuseModeOff && mode != AbuseModeObserve && mode != AbuseModeEnforce {
		return errAbuseMode
	}
	if nodeID == 0 {
		return s.settingService.setString("abuseLocalMode", mode)
	}
	res := database.GetDB().Model(&model.Node{}).Where("id = ?", nodeID).Update("abuse_mode", mode)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return common.NewErrorf("node %d not found", nodeID)
	}
	return (&NodeService{}).MarkNodeDirty(nodeID)
}

// HandleSignals applies the policy to what server nodeID found; changed says a
// ban began, so the servers' configs must be refreshed.
func (s *AbuseService) HandleSignals(nodeID int, signals []abuse.Signal, now time.Time) (changed bool, err error) {
	mode := s.Mode(nodeID)
	if mode == AbuseModeOff || len(signals) == 0 {
		return false, nil
	}
	owners, err := (&IpLimitService{}).ChainTransitOwners()
	if err != nil {
		return false, err
	}
	actions := s.Settings().Actions
	db := database.GetDB()
	for _, sig := range signals {
		email := sig.Email
		if owner, ok := owners[email]; ok {
			email = owner
		}
		var exists int64
		if err := db.Model(&model.ClientRecord{}).Where("email = ?", email).Count(&exists).Error; err != nil {
			return changed, err
		}
		if exists == 0 {
			continue
		}
		event := model.AbuseEvent{
			Email: email, NodeId: nodeID, Rule: sig.Rule, Level: sig.Level, Measure: sig.Measure,
			Count: sig.Count, Limit: sig.Limit, Window: sig.Window, Bytes: sig.Bytes, At: now.Unix(),
		}
		if len(sig.Samples) > 0 {
			raw, _ := json.Marshal(sig.Samples)
			event.Samples = string(raw)
		}
		event.Action = decide(mode, actions.For(event.Rule), event.Level)
		banned, err := s.apply(db, &event, now)
		if err != nil {
			return changed, err
		}
		changed = changed || banned
	}
	return changed, nil
}

// decide is what a hit comes to before a running ban is weighed: an observing
// server only records, an enforcing one does what the rule is set to.
func decide(mode, action, level string) string {
	switch {
	case mode != AbuseModeEnforce:
		return AbuseActionObserved
	case level == abuse.LevelSuspect:
		return AbuseActionNoticed
	case action == AbuseActBan && level != abuse.LevelWarn:
		return AbuseActionBanned
	case action == AbuseActBan || action == AbuseActWarn:
		return AbuseActionWarned
	}
	return AbuseActionNoticed
}

// apply records one hit and bans if it was decided to, inside one transaction so
// two servers reporting the same account at once cannot both count a strike.
func (s *AbuseService) apply(db *gorm.DB, event *model.AbuseEvent, now time.Time) (bool, error) {
	banned := false
	err := db.Transaction(func(tx *gorm.DB) error {
		var strikes int64
		if event.Action == AbuseActionBanned {
			active, err := activeAbuseBan(tx, event.Email, now)
			if err != nil {
				return err
			}
			if strikes, err = strikesSince(tx, event.Email, now.Add(-AbuseStrikeWindow)); err != nil {
				return err
			}
			switch {
			case active != nil:
				event.Action = AbuseActionHeld
			case strikes+1 > AbuseStrikesLimit:
				event.Action = AbuseActionLocked
			}
		}
		if err := tx.Create(event).Error; err != nil {
			return err
		}
		if event.Action != AbuseActionBanned && event.Action != AbuseActionLocked {
			return nil
		}
		record := model.BanRecord{
			Email: event.Email, Kind: BanKindAbuse, Rule: event.Rule, Strike: int(strikes) + 1,
			Reason:  AbuseRuleLabel(event.Rule) + "：" + AbuseEvidence(*event),
			EventId: event.Id, BannedAt: now.Unix(), ExpiresAt: now.Add(AbuseBanMinutes * time.Minute).Unix(),
		}
		if event.Action == AbuseActionLocked {
			record.ExpiresAt = 0
		}
		banned = true
		return tx.Create(&record).Error
	})
	return banned, err
}

func strikesSince(tx *gorm.DB, email string, since time.Time) (int64, error) {
	var n int64
	err := tx.Model(&model.BanRecord{}).
		Where("email = ? AND kind = ? AND banned_at >= ? AND forgiven = ?", email, BanKindAbuse, since.Unix(), false).
		Count(&n).Error
	return n, err
}

func activeAbuseBan(tx *gorm.DB, email string, now time.Time) (*model.BanRecord, error) {
	var rec model.BanRecord
	err := tx.Where("email = ? AND kind IN ? AND lifted_at = 0 AND (expires_at = 0 OR expires_at > ?)", email, []string{BanKindAbuse, BanKindAIPatrol}, now.Unix()).
		Order("banned_at DESC").First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &rec, err
}

// ActiveBans are the abuse bans and locks running now, for the configs.
func (s *AbuseService) ActiveBans(now time.Time) ([]model.BanRecord, error) {
	var recs []model.BanRecord
	err := database.GetDB().Where("kind IN ? AND lifted_at = 0 AND (expires_at = 0 OR expires_at > ?)", []string{BanKindAbuse, BanKindAIPatrol}, now.Unix()).
		Order("email").Find(&recs).Error
	return recs, err
}

// AbuseStatus is an account's standing: its ban running now, if any, and the
// strikes it has within the window.
type AbuseStatus struct {
	Ban     *model.BanRecord `json:"ban"`
	Strikes int              `json:"strikes" example:"1"`
	Limit   int              `json:"limit" example:"3"`
}

func (s *AbuseService) Status(email string, now time.Time) (AbuseStatus, error) {
	db := database.GetDB()
	ban, err := activeAbuseBan(db, email, now)
	if err != nil {
		return AbuseStatus{}, err
	}
	strikes, err := strikesSince(db, email, now.Add(-AbuseStrikeWindow))
	return AbuseStatus{Ban: ban, Strikes: int(strikes), Limit: AbuseStrikesLimit}, err
}

// History is an account's bans of either kind since since, newest first.
func (s *AbuseService) History(email string, since time.Time) ([]model.BanRecord, error) {
	var recs []model.BanRecord
	err := database.GetDB().Where("email = ? AND banned_at >= ?", email, since.Unix()).
		Order("banned_at DESC, id DESC").Find(&recs).Error
	return recs, err
}

// Lift ends an account's running abuse ban early. Ending a lock also clears its
// strikes: the admin has heard the person out.
func (s *AbuseService) Lift(email string, now time.Time) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		ban, err := activeAbuseBan(tx, email, now)
		if err != nil {
			return err
		}
		if ban == nil {
			return common.NewError("no ban is running for", email)
		}
		if err := tx.Model(ban).Update("lifted_at", now.Unix()).Error; err != nil {
			return err
		}
		if ban.ExpiresAt == 0 {
			return forgive(tx, email)
		}
		return nil
	})
}

// Forgive clears an account's strikes; its history keeps them, marked.
func (s *AbuseService) Forgive(email string) error {
	return forgive(database.GetDB(), email)
}

func forgive(tx *gorm.DB, email string) error {
	if err := tx.Model(&model.AIPatrolViolation{}).Where("email = ? AND forgiven = ?", email, false).Update("forgiven", true).Error; err != nil {
		return err
	}
	return tx.Model(&model.BanRecord{}).Where("email = ? AND kind = ? AND forgiven = ?", email, BanKindAbuse, false).
		Update("forgiven", true).Error
}

// Events are the latest hits, newest first, for the admin's review.
func (s *AbuseService) Events(limit int) ([]model.AbuseEvent, error) {
	var events []model.AbuseEvent
	err := database.GetDB().Order("at DESC, id DESC").Limit(limit).Find(&events).Error
	return events, err
}

// Prune drops hits older than they are kept and sign-ups older than a day; ban
// records stay as history.
func (s *AbuseService) Prune(now time.Time) error {
	db := database.GetDB()
	if err := db.Where("at < ?", now.Add(-abuseEventKeep).Unix()).Delete(&model.AbuseEvent{}).Error; err != nil {
		return err
	}
	return db.Where("at < ?", now.Add(-signupDay).Unix()).Delete(&model.PortalSignup{}).Error
}

// AbuseRuleLabel names a rule the way the people affected read it.
func AbuseRuleLabel(rule string) string {
	switch rule {
	case AbuseRuleAIPatrol:
		return "Codex AI 巡查违规"
	case abuse.RuleSpam:
		return "发送垃圾邮件"
	case abuse.RuleBT:
		return "BT 下载"
	case abuse.RuleScan:
		return "端口扫描"
	case abuse.RuleFlood:
		return "网络攻击"
	case abuse.RuleCrawler:
		return "爬虫"
	case abuse.RuleSpeedTest:
		return "反复测速"
	case abuse.RuleFullSpeed:
		return "长时间满速"
	case abuse.RuleRegister:
		return "批量注册 AI/Google/微软账号"
	case BanKindIPLimit:
		return "同时在线 IP 超出上限"
	case AbuseRuleSignup:
		return "批量注册"
	}
	return rule
}

// AbuseEventLabel names a hit's rule and marks a suspect one, too weak to count.
func AbuseEventLabel(e model.AbuseEvent) string {
	if e.Level == abuse.LevelSuspect {
		return AbuseRuleLabel(e.Rule) + "（可疑）"
	}
	return AbuseRuleLabel(e.Rule)
}

func windowText(seconds int) string {
	switch {
	case seconds >= 3600 && seconds%3600 == 0:
		return fmt.Sprintf("%d 小时", seconds/3600)
	case seconds >= 60:
		return fmt.Sprintf("%d 分钟", seconds/60)
	}
	return fmt.Sprintf("%d 秒", seconds)
}

// signInServerName says which platform's sign-in server a bulk sign-up hit was at.
func signInServerName(platform string) string {
	switch platform {
	case abuse.PlatformOpenAI:
		return " OpenAI（ChatGPT）的登录/注册服务器"
	case abuse.PlatformGoogle:
		return " Google（含 Gemini）的登录/注册服务器"
	case abuse.PlatformMicrosoft:
		return "微软的账号注册服务器"
	}
	return "账号登录/注册服务器"
}

// AbuseEvidence says in one sentence what a hit measured.
func AbuseEvidence(e model.AbuseEvent) string {
	w := windowText(e.Window)
	switch e.Measure {
	case "ai_patrol":
		var samples []string
		if json.Unmarshal([]byte(e.Samples), &samples) == nil && len(samples) > 0 {
			return samples[0]
		}
	case abuse.MeasureAttempts:
		if e.Rule == abuse.RuleSpam {
			return fmt.Sprintf("%s内连接邮件端口 25 共 %d 次", w, e.Count)
		}
		return fmt.Sprintf("%s内发起 BT 连接 %d 次", w, e.Count)
	case abuse.MeasureIPs:
		return fmt.Sprintf("%s内连接了 %d 个不同 IP", w, e.Count)
	case abuse.MeasurePorts:
		return fmt.Sprintf("%s内连接同一 IP 的 %d 个端口", w, e.Count)
	case abuse.MeasureSensitive:
		return fmt.Sprintf("%s内连接了 %d 个 IP 的远程登录或数据库端口", w, e.Count)
	case abuse.MeasurePerDest:
		return fmt.Sprintf("1 分钟内向同一目标发起 %d 次连接", e.Count)
	case abuse.MeasureConns:
		return fmt.Sprintf("%s内发起 %d 次连接", w, e.Count)
	case abuse.MeasureHosts:
		return fmt.Sprintf("%s内访问了 %d 个不同网站", w, e.Count)
	case abuse.MeasureTestsHour:
		return fmt.Sprintf("1 小时内测速 %d 次", e.Count)
	case abuse.MeasureTestsDay:
		return fmt.Sprintf("24 小时内测速 %d 次", e.Count)
	case abuse.MeasureMinutes:
		return fmt.Sprintf("连续满速 %d 分钟", e.Count)
	case abuse.MeasureRegisterHour, abuse.MeasureRegisterDay, abuse.MeasureRegisterHours,
		abuse.MeasureRegisterBurst, abuse.MeasureRegisterHelped:
		return registerEvidence(e, w)
	case abuseMeasureSignups:
		var samples []string
		if json.Unmarshal([]byte(e.Samples), &samples) == nil && len(samples) > 0 {
			return fmt.Sprintf("网络 %s 在 24 小时内注册了 %d 个账号", samples[0], e.Count)
		}
		return fmt.Sprintf("同一网络在 24 小时内注册了 %d 个账号", e.Count)
	}
	return ""
}

// registerEvidence says how long an account kept at a platform's sign-in server,
// why that was judged so, and what the account moved in that hour.
func registerEvidence(e model.AbuseEvent, window string) string {
	var samples []string
	_ = json.Unmarshal([]byte(e.Samples), &samples)
	platform, helpers := "", []string(nil)
	if len(samples) > 0 {
		platform, helpers = samples[0], samples[1:]
	}
	server := signInServerName(platform)
	var b strings.Builder
	switch e.Measure {
	case abuse.MeasureRegisterHours:
		fmt.Fprintf(&b, "24 小时内有 %d 个小时，每小时超过 %d 分钟在连%s", e.Count, e.Limit, server)
	default:
		fmt.Fprintf(&b, "%s内有 %d 分钟在连%s", window, e.Count, server)
	}
	switch {
	case e.Level == abuse.LevelSuspect:
		b.WriteString("，但没持续几个小时，也没用过接码/临时邮箱/打码网站")
	case e.Measure == abuse.MeasureRegisterBurst:
		b.WriteString("，几乎没停")
	}
	if len(helpers) > 0 {
		b.WriteString("，当天还用过接码/临时邮箱/打码网站 " + strings.Join(helpers, "、"))
	}
	if e.Bytes > 0 {
		b.WriteString("；这 1 小时流量 " + common.FormatTraffic(e.Bytes))
	}
	return b.String()
}

package tgbot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func scanHit(email string) abuse.Signal {
	return abuse.Signal{
		Email: email, Rule: abuse.RuleScan, Level: abuse.LevelStrike, Measure: abuse.MeasurePorts,
		Count: 52, Limit: 50, Window: 300, Samples: []string{"198.51.100.7"},
	}
}

// abuseOn seeds alice, bound to 5150, with the panel's own core in mode.
func abuseOn(t *testing.T, mode string) *model.ClientRecord {
	t.Helper()
	ib := seedVlessInbound(t, "a")
	alice := seedClient(t, "alice", []int{ib}, nil)
	bindTelegram(t, "alice", 5150)
	turnOffDaily(t, alice)
	if err := (&service.AbuseService{}).SetMode(0, mode); err != nil {
		t.Fatal(err)
	}
	withProbe(t)
	return alice
}

// A ban reaches the person once, saying why, which strike and when it ends, and
// the admin once, with a button that lifts it in a message of its own.
func TestAbuseBanNoticeReachesThePersonAndTheAdminOnce(t *testing.T) {
	tb, rec := newPiggerBot(t)
	alice := abuseOn(t, service.AbuseModeEnforce)
	now := time.Now()
	if _, err := (&service.AbuseService{}).HandleSignals(0, []abuse.Signal{scanHit("alice")}, now); err != nil {
		t.Fatal(err)
	}
	rec.reset()

	tb.sendAccountNotifications(context.Background(), now)
	tb.sendAccountNotifications(context.Background(), now.Add(time.Minute))

	toUser, toAdmin := sentTo(rec, 5150), sentTo(rec, adminTgID)
	if len(toUser) != 1 {
		t.Fatalf("user notices = %d, want 1", len(toUser))
	}
	for _, want := range []string{"账号暂时封禁", "端口扫描", "第 1 次", "还剩"} {
		if !strings.Contains(toUser[0].Text, want) {
			t.Errorf("user notice lacks %q:\n%s", want, toUser[0].Text)
		}
	}
	if len(toAdmin) != 1 || !strings.Contains(toAdmin[0].Text, "198.51.100.7") {
		t.Fatalf("admin notices = %d (%q), want one with the evidence", len(toAdmin), firstText(toAdmin))
	}
	lift := toAdmin[0].dataFor(t, "解除封禁")
	if want := fmt.Sprintf("pg:a:u:%d!", alice.Id); lift != want {
		t.Fatalf("lift button = %q, want %q", lift, want)
	}
	tb.tap(adminTgID, lift)
	if status, _ := (&service.AbuseService{}).Status("alice", time.Now()); status.Ban != nil {
		t.Fatalf("the alert's button left the ban: %+v", status.Ban)
	}
	if n := rec.count("editMessageText"); n != 0 {
		t.Errorf("the lift edited %d messages, want the alert kept", n)
	}
}

// While a server only observes, the admin hears of a hit once an hour and the
// person hears nothing.
func TestObservedHitsReachOnlyTheAdminOncePerHour(t *testing.T) {
	tb, rec := newPiggerBot(t)
	abuseOn(t, service.AbuseModeObserve)
	now := time.Now()
	for i := range 3 {
		if _, err := (&service.AbuseService{}).HandleSignals(0, []abuse.Signal{scanHit("alice")}, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	rec.reset()

	tb.sendAccountNotifications(context.Background(), now.Add(3*time.Minute))

	if n := len(sentTo(rec, 5150)); n != 0 {
		t.Errorf("the person got %d notices from an observing server", n)
	}
	if toAdmin := sentTo(rec, adminTgID); len(toAdmin) != 1 || !strings.Contains(toAdmin[0].Text, "观察模式") {
		t.Errorf("admin notices = %d (%q), want one observed hit", len(toAdmin), firstText(toAdmin))
	}
}

// The history says when and why each ban happened, IP-limit ones included.
func TestBanHistoryShowsWhenAndWhy(t *testing.T) {
	tb, rec := newPiggerBot(t)
	abuseOn(t, service.AbuseModeEnforce)
	now := time.Now()
	ipBan := model.BanRecord{
		Email: "alice", Kind: service.BanKindIPLimit, Rule: service.BanKindIPLimit,
		Reason: "同时在线 IP 超过上限（3 个），暂停 203.0.113.9", Network: "203.0.113.9",
		BannedAt: now.Add(-2 * time.Hour).Unix(), ExpiresAt: now.Add(-90 * time.Minute).Unix(),
	}
	if err := database.GetDB().Create(&ipBan).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (&service.AbuseService{}).HandleSignals(0, []abuse.Signal{scanHit("alice")}, now); err != nil {
		t.Fatal(err)
	}

	tb.tap(5150, "pg:bans")

	view := rec.last(t)
	for _, want := range []string{"封禁记录", "同时在线 IP 超过上限", "端口扫描", "30 分钟", time.Unix(ipBan.BannedAt, 0).In(accountLocation).Format("01-02 15:04")} {
		if !strings.Contains(view.Text, want) {
			t.Errorf("history lacks %q:\n%s", want, view.Text)
		}
	}
}

// A locked account says so on its own home, with whom to ask.
func TestLockedAccountShowsTheLockAtHome(t *testing.T) {
	tb, rec := newPiggerBot(t)
	abuseOn(t, service.AbuseModeEnforce)
	now := time.Now()
	for i := range 4 {
		if _, err := (&service.AbuseService{}).HandleSignals(0, []abuse.Signal{scanHit("alice")}, now.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	tb.send(5150, "/start")

	if home := rec.last(t); !strings.Contains(home.Text, "账号已停用") || !strings.Contains(home.Text, "联系管理员") {
		t.Fatalf("a locked account's home says:\n%s", home.Text)
	}
}

func crawlerHit(email string) abuse.Signal {
	return abuse.Signal{
		Email: email, Rule: abuse.RuleCrawler, Level: abuse.LevelStrike, Measure: abuse.MeasureHosts,
		Count: 640, Limit: 600, Window: 600,
	}
}

// Hits that only record wait for the 09:00 digest unless they are attacks, so a
// week of observing does not page the admin all day.
func TestRecordedHitsWaitForTheDailyDigest(t *testing.T) {
	tb, rec := newPiggerBot(t)
	abuseOn(t, service.AbuseModeObserve)
	afternoon := time.Date(2026, 10, 6, 15, 0, 0, 0, accountLocation)
	s := &service.AbuseService{}
	for i := range 3 {
		if _, err := s.HandleSignals(0, []abuse.Signal{crawlerHit("alice")}, afternoon.Add(time.Duration(i)*20*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetMode(0, service.AbuseModeEnforce); err != nil {
		t.Fatal(err)
	}
	spam := abuse.Signal{Email: "alice", Rule: abuse.RuleSpam, Level: abuse.LevelStrike, Measure: abuse.MeasureAttempts, Count: 12, Limit: 10, Window: 600}
	if _, err := s.HandleSignals(0, []abuse.Signal{spam}, afternoon.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rec.reset()

	tb.sendAccountNotifications(context.Background(), afternoon.Add(41*time.Minute))
	tb.sendAccountNotifications(context.Background(), afternoon.Add(17*time.Hour+59*time.Minute))
	for _, m := range sentTo(rec, adminTgID) {
		if strings.Contains(m.Text, "爬虫") {
			t.Fatalf("a crawler hit reached the admin before the digest: %q", m.Text)
		}
	}
	nextMorning := time.Date(2026, 10, 7, 9, 0, 30, 0, accountLocation)
	tb.sendAccountNotifications(context.Background(), nextMorning)
	tb.sendAccountNotifications(context.Background(), nextMorning.Add(time.Minute))

	var digests []botCall
	for _, m := range sentTo(rec, adminTgID) {
		if strings.Contains(m.Text, "防滥用日报") {
			digests = append(digests, m)
		}
	}
	if len(digests) != 1 {
		t.Fatalf("digests = %d, want 1", len(digests))
	}
	for _, want := range []string{"爬虫", "alice 3 次"} {
		if !strings.Contains(digests[0].Text, want) {
			t.Errorf("digest lacks %q:\n%s", want, digests[0].Text)
		}
	}
	if strings.Contains(digests[0].Text, "垃圾邮件") {
		t.Errorf("the digest lists a hit that banned:\n%s", digests[0].Text)
	}
}

// The digest keeps suspect bulk sign-ups apart from sure ones, so a heavy Google
// evening never reads as a bot.
func TestDigestKeepsSuspectSignUpsApart(t *testing.T) {
	tb, rec := newPiggerBot(t)
	abuseOn(t, service.AbuseModeEnforce)
	afternoon := time.Date(2026, 10, 6, 15, 0, 0, 0, accountLocation)
	hit := func(level string) abuse.Signal {
		return abuse.Signal{
			Email: "alice", Rule: abuse.RuleRegister, Level: level, Measure: abuse.MeasureRegisterHour,
			Count: 35, Limit: 30, Window: 3600, Samples: []string{abuse.PlatformGoogle},
		}
	}
	s := &service.AbuseService{}
	for i, level := range []string{abuse.LevelSuspect, abuse.LevelSuspect, abuse.LevelStrike} {
		if _, err := s.HandleSignals(0, []abuse.Signal{hit(level)}, afternoon.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	rec.reset()
	tb.sendAccountNotifications(context.Background(), time.Date(2026, 10, 7, 9, 0, 30, 0, accountLocation))
	var digest string
	for _, m := range sentTo(rec, adminTgID) {
		if strings.Contains(m.Text, "防滥用日报") {
			digest = m.Text
		}
	}
	for _, want := range []string{"• 批量注册 AI/Google/微软账号：alice 1 次", "• 批量注册 AI/Google/微软账号（可疑）：alice 2 次"} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest lacks %q:\n%s", want, digest)
		}
	}
}

// A rule set to warn tells the person what was seen, at most once an hour.
func TestARuleSetToWarnTellsThePersonOnceAnHour(t *testing.T) {
	tb, rec := newPiggerBot(t)
	abuseOn(t, service.AbuseModeEnforce)
	s := &service.AbuseService{}
	settings := s.Settings()
	settings.Actions.Crawler = service.AbuseActWarn
	if err := s.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Hour)
	for _, at := range []time.Time{now, now.Add(10 * time.Minute)} {
		if _, err := s.HandleSignals(0, []abuse.Signal{crawlerHit("alice")}, at); err != nil {
			t.Fatal(err)
		}
		tb.sendAccountNotifications(context.Background(), at.Add(time.Minute))
	}

	toUser := sentTo(rec, 5150)
	if len(toUser) != 1 {
		t.Fatalf("warnings = %d, want 1 in the hour", len(toUser))
	}
	for _, want := range []string{"爬虫", "640 个不同网站", "不计违规"} {
		if !strings.Contains(toUser[0].Text, want) {
			t.Errorf("warning lacks %q:\n%s", want, toUser[0].Text)
		}
	}
	if status, _ := s.Status("alice", now.Add(11*time.Minute)); status.Ban != nil || status.Strikes != 0 {
		t.Fatalf("a warning cost %+v", status)
	}
}

// The full-speed warning promises a ban only while the rule bans.
func TestFullSpeedWarningPromisesABanOnlyWhileTheRuleBans(t *testing.T) {
	tb, rec := newPiggerBot(t)
	abuseOn(t, service.AbuseModeEnforce)
	s := &service.AbuseService{}
	twoHours := abuse.Signal{
		Email: "alice", Rule: abuse.RuleFullSpeed, Level: abuse.LevelWarn, Measure: abuse.MeasureMinutes,
		Count: 120, Limit: 120, Window: 7200,
	}
	now := time.Now().Truncate(time.Hour)
	if _, err := s.HandleSignals(0, []abuse.Signal{twoHours}, now); err != nil {
		t.Fatal(err)
	}
	tb.sendAccountNotifications(context.Background(), now.Add(time.Minute))
	settings := s.Settings()
	settings.Actions.FullSpeed = service.AbuseActWarn
	if err := s.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleSignals(0, []abuse.Signal{twoHours}, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	tb.sendAccountNotifications(context.Background(), now.Add(2*time.Hour+time.Minute))

	toUser := sentTo(rec, 5150)
	if len(toUser) != 2 {
		t.Fatalf("warnings = %d, want 2", len(toUser))
	}
	if !strings.Contains(toUser[0].Text, "将被暂时封禁") {
		t.Errorf("while the rule bans, the warning says:\n%s", toUser[0].Text)
	}
	if strings.Contains(toUser[1].Text, "封禁") {
		t.Errorf("while the rule only warns, the warning threatens a ban:\n%s", toUser[1].Text)
	}
}

// A network that signed up many accounts reaches the admins with every account
// it made, at once; the digest leaves it out.
func TestSignupReportListsTheAccountsForTheAdmin(t *testing.T) {
	tb, rec := newPiggerBot(t)
	withProbe(t)
	s := &service.AbuseService{}
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, accountLocation)
	for i, name := range []string{"bob", "carol", "dave"} {
		if err := s.Signup(fmt.Sprintf("203.0.113.%d", i+5), now, func() (string, error) { return name, nil }); err != nil {
			t.Fatal(err)
		}
	}

	tb.sendAccountNotifications(context.Background(), now.Add(time.Minute))
	tb.sendAccountNotifications(context.Background(), time.Date(2026, 10, 7, 9, 0, 30, 0, accountLocation))

	toAdmin := sentTo(rec, adminTgID)
	if len(toAdmin) != 1 {
		t.Fatalf("admin messages = %d (%q), want the one report", len(toAdmin), firstText(toAdmin))
	}
	for _, want := range []string{"批量注册", "203.0.113.0/24", "bob、carol、dave", "仅提醒"} {
		if !strings.Contains(toAdmin[0].Text, want) {
			t.Errorf("report lacks %q:\n%s", want, toAdmin[0].Text)
		}
	}
}

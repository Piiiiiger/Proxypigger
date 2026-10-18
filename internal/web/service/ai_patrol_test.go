package service

import (
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"testing"
	"time"
)

func seedAIPatrol(t *testing.T) (int, time.Time, AIPatrolBundle) {
	t.Helper()
	setupAbuse(t, AbuseModeEnforce)
	n := seedAgentNodeRow(t, "AI test")
	db := database.GetDB()
	db.Model(&n).Updates(map[string]any{"abuse_mode": AbuseModeEnforce, "enable": true})
	i := model.Inbound{Tag: "ai-test", NodeID: &n.Id, Enable: true, Port: 443}
	if err := db.Create(&i).Error; err != nil {
		t.Fatal(err)
	}
	var c model.ClientRecord
	db.Where("email = ?", "alice").First(&c)
	if err := db.Create(&model.ClientInbound{ClientId: c.Id, InboundId: i.Id}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	return n.Id, now, aiBundle(now)
}
func aiBundle(now time.Time) AIPatrolBundle {
	var b AIPatrolBundle
	end := now.Add(-time.Minute)
	b.Version = 2
	b.RunID = end.Format("20060102T150405") + "000000Z"
	b.Report.Assessment = "review_needed"
	b.Report.Findings = []AIPatrolFinding{{UserID: "user-1", Severity: "high", Confidence: "high", Category: "scan", Violation: true, Hypothesis: "Sustained scanning", Evidence: []string{"direct.distinct_ports=80"}}}
	b.Identities = map[string]string{"user-1": "alice"}
	c := &b.Evidence.Coverage
	c.WindowEnd = end
	c.WindowStart = end.Add(-12 * time.Hour)
	c.AuditHealth.StartedAt = c.WindowStart.Add(-time.Hour)
	c.AuditHealth.UpdatedAt = end.Add(-time.Second)
	c.Files = append(c.Files, struct {
		Readable bool `json:"readable"`
	}{true})
	u := AIPatrolUser{UserID: "user-1"}
	u.Direct.Connections = 1000
	u.Direct.DistinctPorts = 80
	b.Evidence.Users = []AIPatrolUser{u}
	return b
}
func TestAIPatrolFirstNoticeThenIndependentRepeatSuspends24Hours(t *testing.T) {
	n, now, b := seedAIPatrol(t)
	s := &AIPatrolService{}
	cutoff := now.Add(-2 * time.Minute).Unix()
	if count, err := s.Process(n, cutoff, b, now, false); err != nil || count != 1 {
		t.Fatalf("first %d %v", count, err)
	}
	if len(banRecords(t)) != 0 {
		t.Fatal("first violation banned")
	}
	var first model.AIPatrolViolation
	database.GetDB().First(&first)
	if first.Action != AIPatrolViolationAction {
		t.Fatal(first)
	}
	if count, err := s.Process(n, cutoff, b, now, false); err != nil || count != 0 {
		t.Fatal("duplicate report", count, err)
	}
	database.GetDB().Model(&first).Update("notified_at", now.Unix())
	later := now.Add(13 * time.Hour)
	next := aiBundle(later)
	if count, err := s.Process(n, cutoff, next, later, false); err != nil || count != 1 {
		t.Fatal("repeat", count, err)
	}
	recs := banRecords(t)
	if len(recs) != 1 || recs[0].Kind != BanKindAIPatrol || recs[0].ExpiresAt-recs[0].BannedAt != 86400 {
		t.Fatal(recs)
	}
	status, err := (&AbuseService{}).Status("alice", later)
	if err != nil || status.Ban == nil || status.Strikes != 0 {
		t.Fatalf("AI suspension must not count as fixed-rule strikes: %+v %v", status, err)
	}
	if bans, err := (&AbuseService{}).ActiveBans(later.Add(24 * time.Hour)); err != nil || len(bans) != 0 {
		t.Fatal("did not expire", bans, err)
	}
	if err := (&AbuseService{}).Lift("alice", later.Add(time.Minute)); err != nil {
		t.Fatal("admin cannot lift", err)
	}
}
func TestAIPatrolUndeliveredNoticeCannotEscalate(t *testing.T) {
	n, now, b := seedAIPatrol(t)
	s := &AIPatrolService{}
	cutoff := now.Add(-2 * time.Minute).Unix()
	if _, err := s.Process(n, cutoff, b, now, false); err != nil {
		t.Fatal(err)
	}
	later := now.Add(13 * time.Hour)
	if count, err := s.Process(n, cutoff, aiBundle(later), later, false); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if len(banRecords(t)) != 0 {
		t.Fatal("penalized before notice delivery")
	}
}
func TestAIPatrolPortalAcknowledgementEnablesEscalation(t *testing.T) {
	n, now, b := seedAIPatrol(t)
	s := &AIPatrolService{}
	cutoff := now.Add(-2 * time.Minute).Unix()
	s.Process(n, cutoff, b, now, false)
	var first model.AIPatrolViolation
	database.GetDB().First(&first)
	if err := s.Seen("another-user", []int{first.Id}, now); err != nil {
		t.Fatal(err)
	}
	database.GetDB().First(&first)
	if first.PortalSeenAt != 0 {
		t.Fatal("cross-account receipt")
	}
	if err := s.Seen("alice", []int{first.Id}, now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(13 * time.Hour)
	if _, err := s.Process(n, cutoff, aiBundle(later), later, false); err != nil {
		t.Fatal(err)
	}
	if len(banRecords(t)) != 1 {
		t.Fatal("portal receipt not honored")
	}
}
func TestAIPatrolRejectsWeakStaleIncompleteAndUnattributedFindings(t *testing.T) {
	cases := map[string]func(*AIPatrolBundle){
		"medium confidence":    func(b *AIPatrolBundle) { b.Report.Findings[0].Confidence = "medium" },
		"not a violation":      func(b *AIPatrolBundle) { b.Report.Findings[0].Violation = false },
		"unsupported category": func(b *AIPatrolBundle) { b.Report.Findings[0].Category = "signup" },
		"limited logs":         func(b *AIPatrolBundle) { b.Evidence.Coverage.Limited = true },
		"dropped records":      func(b *AIPatrolBundle) { b.Evidence.Coverage.AuditHealth.DroppedRecords = 1 },
		"parse failure":        func(b *AIPatrolBundle) { b.Evidence.Coverage.UnparsedLines = 1 },
		"partial user":         func(b *AIPatrolBundle) { b.Evidence.Users[0].DetailLimited = true },
		"blocked only":         func(b *AIPatrolBundle) { b.Evidence.Users[0].Direct.Connections = 0 },
		"invented user":        func(b *AIPatrolBundle) { b.Report.Findings[0].UserID = "invented" },
		"relay identity":       func(b *AIPatrolBundle) { b.Identities["user-1"] = "chain-transit-aggregate" },
		"old report schema":    func(b *AIPatrolBundle) { b.Version = 1 },
		"startup window": func(b *AIPatrolBundle) {
			b.Evidence.Coverage.AuditHealth.StartedAt = b.Evidence.Coverage.WindowStart.Add(time.Hour)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			n, now, b := seedAIPatrol(t)
			mutate(&b)
			if count, err := (&AIPatrolService{}).Process(n, now.Add(-2*time.Minute).Unix(), b, now, false); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}
func TestAIPatrolPreviewCutoffAndExpiredStrikeWindow(t *testing.T) {
	n, now, b := seedAIPatrol(t)
	s := &AIPatrolService{}
	cutoff := now.Add(-2 * time.Minute).Unix()
	if count, err := s.Process(n, cutoff, b, now, true); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	var count int64
	database.GetDB().Model(&model.AIPatrolReview{}).Count(&count)
	if count != 0 {
		t.Fatal("preview persisted receipt")
	}
	database.GetDB().Model(&model.AIPatrolViolation{}).Count(&count)
	if count != 0 {
		t.Fatal("preview persisted violation")
	}
	if count, err := s.Process(n, now.Unix(), b, now, false); err != nil || count != 0 {
		t.Fatal("retroactive penalty", count, err)
	}
	// Use a fresh report so the receipt above cannot conceal the expiry check.
	next := now.Add(time.Hour)
	bb := aiBundle(next)
	s.Process(n, cutoff, bb, next, false)
	var first model.AIPatrolViolation
	database.GetDB().First(&first)
	database.GetDB().Model(&first).Update("notified_at", next.Unix())
	later := next.Add(31 * 24 * time.Hour)
	if _, err := s.Process(n, cutoff, aiBundle(later), later, false); err != nil {
		t.Fatal(err)
	}
	if len(banRecords(t)) != 0 {
		t.Fatal("strike did not expire after 30 days")
	}
}
func TestAIPatrolDoesNotDoublePunishRealtimeIncident(t *testing.T) {
	n, now, b := seedAIPatrol(t)
	r := model.BanRecord{Email: "alice", Kind: BanKindAbuse, BannedAt: b.Evidence.Coverage.WindowStart.Add(time.Hour).Unix(), ExpiresAt: b.Evidence.Coverage.WindowStart.Add(90 * time.Minute).Unix()}
	database.GetDB().Create(&r)
	if count, err := (&AIPatrolService{}).Process(n, now.Add(-2*time.Minute).Unix(), b, now, false); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestAIPatrolSuspensionBlocksEveryServingNodeAndLiftRestoresAccess(t *testing.T) {
	setupIpLimitTest(t)
	a, b := seedAgentNodeRow(t, "AI edge"), seedAgentNodeRow(t, "AI other")
	seedLimitedClient(t, &a.Id, "ai-edge", 443, "alice", 3)
	seedLimitedClient(t, &b.Id, "ai-other", 443, "alice", 3)
	now := time.Now()
	ban := model.BanRecord{Email: "alice", Kind: BanKindAIPatrol, Rule: AbuseRuleAIPatrol, BannedAt: now.Unix(), ExpiresAt: now.Add(24 * time.Hour).Unix()}
	if err := database.GetDB().Create(&ban).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{a.Id, b.Id} {
		cfg, err := (&XrayService{}).GetAgentXrayConfig(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(blockedUsers(t, cfg.RouterConfig)) != 1 {
			t.Fatal("AI suspension missing from agent config", id)
		}
	}
	if err := (&AbuseService{}).Lift("alice", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{a.Id, b.Id} {
		cfg, err := (&XrayService{}).GetAgentXrayConfig(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(blockedUsers(t, cfg.RouterConfig)) != 0 {
			t.Fatal("lift did not restore access", id)
		}
	}
}

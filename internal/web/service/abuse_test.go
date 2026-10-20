package service

import (
	"errors"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
	"github.com/mhsanaei/3x-ui/v3/internal/agentproto"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// setupAbuse gives a DB with one account and the panel's own core in mode.
func setupAbuse(t *testing.T, mode string) *AbuseService {
	t.Helper()
	setupConflictDB(t)
	if err := database.GetDB().Create(&model.ClientRecord{Email: "alice", Enable: true}).Error; err != nil {
		t.Fatal(err)
	}
	s := &AbuseService{}
	if err := s.SetMode(0, mode); err != nil {
		t.Fatal(err)
	}
	return s
}

func strike(rule string) abuse.Signal {
	return abuse.Signal{Email: "alice", Rule: rule, Level: abuse.LevelStrike, Measure: abuse.MeasurePorts, Count: 52, Limit: 50, Window: 300}
}

func banRecords(t *testing.T) []model.BanRecord {
	t.Helper()
	var recs []model.BanRecord
	if err := database.GetDB().Order("id").Find(&recs).Error; err != nil {
		t.Fatal(err)
	}
	return recs
}

func lastAction(t *testing.T) string {
	t.Helper()
	var e model.AbuseEvent
	if err := database.GetDB().Order("id DESC").First(&e).Error; err != nil {
		t.Fatal(err)
	}
	return e.Action
}

// Observing records what would have been banned and bans no one.
func TestAbuseObserveModeRecordsWithoutBanning(t *testing.T) {
	s := setupAbuse(t, AbuseModeObserve)
	now := time.Now()

	changed, err := s.HandleSignals(0, []abuse.Signal{strike(abuse.RuleScan)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if changed || len(banRecords(t)) != 0 || lastAction(t) != AbuseActionObserved {
		t.Fatalf("observe mode: changed=%v records=%v action=%s; want only an observed event", changed, banRecords(t), lastAction(t))
	}
}

// Each strike bans for 30 minutes; the fourth within 30 days locks until an admin.
func TestAbuseStrikesBanForThirtyMinutesAndTheFourthLocks(t *testing.T) {
	s := setupAbuse(t, AbuseModeEnforce)
	start := time.Now()
	for i := range 4 {
		at := start.Add(time.Duration(i) * time.Hour)
		if changed, err := s.HandleSignals(0, []abuse.Signal{strike(abuse.RuleScan)}, at); err != nil || !changed {
			t.Fatalf("strike %d: changed=%v err=%v", i+1, changed, err)
		}
	}
	recs := banRecords(t)
	if len(recs) != 4 {
		t.Fatalf("records = %d, want 4", len(recs))
	}
	for i, r := range recs[:3] {
		if r.Strike != i+1 || r.ExpiresAt-r.BannedAt != AbuseBanMinutes*60 || r.Reason == "" {
			t.Errorf("strike %d record = %+v, want a 30-minute ban with a reason", i+1, r)
		}
	}
	if lock := recs[3]; lock.Strike != 4 || lock.ExpiresAt != 0 || lastAction(t) != AbuseActionLocked {
		t.Errorf("fourth record = %+v (%s), want a lock", lock, lastAction(t))
	}
	status, err := s.Status("alice", start.Add(30*24*time.Hour))
	if err != nil || status.Ban == nil || status.Ban.ExpiresAt != 0 {
		t.Errorf("a month later the status is %+v, %v; want the lock still running", status, err)
	}
}

// One incident is one strike: hits while a ban runs are held, not counted.
func TestAbuseHitsDuringABanAreNotStrikes(t *testing.T) {
	s := setupAbuse(t, AbuseModeEnforce)
	now := time.Now()
	if _, err := s.HandleSignals(0, []abuse.Signal{strike(abuse.RuleScan)}, now); err != nil {
		t.Fatal(err)
	}
	changed, err := s.HandleSignals(0, []abuse.Signal{strike(abuse.RuleFlood)}, now.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if changed || len(banRecords(t)) != 1 || lastAction(t) != AbuseActionHeld {
		t.Fatalf("a hit during a ban: changed=%v records=%d action=%s", changed, len(banRecords(t)), lastAction(t))
	}
}

// Strikes older than 30 days no longer count toward the lock.
func TestAbuseStrikesExpireAfterThirtyDays(t *testing.T) {
	s := setupAbuse(t, AbuseModeEnforce)
	start := time.Now().Add(-40 * 24 * time.Hour)
	for i := range 3 {
		if _, err := s.HandleSignals(0, []abuse.Signal{strike(abuse.RuleScan)}, start.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.HandleSignals(0, []abuse.Signal{strike(abuse.RuleScan)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if recs := banRecords(t); recs[3].Strike != 1 || recs[3].ExpiresAt == 0 {
		t.Fatalf("a strike after 30 quiet days = %+v, want strike 1 with a 30-minute ban", recs[3])
	}
}

// Lifting a lock clears the strikes; lifting a short ban leaves them.
func TestAbuseLiftingALockForgivesAndAShortBanDoesNot(t *testing.T) {
	s := setupAbuse(t, AbuseModeEnforce)
	now := time.Now()
	if _, err := s.HandleSignals(0, []abuse.Signal{strike(abuse.RuleScan)}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Lift("alice", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.Status("alice", now.Add(2*time.Minute)); status.Ban != nil || status.Strikes != 1 {
		t.Fatalf("after lifting a ban: %+v, want no ban and the strike kept", status)
	}

	for i := 1; i <= 3; i++ {
		if _, err := s.HandleSignals(0, []abuse.Signal{strike(abuse.RuleScan)}, now.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if status, _ := s.Status("alice", now.Add(4*time.Hour)); status.Ban == nil || status.Ban.ExpiresAt != 0 {
		t.Fatalf("the fourth strike gave %+v, want a lock", status)
	}
	if err := s.Lift("alice", now.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.Status("alice", now.Add(5*time.Hour)); status.Ban != nil || status.Strikes != 0 {
		t.Fatalf("after unlocking: %+v, want no ban and no strikes", status)
	}
}

// The full-speed rule's first stage only warns, even while the rule bans.
func TestAbuseFullSpeedWarnsBeforeItBans(t *testing.T) {
	s := setupAbuse(t, AbuseModeEnforce)
	sig := strike(abuse.RuleFullSpeed)
	sig.Level = abuse.LevelWarn
	if changed, err := s.HandleSignals(0, []abuse.Signal{sig}, time.Now()); err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if got, recs := lastAction(t), banRecords(t); got != AbuseActionWarned || len(recs) != 0 {
		t.Fatalf("first stage = %s with records %+v, want a warning and no ban", got, recs)
	}
}

// On an enforcing server each rule does what the admin set: record tells only
// the admin, warn tells the person, ban strikes; a rule it does not know records.
func TestAbuseEachRuleDoesWhatItIsSetTo(t *testing.T) {
	s := setupAbuse(t, AbuseModeEnforce)
	settings := s.Settings()
	settings.Actions.Scan, settings.Actions.Crawler = AbuseActRecord, AbuseActWarn
	if err := s.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, c := range []struct{ rule, want string }{
		{abuse.RuleScan, AbuseActionNoticed},
		{abuse.RuleCrawler, AbuseActionWarned},
		{abuse.RuleSpam, AbuseActionBanned},
		{"tunnelling", AbuseActionNoticed},
	} {
		if _, err := s.HandleSignals(0, []abuse.Signal{strike(c.rule)}, now); err != nil {
			t.Fatal(err)
		}
		if got := lastAction(t); got != c.want {
			t.Errorf("%s set to %s was handled as %s, want %s", c.rule, settings.Actions.For(c.rule), got, c.want)
		}
	}
	if recs := banRecords(t); len(recs) != 1 || recs[0].Rule != abuse.RuleSpam {
		t.Fatalf("records = %+v, want spam's ban only", recs)
	}
}

// Bulk sign-ups are a guess from busy minutes, so even an enforcing server only
// records them until the admin chooses otherwise.
func TestAbuseBulkSignUpsOnlyRecordUntilTheirActionIsChanged(t *testing.T) {
	s := setupAbuse(t, AbuseModeEnforce)
	now := time.Now()
	hit := abuse.Signal{
		Email: "alice", Rule: abuse.RuleRegister, Level: abuse.LevelStrike, Measure: abuse.MeasureRegisterHour,
		Count: 11, Limit: 10, Window: 3600, Samples: []string{abuse.PlatformOpenAI},
	}
	if _, err := s.HandleSignals(0, []abuse.Signal{hit}, now); err != nil {
		t.Fatal(err)
	}
	if got := lastAction(t); got != AbuseActionNoticed || len(banRecords(t)) != 0 {
		t.Fatalf("by default a bulk sign-up was %s with records %+v, want only noticed", got, banRecords(t))
	}

	settings := s.Settings()
	settings.Actions.Register = AbuseActBan
	if err := s.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleSignals(0, []abuse.Signal{hit}, now); err != nil {
		t.Fatal(err)
	}
	if got := lastAction(t); got != AbuseActionBanned {
		t.Fatalf("set to ban, a bulk sign-up was %s, want banned", got)
	}
}

// A suspect bulk sign-up is the admin's to judge: whatever the rule is set to, it
// never bans the account or warns the person.
func TestAbuseSuspectSignUpsNeverBanOrWarn(t *testing.T) {
	s := setupAbuse(t, AbuseModeEnforce)
	hit := abuse.Signal{
		Email: "alice", Rule: abuse.RuleRegister, Level: abuse.LevelSuspect, Measure: abuse.MeasureRegisterHour,
		Count: 35, Limit: 30, Window: 3600, Samples: []string{abuse.PlatformGoogle},
	}
	for _, act := range []string{AbuseActWarn, AbuseActBan} {
		settings := s.Settings()
		settings.Actions.Register = act
		if err := s.SetSettings(settings); err != nil {
			t.Fatal(err)
		}
		if _, err := s.HandleSignals(0, []abuse.Signal{hit}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := lastAction(t); got != AbuseActionNoticed {
			t.Errorf("set to %s, a suspect bulk sign-up was %s, want only noticed", act, got)
		}
	}
	if recs := banRecords(t); len(recs) != 0 {
		t.Fatalf("records = %+v, want none", recs)
	}
}

// An action the policy cannot take is refused, and the stored ones stay.
func TestAbuseSettingsRefuseAnActionThatCannotBeTaken(t *testing.T) {
	s := setupAbuse(t, AbuseModeEnforce)
	for name, change := range map[string]func(*AbuseSettings){
		"unknown":        func(a *AbuseSettings) { a.Actions.Scan = "explode" },
		"warn a sign-up": func(a *AbuseSettings) { a.Signup.Action = AbuseActWarn },
	} {
		settings := s.Settings()
		change(&settings)
		if err := s.SetSettings(settings); !errors.Is(err, errAbuseAction) {
			t.Fatalf("%s: SetSettings = %v, want errAbuseAction", name, err)
		}
	}
	if got := s.Settings(); got.Actions.Scan != AbuseActBan || got.Signup.Action != AbuseActRecord {
		t.Fatalf("stored now %+v, want the defaults kept", got)
	}
}

// A server with detection off bans no one even if a stale signal arrives.
func TestAbuseSignalsFromAServerTurnedOffAreIgnored(t *testing.T) {
	s := setupAbuse(t, AbuseModeOff)
	if changed, err := s.HandleSignals(0, []abuse.Signal{strike(abuse.RuleScan)}, time.Now()); err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var events int64
	database.GetDB().Model(&model.AbuseEvent{}).Count(&events)
	if events != 0 {
		t.Fatalf("events = %d, want none", events)
	}
}

// A report's signals are handled once: an agent resends an unacked report
// unchanged, and that must not strike the account twice.
func TestAgentReportAbuseIsHandledOnce(t *testing.T) {
	setupIpLimitTest(t)
	node := seedAgentNodeRow(t, "edge")
	seedLimitedClient(t, &node.Id, "edge-in", 443, "alice", 3)
	if err := (&AbuseService{}).SetMode(node.Id, AbuseModeEnforce); err != nil {
		t.Fatal(err)
	}
	report := &agentproto.Traffic{Instance: "i1", Seq: 1, Abuse: []abuse.Signal{strike(abuse.RuleScan)}}

	for range 2 {
		if err := (&AgentService{}).HandleTraffic(node.Id, report); err != nil {
			t.Fatal(err)
		}
	}
	if recs := banRecords(t); len(recs) != 1 || recs[0].Kind != BanKindAbuse {
		t.Fatalf("records after the report and its resend = %+v, want one ban", recs)
	}
	var events int64
	database.GetDB().Model(&model.AbuseEvent{}).Count(&events)
	if events != 1 {
		t.Fatalf("events = %d, want the resent report not to be read again", events)
	}
}

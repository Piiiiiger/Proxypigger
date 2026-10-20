package service

import (
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

var codeShape = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{4}(-[A-HJ-NP-Z2-9]{4}){3}$`)

func codePlan(t *testing.T, name string, limitIp int, inboundIds ...int) *model.Plan {
	t.Helper()
	plan, err := (&PlanService{}).Create(PlanInput{Name: name, LimitIP: limitIp, InboundIds: inboundIds})
	if err != nil {
		t.Fatalf("create plan %s: %v", name, err)
	}
	return plan
}

func mustCreateCodes(t *testing.T, in ActivationCodeInput) []model.ActivationCode {
	t.Helper()
	codes, err := (&ActivationCodeService{}).Create(in)
	if err != nil {
		t.Fatalf("create codes: %v", err)
	}
	return codes
}

func codeRow(t *testing.T, id int) model.ActivationCode {
	t.Helper()
	var row model.ActivationCode
	if err := database.GetDB().First(&row, id).Error; err != nil {
		t.Fatalf("read code %d: %v", id, err)
	}
	return row
}

func clientExists(t *testing.T, email string) bool {
	t.Helper()
	var n int64
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", email).Count(&n).Error; err != nil {
		t.Fatalf("count %s: %v", email, err)
	}
	return n > 0
}

func TestActivationCodesAreMadeForAPlan(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	plan := codePlan(t, "Monthly", 2, a)
	codes := mustCreateCodes(t, ActivationCodeInput{
		PlanId: plan.Id, Count: 3, TotalGB: 100 * planGiB, Days: 30, ResetDay: 9, Note: "batch",
	})
	if len(codes) != 3 {
		t.Fatalf("made %d codes, want 3", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if !codeShape.MatchString(c.Code) || seen[c.Code] {
			t.Errorf("code %q is malformed or repeated", c.Code)
		}
		seen[c.Code] = true
		if c.PlanId != plan.Id || c.TotalGB != 100*planGiB || c.Days != 30 || c.ResetDay != 9 || c.Note != "batch" || c.UsedAt != 0 {
			t.Errorf("code = %+v, want the plan, grants and note asked for, unused", c)
		}
	}
	listed, err := (&ActivationCodeService{}).List(plan.Id)
	if err != nil || len(listed) != 3 {
		t.Fatalf("list = %d codes (err %v), want 3", len(listed), err)
	}
}

func TestCreateCodesRefusesWhatCouldNotBeUsed(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	plan := codePlan(t, "Monthly", 0, a)
	empty := codePlan(t, "Empty", 0)
	for name, in := range map[string]ActivationCodeInput{
		"unknown plan":   {PlanId: 9999, Count: 1},
		"plan no nodes":  {PlanId: empty.Id, Count: 1},
		"no codes":       {PlanId: plan.Id, Count: 0},
		"too many codes": {PlanId: plan.Id, Count: 201},
		"negative quota": {PlanId: plan.Id, Count: 1, TotalGB: -1},
		"negative days":  {PlanId: plan.Id, Count: 1, Days: -1},
		"reset day 32":   {PlanId: plan.Id, Count: 1, ResetDay: 32},
	} {
		if codes, err := (&ActivationCodeService{}).Create(in); err == nil {
			t.Errorf("%s: made %d codes, want it refused", name, len(codes))
		}
	}
	var made int64
	database.GetDB().Model(&model.ActivationCode{}).Count(&made)
	if made != 0 {
		t.Fatalf("%d codes stored by refused requests", made)
	}
}

// What the code grants becomes the new user: its plan's nodes and IP limit, its
// quota, the creation-based deadline and its reset day, behind the password chosen.
func TestRegisterCreatesTheUserOnThePlanWithTheCodesLimits(t *testing.T) {
	a, b, _ := setupPlanDB(t)
	plan := codePlan(t, "Monthly", 2, a, b)
	code := mustCreateCodes(t, ActivationCodeInput{PlanId: plan.Id, Count: 1, TotalGB: 50 * planGiB, Days: 30, ResetDay: 9})[0]

	start := time.Now().UnixMilli()
	// People type codes as they read them: any case, spaces for the dashes.
	typed := strings.ToLower(strings.ReplaceAll(code.Code, "-", " "))
	if _, _, err := (&ActivationCodeService{}).Register(&InboundService{}, "newbie", "secret-pass", typed); err != nil {
		t.Fatalf("register: %v", err)
	}

	rec := planRecord(t, "newbie")
	if rec.PlanId != plan.Id || rec.TotalGB != 50*planGiB || rec.LimitIP != 2 || !rec.Enable ||
		rec.TrafficReset != "monthly" || rec.TrafficResetDay != 9 {
		t.Fatalf("user = plan %d quota %d limitIp %d enable %v reset %s/%d, want the code's grants",
			rec.PlanId, rec.TotalGB, rec.LimitIP, rec.Enable, rec.TrafficReset, rec.TrafficResetDay)
	}
	if rec.ExpiryTime != code.CreatedAt+30*planDayMs {
		t.Fatalf("expiry %d is not 30 days from code creation", rec.ExpiryTime)
	}
	if got := planInboundIdsOf(t, "newbie"); !slices.Equal(got, []int{a, b}) {
		t.Fatalf("inbounds = %v, want the plan's %v", got, []int{a, b})
	}
	if _, _, err := (&ClientPortalService{}).Authenticate("newbie", "secret-pass"); err != nil {
		t.Fatalf("the new user cannot sign in to the portal: %v", err)
	}
	if used := codeRow(t, code.Id); used.UsedBy != "newbie" || used.UsedAt < start {
		t.Fatalf("code = used by %q at %d, want used by newbie now", used.UsedBy, used.UsedAt)
	}
}

// The people on DMIT's Reality nodes all use the vision flow; a new one must too.
func TestRegisterGivesTheVisionFlowWhereTheNodesTakeIt(t *testing.T) {
	setupConflictDB(t)
	startSerializedWriter(t)
	seedInboundConflict(t, "reality", "0.0.0.0", 47201, model.VLESS,
		`{"network":"tcp","security":"reality"}`, `{"clients":[],"decryption":"none"}`)
	var ib model.Inbound
	if err := database.GetDB().Where("tag = ?", "reality").First(&ib).Error; err != nil {
		t.Fatalf("read inbound: %v", err)
	}
	code := mustCreateCodes(t, ActivationCodeInput{PlanId: codePlan(t, "Reality", 0, ib.Id).Id, Count: 1})[0]
	if _, _, err := (&ActivationCodeService{}).Register(&InboundService{}, "vision", "secret-pass", code.Code); err != nil {
		t.Fatalf("register: %v", err)
	}
	if entry := inboundClientEntry(t, ib.Id, "vision"); entry["flow"] != "xtls-rprx-vision" {
		t.Fatalf("inbound entry = %v, want the vision flow", entry)
	}
}

// A refusal claims no code and creates nobody, so the person can correct it and retry.
func TestRegisterRefusesBadCodesNamesAndPasswords(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	plan := codePlan(t, "Monthly", 0, a)
	codes := mustCreateCodes(t, ActivationCodeInput{PlanId: plan.Id, Count: 2})
	createPlanClient(t, "taken", []int{a}, 0)
	s := &ActivationCodeService{}
	if _, _, err := s.Register(&InboundService{}, "first", "secret-pass", codes[1].Code); err != nil {
		t.Fatalf("register first: %v", err)
	}

	for _, tc := range []struct {
		name, user, pass, code string
		want                   error
	}{
		{"unknown code", "someone", "secret-pass", "AAAA-BBBB-CCCC-DDDD", ErrActivationCode},
		{"used code", "someone", "secret-pass", codes[1].Code, ErrActivationCode},
		{"name taken", "TAKEN", "secret-pass", codes[0].Code, ErrUsernameTaken},
		{"name with space", "some one", "secret-pass", codes[0].Code, nil},
		{"name too short", "x", "secret-pass", codes[0].Code, nil},
		{"short password", "someone", "123", codes[0].Code, nil},
	} {
		_, _, err := s.Register(&InboundService{}, tc.user, tc.pass, tc.code)
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%s: err = %v, want it refused (%v)", tc.name, err, tc.want)
		}
		if clientExists(t, tc.user) && tc.user != "TAKEN" {
			t.Errorf("%s: user %q was created", tc.name, tc.user)
		}
	}
	if left := codeRow(t, codes[0].Id); left.UsedAt != 0 {
		t.Fatalf("a refused registration used the code: %+v", left)
	}
}

// Claiming the code comes first, so another registration cannot take it meanwhile;
// when the user then cannot be made, the code is given back.
func TestRegisterGivesTheCodeBackWhenTheUserCannotBeMade(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	code := mustCreateCodes(t, ActivationCodeInput{PlanId: codePlan(t, "Monthly", 0, a).Id, Count: 1})[0]
	s := &ActivationCodeService{}

	mend := breakInbound(t, a)
	if _, _, err := s.Register(&InboundService{}, "unlucky", "secret-pass", code.Code); err == nil {
		t.Fatal("register over a broken inbound succeeded")
	}
	mend()
	if left := codeRow(t, code.Id); left.UsedAt != 0 || clientExists(t, "unlucky") {
		t.Fatalf("after the failure: code %+v, user exists %v; want the code back and no user", left, clientExists(t, "unlucky"))
	}
	if _, _, err := s.Register(&InboundService{}, "unlucky", "secret-pass", code.Code); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

// A signed-in person's next code renews them, or moves them to its plan: its nodes
// and IP limit, its quota and reset, remaining code time added, usage cleared.
func TestRedeemMovesTheUserToTheCodesPlanAndRenewsIt(t *testing.T) {
	a, b, _ := setupPlanDB(t)
	planA, planB := codePlan(t, "A", 1, a), codePlan(t, "B", 3, b)
	future := time.Now().Add(10 * 24 * time.Hour).UnixMilli()
	createPlanClient(t, "member@code", []int{a}, future)
	if _, err := (&PlanService{}).Assign(&InboundService{}, []string{"member@code"}, planA.Id); err != nil {
		t.Fatalf("assign: %v", err)
	}
	seedUsage(t, "member@code", 2*planGiB, 3*planGiB)
	code := mustCreateCodes(t, ActivationCodeInput{PlanId: planB.Id, Count: 1, TotalGB: 80 * planGiB, Days: 30, ResetDay: 5})[0]

	if _, err := (&ActivationCodeService{}).Redeem(&InboundService{}, "member@code", code.Code); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	rec := planRecord(t, "member@code")
	if rec.PlanId != planB.Id || rec.LimitIP != 3 || rec.TotalGB != 80*planGiB || rec.ExpiryTime != future+code.CreatedAt+30*planDayMs-codeRow(t, code.Id).UsedAt ||
		rec.TrafficReset != "monthly" || rec.TrafficResetDay != 5 {
		t.Fatalf("user = plan %d limitIp %d quota %d expiry %d reset %s/%d, want plan B's and the code's",
			rec.PlanId, rec.LimitIP, rec.TotalGB, rec.ExpiryTime, rec.TrafficReset, rec.TrafficResetDay)
	}
	if got := planInboundIdsOf(t, "member@code"); !slices.Equal(got, []int{b}) {
		t.Fatalf("inbounds = %v, want plan B's [%d]", got, b)
	}
	if used := usageOf(t, "member@code"); used != 0 {
		t.Fatalf("usage = %d after a new period, want 0", used)
	}
	if row := codeRow(t, code.Id); row.UsedBy != "member@code" {
		t.Fatalf("code used by %q, want member@code", row.UsedBy)
	}
}

func TestRedeemRefusesAUsedCodeAndChangesNothing(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	plan := codePlan(t, "A", 0, a)
	createPlanClient(t, "one@code", []int{a}, 0)
	createPlanClient(t, "two@code", []int{a}, 0)
	code := mustCreateCodes(t, ActivationCodeInput{PlanId: plan.Id, Count: 1, TotalGB: 10 * planGiB, Days: 7})[0]
	s := &ActivationCodeService{}
	if _, err := s.Redeem(&InboundService{}, "one@code", code.Code); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if _, err := s.Redeem(&InboundService{}, "two@code", code.Code); !errors.Is(err, ErrActivationCode) {
		t.Fatalf("second redeem: err = %v, want ErrActivationCode", err)
	}
	if rec := planRecord(t, "two@code"); rec.PlanId != 0 || rec.TotalGB != 0 || rec.ExpiryTime != 0 {
		t.Fatalf("a refused redeem changed the user: %+v", rec)
	}
}

// A code names its plan; once the plan is gone its codes would grant nothing.
func TestDeletingAPlanDeletesItsCodes(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	gone, kept := codePlan(t, "Gone", 0, a), codePlan(t, "Kept", 0, a)
	mustCreateCodes(t, ActivationCodeInput{PlanId: gone.Id, Count: 2})
	mustCreateCodes(t, ActivationCodeInput{PlanId: kept.Id, Count: 1})
	if err := (&PlanService{}).Delete(gone.Id); err != nil {
		t.Fatalf("delete plan: %v", err)
	}
	var left []model.ActivationCode
	database.GetDB().Find(&left)
	if len(left) != 1 || left[0].PlanId != kept.Id {
		t.Fatalf("codes left = %+v, want only the kept plan's one", left)
	}
}

func TestRedeemRetriesAFailedResetWithoutAddingDaysTwice(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	future := time.Now().Add(10 * 24 * time.Hour).UnixMilli()
	createPlanClient(t, "retry@code", []int{a}, future)
	code := mustCreateCodes(t, ActivationCodeInput{PlanId: codePlan(t, "Retry", 2, a).Id, Count: 1, Days: 30})[0]
	db := database.GetDB()
	const callback = "test:activation_reset_failure"
	if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "client_traffics" {
			tx.AddError(errors.New("reset unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Update().Remove(callback) })
	s := &ActivationCodeService{}
	if _, err := s.Redeem(&InboundService{}, "retry@code", code.Code); err == nil {
		t.Fatal("reset failure was ignored")
	}
	if row := codeRow(t, code.Id); row.UsedAt == 0 {
		t.Fatal("a partly applied code was released for another user")
	}
	createPlanClient(t, "other@code", []int{a}, 0)
	if _, err := s.Redeem(&InboundService{}, "other@code", code.Code); !errors.Is(err, ErrActivationCode) {
		t.Fatalf("another user took the pending code: %v", err)
	}
	db.Callback().Update().Remove(callback)
	if _, err := (&ActivationCodeService{}).Redeem(&InboundService{}, "retry@code", code.Code); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := planRecord(t, "retry@code").ExpiryTime; got != future+code.CreatedAt+30*planDayMs-codeRow(t, code.Id).UsedAt {
		t.Fatalf("expiry = %d, want one 30-day grant", got)
	}
	if _, err := s.Redeem(&InboundService{}, "retry@code", code.Code); !errors.Is(err, ErrActivationCode) {
		t.Fatalf("completed code reused: %v", err)
	}
}

func TestConcurrentCodesAddBothPeriodsToTheSameUser(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	future := time.Now().Add(10 * 24 * time.Hour).UnixMilli()
	createPlanClient(t, "concurrent@code", []int{a}, future)
	codes := mustCreateCodes(t, ActivationCodeInput{PlanId: codePlan(t, "Concurrent", 2, a).Id, Count: 2, Days: 30})
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, code := range codes {
		wg.Go(func() {
			<-start
			_, err := (&ActivationCodeService{}).Redeem(&InboundService{}, "concurrent@code", code.Code)
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := future
	for _, code := range codes {
		want += code.CreatedAt + 30*planDayMs - codeRow(t, code.Id).UsedAt
	}
	if got := planRecord(t, "concurrent@code").ExpiryTime; got != want {
		t.Fatalf("expiry = %d, want both periods", got)
	}
}

func TestPlanCannotBeDeletedDuringCodeRegistration(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	plan := codePlan(t, "Registration", 2, a)
	code := mustCreateCodes(t, ActivationCodeInput{PlanId: plan.Id, Count: 1})[0]
	entered, proceed := make(chan struct{}), make(chan struct{})
	var pause sync.Once
	db := database.GetDB()
	const callback = "test:pause_code_registration"
	if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "inbounds" {
			pause.Do(func() { close(entered); <-proceed })
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Query().Remove(callback) })
	registered, deleted := make(chan error, 1), make(chan error, 1)
	go func() {
		_, _, err := (&ActivationCodeService{}).Register(&InboundService{}, "racing-user", "secret-pass", code.Code)
		registered <- err
	}()
	<-entered
	go func() { deleted <- (&PlanService{}).Delete(plan.Id) }()
	var deleteErr error
	finishedEarly := false
	select {
	case deleteErr = <-deleted:
		finishedEarly = true
	case <-time.After(50 * time.Millisecond):
	}
	close(proceed)
	if err := <-registered; err != nil {
		t.Fatal(err)
	}
	if !finishedEarly {
		deleteErr = <-deleted
	}
	if finishedEarly || deleteErr == nil {
		t.Fatalf("plan deletion ran during registration: %v", deleteErr)
	}
	if rec := planRecord(t, "racing-user"); rec.PlanId != plan.Id {
		t.Fatalf("registered user lost its plan: %+v", rec)
	}
}

func TestRedeemRejectsExpiryOverflowBeforeUsingTheCode(t *testing.T) {
	for name, in := range map[string]ActivationCodeInput{"days": {Days: 1}, "months": {Months: 1}} {
		t.Run(name, func(t *testing.T) {
			a, _, _ := setupPlanDB(t)
			createPlanClient(t, "overflow@code", []int{a}, math.MaxInt64-1000)
			before := *planRecord(t, "overflow@code")
			in.PlanId, in.Count = codePlan(t, "Overflow", 2, a).Id, 1
			code := mustCreateCodes(t, in)[0]
			if _, err := (&ActivationCodeService{}).Redeem(&InboundService{}, before.Email, code.Code); err == nil {
				t.Fatal("overflow was accepted")
			}
			after := planRecord(t, before.Email)
			if after.ExpiryTime != before.ExpiryTime || after.PlanId != before.PlanId || codeRow(t, code.Id).UsedAt != 0 {
				t.Fatal("rejected overflow changed the user or consumed the code")
			}
		})
	}
}

func TestActivationCodeAgesBeforeRegistrationAndRenewal(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	plan := codePlan(t, "Countdown", 0, a)
	codes := mustCreateCodes(t, ActivationCodeInput{PlanId: plan.Id, Count: 3, Days: 12})
	created := time.Now().Add(-24 * time.Hour).UnixMilli()
	for i := range codes {
		if err := database.GetDB().Model(&model.ActivationCode{}).Where("id = ?", codes[i].Id).UpdateColumn("created_at", created).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := &ActivationCodeService{}
	if _, _, err := s.Register(&InboundService{}, "countdown-new", "secret-pass", codes[0].Code); err != nil {
		t.Fatal(err)
	}
	deadline := created + 12*planDayMillis
	if got := planRecord(t, "countdown-new").ExpiryTime; got != deadline {
		t.Fatalf("new expiry = %d, want fixed deadline %d", got, deadline)
	}
	future := time.Now().Add(5 * 24 * time.Hour).UnixMilli()
	createPlanClient(t, "countdown-renew", []int{a}, future)
	if _, err := s.Redeem(&InboundService{}, "countdown-renew", codes[1].Code); err != nil {
		t.Fatal(err)
	}
	want := future + deadline - codeRow(t, codes[1].Id).UsedAt
	if got := planRecord(t, "countdown-renew").ExpiryTime; got != want {
		t.Fatalf("renewal expiry = %d, want only remaining time added: %d", got, want)
	}
	createPlanClient(t, "countdown-expired-account", []int{a}, time.Now().Add(-24*time.Hour).UnixMilli())
	if _, err := s.Redeem(&InboundService{}, "countdown-expired-account", codes[2].Code); err != nil {
		t.Fatal(err)
	}
	if got := planRecord(t, "countdown-expired-account").ExpiryTime; got != deadline {
		t.Fatalf("expired account expiry = %d, want code deadline %d", got, deadline)
	}
}

func TestExpiredActivationCodeCannotCreateOrChangeAUser(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	code := mustCreateCodes(t, ActivationCodeInput{PlanId: codePlan(t, "Expired", 0, a).Id, Count: 1, Days: 1})[0]
	if err := database.GetDB().Model(&model.ActivationCode{}).Where("id = ?", code.Id).UpdateColumn("created_at", time.Now().Add(-48*time.Hour).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	s := &ActivationCodeService{}
	if _, _, err := s.Register(&InboundService{}, "too-late", "secret-pass", code.Code); !errors.Is(err, ErrActivationCode) {
		t.Fatalf("expired registration: %v", err)
	}
	if clientExists(t, "too-late") {
		t.Fatal("expired code created a user")
	}
	future := time.Now().Add(24 * time.Hour).UnixMilli()
	createPlanClient(t, "expired-code-renew", []int{a}, future)
	if _, err := s.Redeem(&InboundService{}, "expired-code-renew", code.Code); !errors.Is(err, ErrActivationCode) {
		t.Fatalf("expired renewal: %v", err)
	}
	if planRecord(t, "expired-code-renew").ExpiryTime != future || codeRow(t, code.Id).UsedAt != 0 {
		t.Fatal("expired code changed state")
	}
	if err := database.GetDB().Model(&model.ActivationCode{}).Where("id = ?", code.Id).UpdateColumn("days", 0).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Register(&InboundService{}, "permanent-code", "secret-pass", code.Code); err != nil {
		t.Fatal(err)
	}
	if planRecord(t, "permanent-code").ExpiryTime != 0 {
		t.Fatal("permanent code gained an expiry")
	}
}

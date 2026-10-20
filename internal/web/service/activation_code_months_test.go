package service

import (
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func setCodeCreatedAt(t *testing.T, id int, at time.Time) {
	t.Helper()
	if err := database.GetDB().Model(&model.ActivationCode{}).Where("id = ?", id).UpdateColumn("created_at", at.UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
}

// A code for a month lapses on the same date a month on, however long the month:
// one made on 31 January lapses on the last day of February.
func TestAMonthCodeListsItsCalendarDeadline(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	code := mustCreateCodes(t, ActivationCodeInput{PlanId: codePlan(t, "Monthly", 0, a).Id, Count: 1, Months: 1})[0]
	setCodeCreatedAt(t, code.Id, time.Date(2035, time.January, 31, 10, 0, 0, 0, AccountLocation))
	codes, err := (&ActivationCodeService{}).List(0)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2035, time.February, 28, 10, 0, 0, 0, AccountLocation).UnixMilli(); len(codes) != 1 || codes[0].ExpiresAt != want {
		t.Fatalf("listed codes = %+v, want one lapsing on %s", codes, dateOf(want))
	}
}

// A new user's period from a month code ends on the same date a month after the
// code was made, and that day is remembered for their renewals.
func TestRegisterWithAMonthCodeEndsOnTheSameDate(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	code := mustCreateCodes(t, ActivationCodeInput{PlanId: codePlan(t, "Monthly", 0, a).Id, Count: 1, Months: 1})[0]
	now := time.Now().In(AccountLocation)
	made := time.Date(now.Year(), now.Month(), min(now.Day(), 28), 0, 0, 0, 0, AccountLocation)
	setCodeCreatedAt(t, code.Id, made)
	if _, _, err := (&ActivationCodeService{}).Register(&InboundService{}, "monthly-new", "secret-pass", code.Code); err != nil {
		t.Fatalf("register: %v", err)
	}
	rec := planRecord(t, "monthly-new")
	want := time.Date(made.Year(), made.Month()+1, made.Day(), 0, 0, 0, 0, AccountLocation).UnixMilli()
	if rec.ExpiryTime != want || rec.RenewDay != made.Day() {
		t.Fatalf("new user expires %s on day %d, want %s on day %d", dateOf(rec.ExpiryTime), rec.RenewDay, dateOf(want), made.Day())
	}
}

// Redeeming a month code adds that month to what is left: 10 February becomes
// 10 March, not 30 or 31 days later (a fresh code loses only its first moments).
func TestRedeemingAMonthCodeAddsCalendarMonths(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	plan := codePlan(t, "Monthly", 0, a)
	createPlanClient(t, "month@code", []int{a}, time.Date(2035, time.February, 10, 10, 0, 0, 0, AccountLocation).UnixMilli())
	code := mustCreateCodes(t, ActivationCodeInput{PlanId: plan.Id, Count: 1, Months: 1})[0]
	if _, err := (&ActivationCodeService{}).Redeem(&InboundService{}, "month@code", code.Code); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	rec := planRecord(t, "month@code")
	month := time.Date(2035, time.March, 10, 10, 0, 0, 0, AccountLocation).UnixMilli()
	if rec.ExpiryTime > month || rec.ExpiryTime < month-60_000 || rec.RenewDay != 10 {
		t.Fatalf("renewed to %s on day %d, want 10 March at 10:00 on day 10", dateOf(rec.ExpiryTime), rec.RenewDay)
	}
}

// A code used late is worth the share of its validity it still has, in the months
// it is added to; one made on 1 January and used an hour before it lapses must not
// end before now just because February is shorter than January.
func TestALateMonthCodeIsWorthWhatIsLeftOfIt(t *testing.T) {
	created := at(2035, time.January, 1, 0, 0)
	late := &model.ActivationCode{Months: 1, CreatedAt: created.UnixMilli(), UsedAt: at(2035, time.January, 31, 23, 0).UnixMilli()}
	now := late.UsedAt
	got, _, err := codeRenewal(late, &model.ClientRecord{}, now)
	if err != nil || got <= now || got > now+time.Hour.Milliseconds() {
		t.Errorf("a code an hour from lapsing gives %s (%v), want under an hour from now", dateOf(got), err)
	}
	early := &model.ActivationCode{Months: 1, CreatedAt: created.UnixMilli(), UsedAt: at(2035, time.January, 21, 0, 0).UnixMilli()}
	got, day, err := codeRenewal(early, &model.ClientRecord{ExpiryTime: atMs(2035, time.March, 10, 10, 0)}, early.UsedAt)
	if err != nil || dateOf(got) != "2035-03-21 10:00" || day != 10 {
		t.Errorf("11 of 31 days left on 10 March gives %s, day %d (%v), want 2035-03-21 10:00 (11 of the 31 days to 10 April)", dateOf(got), day, err)
	}
}

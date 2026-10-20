package service

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func termsOf(t *testing.T, planId int) []int {
	t.Helper()
	plans, err := (&PlanService{}).List()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plans {
		if p.Id == planId {
			return p.TermMonths
		}
	}
	t.Fatalf("plan %d is not listed", planId)
	return nil
}

// A plan sold only by some terms keeps each once, shortest first; a term is a
// whole number of calendar months the panel can renew by.
func TestPlanTermsAreKeptSortedAndChecked(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	s := &PlanService{}
	plan, err := s.Create(PlanInput{Name: "SG", InboundIds: []int{a}, TermMonths: []int{12, 3, 3}})
	if err != nil {
		t.Fatal(err)
	}
	if got := termsOf(t, plan.Id); !slices.Equal(got, []int{3, 12}) {
		t.Fatalf("terms = %v, want [3 12]", got)
	}
	for i, bad := range [][]int{{0}, {-1}, {1201}} {
		_, err := s.Create(PlanInput{Name: fmt.Sprint("bad", i), InboundIds: []int{a}, TermMonths: bad})
		if err == nil || !strings.Contains(err.Error(), "a term is 1 to 1200 months") {
			t.Fatalf("terms %v: %v, want them refused", bad, err)
		}
	}
	if _, err := s.Update(&InboundService{}, plan.Id, PlanInput{Name: "SG", InboundIds: []int{a}}, false); err != nil {
		t.Fatal(err)
	}
	if got := termsOf(t, plan.Id); len(got) != 0 {
		t.Fatalf("terms after clearing = %v, want none", got)
	}
}

// A user on a plan sold by terms renews only by one of its months, from the panel
// and the bot alike, never by days; a renewal with one refused user renews none.
func TestRenewKeepsToThePlansTerms(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	expiry := time.Now().Add(20 * 24 * time.Hour).UnixMilli()
	createPlanClient(t, "sg@plan", []int{a}, expiry)
	createPlanClient(t, "free@plan", []int{a}, expiry)
	s := &PlanService{}
	plan, err := s.Create(PlanInput{Name: "SG", InboundIds: []int{a}, TermMonths: []int{3, 12}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Assign(&InboundService{}, []string{"sg@plan"}, plan.Id); err != nil {
		t.Fatal(err)
	}

	for _, refused := range []RenewalTerm{{Months: 1}, {Days: 90}} {
		_, err = (&ClientService{}).Renew(&InboundService{}, []string{"free@plan", "sg@plan"}, refused, false)
		if err == nil || !strings.Contains(err.Error(), `sg@plan is on plan "SG", which renews by 3 or 12 months only`) {
			t.Fatalf("Renew(%+v) = %v, want it refused", refused, err)
		}
	}
	for _, email := range []string{"free@plan", "sg@plan"} {
		if got := planRecord(t, email).ExpiryTime; got != expiry {
			t.Fatalf("%s expiry moved to %d by a refused renewal", email, got)
		}
	}
	if _, err := (&ClientService{}).Renew(&InboundService{}, []string{"sg@plan"}, RenewalTerm{Months: 3}, false); err != nil {
		t.Fatalf("Renew(3 months) = %v", err)
	}
	if _, err := (&ClientService{}).Renew(&InboundService{}, []string{"free@plan"}, RenewalTerm{Days: 30}, false); err != nil {
		t.Fatalf("a user on no restricted plan renews by any term: %v", err)
	}
}

// Codes for a plan sold by terms grant one of its months, never days or forever.
func TestActivationCodesKeepToThePlansTerms(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	plan, err := (&PlanService{}).Create(PlanInput{Name: "SG", InboundIds: []int{a}, TermMonths: []int{3, 12}})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []ActivationCodeInput{{Days: 0}, {Months: 1}, {Days: 90}} {
		in.PlanId, in.Count = plan.Id, 1
		_, err := (&ActivationCodeService{}).Create(in)
		if err == nil || !strings.Contains(err.Error(), `plan "SG" is sold by 3 or 12 months only`) {
			t.Fatalf("a code for %d months, %d days: %v, want it refused", in.Months, in.Days, err)
		}
	}
	mustCreateCodes(t, ActivationCodeInput{PlanId: plan.Id, Count: 1, Months: 12})
}

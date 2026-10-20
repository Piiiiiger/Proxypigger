package service

import (
	"math"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func seedUsage(t *testing.T, email string, up, down int64) {
	t.Helper()
	if err := database.GetDB().Model(&xray.ClientTraffic{}).Where("email = ?", email).
		Updates(map[string]any{"up": up, "down": down}).Error; err != nil {
		t.Fatalf("seed usage of %s: %v", email, err)
	}
}

func usageOf(t *testing.T, email string) int64 {
	t.Helper()
	var row xray.ClientTraffic
	if err := database.GetDB().Where("email = ?", email).First(&row).Error; err != nil {
		t.Fatalf("read usage of %s: %v", email, err)
	}
	return row.Up + row.Down
}

func disable(t *testing.T, email string) {
	t.Helper()
	if _, _, err := (&ClientService{}).SetClientEnableByEmail(&InboundService{}, email, false); err != nil {
		t.Fatalf("disable %s: %v", email, err)
	}
}

// Renewing gives each user the days on top of what they have left, or from today
// once it has run out; plans play no part.
func TestRenewAddsDaysFromTheLaterOfNowAndTheExpiry(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	future := time.Now().Add(10 * 24 * time.Hour).UnixMilli()
	past := time.Now().Add(-5 * 24 * time.Hour).UnixMilli()
	createPlanClient(t, "future@renew", []int{a}, future)
	createPlanClient(t, "lapsed@renew", []int{a}, past)
	createPlanClient(t, "never@renew", []int{a}, 0)
	// A negative expiry is "this long after the first connection".
	createPlanClient(t, "unstarted@renew", []int{a}, -7*planDayMs)
	emails := []string{"future@renew", "lapsed@renew", "never@renew", "unstarted@renew"}
	for _, email := range emails {
		seedUsage(t, email, 7*planGiB, 9*planGiB)
	}
	disable(t, "lapsed@renew")

	now := time.Now().UnixMilli()
	if _, err := (&ClientService{}).Renew(&InboundService{}, emails, RenewalTerm{Days: 30}, true); err != nil {
		t.Fatalf("renew: %v", err)
	}

	if got := planRecord(t, "future@renew").ExpiryTime; got != future+30*planDayMs {
		t.Errorf("future expiry = %d, want its old expiry plus 30 days (%d)", got, future+30*planDayMs)
	}
	if got := planRecord(t, "lapsed@renew").ExpiryTime; !withinMs(got, now+30*planDayMs, 60_000) {
		t.Errorf("lapsed expiry = %d, want 30 days from now (~%d)", got, now+30*planDayMs)
	}
	if got := planRecord(t, "never@renew").ExpiryTime; got != 0 {
		t.Errorf("a user without an expiry got one: %d", got)
	}
	if got := planRecord(t, "unstarted@renew").ExpiryTime; got != -37*planDayMs {
		t.Errorf("unstarted expiry = %d, want a first period of 37 days (%d)", got, -37*planDayMs)
	}
	if !planRecord(t, "lapsed@renew").Enable {
		t.Error("the lapsed user is still disabled after renewing")
	}
	for _, email := range emails {
		if used := usageOf(t, email); used != 0 {
			t.Errorf("%s usage = %d after a renew with reset, want 0", email, used)
		}
	}
}

// Without a reset the usage stays: a user cut off by their expiry comes back, one who
// used up the quota stays off, and so does one the admin switched off by hand.
func TestRenewWithoutAResetKeepsUsageAndReenablesOnlyUsersItCutOff(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	past := time.Now().Add(-2 * 24 * time.Hour).UnixMilli()
	for _, email := range []string{"within@renew", "spent@renew"} {
		createPlanClient(t, email, []int{a}, past)
		giveQuota(t, email, 10*planGiB)
		disable(t, email)
	}
	seedUsage(t, "within@renew", planGiB, 2*planGiB)
	seedUsage(t, "spent@renew", 5*planGiB, 7*planGiB)
	createPlanClient(t, "paused@renew", []int{a}, time.Now().Add(5*24*time.Hour).UnixMilli())
	disable(t, "paused@renew")

	now := time.Now().UnixMilli()
	emails := []string{"within@renew", "spent@renew", "paused@renew"}
	if _, err := (&ClientService{}).Renew(&InboundService{}, emails, RenewalTerm{Days: 30}, false); err != nil {
		t.Fatalf("renew: %v", err)
	}

	within, spent, paused := planRecord(t, "within@renew"), planRecord(t, "spent@renew"), planRecord(t, "paused@renew")
	if !within.Enable || spent.Enable || paused.Enable {
		t.Errorf("enabled: within %v, spent %v, paused %v; want only the user the expiry cut off back on",
			within.Enable, spent.Enable, paused.Enable)
	}
	for _, rec := range []string{"within@renew", "spent@renew"} {
		if got := planRecord(t, rec).ExpiryTime; !withinMs(got, now+30*planDayMs, 60_000) {
			t.Errorf("%s expiry = %d, want 30 days from now", rec, got)
		}
	}
	if used := usageOf(t, "within@renew"); used != 3*planGiB {
		t.Errorf("usage = %d without a reset, want the 3 GiB kept", used)
	}
}

// Renewing by months saves the day it lands on, so a user on the 31st comes back to
// it after February: 31 January, 28 February, 31 March.
func TestRenewByMonthsRemembersTheDay(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	createPlanClient(t, "eom@renew", []int{a}, time.Date(2035, time.January, 31, 20, 0, 0, 0, AccountLocation).UnixMilli())
	for _, want := range []time.Time{
		time.Date(2035, time.February, 28, 20, 0, 0, 0, AccountLocation),
		time.Date(2035, time.March, 31, 20, 0, 0, 0, AccountLocation),
	} {
		if _, err := (&ClientService{}).Renew(&InboundService{}, []string{"eom@renew"}, RenewalTerm{Months: 1}, false); err != nil {
			t.Fatal(err)
		}
		if rec := planRecord(t, "eom@renew"); rec.ExpiryTime != want.UnixMilli() || rec.RenewDay != 31 {
			t.Fatalf("renewed to %s on day %d, want %s on day 31", dateOf(rec.ExpiryTime), rec.RenewDay, dateOf(want.UnixMilli()))
		}
	}
}

func TestRenewNeedsMonthsOrDays(t *testing.T) {
	a, _, _ := setupPlanDB(t)
	createPlanClient(t, "zero@renew", []int{a}, 0)
	if _, err := (&ClientService{}).Renew(&InboundService{}, []string{"zero@renew"}, RenewalTerm{}, true); err == nil || err.Error() != "a renewal is a number of months or a number of days\n" {
		t.Fatalf("renew without a term = %v, want the term error", err)
	}
}

func TestRenewRejectsOverflowWithoutChangingExpiryOrUsage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expiry int64
		term   RenewalTerm
		want   string
	}{
		{"duration", 0, RenewalTerm{Days: 36501}, "a renewal is at most 1200 months or 36500 days"},
		{"expiry", math.MaxInt64 - planDayMs, RenewalTerm{Days: 2}, "the renewal exceeds the supported expiry range\n"},
		{"expiry by months", math.MaxInt64 - planDayMs, RenewalTerm{Months: 1}, "the renewal exceeds the supported expiry range\n"},
		{"first use", math.MinInt64 + planDayMs, RenewalTerm{Days: 2}, "the renewal exceeds the supported expiry range\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _ := setupPlanDB(t)
			createPlanClient(t, "overflow@renew", []int{a}, tc.expiry)
			beforeExpiry := planRecord(t, "overflow@renew").ExpiryTime
			seedUsage(t, "overflow@renew", planGiB, 2*planGiB)
			if _, err := (&ClientService{}).Renew(&InboundService{}, []string{"overflow@renew"}, tc.term, true); err == nil || err.Error() != tc.want {
				t.Fatalf("renew = %v, want %q", err, tc.want)
			}
			if got := planRecord(t, "overflow@renew").ExpiryTime; got != beforeExpiry {
				t.Errorf("expiry changed to %d, want %d", got, beforeExpiry)
			}
			if got := usageOf(t, "overflow@renew"); got != 3*planGiB {
				t.Errorf("usage changed to %d, want 3 GiB", got)
			}
		})
	}
}

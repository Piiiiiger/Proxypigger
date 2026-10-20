package service

import (
	"testing"
	"time"
)

// beijing is UTC+8 kept apart from AccountLocation, so a renewal counted in another
// zone shows here instead of moving the expected dates along with it.
var beijing = time.FixedZone("UTC+8 (test)", 8*60*60)

func at(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, beijing)
}

func atMs(year int, month time.Month, day, hour, minute int) int64 {
	return at(year, month, day, hour, minute).UnixMilli()
}

func dateOf(ms int64) string { return time.UnixMilli(ms).In(beijing).Format("2006-01-02 15:04") }

// A month is the same date of the next month, whatever its length: 30 days from
// 15 December would land on 14 January.
func TestMonthlyRenewalKeepsTheCalendarDate(t *testing.T) {
	now := at(2026, time.October, 20, 9, 0)
	cases := []struct {
		name   string
		expiry int64
		term   RenewalTerm
		want   string
	}{
		{"monthly", atMs(2026, time.November, 15, 14, 35), RenewalTerm{Months: 1}, "2026-12-15 14:35"},
		{"over a 31-day month", atMs(2026, time.December, 15, 14, 35), RenewalTerm{Months: 1}, "2027-01-15 14:35"},
		{"quarterly", atMs(2026, time.November, 15, 14, 35), RenewalTerm{Months: 3}, "2027-02-15 14:35"},
		{"yearly", atMs(2026, time.November, 15, 14, 35), RenewalTerm{Months: 12}, "2027-11-15 14:35"},
		{"midnight UTC+8 is still the 1st", atMs(2027, time.March, 1, 0, 0), RenewalTerm{Months: 1}, "2027-04-01 00:00"},
		{"days stay days", atMs(2026, time.November, 15, 14, 35), RenewalTerm{Days: 7}, "2026-11-22 14:35"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := RenewedExpiry(c.expiry, 0, c.term, now)
			if err != nil {
				t.Fatal(err)
			}
			if dateOf(got) != c.want {
				t.Errorf("renewed expiry = %s, want %s", dateOf(got), c.want)
			}
		})
	}
}

// A date a shorter month lacks clamps to its last day, and the remembered day
// brings the next renewal back to it: 31 Jan, 28 Feb, 31 Mar.
func TestMonthEndRenewalsReturnToTheOriginalDay(t *testing.T) {
	now := at(2026, time.October, 20, 9, 0)
	expiry, day := atMs(2027, time.January, 31, 20, 0), 0
	var got []string
	for range 4 {
		var err error
		if expiry, day, err = RenewedExpiry(expiry, day, RenewalTerm{Months: 1}, now); err != nil {
			t.Fatal(err)
		}
		got = append(got, dateOf(expiry))
	}
	want := []string{"2027-02-28 20:00", "2027-03-31 20:00", "2027-04-30 20:00", "2027-05-31 20:00"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("renewals = %v, want %v", got, want)
		}
	}
	if day != 31 {
		t.Errorf("remembered day = %d, want 31", day)
	}
	leap, _, err := RenewedExpiry(atMs(2028, time.January, 30, 8, 0), 0, RenewalTerm{Months: 1}, now)
	if err != nil || dateOf(leap) != "2028-02-29 08:00" {
		t.Errorf("30 Jan 2028 + 1 month = %s (%v), want the leap day", dateOf(leap), err)
	}
}

// The remembered day only holds while the expiry still sits on it: once an admin
// moves the expiry by hand, its own day takes over.
func TestAMovedExpiryForgetsTheOldDay(t *testing.T) {
	now := at(2026, time.October, 20, 9, 0)
	got, day, err := RenewedExpiry(atMs(2027, time.February, 20, 10, 0), 31, RenewalTerm{Months: 1}, now)
	if err != nil || dateOf(got) != "2027-03-20 10:00" || day != 20 {
		t.Errorf("renewal of a moved expiry = %s, day %d (%v), want 2027-03-20 10:00 on day 20", dateOf(got), day, err)
	}
}

// A lapsed user's months start now and are remembered from today.
func TestALapsedUserRenewsFromNow(t *testing.T) {
	now := at(2026, time.October, 31, 9, 30)
	got, day, err := RenewedExpiry(atMs(2026, time.September, 15, 10, 0), 15, RenewalTerm{Months: 1}, now)
	if err != nil || dateOf(got) != "2026-11-30 09:30" || day != 31 {
		t.Errorf("lapsed renewal = %s, day %d (%v), want 2026-11-30 09:30 on day 31", dateOf(got), day, err)
	}
}

// A period not started yet grows by the length of those months from today, and an
// account that never expires keeps never expiring.
func TestUnstartedAndUnlimitedExpiriesUnderMonths(t *testing.T) {
	now := at(2026, time.October, 20, 9, 0)
	unstarted := -7 * planDayMillis
	got, _, err := RenewedExpiry(unstarted, 0, RenewalTerm{Months: 1}, now)
	if err != nil || got != unstarted-31*planDayMillis {
		t.Errorf("unstarted renewal = %d (%v), want the 31 days from 20 Oct to 20 Nov added", got, err)
	}
	if got, _, err := RenewedExpiry(0, 0, RenewalTerm{Months: 1}, now); err != nil || got != 0 {
		t.Errorf("unlimited renewal = %d (%v), want 0", got, err)
	}
}

// A renewal is months or days, never both or neither, within a century.
func TestRenewalTermBounds(t *testing.T) {
	now := at(2026, time.October, 20, 9, 0)
	for _, bad := range []RenewalTerm{{}, {Months: 1, Days: 1}, {Months: -1}, {Days: -3}, {Months: 1201}, {Days: 36501}} {
		if _, _, err := RenewedExpiry(atMs(2026, time.November, 1, 0, 0), 0, bad, now); err == nil {
			t.Errorf("term %+v was accepted", bad)
		}
	}
}

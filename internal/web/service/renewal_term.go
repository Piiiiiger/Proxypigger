package service

import (
	"math"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// AccountLocation is the zone users, admins and the bot read dates in, so a month
// of renewal ends on the date they see; the server's own clock is UTC.
var AccountLocation = time.FixedZone("UTC+8", 8*60*60)

// maxTermMonths and maxTermDays bound a term at about a century.
const (
	maxTermMonths = 1200
	maxTermDays   = 36500
)

// RenewalTerm is how long a renewal adds: calendar months, which end on the same
// date however long each month is, or plain days.
type RenewalTerm struct {
	Months int `json:"months" example:"1"`
	Days   int `json:"days" example:"0"`
}

func (t RenewalTerm) check() error {
	switch {
	case t.Months < 0 || t.Days < 0 || (t.Months > 0) == (t.Days > 0):
		return common.NewError("a renewal is a number of months or a number of days")
	case t.Months > maxTermMonths || t.Days > maxTermDays:
		return common.NewErrorf("a renewal is at most %d months or %d days", maxTermMonths, maxTermDays)
	}
	return nil
}

// addMonths moves t by months onto day, clamped to the month's last, at the same
// time of day in loc: time.AddDate would roll 31 February into March.
func addMonths(t time.Time, months, day int, loc *time.Location) time.Time {
	local := t.In(loc)
	total := int(local.Month()) - 1 + months
	year, month := local.Year()+total/12, time.Month(total%12+1)
	return time.Date(year, month, min(day, daysInMonth(year, month)), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), loc)
}

// renewalDay is the day a month renewal of expiry lands on: the remembered day
// while the expiry still sits on it (clamped), else the expiry's own day.
func renewalDay(expiry time.Time, remembered int) int {
	local := expiry.In(AccountLocation)
	if remembered >= 1 && remembered <= 31 && min(remembered, daysInMonth(local.Year(), local.Month())) == local.Day() {
		return remembered
	}
	return local.Day()
}

// RenewedExpiry is the expiry a renewal leaves and the day of the month to remember
// for the next one, so a preview shows exactly what Renew writes. Time is added to
// the later of now and the expiry; none stays none, an unstarted period grows.
func RenewedExpiry(expiry int64, day int, term RenewalTerm, now time.Time) (int64, int, error) {
	if err := term.check(); err != nil {
		return 0, 0, err
	}
	if term.Days > 0 {
		next, err := addDays(expiry, term.Days, now)
		return next, day, err
	}
	switch {
	case expiry > now.UnixMilli():
		day = renewalDay(time.UnixMilli(expiry), day)
		next, err := monthsLater(time.UnixMilli(expiry), term.Months, day)
		return next, day, err
	case expiry > 0:
		day = now.In(AccountLocation).Day()
		next, err := monthsLater(now, term.Months, day)
		return next, day, err
	case expiry < 0:
		span := addMonths(now, term.Months, now.In(AccountLocation).Day(), AccountLocation).UnixMilli() - now.UnixMilli()
		if expiry < math.MinInt64+span {
			return 0, 0, common.NewError("the renewal exceeds the supported expiry range")
		}
		return expiry - span, day, nil
	}
	return 0, day, nil
}

// monthsLater is addMonths as an expiry, refusing one past what an expiry holds:
// beyond it UnixMilli wraps around below where it started.
func monthsLater(t time.Time, months, day int) (int64, error) {
	next := addMonths(t, months, day, AccountLocation).UnixMilli()
	if next <= t.UnixMilli() {
		return 0, common.NewError("the renewal exceeds the supported expiry range")
	}
	return next, nil
}

func addDays(expiry int64, days int, now time.Time) (int64, error) {
	add := int64(days) * planDayMillis
	switch {
	case expiry > 0:
		base := max(expiry, now.UnixMilli())
		if base > math.MaxInt64-add {
			return 0, common.NewError("the renewal exceeds the supported expiry range")
		}
		return base + add, nil
	case expiry < 0:
		if expiry < math.MinInt64+add {
			return 0, common.NewError("the renewal exceeds the supported expiry range")
		}
		return expiry - add, nil
	}
	return 0, nil
}

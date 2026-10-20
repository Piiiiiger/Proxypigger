package service

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// Renew gives each user the term from the later of now and their expiry: a user
// without an expiry keeps none, one not yet started gets a longer first period.
func (s *ClientService) Renew(inboundSvc *InboundService, emails []string, term RenewalTerm, resetUsage bool) (bool, error) {
	if err := term.check(); err != nil {
		return false, err
	}
	for _, email := range emails {
		if err := s.checkRenewalTerm(email, term); err != nil {
			return false, err
		}
	}
	needRestart := false
	for _, email := range emails {
		nr, err := s.renewOne(inboundSvc, email, term, resetUsage)
		needRestart = needRestart || nr
		if err != nil {
			return needRestart, err
		}
	}
	return needRestart, nil
}

func (s *ClientService) renewOne(inboundSvc *InboundService, email string, term RenewalTerm, resetUsage bool) (bool, error) {
	rec, err := s.GetRecordByEmail(nil, email)
	if err != nil {
		return false, err
	}
	cutOff := false
	if !rec.Enable {
		if cutOff, err = depletedNow(email); err != nil {
			return false, err
		}
	}
	client := rec.ToClient()
	var day int
	if client.ExpiryTime, day, err = RenewedExpiry(rec.ExpiryTime, rec.RenewDay, term, time.Now()); err != nil {
		return false, err
	}
	needRestart, err := s.Update(inboundSvc, rec.Id, *client, rec.LimitHwid)
	if err != nil {
		return needRestart, err
	}
	if day != rec.RenewDay {
		if err := database.GetDB().Model(&model.ClientRecord{}).Where("id = ?", rec.Id).UpdateColumn("renew_day", day).Error; err != nil {
			return needRestart, err
		}
	}
	if resetUsage {
		nr, err := s.ResetTrafficByEmail(inboundSvc, email)
		return needRestart || nr, err
	}
	// As bulk adjust: only a user the traffic job cut off, and whom the renewal frees.
	if !cutOff {
		return needRestart, nil
	}
	if still, err := depletedNow(email); err != nil || still {
		return needRestart, err
	}
	_, nr, err := s.BulkSetEnable(inboundSvc, []string{email}, true)
	return needRestart || nr, err
}

// depletedNow reports whether the user is past their expiry or quota, by the test the
// traffic job disables users with.
func depletedNow(email string) (bool, error) {
	db := database.GetDB()
	cond, args := depletedCond(db)
	var found int64
	err := db.Model(xray.ClientTraffic{}).Where(cond+" AND email = ?", append(args, email)...).Count(&found).Error
	return found > 0, err
}

// checkRenewalTerm refuses a term that is not one the user's plan is sold by,
// before any user of the renewal changes.
func (s *ClientService) checkRenewalTerm(email string, term RenewalTerm) error {
	rec, err := s.GetRecordByEmail(nil, email)
	if err != nil || rec.PlanId == 0 {
		return err
	}
	var plan model.Plan
	if err := database.GetDB().First(&plan, rec.PlanId).Error; err != nil {
		return err
	}
	return checkPlanTerm(&plan, term, email+" is on plan %q, which renews by %s months only")
}

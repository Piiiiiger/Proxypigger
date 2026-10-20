package service

import (
	"crypto/rand"
	"errors"
	"math"
	"math/big"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// ErrActivationCode is the one answer to an unknown, expired or used code, so a guess learns
// nothing about which codes exist.
var ErrActivationCode = errors.New("the activation code is not valid, has expired or has already been used")

// ErrUsernameTaken refuses a registration under a name another user already has.
var ErrUsernameTaken = errors.New("that username is taken")

var activationUseMu sync.Mutex

const (
	activationCodeMaxBatch = 200
	// No 0/O or 1/I, which are misread when typed; 32 letters keep each byte unbiased.
	activationCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	activationCodeLength   = 16
	usernameMinLen         = 2
	usernameMaxLen         = 32
)

// ActivationCodeService makes the codes plans are handed out with, and lets the
// portal register a person with one or renew a signed-in person.
type ActivationCodeService struct {
	clientService ClientService
	planService   PlanService
	portalService ClientPortalService
}

// ActivationCodeInput asks for count codes for a plan, each granting the same quota
// (bytes, 0 for none), months or days (neither never expires) and monthly reset day.
type ActivationCodeInput struct {
	PlanId   int    `json:"planId" example:"2"`
	Count    int    `json:"count" example:"5"`
	TotalGB  int64  `json:"totalGB" example:"107374182400"`
	Months   int    `json:"months" example:"1"`
	Days     int    `json:"days" example:"0"`
	ResetDay int    `json:"resetDay" example:"1"`
	Note     string `json:"note" example:"March group"`
}

func (s *ActivationCodeService) Create(in ActivationCodeInput) ([]model.ActivationCode, error) {
	activationUseMu.Lock()
	defer activationUseMu.Unlock()
	switch {
	case in.Count < 1 || in.Count > activationCodeMaxBatch:
		return nil, common.NewErrorf("make 1 to %d codes at a time", activationCodeMaxBatch)
	case in.TotalGB < 0:
		return nil, common.NewError("the quota cannot be negative")
	case in.Months < 0 || in.Days < 0 || (in.Months > 0 && in.Days > 0) || in.Months > maxTermMonths || in.Days > maxTermDays:
		return nil, common.NewErrorf("a code lasts 0 to %d months or 0 to %d days, not both", maxTermMonths, maxTermDays)
	case in.ResetDay < 0 || in.ResetDay > 31:
		return nil, common.NewError("the reset day must be 0 (none) to 31")
	case utf8.RuneCountInString(in.Note) > 256:
		return nil, common.NewError("the note must be at most 256 characters")
	}
	if plan, inboundIds, err := s.planService.Get(in.PlanId); err != nil {
		return nil, err
	} else if len(inboundIds) == 0 {
		return nil, common.NewError("the plan has no nodes yet, so a code for it would give nothing")
	} else if err := checkPlanTerm(plan, RenewalTerm{Months: in.Months, Days: in.Days}, "plan %q is sold by %s months only"); err != nil {
		return nil, err
	}
	codes := make([]model.ActivationCode, 0, in.Count)
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		for range in.Count {
			code, err := newActivationCode()
			if err != nil {
				return err
			}
			row := model.ActivationCode{
				Code: code, PlanId: in.PlanId, TotalGB: in.TotalGB, Months: in.Months, Days: in.Days, ResetDay: in.ResetDay,
				Note: strings.TrimSpace(in.Note),
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			codes = append(codes, row)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// List returns the plan's codes (every code for planId 0), newest first.
func (s *ActivationCodeService) List(planId int) ([]model.ActivationCode, error) {
	codes := []model.ActivationCode{}
	query := database.GetDB().Order("id DESC")
	if planId > 0 {
		query = query.Where("plan_id = ?", planId)
	}
	if err := query.Find(&codes).Error; err != nil {
		return nil, err
	}
	for i := range codes {
		codes[i].ExpiresAt, _ = activationCodeDeadline(&codes[i])
	}
	return codes, nil
}

func (s *ActivationCodeService) Delete(id int) error {
	activationUseMu.Lock()
	defer activationUseMu.Unlock()
	return database.GetDB().Delete(&model.ActivationCode{}, id).Error
}

// Register makes a new user from a code: on its plan, with its quota, a period
// ending at the code's creation-based deadline and its reset day.
func (s *ActivationCodeService) Register(inboundSvc *InboundService, username, password, code string) (*model.ClientRecord, bool, error) {
	activationUseMu.Lock()
	defer activationUseMu.Unlock()
	username = strings.TrimSpace(username)
	if n := utf8.RuneCountInString(username); n < usernameMinLen || n > usernameMaxLen || validateClientEmail(username) != nil {
		return nil, false, common.NewErrorf("a username is %d to %d characters, without spaces or slashes", usernameMinLen, usernameMaxLen)
	}
	if len(password) < portalPasswordMinLen || len(password) > portalPasswordMaxLen {
		return nil, false, common.NewErrorf("a password is %d to %d characters", portalPasswordMinLen, portalPasswordMaxLen)
	}
	var taken int64
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("LOWER(email) = LOWER(?)", username).Count(&taken).Error; err != nil {
		return nil, false, err
	}
	if taken > 0 {
		return nil, false, ErrUsernameTaken
	}
	grant, plan, inboundIds, err := s.claim(code, username, nil)
	if err != nil {
		return nil, false, err
	}
	client := model.Client{Email: username, Enable: true, LimitIP: plan.LimitIP}
	if err := applyCodeGrant(&client, grant, grant.UsedAt); err != nil {
		s.release(grant.Id, username)
		return nil, false, err
	}
	if visionTaken, err := anyInboundTakesVision(inboundIds); err != nil {
		s.release(grant.Id, username)
		return nil, false, err
	} else if visionTaken {
		client.Flow = visionFlow
	}
	needRestart, err := s.clientService.Create(inboundSvc, &ClientCreatePayload{Client: client, InboundIds: inboundIds})
	if err != nil {
		needRestart = s.undoRegistration(inboundSvc, grant, username, needRestart)
		return nil, needRestart, err
	}
	err = database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", username).
		UpdateColumns(map[string]any{"plan_id": plan.Id, "renew_day": codeRenewDay(grant)}).Error
	if err == nil {
		err = s.portalService.SetPassword(username, password)
	}
	if err != nil {
		needRestart = s.undoRegistration(inboundSvc, grant, username, needRestart)
		return nil, needRestart, err
	}
	rec, err := s.clientService.GetRecordByEmail(nil, username)
	if err == nil {
		_, err = EnsureAccountActivation(rec)
	}
	return rec, needRestart, err
}

func (s *ActivationCodeService) undoRegistration(inboundSvc *InboundService, grant *model.ActivationCode, username string, needRestart bool) bool {
	if _, err := s.clientService.GetRecordByEmail(nil, username); errors.Is(err, gorm.ErrRecordNotFound) {
		s.release(grant.Id, username)
	} else if err != nil {
		logger.Warning("activation code: could not check the half-made user", username, err)
	} else if nr, err := s.clientService.DeleteByEmail(inboundSvc, username, false); err != nil {
		logger.Warning("activation code: could not remove the half-made user", username, err)
		needRestart = needRestart || nr
	} else {
		needRestart = needRestart || nr
		s.release(grant.Id, username)
	}
	return needRestart
}

// Redeem applies a code to a signed-in user: its plan, quota and reset day, its
// days on top of what is left (from today once that has run out), usage cleared.
func (s *ActivationCodeService) Redeem(inboundSvc *InboundService, email, code string) (bool, error) {
	activationUseMu.Lock()
	defer activationUseMu.Unlock()
	rec, err := s.clientService.GetRecordByEmail(nil, email)
	if err != nil {
		return false, err
	}
	var pending int64
	if err := database.GetDB().Model(&model.ActivationCode{}).
		Where("used_by = ? AND redeem_expiry IS NOT NULL AND code <> ?", email, canonicalActivationCode(code)).Count(&pending).Error; err != nil {
		return false, err
	}
	if pending > 0 {
		return false, common.NewError("retry the previous activation code to finish its renewal")
	}
	grant, plan, _, err := s.claim(code, email, rec)
	if err != nil {
		return false, err
	}
	needRestart, err := s.redeem(inboundSvc, email, grant, plan)
	if err == nil {
		err = database.GetDB().Model(&model.ActivationCode{}).Where("id = ?", grant.Id).UpdateColumn("redeem_expiry", nil).Error
	}
	return needRestart, err
}

func (s *ActivationCodeService) redeem(inboundSvc *InboundService, email string, grant *model.ActivationCode, plan *model.Plan) (bool, error) {
	needRestart, err := s.planService.Assign(inboundSvc, []string{email}, plan.Id)
	if err != nil {
		return needRestart, err
	}
	rec, err := s.clientService.GetRecordByEmail(nil, email)
	if err != nil {
		return needRestart, err
	}
	client := rec.ToClient()
	start := time.Now().UnixMilli()
	if rec.ExpiryTime > start {
		start = rec.ExpiryTime
	}
	if err := applyCodeGrant(client, grant, start); err != nil {
		return needRestart, err
	}
	client.ExpiryTime = *grant.RedeemExpiry
	nr, err := s.clientService.Update(inboundSvc, rec.Id, *client, rec.LimitHwid)
	needRestart = needRestart || nr
	if err != nil {
		return needRestart, err
	}
	if grant.Months > 0 {
		if err := database.GetDB().Model(&model.ClientRecord{}).Where("id = ?", rec.Id).UpdateColumn("renew_day", grant.RedeemDay).Error; err != nil {
			return needRestart, err
		}
	}
	nr, err = s.clientService.ResetTrafficByEmail(inboundSvc, email)
	return needRestart || nr, err
}

// claim marks the code used by who, so no one else can use it meanwhile, and
// returns it with its plan and the plan's nodes.
func (s *ActivationCodeService) claim(code, who string, renewal *model.ClientRecord) (*model.ActivationCode, *model.Plan, []int, error) {
	canonical := canonicalActivationCode(code)
	if canonical == "" {
		return nil, nil, nil, ErrActivationCode
	}
	db := database.GetDB()
	var grant model.ActivationCode
	if err := db.Where("code = ?", canonical).First(&grant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, ErrActivationCode
		}
		return nil, nil, nil, err
	}
	if grant.UsedAt != 0 && (renewal == nil || grant.RedeemExpiry == nil || grant.UsedBy != who) {
		return nil, nil, nil, ErrActivationCode
	}
	plan, inboundIds, err := s.planService.Get(grant.PlanId)
	if err != nil {
		return nil, nil, nil, ErrActivationCode
	}
	if len(inboundIds) == 0 {
		return nil, nil, nil, common.NewError("the code's plan has no nodes")
	}
	if grant.UsedAt != 0 {
		return &grant, plan, inboundIds, nil
	}
	now := time.Now().UnixMilli()
	expiresAt, err := activationCodeDeadline(&grant)
	if err != nil {
		return nil, nil, nil, err
	}
	if expiresAt != 0 && now >= expiresAt {
		return nil, nil, nil, ErrActivationCode
	}
	// Freeze the remaining duration at the same instant we claim the code.
	grant.UsedAt = now
	updates := map[string]any{"used_at": now, "used_by": who}
	if renewal != nil {
		expiry, day, err := codeRenewal(&grant, renewal, now)
		if err != nil {
			return nil, nil, nil, err
		}
		grant.RedeemExpiry, grant.RedeemDay = &expiry, day
		updates["redeem_expiry"], updates["redeem_day"] = expiry, day
	}
	res := db.Model(&model.ActivationCode{}).Where("id = ? AND used_at = 0", grant.Id).
		Updates(updates)
	if res.Error != nil {
		return nil, nil, nil, res.Error
	}
	if res.RowsAffected != 1 {
		return nil, nil, nil, ErrActivationCode
	}
	grant.UsedAt, grant.UsedBy = now, who
	return &grant, plan, inboundIds, nil
}

// release gives a claimed code back after what it was claimed for failed.
func (s *ActivationCodeService) release(id int, who string) {
	err := database.GetDB().Model(&model.ActivationCode{}).Where("id = ? AND used_by = ?", id, who).
		Updates(map[string]any{"used_at": 0, "used_by": ""}).Error
	if err != nil {
		logger.Warning("activation code: could not give code", id, "back:", err)
	}
}

// applyCodeGrant sets the quota, remaining duration from start (none for 0) and its
// monthly reset day (none for 0).
func applyCodeGrant(client *model.Client, grant *model.ActivationCode, start int64) error {
	expiry := int64(0)
	if grant.RedeemExpiry != nil {
		expiry = *grant.RedeemExpiry
	} else {
		var err error
		expiry, err = codeExpiry(grant, start)
		if err != nil {
			return err
		}
	}
	client.TotalGB = grant.TotalGB
	client.ExpiryTime = expiry
	client.TrafficReset, client.TrafficResetDay = "never", 0
	if grant.ResetDay > 0 {
		client.TrafficReset, client.TrafficResetDay = "monthly", grant.ResetDay
	}
	return nil
}

func codeExpiry(grant *model.ActivationCode, start int64) (int64, error) {
	deadline, err := activationCodeDeadline(grant)
	if err != nil || deadline == 0 {
		return deadline, err
	}
	remaining := deadline - grant.UsedAt
	if grant.UsedAt <= 0 || remaining <= 0 {
		return 0, ErrActivationCode
	}
	if start > math.MaxInt64-remaining {
		return 0, common.NewError("the activation exceeds the supported expiry range")
	}
	return start + remaining, nil
}

// codeRenewal is what a code adds to a signed-in user on top of what is left (from now
// once that has run out), and the day to remember for the next renewal.
func codeRenewal(grant *model.ActivationCode, rec *model.ClientRecord, now int64) (int64, int, error) {
	if grant.Months == 0 {
		expiry, err := codeExpiry(grant, max(now, rec.ExpiryTime))
		return expiry, rec.RenewDay, err
	}
	if grant.Months < 0 || grant.Months > maxTermMonths || grant.UsedAt < grant.CreatedAt {
		return 0, 0, ErrActivationCode
	}
	start, day := time.UnixMilli(now), time.UnixMilli(now).In(AccountLocation).Day()
	if rec.ExpiryTime > now {
		start, day = time.UnixMilli(rec.ExpiryTime), renewalDay(time.UnixMilli(rec.ExpiryTime), rec.RenewDay)
	}
	next, err := monthsLater(start, grant.Months, day)
	if err != nil {
		return 0, 0, err
	}
	deadline, err := activationCodeDeadline(grant)
	if err != nil || deadline <= grant.UsedAt {
		return 0, 0, ErrActivationCode
	}
	// Worth the share of its validity left when used, in the months it is added to:
	// the full months when fresh, never an end before start when nearly lapsed.
	worth := new(big.Int).Mul(big.NewInt(next-start.UnixMilli()), big.NewInt(deadline-grant.UsedAt))
	worth.Quo(worth, big.NewInt(deadline-grant.CreatedAt))
	return start.UnixMilli() + worth.Int64(), day, nil
}

// codeRenewDay is the day a new user's renewals by months land on: the day their
// code was made, which their first period ends on.
func codeRenewDay(grant *model.ActivationCode) int {
	if grant.Months == 0 {
		return 0
	}
	return time.UnixMilli(grant.CreatedAt).In(AccountLocation).Day()
}

func activationCodeDeadline(grant *model.ActivationCode) (int64, error) {
	switch {
	case grant.Months == 0 && grant.Days == 0:
		return 0, nil
	case grant.Months < 0 || grant.Days < 0 || grant.Months > maxTermMonths || grant.Days > maxTermDays ||
		grant.CreatedAt <= 0 || grant.CreatedAt > math.MaxInt64-int64(grant.Days)*planDayMillis:
		return 0, common.NewError("the activation exceeds the supported expiry range")
	case grant.Months > 0:
		created := time.UnixMilli(grant.CreatedAt)
		return addMonths(created, grant.Months, created.In(AccountLocation).Day(), AccountLocation).UnixMilli(), nil
	}
	return grant.CreatedAt + int64(grant.Days)*planDayMillis, nil
}

func anyInboundTakesVision(inboundIds []int) (bool, error) {
	var inbounds []model.Inbound
	if err := database.GetDB().Where("id IN ?", inboundIds).Find(&inbounds).Error; err != nil {
		return false, err
	}
	for _, ib := range inbounds {
		if !ib.DisableFlow && inboundCanEnableTlsFlow(string(ib.Protocol), ib.StreamSettings, ib.Settings) {
			return true, nil
		}
	}
	return false, nil
}

func newActivationCode() (string, error) {
	raw := make([]byte, activationCodeLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, v := range raw {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(activationCodeAlphabet[int(v)%len(activationCodeAlphabet)])
	}
	return b.String(), nil
}

// canonicalActivationCode reads a code however it was typed (any case, spaces or
// dashes anywhere) as it is stored; "" when it cannot be one.
func canonicalActivationCode(typed string) string {
	var letters []rune
	for _, r := range strings.ToUpper(typed) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letters = append(letters, r)
		}
	}
	if len(letters) != activationCodeLength {
		return ""
	}
	var b strings.Builder
	for i, r := range letters {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// LooksLikeActivationCode reports whether text could be a code as a person types
// one, so a chat can tell a code from a message.
func LooksLikeActivationCode(text string) bool {
	canonical := canonicalActivationCode(text)
	if canonical == "" {
		return false
	}
	for _, r := range strings.ReplaceAll(canonical, "-", "") {
		if !strings.ContainsRune(activationCodeAlphabet, r) {
			return false
		}
	}
	return true
}

package model

// ActivationCode lets one person register, or a signed-in person renew or change
// plan: it grants a plan with their quota, validity and monthly reset day.
type ActivationCode struct {
	Id     int    `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	Code   string `json:"code" gorm:"uniqueIndex;not null" example:"7KQ3-X9MZ-2F4H-RT8W"`
	PlanId int    `json:"planId" gorm:"column:plan_id;not null;index" example:"2"`
	// TotalGB is the quota in bytes, 0 for none. Months (calendar) or Days count from
	// CreatedAt, including time spent unused; neither never expires. ResetDay 0 never resets.
	TotalGB  int64  `json:"totalGB" gorm:"column:total_gb;not null;default:0" example:"107374182400"`
	Months   int    `json:"months" gorm:"not null;default:0" example:"1"`
	Days     int    `json:"days" gorm:"not null;default:0" example:"0"`
	ResetDay int    `json:"resetDay" gorm:"column:reset_day;not null;default:0" example:"1"`
	Note     string `json:"note" gorm:"not null;default:''" example:"for alice"`
	// UsedAt is 0 until the code is used; UsedBy is the email of whoever used it.
	UsedAt int64  `json:"usedAt" gorm:"column:used_at;not null;default:0" example:"0"`
	UsedBy string `json:"usedBy" gorm:"column:used_by;not null;default:''" example:""`
	// A failed renewal retries this same expiry and renewal day until every service
	// update succeeds.
	RedeemExpiry *int64 `json:"-"`
	RedeemDay    int    `json:"-" gorm:"column:redeem_day;not null;default:0"`
	CreatedAt    int64  `json:"createdAt" gorm:"autoCreateTime:milli" example:"1735689600000"`
	// ExpiresAt is when an unused code lapses, worked out for the list; 0 for never.
	ExpiresAt int64 `json:"expiresAt" gorm:"-" example:"1738368000000"`
}

func (ActivationCode) TableName() string { return "activation_codes" }

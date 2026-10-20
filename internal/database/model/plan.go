package model

// Plan is the servers (inbounds), rule template and IP limit a set of users share;
// each user keeps their own quota, expiry and reset schedule.
type Plan struct {
	Id      int    `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	Name    string `json:"name" gorm:"uniqueIndex;not null" example:"Monthly 100G"`
	LimitIP int    `json:"limitIp" gorm:"column:limit_ip;default:0" example:"0"`
	Remark  string `json:"remark" example:"Hong Kong and Singapore"`
	// TemplateId is the rule template its members' Clash subscriptions use; 0 is the
	// default template.
	TemplateId int `json:"templateId" gorm:"column:template_id;default:0;index" example:"1"`
	// ProxyGroups stores the plan's node-to-proxy-group assignments as JSON. It is
	// kept out of the model's wire representation; the plan service exposes the
	// typed value to API callers.
	ProxyGroups string `json:"-" gorm:"column:proxy_groups;type:text;default:''"`
	// NodeKeys distinguishes direct and relay subscription variants of an inbound.
	// Empty storage keeps the historical behavior of including both variants.
	NodeKeys string `json:"-" gorm:"column:node_keys;type:text;default:''"`
	// TermMonths lists, as JSON, the only terms in calendar months the plan is sold
	// and renewed by; empty allows any.
	TermMonths string `json:"-" gorm:"column:term_months;type:text;default:''"`
	SortIndex  int    `json:"sortIndex" gorm:"column:sort_index;default:0" example:"0"`
	CreatedAt  int64  `json:"createdAt" gorm:"autoCreateTime:milli" example:"1735689600000"`
	UpdatedAt  int64  `json:"updatedAt" gorm:"autoUpdateTime:milli" example:"1735689600000"`
}

// PlanInbound is the plan-to-inbound join: the servers a plan grants its members.
type PlanInbound struct {
	PlanId    int `json:"planId" gorm:"primaryKey;column:plan_id"`
	InboundId int `json:"inboundId" gorm:"primaryKey;column:inbound_id;index"`
}

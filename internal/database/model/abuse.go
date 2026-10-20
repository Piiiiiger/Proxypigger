package model

// AbuseEvent is one rule an account tripped on a server, with what tripped it
// and what was done; kept for review while the checks are tuned.
type AbuseEvent struct {
	Id      int    `json:"id" gorm:"primaryKey;autoIncrement" example:"7"`
	Email   string `json:"email" gorm:"not null;index" example:"alice"`
	NodeId  int    `json:"nodeId" example:"0"`
	Rule    string `json:"rule" example:"scan"`
	Level   string `json:"level" example:"strike"`
	Measure string `json:"measure" example:"ports"`
	Count   int64  `json:"count" example:"52"`
	Limit   int64  `json:"limit" example:"50"`
	Window  int    `json:"window" example:"300"`
	Samples string `json:"samples" example:"[\"198.51.100.7\"]"`
	Bytes   int64  `json:"bytes" gorm:"not null;default:0" example:"2500000000"`
	Action  string `json:"action" example:"banned"`
	At      int64  `json:"at" gorm:"index" example:"1791172800"`
}

// BanRecord is a ban as people see it afterwards, from the IP limit or the abuse
// checks. ExpiresAt 0 is a lock that only an admin lifts.
type BanRecord struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement" example:"3"`
	Email     string `json:"email" gorm:"not null;index" example:"alice"`
	Kind      string `json:"kind" example:"abuse"`
	Rule      string `json:"rule" example:"scan"`
	Reason    string `json:"reason" example:"端口扫描：5 分钟内连接同一 IP 的 52 个端口"`
	Network   string `json:"network" example:"198.51.100.7"`
	Strike    int    `json:"strike" example:"1"`
	EventId   int    `json:"eventId" example:"7"`
	BannedAt  int64  `json:"bannedAt" gorm:"index" example:"1791172800"`
	ExpiresAt int64  `json:"expiresAt" example:"1791174600"`
	LiftedAt  int64  `json:"liftedAt" example:"0"`
	Forgiven  bool   `json:"forgiven" example:"false"`
}

// PortalSignup is one sign-up on the user page and the network it came from,
// kept a day for the sign-up guard.
type PortalSignup struct {
	Id      int    `gorm:"primaryKey;autoIncrement"`
	Email   string `gorm:"not null"`
	Network string `gorm:"not null;index"`
	At      int64  `gorm:"index"`
}

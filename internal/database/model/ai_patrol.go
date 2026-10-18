package model

// AIPatrolReview is a durable receipt, including reports that did not qualify.
type AIPatrolReview struct {
	Id      int    `gorm:"primaryKey"`
	NodeID  int    `gorm:"uniqueIndex:idx_ai_patrol_report,priority:1"`
	RunID   string `gorm:"uniqueIndex:idx_ai_patrol_report,priority:2"`
	At      int64
	Outcome string
}

// AIPatrolViolation survives report retention, restarts and notification retries.
// Its strikes are independent of the real-time detector's 30-minute penalties.
type AIPatrolViolation struct {
	Id           int    `json:"id" gorm:"primaryKey"`
	Email        string `json:"-" gorm:"not null;index"`
	NodeID       int    `json:"nodeId"`
	RunID        string `json:"runId"`
	Category     string `json:"category"`
	Reason       string `json:"reason"`
	Action       string `json:"action"`
	At           int64  `json:"at" gorm:"index"`
	WindowStart  int64  `json:"windowStart"`
	WindowEnd    int64  `json:"windowEnd"`
	EventID      int    `json:"eventId"`
	NotifiedAt   int64  `json:"notifiedAt"`
	PortalSeenAt int64  `json:"portalSeenAt"`
	Forgiven     bool   `json:"forgiven"`
}

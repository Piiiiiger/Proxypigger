package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

const (
	AbuseRuleAIPatrol       = "ai_patrol"
	BanKindAIPatrol         = "ai_patrol"
	AIPatrolViolationAction = "ai_violation"
	AIPatrolSuspendedAction = "ai_suspended"
	AIPatrolSuspension      = 24 * time.Hour
	AIPatrolNotice          = "你的活动已被标记为一次违规。请联系管理员了解详情。本次不封禁；首次违规记录后的 30 天内，收到通知后再次发生独立且符合条件的违规，将暂停账号 24 小时。"
)

type AIPatrolSource struct {
	NodeID int    `json:"node_id"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
}
type AIPatrolConfig struct {
	Enabled        bool             `json:"enabled"`
	EnabledAfter   int64            `json:"enabled_after"`
	IdentityFile   string           `json:"identity_file"`
	KnownHostsFile string           `json:"known_hosts_file"`
	Nodes          []AIPatrolSource `json:"nodes"`
}
type AIPatrolFinding struct {
	UserID     string   `json:"user_id"`
	Severity   string   `json:"severity"`
	Confidence string   `json:"confidence"`
	Category   string   `json:"category"`
	Violation  bool     `json:"violation"`
	Hypothesis string   `json:"hypothesis"`
	Evidence   []string `json:"evidence"`
}
type AIPatrolUser struct {
	UserID        string `json:"user_id"`
	DetailLimited bool   `json:"detail_limited"`
	Direct        struct {
		Connections   int64 `json:"connections"`
		DistinctPorts int64 `json:"distinct_ports"`
		PeakPerMinute int64 `json:"peak_connections_per_minute"`
	} `json:"direct"`
}
type AIPatrolBundle struct {
	Version int    `json:"version"`
	RunID   string `json:"run_id"`
	Report  struct {
		Assessment string            `json:"assessment"`
		Findings   []AIPatrolFinding `json:"findings"`
	} `json:"report"`
	Evidence struct {
		Coverage struct {
			WindowStart   time.Time `json:"window_start"`
			WindowEnd     time.Time `json:"window_end"`
			Limited       bool      `json:"limited"`
			UnparsedLines int64     `json:"unparsed_lines"`
			AuditHealth   struct {
				StartedAt      time.Time `json:"started_at"`
				UpdatedAt      time.Time `json:"updated_at"`
				DroppedRecords int64     `json:"dropped_records"`
				WriteErrors    int64     `json:"write_errors"`
			} `json:"audit_health"`
			Files []struct {
				Readable bool `json:"readable"`
			} `json:"files"`
		} `json:"coverage"`
		Users []AIPatrolUser `json:"users"`
	} `json:"evidence"`
	Identities map[string]string `json:"identities"`
}

var aiPatrolRunID = regexp.MustCompile(`^[0-9]{8}T[0-9]{12}Z$`)
var aiPatrolMutex sync.Mutex

type AIPatrolService struct{}

func LoadAIPatrolConfig() (AIPatrolConfig, error) {
	path := os.Getenv("XUI_AI_PATROL_CONFIG")
	if path == "" {
		path = "/etc/ai-abuse-patrol/enforcement.json"
	}
	var c AIPatrolConfig
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return c, errors.New("insecure AI patrol config")
	}
	err = json.NewDecoder(io.LimitReader(f, 16384)).Decode(&c)
	if err == nil && c.Enabled && (c.EnabledAfter <= 0 || len(c.Nodes) == 0 || len(c.Nodes) > 8 || !strings.HasPrefix(c.IdentityFile, "/") || !strings.HasPrefix(c.KnownHostsFile, "/")) {
		err = errors.New("invalid AI patrol enforcement config")
	}
	return c, err
}

// Run only reads fixed exports from explicitly selected nodes. Models cannot
// select hosts, commands, identities, penalty durations, or database operations.
func (s *AIPatrolService) Run(now time.Time) error {
	if !aiPatrolMutex.TryLock() {
		return nil
	}
	defer aiPatrolMutex.Unlock()
	c, err := LoadAIPatrolConfig()
	if err != nil || !c.Enabled {
		return err
	}
	var errs []error
	for _, source := range c.Nodes {
		if source.NodeID <= 0 || net.ParseIP(source.Host) == nil || source.Port < 1 || source.Port > 65535 {
			return errors.New("invalid AI patrol source")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		cmd := exec.CommandContext(ctx, "/usr/bin/ssh", "-F", "/dev/null", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=1", "-o", "UserKnownHostsFile="+c.KnownHostsFile, "-i", c.IdentityFile, "-p", fmt.Sprint(source.Port), "root@"+source.Host, "ai-patrol-export-v2")
		var output bytes.Buffer
		cmd.Stdout = &boundedPatrolOutput{buffer: &output, left: 2 << 20}
		err = cmd.Run()
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("node %d export failed", source.NodeID))
			continue
		}
		var bundle AIPatrolBundle
		if err = json.Unmarshal(output.Bytes(), &bundle); err == nil {
			if bundle.RunID == "" {
				continue
			}
			_, err = s.Process(source.NodeID, c.EnabledAfter, bundle, now, false)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("node %d review rejected: %w", source.NodeID, err))
		}
	}
	return errors.Join(errs...)
}

type boundedPatrolOutput struct {
	buffer *bytes.Buffer
	left   int
}

func (b *boundedPatrolOutput) Write(p []byte) (int, error) {
	if len(p) > b.left {
		return 0, errors.New("patrol export exceeds limit")
	}
	b.left -= len(p)
	return b.buffer.Write(p)
}

func aiPatrolCoverageOK(b AIPatrolBundle, enabledAfter int64, now time.Time) bool {
	c := b.Evidence.Coverage
	h := c.AuditHealth
	if b.Version != 2 || !aiPatrolRunID.MatchString(b.RunID) || b.Report.Assessment != "review_needed" || len(b.Report.Findings) > 10 || len(b.Evidence.Users) > 100 {
		return false
	}
	if c.WindowEnd.Unix() <= enabledAfter || c.WindowEnd.After(now) || now.Sub(c.WindowEnd) > 2*time.Hour || c.WindowEnd.Sub(c.WindowStart) < 12*time.Hour || c.WindowEnd.Sub(c.WindowStart) > 24*time.Hour {
		return false
	}
	if c.Limited || c.UnparsedLines != 0 || len(c.Files) == 0 || h.StartedAt.IsZero() || h.StartedAt.After(c.WindowStart) || h.UpdatedAt.Before(c.WindowEnd.Add(-2*time.Minute)) || h.UpdatedAt.After(c.WindowEnd) || h.DroppedRecords != 0 || h.WriteErrors != 0 {
		return false
	}
	for _, f := range c.Files {
		if !f.Readable {
			return false
		}
	}
	return true
}

// Process commits the receipt, persistent violation and optional suspension in
// one transaction. dryRun rolls back all writes, including notification state.
func (s *AIPatrolService) Process(nodeID int, enabledAfter int64, b AIPatrolBundle, now time.Time, dryRun bool) (int, error) {
	if !aiPatrolRunID.MatchString(b.RunID) {
		return 0, errors.New("invalid patrol run id")
	}
	created := 0
	rollback := errors.New("AI patrol preview rollback")
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		var node model.Node
		if err := tx.First(&node, nodeID).Error; err != nil {
			return err
		}
		if !node.Enable || node.AbuseMode != AbuseModeEnforce || node.Kind != model.NodeKindAgent {
			return nil
		}
		receipt := model.AIPatrolReview{NodeID: nodeID, RunID: b.RunID, At: now.Unix(), Outcome: "not_qualified"}
		insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt)
		if insert.Error != nil {
			return insert.Error
		}
		if insert.RowsAffected == 0 {
			return nil
		}
		if aiPatrolCoverageOK(b, enabledAfter, now) {
			users := map[string]AIPatrolUser{}
			for _, u := range b.Evidence.Users {
				users[u.UserID] = u
			}
			for _, finding := range b.Report.Findings {
				if created >= 2 {
					break
				}
				u, ok := users[finding.UserID]
				if !ok || u.DetailLimited || !finding.Violation || finding.Severity != "high" || finding.Confidence != "high" || len(finding.Hypothesis) == 0 || len(finding.Hypothesis) > 2000 || len(finding.Evidence) == 0 || len(finding.Evidence) > 10 {
					continue
				}
				corroborated := finding.Category == "scan" && u.Direct.Connections >= 100 && u.Direct.DistinctPorts >= 50 || finding.Category == "connection_abuse" && u.Direct.Connections >= 5000 && u.Direct.PeakPerMinute >= 1000
				if !corroborated {
					continue
				}
				email := b.Identities[finding.UserID]
				if email == "" || len(email) > 256 {
					continue
				}
				var exists int64
				if err := tx.Table("clients").Joins("JOIN client_inbounds ci ON ci.client_id = clients.id").Joins("JOIN inbounds i ON i.id = ci.inbound_id").Where("clients.email = ? AND clients.enable = ? AND i.node_id = ? AND i.enable = ?", email, true, nodeID, true).Count(&exists).Error; err != nil {
					return err
				}
				if exists == 0 {
					continue
				}
				start, end := b.Evidence.Coverage.WindowStart.Unix(), b.Evidence.Coverage.WindowEnd.Unix()
				if err := tx.Model(&model.AIPatrolViolation{}).Where("email = ? AND window_start < ? AND window_end > ?", email, end, start).Count(&exists).Error; err != nil {
					return err
				}
				if exists > 0 {
					continue
				}
				// Do not add a delayed penalty to an incident already punished by real-time checks.
				if err := tx.Model(&model.BanRecord{}).Where("email = ? AND kind IN ? AND forgiven = ? AND banned_at < ? AND (expires_at = 0 OR expires_at > ?)", email, []string{BanKindAbuse, BanKindAIPatrol}, false, end, start).Count(&exists).Error; err != nil {
					return err
				}
				if exists > 0 {
					continue
				}
				var recent []model.AIPatrolViolation
				if err := tx.Where("email = ? AND at >= ? AND forgiven = ?", email, now.Add(-AbuseStrikeWindow).Unix(), false).Order("id").Find(&recent).Error; err != nil {
					return err
				}
				action := AIPatrolViolationAction
				for _, previous := range recent {
					// A repeat must concern later activity, after the prior notice was delivered.
					notified := previous.NotifiedAt
					if notified == 0 {
						notified = previous.PortalSeenAt
					}
					if previous.WindowEnd <= start && notified > 0 && notified <= start {
						action = AIPatrolSuspendedAction
						break
					}
				}
				active, err := activeAbuseBan(tx, email, now)
				if err != nil {
					return err
				}
				if active != nil {
					continue
				}
				samples, _ := json.Marshal(append([]string{finding.Hypothesis, "Codex report: " + b.RunID}, finding.Evidence...))
				event := model.AbuseEvent{Email: email, NodeId: nodeID, Rule: AbuseRuleAIPatrol, Level: "strike", Measure: "ai_patrol", Count: int64(len(recent) + 1), Limit: 2, Window: int(AbuseStrikeWindow.Seconds()), Samples: string(samples), Action: action, At: now.Unix()}
				if err := tx.Create(&event).Error; err != nil {
					return err
				}
				violation := model.AIPatrolViolation{Email: email, NodeID: nodeID, RunID: b.RunID, Category: finding.Category, Reason: finding.Hypothesis, Action: action, At: now.Unix(), WindowStart: start, WindowEnd: end, EventID: event.Id}
				if err := tx.Create(&violation).Error; err != nil {
					return err
				}
				if action == AIPatrolSuspendedAction {
					ban := model.BanRecord{Email: email, Kind: BanKindAIPatrol, Rule: AbuseRuleAIPatrol, Reason: "Codex AI 巡查：30 天内再次违规，暂停 24 小时。" + finding.Hypothesis, Strike: len(recent) + 1, EventId: event.Id, BannedAt: now.Unix(), ExpiresAt: now.Add(AIPatrolSuspension).Unix()}
					if err := tx.Create(&ban).Error; err != nil {
						return err
					}
				}
				created++
			}
		}
		if created > 0 {
			if err := tx.Model(&receipt).Update("outcome", fmt.Sprintf("%d violations", created)).Error; err != nil {
				return err
			}
		}
		if dryRun {
			return rollback
		}
		return nil
	})
	if errors.Is(err, rollback) {
		err = nil
	}
	return created, err
}

func (s *AIPatrolService) Notices(email string, now time.Time) ([]model.AIPatrolViolation, error) {
	rows := []model.AIPatrolViolation{}
	err := database.GetDB().Where("email = ? AND at >= ?", email, now.AddDate(0, 0, -90).Unix()).Order("id DESC").Limit(100).Find(&rows).Error
	return rows, err
}
func (s *AIPatrolService) Seen(email string, ids []int, now time.Time) error {
	if len(ids) > 100 {
		return errors.New("too many notice ids")
	}
	return database.GetDB().Model(&model.AIPatrolViolation{}).Where("email = ? AND id IN ? AND portal_seen_at = 0", email, ids).Update("portal_seen_at", now.Unix()).Error
}

func RunAIPatrol(now time.Time) {
	if err := (&AIPatrolService{}).Run(now); err != nil {
		logger.Warning("AI patrol enforcement: ", err)
	}
}

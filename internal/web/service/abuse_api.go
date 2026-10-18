package service

import (
	"slices"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

// AbuseServer is one server's detection mode. Capable is false for an agent too
// old to detect, or not connected to say: its mode then has no effect yet.
type AbuseServer struct {
	NodeId  int    `json:"nodeId" example:"0"`
	Name    string `json:"name" example:"de-fra-1"`
	Mode    string `json:"mode" example:"observe"`
	Capable bool   `json:"capable" example:"true"`
}

// AbuseEventView is a hit as the admin page lists it, said in words.
type AbuseEventView struct {
	model.AbuseEvent
	Server   string `json:"server" example:"de-fra-1"`
	Label    string `json:"label" example:"端口扫描"`
	Evidence string `json:"evidence" example:"5 分钟内连接同一 IP 的 52 个端口"`
}

// AbuseOverview is what the admin's abuse page shows at once.
type AbuseOverview struct {
	Servers  []AbuseServer     `json:"servers"`
	Settings AbuseSettings     `json:"settings"`
	Bans     []model.BanRecord `json:"bans"`
	Events   []AbuseEventView  `json:"events"`
}

// AbuseHistory is one account's standing and its bans of the last 90 days.
type AbuseHistory struct {
	Status          AbuseStatus               `json:"status"`
	Records         []model.BanRecord         `json:"records"`
	AIPatrolNotices []model.AIPatrolViolation `json:"aiPatrolNotices"`
}

// abuseHistoryDays is how far back an account's bans are shown.
const abuseHistoryDays = 90

func (s *AbuseService) Overview(now time.Time) (*AbuseOverview, error) {
	out := &AbuseOverview{Settings: s.Settings(), Servers: []AbuseServer{{NodeId: 0, Name: "面板本机", Mode: s.Mode(0), Capable: true}}}
	var nodes []model.Node
	if err := database.GetDB().Where("kind = ?", model.NodeKindAgent).Order("name").Find(&nodes).Error; err != nil {
		return nil, err
	}
	hub := runtime.GetAgentHub()
	names := map[int]string{0: "面板本机"}
	for _, n := range nodes {
		capable := false
		if hub != nil {
			if state, ok := hub.Session(n.Id); ok {
				capable = slices.Contains(state.Hello.Capabilities, abuse.Capability)
			}
		}
		mode := n.AbuseMode
		if mode != AbuseModeObserve && mode != AbuseModeEnforce {
			mode = AbuseModeOff
		}
		out.Servers = append(out.Servers, AbuseServer{NodeId: n.Id, Name: n.Name, Mode: mode, Capable: capable})
		names[n.Id] = n.Name
	}
	bans, err := s.ActiveBans(now)
	if err != nil {
		return nil, err
	}
	out.Bans = bans
	events, err := s.Events(100)
	if err != nil {
		return nil, err
	}
	out.Events = make([]AbuseEventView, 0, len(events))
	for _, e := range events {
		server := names[e.NodeId]
		if e.Rule == AbuseRuleSignup {
			server = "用户页面"
		}
		out.Events = append(out.Events, AbuseEventView{AbuseEvent: e, Server: server, Label: AbuseRuleLabel(e.Rule), Evidence: AbuseEvidence(e)})
	}
	return out, nil
}

func (s *AbuseService) HistoryOf(email string, now time.Time) (*AbuseHistory, error) {
	status, err := s.Status(email, now)
	if err != nil {
		return nil, err
	}
	records, err := s.History(email, now.AddDate(0, 0, -abuseHistoryDays))
	if err != nil {
		return nil, err
	}
	notices, err := (&AIPatrolService{}).Notices(email, now)
	if err != nil {
		return nil, err
	}
	return &AbuseHistory{Status: status, Records: records, AIPatrolNotices: notices}, nil
}

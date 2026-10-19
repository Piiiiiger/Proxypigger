package service

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

type socksAccount struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

type socksSettings struct {
	Auth      string          `json:"auth"`
	Accounts  []socksAccount  `json:"accounts"`
	UserLevel *int            `json:"userLevel"`
	Clients   json.RawMessage `json:"clients"`
}

func socksInboundSettings(t *testing.T, cfg *xray.Config, tag string) socksSettings {
	t.Helper()
	for i := range cfg.InboundConfigs {
		if cfg.InboundConfigs[i].Tag != tag {
			continue
		}
		var s socksSettings
		if err := json.Unmarshal(cfg.InboundConfigs[i].Settings, &s); err != nil {
			t.Fatalf("settings of %s: %v", tag, err)
		}
		return s
	}
	t.Fatalf("inbound %s missing from the config", tag)
	return socksSettings{}
}

func seedMixedInbound(t *testing.T, nodeID *int, tag string, port int, clients []model.Client) *model.Inbound {
	t.Helper()
	in := seedNodeInbound(t, nodeID, tag, port, model.Mixed, true, clients)
	stored := `{"auth":"password","accounts":[{"user":"leftover","pass":"form-default"}],"udp":true,"ip":"127.0.0.1"}`
	if err := database.GetDB().Model(&model.Inbound{}).Where("id = ?", in.Id).Update("settings", stored).Error; err != nil {
		t.Fatal(err)
	}
	in.Settings = stored
	return in
}

// A SOCKS5 inbound with Pigger users must log in exactly its enabled users, as a
// VLESS inbound does; a form's leftover static login would bypass quota and expiry.
func TestMixedInboundLogsInOnlyItsEnabledUsers(t *testing.T) {
	setupSettingTestDB(t)
	node := seedAgentNodeRow(t, "socks")
	alice := model.Client{Email: "alice", ID: "11111111-1111-4111-8111-111111111111", Password: "alice-pass", Enable: true}
	bob := model.Client{Email: "bob", ID: "22222222-2222-4222-8222-222222222222", Password: "bob-pass", Enable: false}
	withUsers := seedMixedInbound(t, &node.Id, "socks-users", 30001, []model.Client{alice, bob})
	legacy := seedMixedInbound(t, &node.Id, "socks-static", 30002, nil)

	cfg, err := (&XrayService{}).GetAgentXrayConfig(node.Id)
	if err != nil {
		t.Fatal(err)
	}
	got := socksInboundSettings(t, cfg, withUsers.Tag)
	if want := []socksAccount{{User: "alice", Pass: "alice-pass"}}; got.Auth != "password" || !reflect.DeepEqual(got.Accounts, want) {
		t.Fatalf("SOCKS5 logins = auth %q %+v, want password %+v", got.Auth, got.Accounts, want)
	}
	if got.Clients != nil {
		t.Fatalf("clients leaked into the SOCKS5 settings: %s", got.Clients)
	}
	static := socksInboundSettings(t, cfg, legacy.Tag)
	if want := []socksAccount{{User: "leftover", Pass: "form-default"}}; !reflect.DeepEqual(static.Accounts, want) {
		t.Fatalf("a SOCKS5 inbound without users lost its own logins: %+v", static.Accounts)
	}
}

// Every user today is VLESS-only with no stored password; attaching one to a
// SOCKS5 inbound must mint one, or the user gets no login there at all.
func TestAttachingAVlessUserToSocksMintsTheirLogin(t *testing.T) {
	setupSettingTestDB(t)
	node := seedAgentNodeRow(t, "socks")
	carol := model.Client{Email: "carol", SubID: "carolsub", ID: "33333333-3333-4333-8333-333333333333", Enable: true}
	seedNodeInbound(t, &node.Id, "vless-in", 443, model.VLESS, true, []model.Client{carol})
	socks := seedMixedInbound(t, &node.Id, "socks-in", 30001, nil)
	svc := &ClientService{}
	rec, err := svc.GetRecordByEmail(nil, "carol")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Attach(&InboundService{}, rec.Id, []int{socks.Id}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if rec, err = svc.GetRecordByEmail(nil, "carol"); err != nil {
		t.Fatal(err)
	}
	if len(rec.Password) < 24 {
		t.Fatalf("stored SOCKS5 password %q is missing or too short", rec.Password)
	}
	cfg, err := (&XrayService{}).GetAgentXrayConfig(node.Id)
	if err != nil {
		t.Fatal(err)
	}
	got := socksInboundSettings(t, cfg, socks.Tag)
	if want := []socksAccount{{User: "carol", Pass: rec.Password}}; !reflect.DeepEqual(got.Accounts, want) {
		t.Fatalf("SOCKS5 logins %+v, want %+v", got.Accounts, want)
	}
}

// The raw add-client API skips the defaults; a passwordless SOCKS5 entry would never log in.
func TestAddingAPasswordlessSocksClientIsRefused(t *testing.T) {
	setupSettingTestDB(t)
	node := seedAgentNodeRow(t, "socks")
	socks := seedMixedInbound(t, &node.Id, "socks-in", 30001, nil)
	payload := `{"clients":[{"email":"dave","id":"44444444-4444-4444-8444-444444444444","enable":true}]}`
	_, err := (&ClientService{}).AddInboundClient(&InboundService{}, &model.Inbound{Id: socks.Id, Settings: payload})
	if err == nil || strings.TrimSpace(err.Error()) != "socks5 client requires a password" {
		t.Fatalf("AddInboundClient error = %v, want the missing-password refusal", err)
	}
}

// A user SOCKS5 port lives on the server it exits from, like a VLESS one; an agent
// builds its whole config from the panel, so no sidecar has to reconcile it.
func TestSocksInboundCanLiveOnAnAgentNode(t *testing.T) {
	setupSettingTestDB(t)
	node := seedAgentNodeRow(t, "socks")
	in := &model.Inbound{
		Remark: "node socks", Enable: true, Port: 30010, Protocol: model.Mixed, NodeID: &node.Id,
		Settings: `{"auth":"password","accounts":[],"udp":true}`, ShareAddrStrategy: "node",
	}
	if _, _, err := (&InboundService{}).AddInbound(in); err != nil {
		t.Fatalf("AddInbound(mixed on an agent node): %v", err)
	}
}

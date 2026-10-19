package sub

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

const socksChainRealityStream = `{"network":"tcp","security":"reality","tcpSettings":{"acceptProxyProtocol":false,"header":{"type":"none"}},` +
	`"realitySettings":{"show":false,"target":"www.example.com:443","serverNames":["www.example.com","alt.example.com"],` +
	`"privateKey":"yBaw532IIUNuQWDTncozoBaLJmcd1JZzvsHUgVPxMk8","shortIds":["ab12","cd34"],` +
	`"settings":{"publicKey":"Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw","fingerprint":"chrome","spiderX":"/"}}}`

type socksChainFixture struct {
	relayNode, exitNode model.Node
	relay, target       model.Inbound
	chain               model.ProxyChain
	alice               model.Client
}

// alice may use the relay and the exit; erin only the relay, so she has no way out.
func seedSocksChain(t *testing.T) socksChainFixture {
	t.Helper()
	initSubDB(t)
	service.RegisterChainRelayOutbound(ChainRelayOutbound)
	db := database.GetDB()
	f := socksChainFixture{
		relayNode: model.Node{Name: "relay", Kind: model.NodeKindAgent, Address: "198.51.100.10", Enable: true},
		exitNode:  model.Node{Name: "exit", Kind: model.NodeKindAgent, Address: "203.0.113.20", Enable: true},
		alice:     model.Client{Email: "alice", SubID: "alicesub", ID: "11111111-1111-4111-8111-111111111111", Password: "alice-socks", Flow: "xtls-rprx-vision", LimitIP: 3, Enable: true},
	}
	for _, n := range []*model.Node{&f.relayNode, &f.exitNode} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.target = model.Inbound{
		Tag: "exit-in", Remark: "exit", Enable: true, Port: 443, SharePort: 2443, Protocol: model.VLESS,
		Settings: `{"clients":[],"decryption":"none"}`, StreamSettings: socksChainRealityStream, NodeID: &f.exitNode.Id,
	}
	f.relay = model.Inbound{
		Tag: "relay-socks", Remark: "relay socks", Enable: true, Port: 30002, Protocol: model.Mixed,
		Settings: `{"auth":"password","accounts":[],"udp":true}`, Sniffing: `{"enabled":true,"destOverride":["http","tls"]}`, NodeID: &f.relayNode.Id,
	}
	for _, in := range []*model.Inbound{&f.target, &f.relay} {
		if err := db.Create(in).Error; err != nil {
			t.Fatal(err)
		}
	}
	erin := model.Client{Email: "erin", SubID: "erinsub", ID: "55555555-5555-4555-8555-555555555555", Password: "erin-socks", Enable: true}
	svc := &service.ClientService{}
	if err := svc.SyncInbound(nil, f.target.Id, []model.Client{f.alice}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncInbound(nil, f.relay.Id, []model.Client{f.alice, erin}); err != nil {
		t.Fatal(err)
	}
	f.chain = model.ProxyChain{TargetInboundId: f.target.Id, RelayInboundId: f.relay.Id, Enabled: true}
	if err := db.Create(&f.chain).Error; err != nil {
		t.Fatal(err)
	}
	return f
}

func relayConfigParts(t *testing.T, nodeID int) (rules []map[string]any, outbounds []map[string]any, cfg *xray.Config) {
	t.Helper()
	cfg, err := (&service.XrayService{}).GetAgentXrayConfig(nodeID)
	if err != nil {
		t.Fatal(err)
	}
	var routing struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(cfg.RouterConfig, &routing); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
		t.Fatal(err)
	}
	return routing.Rules, outbounds, cfg
}

// A SOCKS5 login cannot ask for a dialer chain, so the relay must carry each user
// on to the exit itself, as that user, and let nobody leave at the relay.
func TestSocksRelayForwardsEachUserToTheChainExit(t *testing.T) {
	f := seedSocksChain(t)
	rules, outbounds, _ := relayConfigParts(t, f.relayNode.Id)

	hop := abuse.ChainSocksTag(f.chain.Id, "alice")
	wantRules := []map[string]any{
		{"type": "field", "inboundTag": []any{f.relay.Tag}, "user": []any{"alice"}, "outboundTag": hop},
		{"type": "field", "inboundTag": []any{f.relay.Tag}, "outboundTag": abuse.TagChainSocksBlock},
	}
	if len(rules) < 2 || !reflect.DeepEqual(rules[:2], wantRules) {
		t.Fatalf("relay rules = %v, want first %v", rules, wantRules)
	}
	var out map[string]any
	for _, o := range outbounds {
		if o["tag"] == hop {
			out = o
		}
	}
	if out == nil {
		t.Fatalf("no outbound %s in %v", hop, outbounds)
	}
	settings, _ := out["settings"].(map[string]any)
	if out["protocol"] != "vless" || settings["address"] != "203.0.113.20" || settings["port"] != float64(2443) ||
		settings["id"] != f.alice.ID || settings["flow"] != "xtls-rprx-vision" {
		t.Fatalf("hop does not dial the exit as alice: %v", out)
	}
	raw, _ := json.Marshal(out)
	if err := xray.ValidateOutboundConfig(raw); err != nil {
		t.Fatalf("xray-core rejects the hop outbound: %v\n%s", err, raw)
	}
}

// The exit already bills the user under their own login; billing the relay hop
// too would charge every byte twice. Online IPs still count there for the IP limit.
func TestSocksRelayHopIsNotBilledButStaysOnline(t *testing.T) {
	f := seedSocksChain(t)
	_, _, cfg := relayConfigParts(t, f.relayNode.Id)
	var relaySettings struct {
		UserLevel *int `json:"userLevel"`
	}
	for _, ib := range cfg.InboundConfigs {
		if ib.Tag == f.relay.Tag {
			if err := json.Unmarshal(ib.Settings, &relaySettings); err != nil {
				t.Fatal(err)
			}
		}
	}
	if relaySettings.UserLevel == nil {
		t.Fatal("relay inbound keeps the billed default level")
	}
	var policy struct {
		Levels map[string]map[string]any `json:"levels"`
	}
	if err := json.Unmarshal(cfg.Policy, &policy); err != nil {
		t.Fatal(err)
	}
	level := policy.Levels[strconv.Itoa(*relaySettings.UserLevel)]
	if level["statsUserUplink"] != false || level["statsUserDownlink"] != false || level["statsUserOnline"] != true {
		t.Fatalf("relay level %d = %v, want traffic stats off and online on", *relaySettings.UserLevel, level)
	}
	if base := policy.Levels["0"]; base["statsUserUplink"] != true || base["statsUserDownlink"] != true {
		t.Fatalf("direct users lost their billing level: %v", base)
	}
}

// An agent applies a config whenever its hash moves; a random REALITY pick
// per build would restart the relay's core on every sync.
func TestSocksRelayConfigIsStableAcrossBuilds(t *testing.T) {
	f := seedSocksChain(t)
	first, err := (&service.XrayService{}).GetAgentXrayConfig(f.relayNode.Id)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		again, err := (&service.XrayService{}).GetAgentXrayConfig(f.relayNode.Id)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(first)
		b, _ := json.Marshal(again)
		if string(a) != string(b) {
			t.Fatalf("relay config changed between builds:\n%s\n%s", a, b)
		}
	}
}

// Subscriptions never carry SOCKS5, so the portal and the admin show each login
// with the address and NAT port the user must dial, and where it leaves.
func TestSocks5ForClientListsTheUsersLogins(t *testing.T) {
	f := seedSocksChain(t)
	db := database.GetDB()
	if err := db.Model(&model.Inbound{}).Where("id = ?", f.relay.Id).Update("share_port", 40002).Error; err != nil {
		t.Fatal(err)
	}
	got, err := Socks5ForClient("sub.example.com", "alice")
	if err != nil {
		t.Fatal(err)
	}
	want := []service.Socks5Proxy{{
		InboundId: f.relay.Id, Remark: "relay socks", Host: "198.51.100.10", Port: 40002,
		Username: "alice", Password: "alice-socks", Exit: "exit", Enable: true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Socks5ForClient = %+v, want %+v", got, want)
	}
	if err := (&service.ClientService{}).SyncInbound(nil, f.relay.Id, nil); err != nil {
		t.Fatal(err)
	}
	if got, err = Socks5ForClient("sub.example.com", "alice"); err != nil || len(got) != 0 {
		t.Fatalf("a detached login is still listed: %+v %v", got, err)
	}
}

// A SOCKS5 inbound on the panel itself has no node address; like a share link it
// must fall back to the host the user reached, or the login has nowhere to dial.
func TestSocks5ForClientOnThePanelUsesTheRequestHost(t *testing.T) {
	initSubDB(t)
	local := model.Inbound{Tag: "panel-socks", Remark: "panel socks", Enable: true, Port: 30003, Protocol: model.Mixed, Settings: `{"auth":"password"}`}
	if err := database.GetDB().Create(&local).Error; err != nil {
		t.Fatal(err)
	}
	user := model.Client{Email: "bob", SubID: "bobsub", ID: "66666666-6666-4666-8666-666666666666", Password: "bob-socks", Enable: true}
	if err := (&service.ClientService{}).SyncInbound(nil, local.Id, []model.Client{user}); err != nil {
		t.Fatal(err)
	}
	got, err := Socks5ForClient("sub.example.com", "bob")
	if err != nil || len(got) != 1 || got[0].Host != "sub.example.com" || got[0].Port != 30003 {
		t.Fatalf("Socks5ForClient = %+v, %v; want sub.example.com:30003", got, err)
	}
}

// A relay with abuse detection on classifies speed tests, BitTorrent and mail
// itself; those rules must not catch a SOCKS5 relay user first, or the traffic
// leaves at the relay, unbilled, instead of at the exit that judges it.
func TestSocksRelayHopsComeBeforeTheRelaysAbuseChecks(t *testing.T) {
	f := seedSocksChain(t)
	if err := database.GetDB().Model(&model.Node{}).Where("id = ?", f.relayNode.Id).Update("abuse_mode", "enforce").Error; err != nil {
		t.Fatal(err)
	}
	rules, _, _ := relayConfigParts(t, f.relayNode.Id)
	hop, check := -1, -1
	for i, r := range rules {
		switch r["outboundTag"] {
		case abuse.ChainSocksTag(f.chain.Id, "alice"):
			hop = i
		case abuse.TagSpeedTest, abuse.TagBT, abuse.TagSMTP:
			if check < 0 {
				check = i
			}
		}
	}
	if hop < 0 || check < 0 || hop > check {
		t.Fatalf("hop rule at %d, first abuse check at %d; the hop must come first: %v", hop, check, rules)
	}
}

// The IP limit bans a device by user and source address; the relay is where that
// device connects, so its ban must catch the connection before the hop does.
func TestSocksRelayHopsStillHonourIpLimitBans(t *testing.T) {
	f := seedSocksChain(t)
	now := time.Now().Unix()
	ban := model.ClientIpBan{Email: "alice", Network: "198.51.100.9", BannedAt: now, ExpiresAt: now + 600}
	if err := database.GetDB().Create(&ban).Error; err != nil {
		t.Fatal(err)
	}
	rules, _, _ := relayConfigParts(t, f.relayNode.Id)
	hop, banned := -1, -1
	for i, r := range rules {
		switch r["outboundTag"] {
		case abuse.ChainSocksTag(f.chain.Id, "alice"):
			hop = i
		case abuse.TagIPLimitBlock:
			banned = i
		}
	}
	if hop < 0 || banned < 0 || banned > hop {
		t.Fatalf("ban rule at %d, hop rule at %d; the ban must come first: %v", banned, hop, rules)
	}
}

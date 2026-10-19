package controller

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/locale"
)

func TestProxyChainsAllowDistinctRelaysForSameTarget(t *testing.T) {
	newControllerTestDB(t)
	db := database.GetDB()
	nodes := []model.Inbound{{Tag: "multi-target", Remark: "Singapore", Port: 4601, Protocol: model.VLESS}, {Tag: "multi-relay-a", Remark: "Hong Kong A", Port: 4602, Protocol: model.VLESS}, {Tag: "multi-relay-b", Remark: "Hong Kong B", Port: 4603, Protocol: model.VLESS}}
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
		c.Next()
	})
	NewProxyChainController(engine.Group("/panel/api/proxyChains"))
	payload := map[string]any{"name": "route A", "targetInboundId": nodes[0].Id, "relayInboundId": nodes[1].Id, "enabled": true}
	if reply := postJSON(t, engine, "/panel/api/proxyChains/add", payload); !reply.Success {
		t.Fatal(reply.Msg)
	}
	payload["relayInboundId"] = nodes[2].Id
	if reply := postJSON(t, engine, "/panel/api/proxyChains/add", payload); !reply.Success {
		t.Fatalf("second relay rejected: %s", reply.Msg)
	}
	if reply := postJSON(t, engine, "/panel/api/proxyChains/add", payload); reply.Success || !strings.Contains(reply.Msg, "同一目标和中转节点不能重复配置") {
		t.Fatalf("duplicate route should explain the conflict: %+v", reply)
	}
	var count int64
	db.Model(&model.ProxyChain{}).Where("target_inbound_id = ?", nodes[0].Id).Count(&count)
	if count != 2 {
		t.Fatalf("route count = %d", count)
	}
}

func TestProxyChainTargetEditPreservesPlanRoute(t *testing.T) {
	newControllerTestDB(t)
	db := database.GetDB()
	nodes := []model.Inbound{{Tag: "route-hk", Remark: "HK", Port: 4801, Protocol: model.VLESS}, {Tag: "route-sg", Remark: "SG", Port: 4802, Protocol: model.VLESS}, {Tag: "route-other", Remark: "Other", Port: 4803, Protocol: model.VLESS}}
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	chain := model.ProxyChain{TargetInboundId: nodes[0].Id, RelayInboundId: nodes[1].Id, Enabled: true}
	other := model.ProxyChain{TargetInboundId: nodes[1].Id, RelayInboundId: nodes[2].Id, Enabled: true}
	for _, c := range []*model.ProxyChain{&chain, &other} {
		if err := db.Create(c).Error; err != nil {
			t.Fatal(err)
		}
	}
	oldKey := model.PlanChainKey(nodes[0].Id, chain.Id)
	keys, _ := json.Marshal([]string{oldKey, model.PlanNodeKey(nodes[1].Id, false)})
	groups, _ := json.Marshal([]map[string]any{{"name": "AI", "inboundIds": []int{nodes[0].Id}, "nodeKeys": []string{oldKey}, "extra": "keep"}})
	plan := model.Plan{Name: "Existing route", NodeKeys: string(keys), ProxyGroups: string(groups)}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes[:2] {
		if err := db.Create(&model.PlanInbound{PlanId: plan.Id, InboundId: n.Id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
		c.Next()
	})
	NewProxyChainController(engine.Group("/panel/api/proxyChains"))
	reply := postJSON(t, engine, fmt.Sprintf("/panel/api/proxyChains/update/%d", chain.Id), map[string]any{"name": "route", "targetInboundId": nodes[1].Id, "relayInboundId": nodes[0].Id, "enabled": true})
	if !reply.Success {
		t.Fatal(reply.Msg)
	}
	if err := db.First(&plan, plan.Id).Error; err != nil {
		t.Fatal(err)
	}
	newKey := model.PlanChainKey(nodes[1].Id, chain.Id)
	if strings.Contains(plan.NodeKeys, oldKey) || !strings.Contains(plan.NodeKeys, newKey) || !strings.Contains(plan.ProxyGroups, newKey) || !strings.Contains(plan.ProxyGroups, "keep") {
		t.Fatalf("plan lost route: %s %s", plan.NodeKeys, plan.ProxyGroups)
	}
	var ids []int
	db.Model(&model.PlanInbound{}).Where("plan_id = ?", plan.Id).Order("inbound_id").Pluck("inbound_id", &ids)
	if len(ids) != 2 || ids[0] != nodes[0].Id || ids[1] != nodes[1].Id {
		t.Fatalf("grants changed: %v", ids)
	}
}

func TestProxyChainDisplayNamesPersistPreserveAndClear(t *testing.T) {
	newControllerTestDB(t)
	db := database.GetDB()
	target := model.Inbound{Tag: "target", Remark: "新加坡-家宽", Port: 4401, Protocol: model.VLESS}
	relay := model.Inbound{Tag: "relay", Remark: "奶爸", Port: 4402, Protocol: model.VLESS}
	for _, inbound := range []*model.Inbound{&target, &relay} {
		if err := db.Create(inbound).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
		c.Next()
	})
	NewProxyChainController(engine.Group("/panel/api/proxyChains"))
	payload := map[string]any{"name": "route", "targetInboundId": target.Id, "relayInboundId": relay.Id, "enabled": true, "directName": "  SG A  ", "relayName": "  SG B  "}
	reply := postJSON(t, engine, "/panel/api/proxyChains/add", payload)
	var row model.ProxyChain
	if err := json.Unmarshal(reply.Obj, &row); err != nil || !reply.Success || row.DirectName != "" || row.RelayName != "SG B" {
		t.Fatalf("save: %+v %s %v", row, reply.Msg, err)
	}
	path := fmt.Sprintf("/panel/api/proxyChains/update/%d", row.Id)
	delete(payload, "directName")
	delete(payload, "relayName")
	if reply = postJSON(t, engine, path, payload); !reply.Success {
		t.Fatal(reply.Msg)
	}
	if err := db.First(&row, row.Id).Error; err != nil || row.DirectName != "" || row.RelayName != "SG B" {
		t.Fatalf("direct alias was not retired: %+v %v", row, err)
	}
	for _, names := range [][2]string{{"", "新加坡-家宽"}, {"", "DIRECT"}, {"", "SG\nB"}, {"", strings.Repeat("字", 129)}} {
		delete(payload, "directName")
		payload["relayName"] = names[1]
		if reply = postJSON(t, engine, path, payload); reply.Success {
			t.Fatalf("accepted invalid names: %v", names)
		}
	}
	payload["directName"], payload["relayName"] = "", ""
	if reply = postJSON(t, engine, path, payload); !reply.Success {
		t.Fatal(reply.Msg)
	}
	if err := db.First(&row, row.Id).Error; err != nil || row.DirectName != "" || row.RelayName != "" {
		t.Fatalf("clear did not persist: %+v %v", row, err)
	}
}

func TestProxyChainRefusesUnverifiedRemoteRouteButAllowsDisabledDraft(t *testing.T) {
	newControllerTestDB(t)
	db := database.GetDB()
	node := model.Node{Name: "offline-relay", Kind: "agent", Address: "192.0.2.2"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	target := model.Inbound{Tag: "check-target", Remark: "target", Port: 4591, Protocol: model.VLESS}
	relay := model.Inbound{Tag: "check-relay", Remark: "relay", Port: 4592, Protocol: model.VLESS, NodeID: &node.Id}
	for _, ib := range []*model.Inbound{&target, &relay} {
		if err := db.Create(ib).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
		c.Next()
	})
	NewProxyChainController(engine.Group("/panel/api/proxyChains"))
	body := map[string]any{"targetInboundId": target.Id, "relayInboundId": relay.Id, "enabled": true}
	if reply := postJSON(t, engine, "/panel/api/proxyChains/add", body); reply.Success {
		t.Fatal("unverified remote route enabled")
	}
	var count int64
	db.Model(&model.ProxyChain{}).Count(&count)
	if count != 0 {
		t.Fatal("failed check changed database")
	}
	body["enabled"] = false
	if reply := postJSON(t, engine, "/panel/api/proxyChains/add", body); !reply.Success {
		t.Fatal(reply.Msg)
	}
	var draft model.ProxyChain
	if err := db.First(&draft).Error; err != nil {
		t.Fatal(err)
	}
	if draft.Enabled {
		t.Fatal("disabled draft was silently enabled by the database default")
	}
}

// A relay must be able to carry the chain: transit logins need a VLESS/VMess/Trojan
// relay, and a SOCKS5 relay dials one exit itself; anything else breaks its config.
func TestProxyChainRefusesRelaysThatCannotCarryIt(t *testing.T) {
	newControllerTestDB(t)
	db := database.GetDB()
	nodes := []model.Inbound{
		{Tag: "carry-exit", Remark: "exit", Port: 4611, Protocol: model.VLESS},
		{Tag: "carry-exit-2", Remark: "exit 2", Port: 4612, Protocol: model.Trojan},
		{Tag: "carry-hy", Remark: "hysteria", Port: 4613, Protocol: model.Hysteria},
		{Tag: "carry-socks", Remark: "socks", Port: 4614, Protocol: model.Mixed},
	}
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	exit, exit2, hysteria, socks := nodes[0].Id, nodes[1].Id, nodes[2].Id, nodes[3].Id
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
		c.Next()
	})
	NewProxyChainController(engine.Group("/panel/api/proxyChains"))
	add := func(target, relay int) apiReply {
		return postJSON(t, engine, "/panel/api/proxyChains/add", map[string]any{"targetInboundId": target, "relayInboundId": relay, "enabled": true})
	}
	if reply := add(exit, hysteria); reply.Success || !strings.Contains(reply.Msg, "中转节点不支持") {
		t.Fatalf("a hysteria relay was accepted: %+v", reply)
	}
	if reply := add(hysteria, socks); reply.Success || !strings.Contains(reply.Msg, "SOCKS5 中转不能连接") {
		t.Fatalf("a SOCKS5 relay to a hysteria exit was accepted: %+v", reply)
	}
	if reply := add(exit, socks); !reply.Success {
		t.Fatalf("SOCKS5 relay to a VLESS exit refused: %s", reply.Msg)
	}
	if reply := add(exit2, socks); reply.Success || !strings.Contains(reply.Msg, "SOCKS5 中转只能连接一个出口") {
		t.Fatalf("a second exit for one SOCKS5 relay was accepted: %+v", reply)
	}
}

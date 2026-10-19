package service

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// chainTransitRoute is one enabled chain with both of its inbounds enabled, and
// the target's enabled clients, the only ones its relay may carry.
type chainTransitRoute struct {
	chain   model.ProxyChain
	relay   model.Inbound
	target  model.Inbound
	granted map[string]bool
	targets map[string]model.Client
}

func loadChainTransitRoutes() ([]chainTransitRoute, error) {
	db := database.GetDB()
	var chains []model.ProxyChain
	if err := db.Where("enabled = ?", true).Find(&chains).Error; err != nil {
		return nil, err
	}
	var routes []chainTransitRoute
	for _, chain := range chains {
		r := chainTransitRoute{chain: chain, granted: map[string]bool{}, targets: map[string]model.Client{}}
		if err := db.First(&r.relay, chain.RelayInboundId).Error; err != nil {
			return nil, err
		}
		if err := db.First(&r.target, chain.TargetInboundId).Error; err != nil {
			return nil, err
		}
		if !r.target.Enable || !r.relay.Enable {
			continue
		}
		targets, err := (&ClientService{}).ListForInbound(nil, r.target.Id)
		if err != nil {
			return nil, err
		}
		for _, c := range targets {
			if c.Enable {
				r.granted[c.Email] = true
				r.targets[c.Email] = c
			}
		}
		routes = append(routes, r)
	}
	return routes, nil
}

// chainRelayOutbound builds the outbound a SOCKS5 relay dials its chain exit with,
// as the user's own client would; the subscription package owns client configs.
var chainRelayOutbound func(target *model.Inbound, client model.Client) (map[string]any, error)

func RegisterChainRelayOutbound(b func(target *model.Inbound, client model.Client) (map[string]any, error)) {
	chainRelayOutbound = b
}

func chainTransitProtocol(p model.Protocol) bool {
	return p == model.VLESS || p == model.VMESS || p == model.Trojan
}

// injectChainTransit creates non-billable relay identities. The destination hop
// retains the user's original identity and charges once. Direct use of a relay
// also retains its original identity. Transit credentials are destination-bound.
// It returns each identity it installed with the client it stands in for.
func injectChainTransit(cfg *xray.Config) (map[string]string, error) {
	routes, err := loadChainTransitRoutes()
	if err != nil {
		return nil, err
	}
	owners := map[string]string{}
	var extraRules []any
	for _, r := range routes {
		chain, relay, target := r.chain, r.relay, r.target
		ib := inboundConfigByTag(cfg, relay.Tag)
		if ib == nil || relay.Protocol == model.Mixed {
			continue
		}
		if !chainTransitProtocol(relay.Protocol) {
			return nil, fmt.Errorf("chain %d: relay protocol %s cannot isolate transit accounting", chain.Id, relay.Protocol)
		}
		var settings map[string]any
		if err := json.Unmarshal(ib.Settings, &settings); err != nil {
			return nil, err
		}
		clients, _ := settings["clients"].([]any)
		users := []string{}
		// Iterate only original users; aliases from another chain cannot match grants.
		for _, v := range clients {
			entry, ok := v.(map[string]any)
			if !ok {
				continue
			}
			email, _ := entry["email"].(string)
			if !r.granted[email] {
				continue
			}
			field := "id"
			if relay.Protocol == model.Trojan {
				field = "password"
			}
			secret, _ := entry[field].(string)
			if secret == "" {
				continue
			}
			credential, alias := model.ChainTransitCredential(secret, chain.Id)
			clone := map[string]any{}
			for k, v := range entry {
				clone[k] = v
			}
			clone[field] = credential
			clone["email"] = alias
			delete(clone, "reverse")
			clients = append(clients, clone)
			users = append(users, alias)
			owners[alias] = email
		}
		if len(users) == 0 {
			continue
		}
		settings["clients"] = clients
		raw, err := json.Marshal(settings)
		if err != nil {
			return nil, err
		}
		ib.Settings = raw
		endpoints, err := chainTransitEndpoints(&target)
		if err != nil {
			return nil, err
		}
		for _, ep := range endpoints {
			rule := map[string]any{"type": "field", "user": users, "inboundTag": []string{relay.Tag}, "port": strconv.Itoa(ep.port), "outboundTag": abuse.TagChainTransitDirect}
			if net.ParseIP(ep.host) != nil {
				rule["ip"] = []string{ep.host}
			} else {
				rule["domain"] = []string{"full:" + ep.host}
			}
			extraRules = append(extraRules, rule)
		}
		extraRules = append(extraRules, map[string]any{"type": "field", "user": users, "inboundTag": []string{relay.Tag}, "outboundTag": abuse.TagChainTransitBlock})
	}
	if len(extraRules) == 0 {
		return owners, nil
	}
	err = prependRoutingWithOutbound(cfg, extraRules,
		map[string]any{"tag": abuse.TagChainTransitDirect, "protocol": "freedom", "settings": map[string]any{}},
		map[string]any{"tag": abuse.TagChainTransitBlock, "protocol": "blackhole", "settings": map[string]any{}})
	return owners, err
}

func inboundConfigByTag(cfg *xray.Config, tag string) *xray.InboundConfig {
	for i := range cfg.InboundConfigs {
		if cfg.InboundConfigs[i].Tag == tag {
			return &cfg.InboundConfigs[i]
		}
	}
	return nil
}

// injectSocksRelayHops carries SOCKS5 relay users on to their chain exits. It runs
// after the abuse checks, whose rules would otherwise keep relayed traffic here.
func injectSocksRelayHops(cfg *xray.Config) error {
	routes, err := loadChainTransitRoutes()
	if err != nil {
		return err
	}
	var rules []any
	var hops []map[string]any
	level := -1
	for _, r := range routes {
		ib := inboundConfigByTag(cfg, r.relay.Tag)
		if ib == nil || r.relay.Protocol != model.Mixed {
			continue
		}
		if level < 0 {
			if level, err = addUnbilledPolicyLevel(cfg); err != nil {
				return err
			}
		}
		relayRules, outbounds, err := socksRelayHops(ib, r, level)
		if err != nil {
			return err
		}
		rules = append(rules, relayRules...)
		hops = append(hops, outbounds...)
	}
	if len(rules) == 0 {
		return nil
	}
	return prependRoutingWithOutbound(cfg, rules,
		append(hops, map[string]any{"tag": abuse.TagChainSocksBlock, "protocol": "blackhole", "settings": map[string]any{}})...)
}

// socksRelayHops carries each SOCKS5 login of a relay on to the chain exit as that
// user, at a level without traffic stats: the exit bills the same bytes already.
func socksRelayHops(ib *xray.InboundConfig, r chainTransitRoute, level int) ([]any, []map[string]any, error) {
	var settings map[string]any
	if err := json.Unmarshal(ib.Settings, &settings); err != nil {
		return nil, nil, err
	}
	settings["userLevel"] = level
	raw, err := json.Marshal(settings)
	if err != nil {
		return nil, nil, err
	}
	ib.Settings = raw
	accounts, _ := settings["accounts"].([]any)
	var rules []any
	var outbounds []map[string]any
	for _, v := range accounts {
		account, _ := v.(map[string]any)
		user, _ := account["user"].(string)
		client, ok := r.targets[user]
		if !ok {
			continue
		}
		if chainRelayOutbound == nil {
			return nil, nil, fmt.Errorf("chain %d: no outbound builder for SOCKS5 relays", r.chain.Id)
		}
		out, err := chainRelayOutbound(&r.target, client)
		if err != nil {
			return nil, nil, fmt.Errorf("chain %d: %w", r.chain.Id, err)
		}
		tag := abuse.ChainSocksTag(r.chain.Id, user)
		out["tag"] = tag
		outbounds = append(outbounds, out)
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{r.relay.Tag}, "user": []string{user}, "outboundTag": tag})
	}
	rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{r.relay.Tag}, "outboundTag": abuse.TagChainSocksBlock})
	return rules, outbounds, nil
}

// addUnbilledPolicyLevel copies level 0 under a free number with traffic stats
// off; online stats stay on, so the relay still counts devices for the IP limit.
func addUnbilledPolicyLevel(cfg *xray.Config) (int, error) {
	policy := map[string]any{}
	if len(cfg.Policy) > 0 {
		if err := json.Unmarshal(cfg.Policy, &policy); err != nil {
			return 0, err
		}
	}
	levels, _ := policy["levels"].(map[string]any)
	if levels == nil {
		levels = map[string]any{}
	}
	level := 100
	for levels[strconv.Itoa(level)] != nil {
		level++
	}
	unbilled := map[string]any{}
	if base, ok := levels["0"].(map[string]any); ok {
		for k, v := range base {
			unbilled[k] = v
		}
	}
	unbilled["statsUserUplink"] = false
	unbilled["statsUserDownlink"] = false
	unbilled["statsUserOnline"] = true
	levels[strconv.Itoa(level)] = unbilled
	policy["levels"] = levels
	raw, err := json.Marshal(policy)
	if err != nil {
		return 0, err
	}
	cfg.Policy = raw
	return level, nil
}

// prependRoutingWithOutbound adds outbounds under tags the template must not use
// and puts rules ahead of every existing rule.
func prependRoutingWithOutbound(cfg *xray.Config, rules []any, added ...map[string]any) error {
	var outbounds []map[string]any
	if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
		return err
	}
	for _, o := range outbounds {
		for _, a := range added {
			if o["tag"] == a["tag"] {
				return fmt.Errorf("reserved outbound tag %v is already in use", a["tag"])
			}
		}
	}
	outbounds = append(outbounds, added...)
	raw, err := json.Marshal(outbounds)
	if err != nil {
		return err
	}
	cfg.OutboundConfigs = raw
	routing := map[string]any{}
	if len(cfg.RouterConfig) > 0 {
		if err := json.Unmarshal(cfg.RouterConfig, &routing); err != nil {
			return err
		}
	}
	existing, _ := routing["rules"].([]any)
	routing["rules"] = append(rules, existing...)
	raw, err = json.Marshal(routing)
	if err != nil {
		return err
	}
	cfg.RouterConfig = raw
	return nil
}

type chainTransitEndpoint struct {
	host string
	port int
}

func chainTransitEndpoints(target *model.Inbound) ([]chainTransitEndpoint, error) {
	stream := map[string]any{}
	if err := json.Unmarshal([]byte(target.StreamSettings), &stream); strings.TrimSpace(target.StreamSettings) != "" && err != nil {
		return nil, err
	}
	var eps []chainTransitEndpoint
	if entries, ok := stream["externalProxy"].([]any); ok {
		for _, v := range entries {
			if e, ok := v.(map[string]any); ok {
				host, _ := e["dest"].(string)
				port, _ := e["port"].(float64)
				eps = append(eps, chainTransitEndpoint{strings.Trim(host, "[]"), int(port)})
			}
		}
	}
	if len(eps) == 0 {
		host := ""
		if target.NodeID != nil {
			var n model.Node
			if err := database.GetDB().First(&n, *target.NodeID).Error; err != nil {
				return nil, err
			}
			host = n.Address
		}
		listen := strings.Trim(target.Listen, "[]")
		ip := net.ParseIP(listen)
		usable := listen != "" && (ip == nil || (!ip.IsUnspecified() && !ip.IsLoopback()))
		if (host == "" || target.ShareAddrStrategy == "listen") && usable {
			host = listen
		}
		if target.ShareAddrStrategy == "custom" && strings.TrimSpace(target.ShareAddr) != "" {
			host = target.ShareAddr
		}
		port := target.Port
		if target.SharePort > 0 {
			port = target.SharePort
		}
		eps = append(eps, chainTransitEndpoint{strings.Trim(host, "[]"), port})
	}
	for _, ep := range eps {
		if ep.host == "" || ep.port <= 0 || ep.port > 65535 {
			return nil, fmt.Errorf("chain target %s requires a public endpoint", target.Remark)
		}
	}
	return eps, nil
}

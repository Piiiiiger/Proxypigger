package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IpObservation is one live source address of a client on one server. LastSeen
// is when that address last opened a connection (unix seconds); Server is the
// node it was seen on, 0 for this panel.
type IpObservation struct {
	Email    string
	IP       string
	LastSeen int64
	Server   int
}

// IpLimitService enforces clients' IP limits across this panel and its agents.
// A ban is a routing rule in the generated configs, so it needs no fail2ban.
type IpLimitService struct {
	settingService SettingService
}

// Why an address is left out of the IP limit, as the IP log labels it.
const (
	IpExemptHost      = "host"
	IpExemptAllowlist = "allowlist"
	IpExemptPrivate   = "private"
)

const (
	defaultIpLimitBanMinutes = 30
	ipLimitResolveTTL        = 10 * time.Minute
	ipLimitOwnersTTL         = 15 * time.Second
	// ipLimitScanStaleAfter is a few missed 10 s scans; an older snapshot means
	// the scan stopped and no longer says who is online.
	ipLimitScanStaleAfter = time.Minute
)

var (
	ipLimitLocalAddrs = localGlobalAddrs
	ipLimitLookupHost = lookupHostAddrs

	errIpNotBanned = errors.New("this network is not banned for this client")
	cgnatPrefix    = netip.MustParsePrefix("100.64.0.0/10")
)

// ipLimitNetwork maps a source address to the network the limit counts: its /24
// for IPv4, since carrier NAT spreads one phone over a pool, its /64 for IPv6.
func ipLimitNetwork(addr netip.Addr) netip.Prefix {
	if addr.Is4() {
		return netip.PrefixFrom(addr, 24).Masked()
	}
	return netip.PrefixFrom(addr, 64).Masked()
}

// parseSourceAddr reads a client address as Xray reports it, IPv4 in plain form.
func parseSourceAddr(ip string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(ip), "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap().WithZone(""), true
}

func isPublicAddr(a netip.Addr) bool {
	return a.IsGlobalUnicast() && !a.IsPrivate() && !cgnatPrefix.Contains(a)
}

func hostPrefix(a netip.Addr) netip.Prefix {
	if a.Is4() {
		return netip.PrefixFrom(a, 32)
	}
	return netip.PrefixFrom(a, 64).Masked()
}

type ipLimitHost struct {
	prefix netip.Prefix
	name   string
}

// ipLimitExemptions holds the addresses that are never counted or banned.
type ipLimitExemptions struct {
	hosts     []ipLimitHost
	allowlist ipLimitAllowlist
}

// reason says why addr is exempt; host names the server for a host address
// ("" for this panel's own host).
func (e *ipLimitExemptions) reason(addr netip.Addr) (kind, host string) {
	if !isPublicAddr(addr) {
		return IpExemptPrivate, ""
	}
	for _, h := range e.hosts {
		if h.prefix.Contains(addr) {
			return IpExemptHost, h.name
		}
	}
	if e.allowlist.contains(addr) {
		return IpExemptAllowlist, ""
	}
	return "", ""
}

// holdsExempt says whether a server or allowlisted address lies in p.
func (e *ipLimitExemptions) holdsExempt(p netip.Prefix) bool {
	for _, h := range e.hosts {
		if h.prefix.Overlaps(p) {
			return true
		}
	}
	for _, a := range e.allowlist.prefixes {
		if a.Overlaps(p) {
			return true
		}
	}
	return slices.ContainsFunc(e.allowlist.addrs, p.Contains)
}

// exemptions collects every Pigger server's addresses (any of them can relay
// for another), the operator's allowlist, and nothing else.
func (s *IpLimitService) exemptions(now time.Time) (*ipLimitExemptions, error) {
	var nodes []model.Node
	if err := database.GetDB().Select("id", "name", "address", "agent_remote_ip").Order("id").Find(&nodes).Error; err != nil {
		return nil, err
	}
	raw, err := s.settingService.GetIpLimitAllowlist()
	if err != nil {
		return nil, err
	}
	ex := &ipLimitExemptions{allowlist: parseIpLimitAllowlist(raw)}
	for _, a := range ipLimitLocalAddrs() {
		ex.hosts = append(ex.hosts, ipLimitHost{prefix: hostPrefix(a)})
	}
	for _, n := range nodes {
		for _, a := range nodeHostAddrs(n, now) {
			ex.hosts = append(ex.hosts, ipLimitHost{prefix: hostPrefix(a), name: n.Name})
		}
	}
	return ex, nil
}

func nodeHostAddrs(n model.Node, now time.Time) []netip.Addr {
	var out []netip.Addr
	host := strings.Trim(strings.TrimSpace(n.Address), "[]")
	if a, err := netip.ParseAddr(host); err == nil {
		out = append(out, a.Unmap())
	} else if host != "" {
		out = append(out, resolveHostCached(host, now)...)
	}
	if a, err := netip.ParseAddr(strings.TrimSpace(n.AgentRemoteIP)); err == nil {
		out = append(out, a.Unmap())
	}
	return out
}

type resolvedHost struct {
	addrs []netip.Addr
	at    time.Time
}

var ipLimitResolved = struct {
	sync.Mutex
	m map[string]resolvedHost
}{m: map[string]resolvedHost{}}

// resolveHostCached keeps the last good answer when a lookup fails, so a DNS
// hiccup cannot strip a relay of its exemption.
func resolveHostCached(host string, now time.Time) []netip.Addr {
	ipLimitResolved.Lock()
	cached, ok := ipLimitResolved.m[host]
	ipLimitResolved.Unlock()
	if ok && now.Sub(cached.at) < ipLimitResolveTTL {
		return cached.addrs
	}
	addrs, err := ipLimitLookupHost(host)
	if err != nil {
		logger.Debug("[LimitIP] resolving", host, "failed:", err)
		addrs = cached.addrs
	}
	for i := range addrs {
		addrs[i] = addrs[i].Unmap()
	}
	ipLimitResolved.Lock()
	ipLimitResolved.m[host] = resolvedHost{addrs: addrs, at: now}
	ipLimitResolved.Unlock()
	return addrs
}

func lookupHostAddrs(host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func localGlobalAddrs() []netip.Addr {
	ifAddrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, a := range ifAddrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if addr, ok := netip.AddrFromSlice(ipNet.IP); ok && isPublicAddr(addr.Unmap()) {
			out = append(out, addr.Unmap())
		}
	}
	return out
}

var ipLimitOwners = struct {
	sync.Mutex
	owners map[string]string
	at     time.Time
}{}

// resetIpLimitCaches forgets cached lookups; tests change the data under them.
func resetIpLimitCaches() {
	ipLimitResolved.Lock()
	ipLimitResolved.m = map[string]resolvedHost{}
	ipLimitResolved.Unlock()
	ipLimitOwners.Lock()
	ipLimitOwners.owners, ipLimitOwners.at = nil, time.Time{}
	ipLimitOwners.Unlock()
	storeOnlineSnapshot(time.Time{}, nil)
}

// ChainTransitOwners maps every relay identity of an enabled chain to the client
// it stands in for, so a device that comes in through a relay counts as its owner's.
func (s *IpLimitService) ChainTransitOwners() (map[string]string, error) {
	routes, err := loadChainTransitRoutes()
	if err != nil {
		return nil, err
	}
	owners := map[string]string{}
	for _, r := range routes {
		if !chainTransitProtocol(r.relay.Protocol) {
			continue
		}
		clients, err := (&ClientService{}).ListForInbound(nil, r.relay.Id)
		if err != nil {
			return nil, err
		}
		for _, c := range clients {
			secret := c.ID
			if r.relay.Protocol == model.Trojan {
				secret = c.Password
			}
			if !r.granted[c.Email] || secret == "" {
				continue
			}
			_, alias := model.ChainTransitCredential(secret, r.chain.Id)
			owners[alias] = c.Email
		}
	}
	return owners, nil
}

// cachedChainTransitOwners serves the agents' frequent status reports without a
// chain walk on each one; a minted identity at worst shows unfolded for 15 s.
func (s *IpLimitService) cachedChainTransitOwners(now time.Time) (map[string]string, error) {
	ipLimitOwners.Lock()
	defer ipLimitOwners.Unlock()
	if ipLimitOwners.owners != nil && now.Sub(ipLimitOwners.at) < ipLimitOwnersTTL {
		return ipLimitOwners.owners, nil
	}
	owners, err := s.ChainTransitOwners()
	if err != nil {
		return nil, err
	}
	ipLimitOwners.owners, ipLimitOwners.at = owners, now
	return owners, nil
}

// Enforce applies every client's IP limit to one scan of live observations and
// reports whether the set of bans changed, i.e. the configs must be pushed.
func (s *IpLimitService) Enforce(now time.Time, observed []IpObservation) (bool, error) {
	db := database.GetDB()
	nowSec := now.Unix()
	lifted, err := liftLapsedIpBans(db, nowSec)
	if err != nil {
		return false, err
	}
	changed := len(lifted) > 0
	live, err := s.liveNetworks(now, observed)
	if err != nil {
		return changed, err
	}
	storeOnlineSnapshot(now, live)
	if len(live) == 0 {
		return changed, nil
	}
	emails := make([]string, 0, len(live))
	for email := range live {
		emails = append(emails, email)
	}
	sort.Strings(emails)
	limits, err := clientIpLimits(db, emails)
	if err != nil {
		return changed, err
	}
	banned, err := activeBanSet(db, nowSec)
	if err != nil {
		return changed, err
	}
	minutes, err := s.settingService.GetIpLimitBanMinutes()
	if err != nil {
		return changed, err
	}

	var fresh []model.ClientIpBan
	for _, email := range emails {
		limit := limits[email]
		if limit <= 0 {
			continue
		}
		for _, network := range networksOverLimit(live[email], banned[email], limit) {
			fresh = append(fresh, model.ClientIpBan{
				Email: email, Network: network, BannedAt: nowSec, ExpiresAt: nowSec + int64(minutes)*60,
			})
		}
	}
	if len(fresh) == 0 {
		return changed, nil
	}
	err = db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "email"}, {Name: "network"}},
		DoUpdates: clause.AssignmentColumns([]string{"banned_at", "expires_at"}),
	}).Create(&fresh).Error
	if err != nil {
		return changed, err
	}
	history := make([]model.BanRecord, 0, len(fresh))
	for _, b := range fresh {
		history = append(history, model.BanRecord{
			Email: b.Email, Kind: BanKindIPLimit, Rule: BanKindIPLimit, Network: b.Network,
			Reason:   fmt.Sprintf("同时在线 IP 超过上限（%d 个），暂停 %s", limits[b.Email], b.Network),
			BannedAt: b.BannedAt, ExpiresAt: b.ExpiresAt,
		})
	}
	// The ban itself stands even if its history cannot be written.
	if err := db.Create(&history).Error; err != nil {
		logger.Warning("[LimitIP] writing ban history failed:", err)
	}
	stamp := now.Format("2006/01/02 15:04:05")
	lines := make([]string, 0, len(fresh))
	for _, b := range fresh {
		logger.Infof("[LimitIP] %s is over its limit: %s banned for %d minutes", b.Email, b.Network, minutes)
		lines = append(lines, fmt.Sprintf("%s   BAN   [Email] = %s [IP] = %s banned for %d seconds.", stamp, b.Email, b.Network, minutes*60))
	}
	appendIpLimitLog(lines)
	return true, nil
}

// networksOverLimit keeps the limit's worth of most recently seen networks and
// returns the rest; banned and allowlisted networks are out of the count.
func networksOverLimit(seen map[string]*liveNetwork, banned map[string]bool, limit int) []string {
	type network struct {
		key      string
		lastSeen int64
	}
	nets := make([]network, 0, len(seen))
	for key, n := range seen {
		if !banned[key] && !n.allowlisted {
			nets = append(nets, network{key, n.lastSeen})
		}
	}
	if len(nets) <= limit {
		return nil
	}
	sort.Slice(nets, func(i, j int) bool {
		if nets[i].lastSeen != nets[j].lastSeen {
			return nets[i].lastSeen < nets[j].lastSeen
		}
		return nets[i].key < nets[j].key
	})
	out := make([]string, 0, len(nets)-limit)
	for _, n := range nets[:len(nets)-limit] {
		out = append(out, n.key)
	}
	return out
}

// liveNetwork is one network a client is online from, as one scan saw it.
type liveNetwork struct {
	lastSeen    int64
	addresses   map[string]struct{}
	servers     map[int]struct{}
	allowlisted bool
}

// liveNetworks folds relay identities into their owners and drops the addresses
// that are not the client's (Pigger servers, private ranges); allowlisted ones
// stay, marked, since they are the client's but never count.
func (s *IpLimitService) liveNetworks(now time.Time, observed []IpObservation) (map[string]map[string]*liveNetwork, error) {
	if len(observed) == 0 {
		return nil, nil
	}
	owners, err := s.ChainTransitOwners()
	if err != nil {
		return nil, err
	}
	ex, err := s.exemptions(now)
	if err != nil {
		return nil, err
	}
	live := map[string]map[string]*liveNetwork{}
	for _, o := range observed {
		email := o.Email
		if owner, ok := owners[email]; ok {
			email = owner
		}
		addr, ok := parseSourceAddr(o.IP)
		if !ok || email == "" {
			continue
		}
		kind, _ := ex.reason(addr)
		if kind == IpExemptHost || kind == IpExemptPrivate {
			continue
		}
		network := ipLimitNetwork(addr).String()
		// Beside an exempt relay a network is the address alone: a ban on the
		// range would cut the relay off, and a relay never shares with a device.
		if kind == IpExemptAllowlist || ex.holdsExempt(ipLimitNetwork(addr)) {
			network = addr.String()
		}
		if live[email] == nil {
			live[email] = map[string]*liveNetwork{}
		}
		n := live[email][network]
		if n == nil {
			n = &liveNetwork{addresses: map[string]struct{}{}, servers: map[int]struct{}{}, allowlisted: kind == IpExemptAllowlist}
			live[email][network] = n
		}
		n.addresses[addr.String()] = struct{}{}
		n.servers[o.Server] = struct{}{}
		n.lastSeen = max(n.lastSeen, o.LastSeen)
	}
	return live, nil
}

var ipLimitOnline = struct {
	sync.Mutex
	at   time.Time
	live map[string]map[string]*liveNetwork
}{}

func storeOnlineSnapshot(now time.Time, live map[string]map[string]*liveNetwork) {
	ipLimitOnline.Lock()
	ipLimitOnline.at, ipLimitOnline.live = now, live
	ipLimitOnline.Unlock()
}

// onlineSnapshot returns the last scan's networks, or nothing once that scan is
// too old to say who is online; callers only read the maps.
func onlineSnapshot(now time.Time) map[string]map[string]*liveNetwork {
	ipLimitOnline.Lock()
	defer ipLimitOnline.Unlock()
	if ipLimitOnline.at.IsZero() || now.Sub(ipLimitOnline.at) > ipLimitScanStaleAfter {
		return nil
	}
	return ipLimitOnline.live
}

// OnlineNetwork is one network a client is online from right now: an IPv4
// address, or an IPv6 /64 with the addresses seen in it.
type OnlineNetwork struct {
	Network   string   `json:"network" example:"198.51.100.7"`
	Addresses []string `json:"addresses" example:"[\"198.51.100.7\"]"`
	Servers   []string `json:"servers" example:"[\"HK relay\"]"`
	LastSeen  int64    `json:"lastSeen" example:"1791172800"`
	// Counted is false for an allowlisted network, which never uses a slot.
	Counted bool `json:"counted" example:"true"`
}

// ClientOnlineIps is what both panels show about a client's IP limit: Count of
// Limit slots in use now (0 = no limit), the networks online and running bans.
type ClientOnlineIps struct {
	Limit  int                 `json:"limit" example:"3"`
	Count  int                 `json:"count" example:"1"`
	Online []OnlineNetwork     `json:"online"`
	Bans   []model.ClientIpBan `json:"bans"`
}

// OnlineIps reports a client's networks online as of the last scan, without
// the banned ones, which are listed with their bans instead.
func (s *IpLimitService) OnlineIps(email string, now time.Time) (ClientOnlineIps, error) {
	out := ClientOnlineIps{Online: []OnlineNetwork{}, Bans: []model.ClientIpBan{}}
	db := database.GetDB()
	var rec model.ClientRecord
	if err := db.Select("limit_ip").Where("email = ?", email).First(&rec).Error; err != nil {
		return out, err
	}
	out.Limit = rec.LimitIP
	bans, err := s.BansForEmail(email, now)
	if err != nil {
		return out, err
	}
	out.Bans = bans
	banned := make(map[string]bool, len(bans))
	for _, b := range bans {
		banned[b.Network] = true
	}
	networks := onlineSnapshot(now)[email]
	if len(networks) == 0 {
		return out, nil
	}
	labels, err := clientServerLabels(db, email)
	if err != nil {
		return out, err
	}
	for key, n := range networks {
		if banned[key] {
			continue
		}
		entry := OnlineNetwork{Network: key, LastSeen: n.lastSeen, Counted: !n.allowlisted}
		for addr := range n.addresses {
			entry.Addresses = append(entry.Addresses, addr)
		}
		slices.Sort(entry.Addresses)
		for server := range n.servers {
			entry.Servers = append(entry.Servers, labels[server]...)
		}
		slices.Sort(entry.Servers)
		entry.Servers = slices.Compact(entry.Servers)
		if entry.Counted {
			out.Count++
		}
		out.Online = append(out.Online, entry)
	}
	sort.Slice(out.Online, func(i, j int) bool {
		if out.Online[i].LastSeen != out.Online[j].LastSeen {
			return out.Online[i].LastSeen > out.Online[j].LastSeen
		}
		return out.Online[i].Network < out.Online[j].Network
	})
	return out, nil
}

// clientServerLabels names each server by the remarks of the client's own
// inbounds there, the names its subscription shows; key 0 is this panel.
func clientServerLabels(db *gorm.DB, email string) (map[int][]string, error) {
	var rows []struct {
		NodeId *int
		Remark string
	}
	err := db.Table("inbounds").Select("inbounds.node_id AS node_id, inbounds.remark AS remark").
		Joins("JOIN client_inbounds ON client_inbounds.inbound_id = inbounds.id").
		Joins("JOIN clients ON clients.id = client_inbounds.client_id").
		Where("clients.email = ?", email).Order("inbounds.id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	labels := map[int][]string{}
	for _, r := range rows {
		node := 0
		if r.NodeId != nil {
			node = *r.NodeId
		}
		labels[node] = append(labels[node], r.Remark)
	}
	return labels, nil
}

// OnlineIpCounts maps each of emails online now to the slots it uses and its
// running bans, for the client list.
func (s *IpLimitService) OnlineIpCounts(emails []string, now time.Time) (online, bans map[string]int, err error) {
	online, bans = map[string]int{}, map[string]int{}
	if len(emails) == 0 {
		return online, bans, nil
	}
	var active []model.ClientIpBan
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var page []model.ClientIpBan
		if err := database.GetDB().Where("email IN ? AND expires_at > ?", batch, now.Unix()).Find(&page).Error; err != nil {
			return nil, nil, err
		}
		active = append(active, page...)
	}
	banned := map[string]map[string]bool{}
	for _, b := range active {
		bans[b.Email]++
		if banned[b.Email] == nil {
			banned[b.Email] = map[string]bool{}
		}
		banned[b.Email][b.Network] = true
	}
	snapshot := onlineSnapshot(now)
	for _, email := range emails {
		for key, n := range snapshot[email] {
			if !n.allowlisted && !banned[email][key] {
				online[email]++
			}
		}
	}
	return online, bans, nil
}

func clientIpLimits(db *gorm.DB, emails []string) (map[string]int, error) {
	out := make(map[string]int, len(emails))
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []struct {
			Email   string
			LimitIp int
		}
		err := db.Model(&model.ClientRecord{}).Select("email, limit_ip").
			Where("email IN ? AND limit_ip > 0 AND enable = ?", batch, true).Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.Email] = r.LimitIp
		}
	}
	return out, nil
}

func activeBanSet(db *gorm.DB, now int64) (map[string]map[string]bool, error) {
	var bans []model.ClientIpBan
	if err := db.Where("expires_at > ?", now).Find(&bans).Error; err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for _, b := range bans {
		if out[b.Email] == nil {
			out[b.Email] = map[string]bool{}
		}
		out[b.Email][b.Network] = true
	}
	return out, nil
}

// activeIpLimitBans are the bans the configs carry: unexpired, on a client that
// still has a limit and is enabled.
func activeIpLimitBans(db *gorm.DB, now int64) ([]model.ClientIpBan, error) {
	var bans []model.ClientIpBan
	err := db.Table("client_ip_bans").Select("client_ip_bans.*").
		Joins("JOIN clients ON clients.email = client_ip_bans.email").
		Where("client_ip_bans.expires_at > ? AND clients.limit_ip > 0 AND clients.enable = ?", now, true).
		Order("client_ip_bans.email, client_ip_bans.network").
		Find(&bans).Error
	return bans, err
}

// liftLapsedIpBans deletes bans that expired or whose client no longer has a
// limit (or is gone, disabled or renamed), logging each one it lifts.
func liftLapsedIpBans(db *gorm.DB, now int64) ([]model.ClientIpBan, error) {
	var lapsed []model.ClientIpBan
	err := db.Where("expires_at <= ? OR email NOT IN (?)", now,
		db.Model(&model.ClientRecord{}).Select("email").Where("limit_ip > 0 AND enable = ?", true)).
		Find(&lapsed).Error
	if err != nil || len(lapsed) == 0 {
		return nil, err
	}
	ids := make([]int, 0, len(lapsed))
	for _, b := range lapsed {
		ids = append(ids, b.Id)
	}
	if err := db.Where("id IN ?", ids).Delete(&model.ClientIpBan{}).Error; err != nil {
		return nil, err
	}
	logUnbans(time.Unix(now, 0), lapsed)
	return lapsed, nil
}

func logUnbans(now time.Time, bans []model.ClientIpBan) {
	stamp := now.Format("2006/01/02 15:04:05")
	lines := make([]string, 0, len(bans))
	for _, b := range bans {
		lines = append(lines, fmt.Sprintf("%s   UNBAN   [Email] = %s [IP] = %s unbanned.", stamp, b.Email, b.Network))
	}
	appendIpLimitLog(lines)
}

// appendIpLimitLog keeps the wording the fail2ban action used, so the daily log
// rotation and the Telegram backup carry these lines unchanged.
func appendIpLimitLog(lines []string) {
	if len(lines) == 0 {
		return
	}
	f, err := os.OpenFile(xray.GetIPLimitBannedLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		logger.Warning("[LimitIP] could not open the ban log:", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		logger.Warning("[LimitIP] could not write the ban log:", err)
	}
}

// IpLimitExemptHost is one Pigger server whose addresses the IP limit never
// counts or bans; Name is "" for this panel's own host.
type IpLimitExemptHost struct {
	Name      string   `json:"name" example:"hk-relay"`
	Addresses []string `json:"addresses" example:"[\"203.0.113.7\",\"2001:db8:1:2::/64\"]"`
}

// ExemptHosts lists the server addresses exempt from the IP limit, this panel
// first, so an operator can check that every relay is covered.
func (s *IpLimitService) ExemptHosts() ([]IpLimitExemptHost, error) {
	ex, err := s.exemptions(time.Now())
	if err != nil {
		return nil, err
	}
	var out []IpLimitExemptHost
	for _, h := range ex.hosts {
		addr := h.prefix.String()
		if h.prefix.Addr().Is4() {
			addr = h.prefix.Addr().String()
		}
		if n := len(out); n > 0 && out[n-1].Name == h.name {
			if !slices.Contains(out[n-1].Addresses, addr) {
				out[n-1].Addresses = append(out[n-1].Addresses, addr)
			}
			continue
		}
		out = append(out, IpLimitExemptHost{Name: h.name, Addresses: []string{addr}})
	}
	return out, nil
}

// BansForEmail lists a client's running bans, oldest network first.
func (s *IpLimitService) BansForEmail(email string, now time.Time) ([]model.ClientIpBan, error) {
	var bans []model.ClientIpBan
	err := database.GetDB().Where("email = ? AND expires_at > ?", email, now.Unix()).
		Order("network").Find(&bans).Error
	return bans, err
}

// Unban lifts one ban before it expires; the next config sync drops the rule.
func (s *IpLimitService) Unban(email, network string) error {
	db := database.GetDB()
	var ban model.ClientIpBan
	if err := db.Where("email = ? AND network = ?", email, network).First(&ban).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errIpNotBanned
		}
		return err
	}
	if err := db.Delete(&ban).Error; err != nil {
		return err
	}
	now := time.Now()
	if err := db.Model(&model.BanRecord{}).
		Where("email = ? AND kind = ? AND network = ? AND banned_at = ? AND lifted_at = 0", email, BanKindIPLimit, network, ban.BannedAt).
		Update("lifted_at", now.Unix()).Error; err != nil {
		logger.Warning("[LimitIP] marking the ban lifted in its history failed:", err)
	}
	logUnbans(now, []model.ClientIpBan{ban})
	return nil
}

// injectIpLimitBans puts each banned client's networks first in routing, for the
// users this config serves, their relay identities included.
func injectIpLimitBans(cfg *xray.Config, transitOwners map[string]string, now time.Time) error {
	bans, err := activeIpLimitBans(database.GetDB(), now.Unix())
	if err != nil || len(bans) == 0 {
		return err
	}
	identities := map[string][]string{}
	for _, email := range servedEmails(cfg) {
		owner := email
		if o, ok := transitOwners[email]; ok {
			owner = o
		}
		identities[owner] = append(identities[owner], email)
	}
	networks := map[string][]string{}
	var owners []string
	for _, b := range bans {
		if _, served := identities[b.Email]; !served {
			continue
		}
		if networks[b.Email] == nil {
			owners = append(owners, b.Email)
		}
		networks[b.Email] = append(networks[b.Email], b.Network)
	}
	if len(owners) == 0 {
		return nil
	}
	rules := make([]any, 0, len(owners))
	for _, owner := range owners {
		users := slices.Clone(identities[owner])
		slices.Sort(users)
		users = slices.Compact(users)
		rules = append(rules, map[string]any{
			"type": "field", "user": users, "sourceIP": networks[owner], "outboundTag": abuse.TagIPLimitBlock,
		})
	}
	return prependRoutingWithOutbound(cfg, rules, map[string]any{
		"tag": abuse.TagIPLimitBlock, "protocol": "blackhole", "settings": map[string]any{},
	})
}

// servedEmails lists the client emails of every inbound in cfg.
func servedEmails(cfg *xray.Config) []string {
	var out []string
	for _, ib := range cfg.InboundConfigs {
		var settings struct {
			Clients []struct {
				Email string `json:"email"`
			} `json:"clients"`
			// A user SOCKS5 inbound logs its users in by name: Xray reports that name as the user.
			Accounts []struct {
				User string `json:"user"`
			} `json:"accounts"`
		}
		if len(ib.Settings) == 0 || json.Unmarshal(ib.Settings, &settings) != nil {
			continue
		}
		for _, c := range settings.Clients {
			if c.Email != "" {
				out = append(out, c.Email)
			}
		}
		if ib.Protocol == string(model.Mixed) {
			for _, a := range settings.Accounts {
				if a.User != "" {
					out = append(out, a.User)
				}
			}
		}
	}
	return out
}

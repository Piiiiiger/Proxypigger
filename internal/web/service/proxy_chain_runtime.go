package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Enforce at render time, including subsequent inbound edits and restored rows.
// Sniffed Reality camouflage names must never replace the actual next hop.
func protectChainRelay(tx *gorm.DB, inbound *model.Inbound) error {
	var count int64
	if err := tx.Model(&model.ProxyChain{}).Where("relay_inbound_id = ? AND enabled = ?", inbound.Id, true).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 || strings.TrimSpace(inbound.Sniffing) == "" {
		return nil
	}
	var sniff map[string]any
	if err := json.Unmarshal([]byte(inbound.Sniffing), &sniff); err != nil {
		return err
	}
	if sniff == nil {
		return nil
	}
	sniff["routeOnly"] = true
	raw, err := json.Marshal(sniff)
	inbound.Sniffing = string(raw)
	return err
}

// ValidateChainRelay refuses a chain its relay cannot carry: transit logins need a
// VLESS/VMess/Trojan relay, and a SOCKS5 relay dials exactly one exit itself.
func ValidateChainRelay(db *gorm.DB, chainID int, relay, target *model.Inbound) error {
	if chainTransitProtocol(relay.Protocol) {
		return nil
	}
	if relay.Protocol != model.Mixed {
		return fmt.Errorf("中转节点不支持 %s 协议", relay.Protocol)
	}
	switch target.Protocol {
	case model.VLESS, model.VMESS, model.Trojan, model.Shadowsocks:
	default:
		return fmt.Errorf("SOCKS5 中转不能连接 %s 出口", target.Protocol)
	}
	others := db.Model(&model.ProxyChain{}).Where("relay_inbound_id = ?", relay.Id)
	if chainID > 0 {
		others = others.Where("id <> ?", chainID)
	}
	var count int64
	if err := others.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("SOCKS5 中转只能连接一个出口")
	}
	return nil
}

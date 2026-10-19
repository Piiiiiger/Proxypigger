package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type PlanNodeOption struct {
	Key            string `json:"key"`
	Label          string `json:"label"`
	InboundId      int    `json:"inboundId"`
	RelayInboundId int    `json:"relayInboundId"`
}

func (s *PlanService) NodeOptions() ([]PlanNodeOption, error) {
	return planNodeOptions(database.GetDB())
}

func planNodeOptions(db *gorm.DB) ([]PlanNodeOption, error) {
	var inbounds []model.Inbound
	if err := db.Select("id", "remark", "tag", "protocol").Order("id").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	var chains []model.ProxyChain
	if err := db.Where("enabled = ?", true).Order("id").Find(&chains).Error; err != nil {
		return nil, err
	}
	names := make(map[int]string)
	for _, ib := range inbounds {
		// SOCKS5 nodes are attached per user: no subscription can carry them.
		if ib.Protocol == model.Mixed {
			continue
		}
		name := strings.TrimSpace(ib.Remark)
		if name == "" {
			name = ib.Tag
		}
		if name == "" {
			name = fmt.Sprintf("#%d", ib.Id)
		}
		names[ib.Id] = name
	}
	byTarget := make(map[int][]model.ProxyChain)
	for _, c := range chains {
		if names[c.RelayInboundId] != "" {
			byTarget[c.TargetInboundId] = append(byTarget[c.TargetInboundId], c)
		}
	}
	out := []PlanNodeOption{}
	for _, ib := range inbounds {
		label := names[ib.Id]
		if label == "" {
			continue
		}
		out = append(out, PlanNodeOption{Key: model.PlanNodeKey(ib.Id, false), Label: label, InboundId: ib.Id})
		for _, chain := range byTarget[ib.Id] {
			relayID := chain.RelayInboundId
			out = append(out, PlanNodeOption{Key: model.PlanChainKey(ib.Id, chain.Id), Label: chain.RelayProxyName(label, names[relayID]), InboundId: ib.Id, RelayInboundId: relayID})
		}
	}
	return out, nil
}

func effectivePlanNodeKeys(raw string, ids []int, options []PlanNodeOption) []string {
	var keys []string
	if strings.TrimSpace(raw) != "" && json.Unmarshal([]byte(raw), &keys) == nil {
		// Explicit choices are authoritative. Inbound grants also contain hidden
		// relay dependencies and must never be promoted into visible selections.
		return nodeKeysForInbounds(keys, ids)
	}
	keys = []string{}
	for _, option := range options {
		if slices.Contains(ids, option.InboundId) {
			keys = append(keys, option.Key)
		}
	}
	return keys
}

func nodeKeysForInbounds(keys []string, ids []int) []string {
	out := []string{}
	for _, key := range keys {
		id, _, err := model.ParsePlanNodeKey(key)
		if err == nil && slices.Contains(ids, id) {
			out = append(out, key)
		}
	}
	return out
}

func normalizePlanNodeKeys(db *gorm.DB, in *PlanInput) error {
	// Old API clients can continue sending inboundIds alone.
	if in.NodeKeys == nil {
		return nil
	}
	options, err := planNodeOptions(db)
	if err != nil {
		return err
	}
	byKey := make(map[string]PlanNodeOption)
	for _, option := range options {
		byKey[option.Key] = option
	}
	keys := []string{}
	ids := []int{}
	for _, key := range in.NodeKeys {
		option, ok := byKey[key]
		if !ok {
			return fmt.Errorf("节点版本不存在或中转已停用：%s", key)
		}
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
		ids = append(ids, option.InboundId)
		// A relay is an actual authenticated hop, so grant its inbound too.
		if option.RelayInboundId != 0 {
			ids = append(ids, option.RelayInboundId)
		}
	}
	in.NodeKeys = keys
	in.InboundIds = uniqueSortedIds(ids)
	for i := range in.ProxyGroups {
		group := &in.ProxyGroups[i]
		if group.NodeKeys == nil {
			continue
		}
		groupIDs := []int{}
		for _, key := range group.NodeKeys {
			if !slices.Contains(keys, key) {
				return fmt.Errorf("代理组包含套餐未选择的节点版本：%s", key)
			}
			groupIDs = append(groupIDs, byKey[key].InboundId)
		}
		group.InboundIds = uniqueSortedIds(groupIDs)
	}
	return nil
}

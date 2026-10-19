package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mhsanaei/3x-ui/v3/internal/agentproto"
	"github.com/mhsanaei/3x-ui/v3/internal/sub"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// ProxyChainController manages the admin-facing target -> relay relations used
// by Clash/Mihomo subscriptions.
type ProxyChainController struct{}

func NewProxyChainController(g *gin.RouterGroup) *ProxyChainController {
	a := &ProxyChainController{}
	g.GET("/list", a.list)
	g.POST("/add", a.save)
	g.POST("/update/:id", a.save)
	g.POST("/del/:id", a.delete)
	g.POST("/check/:id", a.check)
	return a
}

type proxyChainInput struct {
	Name            string  `json:"name"`
	DirectName      *string `json:"directName"`
	RelayName       *string `json:"relayName"`
	TargetInboundId int     `json:"targetInboundId"`
	RelayInboundId  int     `json:"relayInboundId"`
	Enabled         *bool   `json:"enabled"`
}

func (a *ProxyChainController) list(c *gin.Context) {
	var rows []model.ProxyChain
	err := database.GetDB().Order("id ASC").Find(&rows).Error
	jsonObj(c, rows, err)
}

func (a *ProxyChainController) save(c *gin.Context) {
	var in proxyChainInput
	if err := c.ShouldBindJSON(&in); err != nil {
		jsonMsg(c, "保存链式配置失败", err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.TargetInboundId <= 0 || in.RelayInboundId <= 0 || in.TargetInboundId == in.RelayInboundId {
		jsonMsg(c, "入口节点和中转节点必须有效且不能相同", errors.New("invalid proxy chain"))
		return
	}
	db := database.GetDB()
	var target, relay model.Inbound
	if err := db.First(&target, in.TargetInboundId).Error; err != nil {
		jsonMsg(c, "目标入站不存在", err)
		return
	}
	if err := db.First(&relay, in.RelayInboundId).Error; err != nil {
		jsonMsg(c, "中转入站不存在", err)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	var id int
	if raw := c.Param("id"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			jsonMsg(c, "无效链式配置", err)
			return
		}
		id = parsed
	}
	var row model.ProxyChain
	if id > 0 {
		if err := db.First(&row, id).Error; err != nil {
			jsonMsg(c, "链式配置不存在", err)
			return
		}
	}
	var conflict model.ProxyChain
	conflictQuery := db.Where("target_inbound_id = ? AND relay_inbound_id = ?", in.TargetInboundId, in.RelayInboundId)
	if id > 0 {
		conflictQuery = conflictQuery.Where("id <> ?", id)
	}
	if err := conflictQuery.First(&conflict).Error; err == nil {
		jsonMsg(c, "链式转发已存在", errors.New("同一目标和中转节点不能重复配置，请编辑已有线路"))
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		jsonMsg(c, "检查链式配置失败", err)
		return
	}
	if err := service.ValidateChainRelay(db, id, &relay, &target); err != nil {
		jsonMsg(c, "中转节点无法承载此线路", err)
		return
	}
	wasEnabled := row.Enabled
	oldTarget, oldRelay := row.TargetInboundId, row.RelayInboundId
	row.Name, row.TargetInboundId, row.RelayInboundId, row.Enabled = in.Name, in.TargetInboundId, in.RelayInboundId, enabled
	// Direct links use the target inbound's own name; the old override is retired.
	row.DirectName = ""
	if in.RelayName != nil {
		row.RelayName = strings.TrimSpace(*in.RelayName)
	}
	for _, name := range []string{row.RelayName} {
		if utf8.RuneCountInString(name) > 128 || strings.ContainsFunc(name, unicode.IsControl) {
			jsonMsg(c, "节点显示名称无效", errors.New("名称最多 128 个字符，不能包含换行或控制字符"))
			return
		}
		switch strings.ToUpper(name) {
		case "DIRECT", "REJECT", "REJECT-DROP", "PASS", "COMPATIBLE", "GLOBAL":
			jsonMsg(c, "节点显示名称无效", errors.New("不能使用内置策略名称"))
			return
		}
	}
	if row.DirectProxyName(target.Remark) == row.RelayProxyName(target.Remark, relay.Remark) {
		jsonMsg(c, "节点显示名称无效", errors.New("中转名称不能和目标节点名称相同"))
		return
	}
	if enabled && (id == 0 || !wasEnabled || oldTarget != in.TargetInboundId || oldRelay != in.RelayInboundId) && relay.NodeID != nil {
		if err := checkChainRoute(c.Request.Context(), &target, &relay); err != nil {
			jsonMsg(c, "链路未通过检查", err)
			return
		}
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if id > 0 && (oldTarget != row.TargetInboundId || oldRelay != row.RelayInboundId) {
			oldKey := model.PlanChainKey(oldTarget, id)
			var plans []model.Plan
			if err := tx.Find(&plans).Error; err != nil {
				return err
			}
			for _, plan := range plans {
				var keys []string
				if strings.TrimSpace(plan.NodeKeys) == "" {
					continue
				}
				if err := json.Unmarshal([]byte(plan.NodeKeys), &keys); err != nil {
					return err
				}
				if !slices.Contains(keys, oldKey) {
					continue
				}
				var count int64
				if err := tx.Model(&model.PlanInbound{}).Where("plan_id = ? AND inbound_id IN ?", plan.Id, []int{row.TargetInboundId, row.RelayInboundId}).Count(&count).Error; err != nil {
					return err
				}
				if count != 2 {
					return fmt.Errorf("套餐 %s 正在使用此线路，请先将新的目标和中转节点加入该套餐", plan.Name)
				}
			}
			if err := database.RemapPlanChainKeys(tx, map[string]string{oldKey: model.PlanChainKey(row.TargetInboundId, id)}); err != nil {
				return err
			}
		}
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		// GORM applies default:true on inserts even when a disabled draft was requested.
		if !enabled {
			if err := tx.Model(&row).Update("enabled", false).Error; err != nil {
				return err
			}
			row.Enabled = false
		}
		if relay.NodeID != nil {
			return (&service.NodeService{}).MarkNodeDirtyTx(tx, *relay.NodeID)
		}
		return nil
	}); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") && strings.Contains(err.Error(), "proxy_chains") {
			err = errors.New("同一目标和中转节点已经存在，请编辑已有线路")
		}
		jsonMsg(c, "保存链式配置失败", err)
		return
	}
	if relay.NodeID == nil {
		(&service.XrayService{}).SetToNeedRestart()
	} else if h := runtime.GetAgentHub(); h != nil {
		h.Nudge(*relay.NodeID)
	}
	jsonObj(c, row, nil)
}

func (a *ProxyChainController) delete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "msg": "invalid id"})
		return
	}
	err = database.GetDB().Delete(&model.ProxyChain{}, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	jsonObj(c, gin.H{"id": id}, err)
}

func checkChainRoute(ctx context.Context, target, relay *model.Inbound) error {
	if relay.NodeID == nil {
		return errors.New("本地中转尚未提供远程连通性检查")
	}
	h := runtime.GetAgentHub()
	if h == nil {
		return errors.New("中转 Agent 未连接")
	}
	eps, err := sub.ChainEndpoints(target)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	for _, ep := range eps {
		if err := h.Probe(ctx, *relay.NodeID, agentproto.Probe{Host: ep.Address, Port: ep.Port}); err != nil {
			return fmt.Errorf("%s → %s (%s:%d)：%w", relay.Remark, target.Remark, ep.Address, ep.Port, err)
		}
	}
	return nil
}

func (a *ProxyChainController) check(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		jsonMsg(c, "无效链路", errors.New("invalid chain id"))
		return
	}
	var row model.ProxyChain
	if err := database.GetDB().First(&row, id).Error; err != nil {
		jsonMsg(c, "链路不存在", err)
		return
	}
	var target, relay model.Inbound
	if err := database.GetDB().First(&target, row.TargetInboundId).Error; err != nil {
		jsonMsg(c, "目标不存在", err)
		return
	}
	if err := database.GetDB().First(&relay, row.RelayInboundId).Error; err != nil {
		jsonMsg(c, "中转不存在", err)
		return
	}
	if err := checkChainRoute(c.Request.Context(), &target, &relay); err != nil {
		jsonMsg(c, "连通性检查失败", err)
		return
	}
	jsonObj(c, map[string]string{"message": "中转到目标 TCP 可达；此检查不验证用户认证和完整代理请求"}, nil)
}

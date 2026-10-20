package service

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/clashmerge"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// PlanService manages plans and stamps them onto clients through ClientService,
// so every change still reaches each inbound through the runtime dispatch.
type PlanService struct {
	clientService ClientService
}

// PlanProxyGroup assigns a plan's inbounds to one proxy group from its rule template.
type PlanProxyGroup struct {
	Name       string   `json:"name" example:"🔰 节点选择"`
	InboundIds []int    `json:"inboundIds" example:"[1,2]"`
	NodeKeys   []string `json:"nodeKeys,omitempty"`
}

// PlanInput is the editable part of a plan plus the inbounds it grants.
type PlanInput struct {
	Name        string           `json:"name" example:"Monthly 100G"`
	LimitIP     int              `json:"limitIp" example:"0"`
	Remark      string           `json:"remark" example:"Hong Kong and Singapore"`
	TemplateId  int              `json:"templateId" example:"1"`
	InboundIds  []int            `json:"inboundIds" example:"[1,2]"`
	ProxyGroups []PlanProxyGroup `json:"proxyGroups,omitempty"`
	NodeKeys    []string         `json:"nodeKeys,omitempty"`
	// TermMonths are the only terms in calendar months the plan is sold and renewed
	// by; none allows any.
	TermMonths []int `json:"termMonths,omitempty" example:"[3,12]"`
}

// PlanSummary is a plan with the inbounds it grants and how many clients use it.
type PlanSummary struct {
	model.Plan
	InboundIds      []int            `json:"inboundIds" example:"[1,2]"`
	ProxyGroups     []PlanProxyGroup `json:"proxyGroups,omitempty"`
	ProxyGroupNames []string         `json:"proxyGroupNames,omitempty"`
	MemberCount     int              `json:"memberCount" example:"4"`
	NodeKeys        []string         `json:"nodeKeys,omitempty"`
	TermMonths      []int            `json:"termMonths,omitempty" example:"[3,12]"`
}

const planDayMillis = int64(24 * time.Hour / time.Millisecond)

func (s *PlanService) List() ([]PlanSummary, error) {
	db := database.GetDB()
	options, err := s.NodeOptions()
	if err != nil {
		return nil, err
	}
	var plans []model.Plan
	if err := db.Order("sort_index ASC, id ASC").Find(&plans).Error; err != nil {
		return nil, err
	}
	out := make([]PlanSummary, 0, len(plans))
	for _, p := range plans {
		ids, err := planInboundIds(db, p.Id)
		if err != nil {
			return nil, err
		}
		var members int64
		if err := db.Model(&model.ClientRecord{}).Where("plan_id = ?", p.Id).Count(&members).Error; err != nil {
			return nil, err
		}
		nodeKeys := effectivePlanNodeKeys(p.NodeKeys, ids, options)
		groups := decodePlanProxyGroups(p.ProxyGroups)
		for i := range groups {
			if groups[i].NodeKeys == nil {
				groups[i].NodeKeys = nodeKeysForInbounds(nodeKeys, groups[i].InboundIds)
			}
		}
		out = append(out, PlanSummary{
			Plan:            p,
			InboundIds:      ids,
			ProxyGroups:     groups,
			NodeKeys:        nodeKeys,
			TermMonths:      PlanTermMonths(&p),
			ProxyGroupNames: planTemplateGroupNames(db, p.TemplateId),
			MemberCount:     int(members),
		})
	}
	return out, nil
}

func (s *PlanService) Get(id int) (*model.Plan, []int, error) {
	db := database.GetDB()
	var plan model.Plan
	if err := db.First(&plan, id).Error; err != nil {
		return nil, nil, common.NewError("plan not found:", id)
	}
	ids, err := planInboundIds(db, id)
	if err != nil {
		return nil, nil, err
	}
	return &plan, ids, nil
}

func (s *PlanService) Create(in PlanInput) (*model.Plan, error) {
	plan := &model.Plan{}
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := validatePlanInput(tx, 0, &in); err != nil {
			return err
		}
		applyPlanInput(plan, in)
		if err := tx.Create(plan).Error; err != nil {
			return err
		}
		return replacePlanInbounds(tx, plan.Id, in.InboundIds)
	})
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// Update attaches the plan's members to the inbounds it gains and detaches them from those
// it loses, then saves the plan; reapplyLimits also re-stamps its IP limit on them.
func (s *PlanService) Update(inboundSvc *InboundService, id int, in PlanInput, reapplyLimits bool) (bool, error) {
	db := database.GetDB()
	var plan model.Plan
	if err := db.First(&plan, id).Error; err != nil {
		return false, common.NewError("plan not found:", id)
	}
	if err := validatePlanInput(db, id, &in); err != nil {
		return false, err
	}
	before, err := planInboundIds(db, id)
	if err != nil {
		return false, err
	}
	members, err := planMemberEmails(id)
	if err != nil {
		return false, err
	}
	applyPlanInput(&plan, in)
	gained := idsMissingFrom(in.InboundIds, before)
	lost := idsMissingFrom(before, in.InboundIds)
	needRestart := false
	for _, email := range members {
		nr, err := s.updateMember(inboundSvc, &plan, email, gained, lost, reapplyLimits)
		needRestart = needRestart || nr
		if err != nil {
			return needRestart, err
		}
	}
	// Saved last: a save that fails on a member leaves the plan as it was, so saving
	// again works out the same gained and lost inbounds and skips members already done.
	return needRestart, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&plan).Error; err != nil {
			return err
		}
		return replacePlanInbounds(tx, id, in.InboundIds)
	})
}

func (s *PlanService) updateMember(inboundSvc *InboundService, plan *model.Plan, email string, gained, lost []int, reapplyLimits bool) (bool, error) {
	needRestart := false
	if reapplyLimits {
		rec, err := s.clientService.GetRecordByEmail(nil, email)
		if err != nil {
			return false, err
		}
		if needRestart, err = s.stampPlanIPLimit(inboundSvc, plan, rec); err != nil {
			return needRestart, err
		}
	}
	nr, err := s.attachAndDetach(inboundSvc, email, gained, lost)
	return needRestart || nr, err
}

// AddInboundToPlans grants the inbound to each plan and attaches it to their members,
// leaving their limits and other inbounds alone; granting it again changes nothing.
func (s *PlanService) AddInboundToPlans(inboundSvc *InboundService, inboundId int, planIds []int) (bool, error) {
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		var known []int
		if err := tx.Model(&model.Plan{}).Where("id IN ?", planIds).Pluck("id", &known).Error; err != nil {
			return err
		}
		if missing := idsMissingFrom(planIds, known); len(missing) > 0 {
			return common.NewError("plan not found:", missing)
		}
		var inbounds int64
		if err := tx.Model(&model.Inbound{}).Where("id = ?", inboundId).Count(&inbounds).Error; err != nil {
			return err
		}
		if inbounds == 0 {
			return common.NewError("inbound not found:", inboundId)
		}
		for _, planId := range planIds {
			row := model.PlanInbound{PlanId: planId, InboundId: inboundId}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	members, err := planMemberEmails(planIds...)
	if err != nil {
		return false, err
	}
	needRestart := false
	for _, email := range members {
		nr, err := s.clientService.AttachByEmail(inboundSvc, email, []int{inboundId})
		needRestart = needRestart || nr
		if err != nil {
			return needRestart, err
		}
	}
	return needRestart, nil
}

func (s *PlanService) Delete(id int) error {
	activationUseMu.Lock()
	defer activationUseMu.Unlock()
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		var members int64
		if err := tx.Model(&model.ClientRecord{}).Where("plan_id = ?", id).Count(&members).Error; err != nil {
			return err
		}
		if members > 0 {
			return common.NewErrorf("plan still has %d clients; move them to another plan first", members)
		}
		if err := tx.Where("plan_id = ?", id).Delete(&model.PlanInbound{}).Error; err != nil {
			return err
		}
		if err := tx.Where("plan_id = ?", id).Delete(&model.ActivationCode{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Plan{}, id).Error
	})
}

// Assign puts each client on the plan: its IP limit and exactly its inbounds (attaching
// and detaching); the client's own quota, expiry and reset schedule stay as they are.
func (s *PlanService) Assign(inboundSvc *InboundService, emails []string, planId int) (bool, error) {
	plan, planIds, err := s.Get(planId)
	if err != nil {
		return false, err
	}
	needRestart := false
	for _, email := range emails {
		nr, err := s.assignOne(inboundSvc, plan, planIds, email)
		needRestart = needRestart || nr
		if err != nil {
			return needRestart, err
		}
	}
	return needRestart, nil
}

func (s *PlanService) assignOne(inboundSvc *InboundService, plan *model.Plan, planIds []int, email string) (bool, error) {
	rec, err := s.clientService.GetRecordByEmail(nil, email)
	if err != nil {
		return false, err
	}
	needRestart, err := s.stampPlanIPLimit(inboundSvc, plan, rec)
	if err != nil {
		return needRestart, err
	}
	current, err := s.clientService.GetInboundIdsForRecord(rec.Id)
	if err != nil {
		return needRestart, err
	}
	nr, err := s.attachAndDetach(inboundSvc, email, idsMissingFrom(planIds, current), idsMissingFrom(current, planIds))
	needRestart = needRestart || nr
	if err != nil {
		return needRestart, err
	}
	return needRestart, database.GetDB().Model(&model.ClientRecord{}).
		Where("id = ?", rec.Id).UpdateColumn("plan_id", plan.Id).Error
}

// stampPlanIPLimit writes the plan's IP limit onto the client.
func (s *PlanService) stampPlanIPLimit(inboundSvc *InboundService, plan *model.Plan, rec *model.ClientRecord) (bool, error) {
	client := rec.ToClient()
	client.LimitIP = plan.LimitIP
	return s.clientService.Update(inboundSvc, rec.Id, *client, rec.LimitHwid)
}

func (s *PlanService) attachAndDetach(inboundSvc *InboundService, email string, attach, detach []int) (bool, error) {
	needRestart := false
	if len(attach) > 0 {
		nr, err := s.clientService.AttachByEmail(inboundSvc, email, attach)
		needRestart = needRestart || nr
		if err != nil {
			return needRestart, err
		}
	}
	if len(detach) > 0 {
		nr, err := s.clientService.DetachByEmailMany(inboundSvc, email, detach)
		needRestart = needRestart || nr
		if err != nil {
			return needRestart, err
		}
	}
	return needRestart, nil
}

// Unassign drops the plan from each client and leaves its current limits as they are.
func (s *PlanService) Unassign(emails []string) error {
	if len(emails) == 0 {
		return nil
	}
	return database.GetDB().Model(&model.ClientRecord{}).
		Where("email IN ?", emails).UpdateColumn("plan_id", 0).Error
}

func decodePlanProxyGroups(raw string) []PlanProxyGroup {
	if strings.TrimSpace(raw) == "" {
		return []PlanProxyGroup{}
	}
	var groups []PlanProxyGroup
	if err := json.Unmarshal([]byte(raw), &groups); err != nil || groups == nil {
		return []PlanProxyGroup{}
	}
	return groups
}

func encodePlanProxyGroups(groups []PlanProxyGroup) string {
	if len(groups) == 0 {
		return ""
	}
	encoded, err := json.Marshal(groups)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func normalizePlanProxyGroups(groups []PlanProxyGroup, inboundIds []int) error {
	allowed := make(map[int]struct{}, len(inboundIds))
	for _, id := range inboundIds {
		allowed[id] = struct{}{}
	}
	seenNames := make(map[string]struct{}, len(groups))
	for i := range groups {
		groups[i].Name = strings.TrimSpace(groups[i].Name)
		if groups[i].Name == "" {
			return common.NewError("plan proxy group name is required")
		}
		if _, duplicate := seenNames[groups[i].Name]; duplicate {
			return common.NewError("duplicate plan proxy group:", groups[i].Name)
		}
		seenNames[groups[i].Name] = struct{}{}
		groups[i].InboundIds = uniqueSortedIds(groups[i].InboundIds)
		for _, id := range groups[i].InboundIds {
			if _, ok := allowed[id]; !ok {
				return common.NewError("plan proxy group refers to an inbound outside this plan:", id)
			}
		}
	}
	return nil
}

func planTemplateGroupNames(db *gorm.DB, templateId int) []string {
	var tpl model.RuleTemplate
	query := db
	if templateId > 0 {
		query = query.Where("id = ?", templateId)
	} else {
		query = query.Where("is_default = ?", true).Order("id ASC")
	}
	if err := query.First(&tpl).Error; err != nil {
		return []string{}
	}
	doc, err := templateMap(tpl.Content)
	if err != nil {
		return []string{}
	}
	if tpl.BaseId != 0 {
		base, baseDoc, err := loadBase(db, tpl.BaseId)
		if err != nil || base.Id == 0 {
			return []string{}
		}
		doc = clashmerge.Apply(baseDoc, doc)
	}
	values, _ := doc["proxy-groups"].([]any)
	names := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		group, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, _ := group["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func validatePlanInput(tx *gorm.DB, selfId int, in *PlanInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return common.NewError("plan name is required")
	}
	var taken int64
	if err := tx.Model(&model.Plan{}).Where("name = ? AND id <> ?", in.Name, selfId).Count(&taken).Error; err != nil {
		return err
	}
	if taken > 0 {
		return common.NewError("a plan with this name already exists:", in.Name)
	}
	if in.LimitIP < 0 {
		return common.NewError("the IP limit cannot be negative")
	}
	for _, months := range in.TermMonths {
		if months < 1 || months > maxTermMonths {
			return common.NewErrorf("a term is 1 to %d months", maxTermMonths)
		}
	}
	in.TermMonths = uniqueSortedIds(in.TermMonths)
	if in.TemplateId < 0 {
		in.TemplateId = 0
	}
	if in.TemplateId > 0 {
		var found int64
		if err := tx.Model(&model.RuleTemplate{}).Where("id = ?", in.TemplateId).Count(&found).Error; err != nil {
			return err
		}
		if found == 0 {
			return common.NewError("plan refers to a rule template that does not exist")
		}
	}
	in.InboundIds = uniqueSortedIds(in.InboundIds)
	if err := normalizePlanNodeKeys(tx, in); err != nil {
		return err
	}
	if err := normalizePlanProxyGroups(in.ProxyGroups, in.InboundIds); err != nil {
		return err
	}
	if len(in.InboundIds) > 0 {
		var found int64
		if err := tx.Model(&model.Inbound{}).Where("id IN ?", in.InboundIds).Count(&found).Error; err != nil {
			return err
		}
		if int(found) != len(in.InboundIds) {
			return common.NewError("plan refers to an inbound that does not exist")
		}
	}
	return nil
}

func applyPlanInput(plan *model.Plan, in PlanInput) {
	plan.Name = in.Name
	plan.LimitIP = in.LimitIP
	plan.Remark = in.Remark
	plan.TemplateId = in.TemplateId
	plan.ProxyGroups = encodePlanProxyGroups(in.ProxyGroups)
	if in.NodeKeys != nil {
		encoded, _ := json.Marshal(in.NodeKeys)
		plan.NodeKeys = string(encoded)
	}
	plan.TermMonths = ""
	if len(in.TermMonths) > 0 {
		encoded, _ := json.Marshal(in.TermMonths)
		plan.TermMonths = string(encoded)
	}
}

// PlanTermMonths are the only terms in months a plan is sold and renewed by; none
// allows any.
func PlanTermMonths(plan *model.Plan) []int {
	var months []int
	if plan == nil || plan.TermMonths == "" || json.Unmarshal([]byte(plan.TermMonths), &months) != nil {
		return nil
	}
	return months
}

// checkPlanTerm refuses a term that is not one of the plan's months.
func checkPlanTerm(plan *model.Plan, term RenewalTerm, refusal string) error {
	terms := PlanTermMonths(plan)
	if len(terms) == 0 || (term.Days == 0 && slices.Contains(terms, term.Months)) {
		return nil
	}
	list := make([]string, len(terms))
	for i, d := range terms {
		list[i] = strconv.Itoa(d)
	}
	words := list[len(list)-1]
	if len(list) > 1 {
		words = strings.Join(list[:len(list)-1], ", ") + " or " + words
	}
	return common.NewErrorf(refusal, plan.Name, words)
}

func replacePlanInbounds(tx *gorm.DB, planId int, inboundIds []int) error {
	if err := tx.Where("plan_id = ?", planId).Delete(&model.PlanInbound{}).Error; err != nil {
		return err
	}
	for _, id := range inboundIds {
		if err := tx.Create(&model.PlanInbound{PlanId: planId, InboundId: id}).Error; err != nil {
			return err
		}
	}
	return nil
}

// planInboundIds lists the plan's inbounds that still exist, ascending.
func planInboundIds(db *gorm.DB, planId int) ([]int, error) {
	ids := []int{}
	err := db.Model(&model.PlanInbound{}).
		Joins("JOIN inbounds ON inbounds.id = plan_inbounds.inbound_id").
		Where("plan_inbounds.plan_id = ?", planId).
		Order("plan_inbounds.inbound_id ASC").
		Pluck("plan_inbounds.inbound_id", &ids).Error
	return ids, err
}

func planMemberEmails(planIds ...int) ([]string, error) {
	var emails []string
	err := database.GetDB().Model(&model.ClientRecord{}).
		Where("plan_id IN ?", planIds).Order("id ASC").Pluck("email", &emails).Error
	return emails, err
}

func uniqueSortedIds(ids []int) []int {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}

// idsMissingFrom returns the ids in want that are not in have.
func idsMissingFrom(want, have []int) []int {
	var out []int
	for _, id := range want {
		if !slices.Contains(have, id) {
			out = append(out, id)
		}
	}
	return out
}

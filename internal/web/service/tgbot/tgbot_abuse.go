package tgbot

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mymmrac/telego"

	"github.com/mhsanaei/3x-ui/v3/internal/abuse"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// banHistoryDays is how far back people see their bans.
const banHistoryDays = 90

// abuseDigestHour is when the admins get the day's hits that only recorded.
const abuseDigestHour = 9

// abuseAtOnce are the rules whose recorded hits reach the admins at once; the
// rest wait for the daily digest.
var abuseAtOnce = map[string]bool{abuse.RuleScan: true, abuse.RuleFlood: true}

func serverLabel(nodeID int) string {
	if nodeID == 0 {
		return "面板本机"
	}
	var node model.Node
	if database.GetDB().Select("name").First(&node, nodeID).Error == nil && node.Name != "" {
		return node.Name
	}
	return fmt.Sprintf("节点 %d", nodeID)
}

func eventSamples(e model.AbuseEvent) string {
	var samples []string
	if e.Samples == "" || json.Unmarshal([]byte(e.Samples), &samples) != nil || len(samples) == 0 {
		return ""
	}
	return strings.Join(samples, "、")
}

// notifyAbuse tells people about each fresh hit the policy acted on: bans and
// locks to the person and the admins, warnings to the person, the rest to admins.
func (t *Tgbot) notifyAbuse(ctx context.Context, now time.Time) {
	var events []model.AbuseEvent
	if err := database.GetDB().Where("at >= ?", now.Add(-10*time.Minute).Unix()).Order("id").Find(&events).Error; err != nil {
		return
	}
	admins := adminSnapshot()
	actions := (&service.AbuseService{}).Settings().Actions
	hour := now.Unix() / 3600
	for _, e := range events {
		if e.Rule == service.AbuseRuleAIPatrol {
			continue
		} // its durable notification queue handles delivery
		if e.Action == service.AbuseActionHeld {
			continue
		}
		if e.Rule == service.AbuseRuleSignup {
			t.notifySignups(ctx, e, admins, now)
			continue
		}
		recorded := e.Action == service.AbuseActionObserved || e.Action == service.AbuseActionNoticed
		if recorded && !abuseAtOnce[e.Rule] {
			continue
		}
		var client model.ClientRecord
		if database.GetDB().Where("email = ?", e.Email).First(&client).Error != nil {
			continue
		}
		label, evidence := service.AbuseRuleLabel(e.Rule), service.AbuseEvidence(e)
		toUser := client.TgID > 0 && !checkAdmin(client.TgID)
		adminText := ""
		var adminKeys []string
		adminRows := [][]telego.InlineKeyboardButton{{button("👤 查看 "+client.Email, fmt.Sprintf("pg:a:c:%d!", client.Id))}}
		switch e.Action {
		case service.AbuseActionBanned, service.AbuseActionLocked:
			status, _ := (&service.AbuseService{}).Status(e.Email, now)
			if toUser {
				t.deliverAccountNotification(ctx, fmt.Sprintf("abuse:%d", e.Id), client.TgID, t.banNotice(e, status, now), now)
			}
			if e.Action == service.AbuseActionLocked {
				adminText = fmt.Sprintf("🔒 <b>%s</b> 30 天内第 %d 次违规（%s），账号已停用，等你处理\n依据：%s", esc(e.Email), strikeNumber(status), label, evidence)
			} else {
				adminText = fmt.Sprintf("🚫 <b>%s</b> 因「%s」封禁 %d 分钟（30 天内第 %d 次）\n依据：%s", esc(e.Email), label, service.AbuseBanMinutes, strikeNumber(status), evidence)
			}
			adminRows = append([][]telego.InlineKeyboardButton{{button("🔓 解除封禁", fmt.Sprintf("pg:a:u:%d!", client.Id))}}, adminRows...)
			for _, admin := range admins {
				adminKeys = append(adminKeys, fmt.Sprintf("abuse:%d:%d", e.Id, admin))
			}
		case service.AbuseActionWarned:
			// A rule set to warn fires every few minutes while it lasts: once an hour is enough.
			if toUser {
				key := fmt.Sprintf("abuse-warn:%s:%s:%d", e.Email, e.Rule, hour)
				t.deliverAccountNotification(ctx, key, client.TgID, warningNotice(e, actions.For(e.Rule)), now)
			}
			adminText = fmt.Sprintf("⚠️ <b>%s</b> 触发「%s」，已提醒用户（不计违规）\n依据：%s", esc(e.Email), label, evidence)
			for _, admin := range admins {
				adminKeys = append(adminKeys, fmt.Sprintf("abuse-warn:%s:%s:%d:%d", e.Email, e.Rule, hour, admin))
			}
		default:
			prefix := "👁 观察模式：<b>%s</b> 触发「%s」（未封禁）"
			if e.Action == service.AbuseActionNoticed {
				prefix = "🔎 <b>%s</b> 疑似「%s」（仅提醒，不封禁）"
			}
			adminText = fmt.Sprintf(prefix+"\n依据：%s", esc(e.Email), label, evidence)
			// A recorded rule repeats while it lasts: one word an hour is enough.
			for _, admin := range admins {
				adminKeys = append(adminKeys, fmt.Sprintf("abuse-seen:%s:%s:%d:%d", e.Email, e.Rule, hour, admin))
			}
		}
		adminText += "\n服务器：" + esc(serverLabel(e.NodeId))
		if samples := eventSamples(e); samples != "" {
			adminText += "\n样本：" + esc(samples)
		}
		for i, admin := range admins {
			t.deliverAccountNotification(ctx, adminKeys[i], admin, botView{adminText, keyboard(adminRows...)}, now)
		}
	}
	t.abuseDigest(ctx, now)
}

// notifySignups tells the admins which accounts one network signed up in a day.
func (t *Tgbot) notifySignups(ctx context.Context, e model.AbuseEvent, admins []int64, now time.Time) {
	var samples []string
	if json.Unmarshal([]byte(e.Samples), &samples) != nil || len(samples) < 2 {
		return
	}
	text := fmt.Sprintf("🧾 <b>批量注册</b>：网络 %s 在 24 小时内注册了 %d 个账号：%s",
		esc(samples[0]), e.Count, esc(strings.Join(samples[1:], "、")))
	if e.Action == service.AbuseActionBlocked {
		text += "\n该网络 24 小时内不能再注册。"
	} else {
		text += "\n（仅提醒，没有阻止注册）"
	}
	for _, admin := range admins {
		t.deliverAccountNotification(ctx, fmt.Sprintf("abuse:%d:%d", e.Id, admin), admin, botView{text: text}, now)
	}
}

// warningNotice tells a person about a hit that costs no strike. The full-speed
// rule's first stage says when a ban follows, if the rule bans at all.
func warningNotice(e model.AbuseEvent, action string) botView {
	evidence := service.AbuseEvidence(e)
	if e.Rule == abuse.RuleFullSpeed && e.Level == abuse.LevelWarn && action == service.AbuseActBan {
		text := fmt.Sprintf("⚠️ <b>长时间满速提醒</b>\n你的账号已%s。如果是在下载大文件，可以忽略；继续满速到 %d 分钟将被暂时封禁 %d 分钟。",
			evidence, (&service.AbuseService{}).Rules().FullSpeedStrikeMin, service.AbuseBanMinutes)
		return botView{text: text}
	}
	text := fmt.Sprintf("⚠️ <b>使用提醒</b>\n检测到「%s」：%s。\n这次只是提醒，不计违规；如果不清楚原因，请检查设备上的软件，或联系管理员。",
		service.AbuseRuleLabel(e.Rule), evidence)
	return botView{text, keyboard([]telego.InlineKeyboardButton{button("❓ 使用规则", "pg:help!")})}
}

// abuseDigest sends the admins, once a day at 09:00, the hits of the 24 hours
// before that only recorded: what a week of observing is for.
func (t *Tgbot) abuseDigest(ctx context.Context, now time.Time) {
	local := now.In(accountLocation)
	end := time.Date(local.Year(), local.Month(), local.Day(), abuseDigestHour, 0, 0, 0, accountLocation)
	if local.Before(end) {
		return
	}
	var events []model.AbuseEvent
	err := database.GetDB().Where("at >= ? AND at < ? AND action IN ? AND rule <> ?", end.Add(-24*time.Hour).Unix(), end.Unix(),
		[]string{service.AbuseActionObserved, service.AbuseActionNoticed}, service.AbuseRuleSignup).Order("rule, email").Find(&events).Error
	if err != nil || len(events) == 0 {
		return
	}
	text := digestText(events, end)
	for _, admin := range adminSnapshot() {
		key := fmt.Sprintf("abuse-digest:%s:%d", end.Format("2006-01-02"), admin)
		t.deliverAccountNotification(ctx, key, admin, botView{text: text}, now)
	}
}

// digestText counts the hits per rule and account, most frequent first.
func digestText(events []model.AbuseEvent, end time.Time) string {
	type tally struct {
		email string
		n     int
	}
	var labels []string
	byLabel := map[string][]tally{}
	for _, e := range events {
		label := service.AbuseEventLabel(e)
		list := byLabel[label]
		if len(list) == 0 {
			labels = append(labels, label)
		}
		if i := slices.IndexFunc(list, func(c tally) bool { return c.email == e.Email }); i >= 0 {
			list[i].n++
		} else {
			list = append(list, tally{e.Email, 1})
		}
		byLabel[label] = list
	}
	var b strings.Builder
	fmt.Fprintf(&b, "📋 <b>防滥用日报</b>\n%s 至 %s，只记录、未处罚的检测：\n",
		end.Add(-24*time.Hour).Format("01-02 15:04"), end.Format("01-02 15:04"))
	for _, label := range labels {
		list := byLabel[label]
		sort.SliceStable(list, func(i, j int) bool { return list[i].n > list[j].n })
		var parts []string
		for i, c := range list {
			if i == 10 {
				parts = append(parts, fmt.Sprintf("等 %d 人", len(list)))
				break
			}
			parts = append(parts, fmt.Sprintf("%s %d 次", esc(c.email), c.n))
		}
		fmt.Fprintf(&b, "\n• %s：%s", label, strings.Join(parts, "、"))
	}
	fmt.Fprintf(&b, "\n\n共 %d 条，详情见面板「防滥用」页面。", len(events))
	return b.String()
}

// strikeNumber is the strike a ban counted as, the lock included.
func strikeNumber(status service.AbuseStatus) int {
	if status.Ban != nil && status.Ban.Strike > 0 {
		return status.Ban.Strike
	}
	return status.Strikes
}

func (t *Tgbot) banNotice(e model.AbuseEvent, status service.AbuseStatus, now time.Time) botView {
	reason := service.AbuseRuleLabel(e.Rule) + "（" + service.AbuseEvidence(e) + "）"
	var text string
	if e.Action == service.AbuseActionLocked {
		text = fmt.Sprintf("🔒 <b>账号已停用</b>\n原因：%s\n30 天内已违规 %d 次，账号已停用，请联系管理员说明情况。", reason, strikeNumber(status))
	} else {
		until := ""
		if status.Ban != nil && status.Ban.ExpiresAt > 0 {
			until = "，" + banLeft(status.Ban.ExpiresAt, now)
		}
		text = fmt.Sprintf("⛔ <b>账号暂时封禁</b>\n原因：%s\n这是 30 天内第 %d 次违规，封禁 %d 分钟%s。30 天内超过 %d 次，账号将被停用。\n如果不清楚原因，请检查设备上的软件，或联系管理员。",
			reason, strikeNumber(status), service.AbuseBanMinutes, until, service.AbuseStrikesLimit)
	}
	return botView{text, keyboard([]telego.InlineKeyboardButton{button("📜 封禁记录", "pg:bans!"), button("❓ 使用规则", "pg:help!")})}
}

// abuseStatusLine is one line on an account's standing, empty when clean.
func abuseStatusLine(email string, now time.Time) string {
	status, err := (&service.AbuseService{}).Status(email, now)
	if err != nil {
		return ""
	}
	var parts []string
	switch {
	case status.Ban != nil && status.Ban.ExpiresAt == 0:
		parts = append(parts, "🔒 账号已停用（多次违规），请联系管理员")
	case status.Ban != nil:
		parts = append(parts, "⛔ 封禁中："+esc(status.Ban.Reason)+"，"+banLeft(status.Ban.ExpiresAt, now))
	}
	if status.Strikes > 0 {
		parts = append(parts, fmt.Sprintf("违规 %d/%d（30 天内）", status.Strikes, status.Limit))
	}
	return strings.Join(parts, "\n")
}

// banHistoryView lists an account's bans of the last 90 days, newest first.
func (t *Tgbot) banHistoryView(email string, now time.Time, back string) botView {
	recs, err := (&service.AbuseService{}).History(email, now.AddDate(0, 0, -banHistoryDays))
	backRow := []telego.InlineKeyboardButton{button("⬅️ 返回", back)}
	if err != nil {
		return botView{"暂时无法读取封禁记录，请稍后重试。", keyboard(backRow)}
	}
	if len(recs) == 0 {
		return botView{"📜 <b>封禁记录</b>\n最近 90 天没有封禁。", keyboard(backRow)}
	}
	var b strings.Builder
	b.WriteString("📜 <b>封禁记录</b>（最近 90 天）\n")
	for i, r := range recs {
		if i == 15 {
			fmt.Fprintf(&b, "……还有 %d 条，完整记录在用户页面", len(recs)-i)
			break
		}
		fmt.Fprintf(&b, "\n• %s %s\n  %s\n", time.Unix(r.BannedAt, 0).In(accountLocation).Format("01-02 15:04"), banOutcome(r, now), esc(r.Reason))
	}
	return botView{b.String(), keyboard(backRow)}
}

func banOutcome(r model.BanRecord, now time.Time) string {
	switch {
	case r.LiftedAt > 0:
		return "· 已由管理员解除"
	case r.ExpiresAt == 0:
		return "· 🔒 停用中"
	case r.ExpiresAt > now.Unix():
		return "· ⛔ " + banLeft(r.ExpiresAt, now)
	}
	return fmt.Sprintf("· %d 分钟", (r.ExpiresAt-r.BannedAt)/60)
}

func (t *Tgbot) liftAbuseNow(id int, now time.Time) (string, botView) {
	rec, ok := loadClient(id)
	if !ok {
		return "", goneView()
	}
	if err := (&service.AbuseService{}).Lift(rec.Email, now); err != nil {
		return "现在没有封禁", t.userCardView(id, now, "")
	}
	t.xrayService.SetToNeedRestart()
	return "已解除封禁", t.userCardView(id, now, "✅ 已解除封禁")
}

func (t *Tgbot) forgiveAbuseNow(id int, now time.Time) (string, botView) {
	rec, ok := loadClient(id)
	if !ok {
		return "", goneView()
	}
	if err := (&service.AbuseService{}).Forgive(rec.Email); err != nil {
		return "操作失败", t.userCardView(id, now, "")
	}
	return "已清除违规记录", t.userCardView(id, now, "✅ 已清除 30 天内的违规次数")
}

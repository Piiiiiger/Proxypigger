package tgbot

import (
	"context"
	"fmt"
	"html"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"gorm.io/gorm/clause"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// dailyTimeState marks a person whose next message is a daily report time.
const dailyTimeState = "pg_daily_time"

var dailyTimePresets = []string{"08:00", "12:00", "18:00", "20:00", "21:00", "22:00"}

// The account commands are answered here, before the upstream router; the admin
// ones fall through to it, and its unknown-command reply, for anyone else.
var (
	accountCommands      = map[string]bool{"start": true, "menu": true, "help": true, "bind": true, "report": true, "ips": true, "daily": true}
	adminAccountCommands = map[string]bool{"users": true, "user": true}
)

func esc(s string) string { return html.EscapeString(s) }

// handleAccountMessage answers the account side of the bot: binding, the menus
// and the daily report settings, all in a private chat.
func (t *Tgbot) handleAccountMessage(m *telego.Message) bool {
	if m.From == nil {
		return false
	}
	fields := strings.Fields(m.Text)
	if len(fields) == 0 {
		return false
	}
	isCommand := strings.HasPrefix(fields[0], "/")
	cmd, args := "", fields[1:]
	if isCommand {
		cmd = strings.ToLower(strings.TrimPrefix(strings.Split(fields[0], "@")[0], "/"))
	}
	admin := checkAdmin(m.From.ID)
	ours := isCommand && (accountCommands[cmd] || (admin && adminAccountCommands[cmd]))
	isCode := !isCommand && service.LooksLikeActivationCode(m.Text)

	if m.Chat.Type != telego.ChatTypePrivate || m.Chat.ID != m.From.ID {
		if (ours && cmd != "start") || isCode {
			t.SendMsgToTgbot(m.Chat.ID, "请在与机器人的私聊中使用。")
		}
		return ours || isCode
	}
	if state, pending := userStateMgr.get(messageActor(*m)); pending {
		if state != dailyTimeState && !ours {
			return false
		}
		if state == dailyTimeState && !isCommand {
			t.saveTypedDailyTime(m, strings.TrimSpace(m.Text))
			return true
		}
	}
	if isCommand && !ours {
		return false
	}

	chatID, now := m.Chat.ID, time.Now()
	switch {
	case isCode:
		t.bindAccount(m, m.Text)
	case cmd == "start" && len(args) > 0 && service.LooksLikeActivationCode(args[0]):
		t.bindAccount(m, args[0])
	case !isCommand, cmd == "start", cmd == "menu":
		t.showHome(chatID, 0, m.From.ID, now)
	case cmd == "bind" && len(args) == 0:
		t.SendMsgToTgbot(chatID, "请发送 <code>/bind 你的激活码</code>，激活码在用户页面的「我的激活码」里。")
	case cmd == "bind":
		t.bindAccount(m, strings.Join(args, " "))
	case cmd == "help":
		t.present(chatID, 0, t.helpView(m.From.ID))
	case cmd == "users":
		t.present(chatID, 0, t.userListView(0, now))
	case cmd == "user":
		t.present(chatID, 0, t.userSearchView(strings.Join(args, " "), now))
	default:
		client, bound := t.boundClient(m.From.ID)
		if !bound {
			t.present(chatID, 0, t.welcomeView("请先绑定账号。"))
			return true
		}
		switch cmd {
		case "report":
			t.present(chatID, 0, t.usageView(client, now))
		case "ips":
			t.present(chatID, 0, t.ipsView(client, now))
		case "daily":
			t.dailyCommand(chatID, client, args)
		}
	}
	return true
}

// handleAccountCallback answers the buttons of the account and admin menus. A
// tap is acted on only for the person who pressed it, never for an id it names.
func (t *Tgbot) handleAccountCallback(q *telego.CallbackQuery) bool {
	rest, ok := strings.CutPrefix(q.Data, "pg:")
	if !ok || q.Message == nil {
		return false
	}
	chat := q.Message.GetChat()
	if chat.Type != telego.ChatTypePrivate || chat.ID != q.From.ID {
		t.sendCallbackAnswerTgBot(q.ID, "请在与机器人的私聊中使用。")
		return true
	}
	// A notification's buttons end in "!": their view comes as a new message,
	// so the report or alert they sit under stays in the chat.
	messageID := q.Message.GetMessageID()
	if trimmed, fresh := strings.CutSuffix(rest, "!"); fresh {
		rest, messageID = trimmed, 0
	}
	if adminRest, isAdmin := strings.CutPrefix(rest, "a"); isAdmin && (adminRest == "" || adminRest[0] == ':') {
		if !checkAdmin(q.From.ID) {
			t.sendCallbackAnswerTgBot(q.ID, "")
			return true
		}
		t.answerAdminCallback(q, messageID, strings.TrimPrefix(adminRest, ":"))
		return true
	}
	t.answerUserCallback(q, messageID, rest)
	return true
}

func (t *Tgbot) answerUserCallback(q *telego.CallbackQuery, messageID int, rest string) {
	chatID, now := q.Message.GetChat().ID, time.Now()
	action, arg, _ := strings.Cut(rest, ":")
	if action == "help" {
		t.sendCallbackAnswerTgBot(q.ID, "")
		t.present(chatID, messageID, t.helpView(q.From.ID))
		return
	}
	client, bound := t.boundClient(q.From.ID)
	if action == "home" && !bound && checkAdmin(q.From.ID) {
		t.sendCallbackAnswerTgBot(q.ID, "")
		t.present(chatID, messageID, t.adminHomeView(q.From.ID, now))
		return
	}
	if !bound {
		t.sendCallbackAnswerTgBot(q.ID, "请先绑定账号")
		t.present(chatID, messageID, t.welcomeView(""))
		return
	}
	toast := ""
	var view botView
	switch action {
	case "use":
		view = t.usageView(client, now)
	case "ips":
		view = t.ipsView(client, now)
	case "daily":
		toast, view = t.dailyButton(q, client, arg)
	case "bans":
		view = t.banHistoryView(client.Email, now, "pg:home")
	default:
		view = t.homeView(client, now, checkAdmin(q.From.ID))
	}
	t.sendCallbackAnswerTgBot(q.ID, toast)
	t.present(chatID, messageID, view)
}

func (t *Tgbot) showHome(chatID int64, messageID int, tgID int64, now time.Time) {
	if checkAdmin(tgID) {
		t.present(chatID, messageID, t.adminHomeView(tgID, now))
		return
	}
	if client, bound := t.boundClient(tgID); bound {
		t.present(chatID, messageID, t.homeView(client, now, false))
		return
	}
	t.present(chatID, messageID, t.welcomeView(""))
}

func (t *Tgbot) boundClient(tgID int64) (*model.ClientRecord, bool) {
	if tgID <= 0 {
		return nil, false
	}
	var client model.ClientRecord
	if err := database.GetDB().Where("tg_id = ?", tgID).Order("id").First(&client).Error; err != nil {
		return nil, false
	}
	return &client, true
}

func (t *Tgbot) bindAccount(m *telego.Message, code string) {
	if !t.allowInviteAttempt(m.From) {
		t.SendMsgToTgbot(m.Chat.ID, "尝试次数过多，请一小时后再试。")
		return
	}
	client, needRestart, err := service.BindAccountActivation(code, m.From.ID)
	if needRestart {
		t.xrayService.SetToNeedRestart()
	}
	if err != nil {
		t.present(m.Chat.ID, 0, t.welcomeView("❌ 无法绑定：激活码不正确，或者这个账号、这个 Telegram 已经绑定了别的用户。需要更换绑定请联系管理员。"))
		return
	}
	intro := "✅ 绑定成功！账号 <b>" + esc(client.Email) + "</b> 已与这个 Telegram 绑定。\n"
	if prefs, err := service.EnsureAccountActivation(client); err == nil && prefs.DailyEnabled {
		intro += fmt.Sprintf("每天 %s（北京时间）会收到用量日报，可在「日报设置」修改。\n", prefs.DailyTime)
	}
	view := t.homeView(client, time.Now(), checkAdmin(m.From.ID))
	view.text = intro + "\n" + view.text
	t.present(m.Chat.ID, 0, view)
}

// portalURL is the user page under the subscription address; empty when the
// panel only knows a host name a phone could not reach.
func (t *Tgbot) portalURL() string {
	if subURI, _ := t.settingService.GetSubURI(); subURI != "" {
		return strings.TrimSuffix(subURI, "/") + "/portal"
	}
	origin, public := t.subOrigin()
	if !public {
		return ""
	}
	subPath, _ := t.settingService.GetSubPath()
	return origin + slashed(subPath) + "portal"
}

// accountUsage is what the account views read for one client.
type accountUsage struct {
	up, down, today int64
	nextReset       int64
	planName        string
	ips             service.ClientOnlineIps
}

func (t *Tgbot) accountUsage(client *model.ClientRecord, now time.Time) (accountUsage, error) {
	var u accountUsage
	traffic, err := t.inboundService.GetClientTrafficByEmail(client.Email)
	if err != nil {
		return u, err
	}
	if traffic != nil {
		u.up, u.down = traffic.Up, traffic.Down
	}
	days, err := (&service.TrafficStatsService{}).ClientDaily(client.Email, now.In(accountLocation), 1)
	if err != nil {
		return u, err
	}
	for _, day := range days {
		u.today += day.Up + day.Down
	}
	if next, err := t.clientService.NextReset(*client, now); err == nil {
		u.nextReset = next
	}
	if client.PlanId > 0 {
		var plan model.Plan
		if database.GetDB().First(&plan, client.PlanId).Error == nil {
			u.planName = plan.Name
		}
	}
	u.ips, err = (&service.IpLimitService{}).OnlineIps(client.Email, now)
	return u, err
}

func trafficSummary(client *model.ClientRecord, used int64) string {
	if client.TotalGB <= 0 {
		return "📊 本期已用 " + formatBytes(used) + "（不限量）\n"
	}
	return fmt.Sprintf("📊 剩余 %s / %s\n%s\n", formatBytes(max(0, client.TotalGB-used)), formatBytes(client.TotalGB), usageBar(used, client.TotalGB))
}

func (t *Tgbot) homeView(client *model.ClientRecord, now time.Time, admin bool) botView {
	u, err := t.accountUsage(client, now)
	used := u.up + u.down
	emoji, label := clientState(client, used, now)
	var b strings.Builder
	fmt.Fprintf(&b, "👤 <b>%s</b> · %s %s\n", esc(client.Email), emoji, label)
	if err != nil {
		b.WriteString("暂时无法读取用量，请稍后重试。\n")
	} else {
		b.WriteString(trafficSummary(client, used))
	}
	b.WriteString("📅 到期：" + expiryText(client.ExpiryTime, now) + "\n")
	b.WriteString("🌐 在线 IP：" + ipSlots(u.ips.Count, u.ips.Limit))
	if len(u.ips.Bans) > 0 {
		fmt.Fprintf(&b, " · ⛔ %d 个暂停中", len(u.ips.Bans))
	}
	if line := abuseStatusLine(client.Email, now); line != "" {
		b.WriteString("\n" + line)
	}
	rows := [][]telego.InlineKeyboardButton{
		{button("📊 用量详情", "pg:use"), button("🌐 在线设备", "pg:ips")},
		{button("🔔 日报设置", "pg:daily"), button("📜 封禁记录", "pg:bans")},
		{button("❓ 帮助", "pg:help")},
	}
	if url := t.portalURL(); url != "" {
		rows = append(rows, []telego.InlineKeyboardButton{tu.InlineKeyboardButton("🌍 打开用户页面").WithURL(url)})
	}
	if admin {
		rows = append(rows, []telego.InlineKeyboardButton{button("🛠 管理菜单", "pg:a")})
	}
	return botView{b.String(), keyboard(rows...)}
}

// usageText is the detailed usage report, shared by its view and the daily report.
func (t *Tgbot) usageText(client *model.ClientRecord, now time.Time, title string) (string, error) {
	u, err := t.accountUsage(client, now)
	if err != nil {
		return "", err
	}
	used := u.up + u.down
	var b strings.Builder
	fmt.Fprintf(&b, "📊 <b>%s</b> · %s\n", esc(client.Email), title)
	if u.planName != "" {
		b.WriteString("套餐：" + esc(u.planName) + "\n")
	}
	if client.TotalGB > 0 {
		fmt.Fprintf(&b, "剩余流量：%s / %s\n%s\n", formatBytes(max(0, client.TotalGB-used)), formatBytes(client.TotalGB), usageBar(used, client.TotalGB))
	} else {
		b.WriteString("流量：不限量\n")
	}
	fmt.Fprintf(&b, "本期已用：%s（↑ %s · ↓ %s）\n", formatBytes(used), formatBytes(u.up), formatBytes(u.down))
	b.WriteString("今日已用：" + formatBytes(u.today) + "\n")
	if u.nextReset > 0 {
		b.WriteString("流量清零：" + formatClock(u.nextReset) + "\n")
	}
	b.WriteString("到期：" + expiryText(client.ExpiryTime, now) + "\n")
	b.WriteString("在线 IP：" + ipSlots(u.ips.Count, u.ips.Limit) + "\n")
	fmt.Fprintf(&b, "<i>更新于 %s（北京时间）</i>", now.In(accountLocation).Format("01-02 15:04"))
	return b.String(), nil
}

func (t *Tgbot) usageView(client *model.ClientRecord, now time.Time) botView {
	text, err := t.usageText(client, now, "用量详情")
	if err != nil {
		text = "暂时无法读取用量，请稍后重试。"
	}
	return botView{text, keyboard([]telego.InlineKeyboardButton{button("🔄 刷新", "pg:use"), button("⬅️ 返回", "pg:home")})}
}

func (t *Tgbot) ipRule(limit int) string {
	if limit <= 0 {
		return "ℹ️ 你的账号不限制同时在线的 IP 数。"
	}
	minutes, err := t.settingService.GetIpLimitBanMinutes()
	if err != nil || minutes <= 0 {
		minutes = 30
	}
	return fmt.Sprintf("ℹ️ 同一时间最多 %d 个 IP 在线（同一网段的 IP 算一个：IPv4 按 /24、IPv6 按 /64）。超出时，最久未活动的 IP 会暂停使用 %d 分钟，到时自动恢复。", limit, minutes)
}

// onlineLines lists the networks online now, relays never among them: the
// limit's scan drops them before anything is counted or shown.
func onlineLines(ips service.ClientOnlineIps, now time.Time) string {
	var b strings.Builder
	if len(ips.Online) == 0 {
		b.WriteString("现在没有设备在线。\n")
	}
	for _, n := range ips.Online {
		b.WriteString("• " + esc(strings.Join(n.Addresses, ", ")))
		if len(n.Servers) > 0 {
			b.WriteString(" · " + esc(strings.Join(n.Servers, "、")))
		}
		b.WriteString(" · " + agoText(n.LastSeen, now))
		if !n.Counted {
			b.WriteString(" · 不计入")
		}
		b.WriteString("\n")
	}
	for i, ban := range ips.Bans {
		if i == 0 {
			b.WriteString("⛔ <b>暂停使用</b>（超出同时在线上限）\n")
		}
		b.WriteString("• " + esc(ban.Network) + " · " + banLeft(ban.ExpiresAt, now) + "\n")
	}
	return b.String()
}

func (t *Tgbot) ipsView(client *model.ClientRecord, now time.Time) botView {
	ips, err := (&service.IpLimitService{}).OnlineIps(client.Email, now)
	var b strings.Builder
	fmt.Fprintf(&b, "🌐 <b>在线设备</b> · %s\n", ipSlots(ips.Count, ips.Limit))
	if err != nil {
		b.WriteString("暂时无法读取，请稍后重试。\n")
	} else {
		b.WriteString(onlineLines(ips, now))
	}
	b.WriteString("\n" + t.ipRule(ips.Limit))
	return botView{b.String(), keyboard([]telego.InlineKeyboardButton{button("🔄 刷新", "pg:ips"), button("⬅️ 返回", "pg:home")})}
}

func (t *Tgbot) dailyView(client *model.ClientRecord, note string) botView {
	back := keyboard([]telego.InlineKeyboardButton{button("⬅️ 返回", "pg:home")})
	prefs, err := service.EnsureAccountActivation(client)
	if err != nil {
		return botView{"暂时无法读取日报设置，请稍后重试。", back}
	}
	state, toggle := "🔕 已关闭", button("🔔 开启日报", "pg:daily:on")
	if prefs.DailyEnabled {
		state, toggle = "✅ 已开启", button("🔕 关闭日报", "pg:daily:off")
	}
	text := fmt.Sprintf("🔔 <b>日报设置</b>\n日报：%s · 每天 %s（北京时间）\n日报会发送剩余流量、今日用量和到期时间。\n到期前 7、3、1 天的续费提醒不受日报开关影响，也在这个时间发送。", state, prefs.DailyTime)
	if note != "" {
		text = note + "\n\n" + text
	}
	presets := make([]telego.InlineKeyboardButton, 0, len(dailyTimePresets))
	for _, hm := range dailyTimePresets {
		label := hm
		if hm == prefs.DailyTime {
			label = "✓ " + hm
		}
		presets = append(presets, button(label, "pg:daily:t:"+strings.ReplaceAll(hm, ":", "")))
	}
	return botView{text, keyboard(
		[]telego.InlineKeyboardButton{toggle},
		presets[:3], presets[3:],
		[]telego.InlineKeyboardButton{button("⏰ 其他时间", "pg:daily:ask"), button("⬅️ 返回", "pg:home")},
	)}
}

func saveDailyPrefs(client *model.ClientRecord, updates map[string]any) error {
	if _, err := service.EnsureAccountActivation(client); err != nil {
		return err
	}
	return database.GetDB().Model(&model.AccountActivation{}).Where("client_id = ?", client.Id).Updates(updates).Error
}

// dailyButton applies a daily-settings button and returns its toast and the
// settings view as they now stand.
func (t *Tgbot) dailyButton(q *telego.CallbackQuery, client *model.ClientRecord, arg string) (string, botView) {
	switch {
	case arg == "on" || arg == "off":
		if err := saveDailyPrefs(client, map[string]any{"daily_enabled": arg == "on"}); err != nil {
			return "保存失败，请稍后重试", t.dailyView(client, "")
		}
		if arg == "on" {
			return "已开启日报", t.dailyView(client, "")
		}
		return "已关闭日报", t.dailyView(client, "")
	case strings.HasPrefix(arg, "t:") && len(arg) == 6:
		hm := arg[2:4] + ":" + arg[4:]
		if !validDailyTime(hm) {
			return "时间无效", t.dailyView(client, "")
		}
		if err := saveDailyPrefs(client, map[string]any{"daily_time": hm, "daily_enabled": true}); err != nil {
			return "保存失败，请稍后重试", t.dailyView(client, "")
		}
		return "日报时间已改为 " + hm, t.dailyView(client, "")
	case arg == "ask":
		userStateMgr.set(callbackActor(q), dailyTimeState)
		return "", botView{"⏰ 请发送日报时间（北京时间），例如 <code>21:30</code>。", keyboard([]telego.InlineKeyboardButton{button("取消", "pg:daily")})}
	}
	return "", t.dailyView(client, "")
}

// normalizeDailyTime reads a time as people type it, "9:30" or "21：30" included.
func normalizeDailyTime(text string) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "：", ":")
	if len(text) == 4 && text[1] == ':' {
		text = "0" + text
	}
	return text
}

func (t *Tgbot) saveTypedDailyTime(m *telego.Message, text string) {
	client, bound := t.boundClient(m.From.ID)
	if !bound {
		userStateMgr.clear(messageActor(*m))
		t.present(m.Chat.ID, 0, t.welcomeView("请先绑定账号。"))
		return
	}
	hm := normalizeDailyTime(text)
	if !validDailyTime(hm) {
		t.present(m.Chat.ID, 0, botView{"时间格式不对，请发送如 <code>21:30</code> 的时间，或点「取消」。", keyboard([]telego.InlineKeyboardButton{button("取消", "pg:daily")})})
		return
	}
	userStateMgr.clear(messageActor(*m))
	if err := saveDailyPrefs(client, map[string]any{"daily_time": hm, "daily_enabled": true}); err != nil {
		t.SendMsgToTgbot(m.Chat.ID, "保存失败，请稍后重试。")
		return
	}
	t.present(m.Chat.ID, 0, t.dailyView(client, "✅ 日报时间已改为 "+hm))
}

// dailyCommand keeps the typed form: /daily, /daily on|off, /daily HH:MM.
func (t *Tgbot) dailyCommand(chatID int64, client *model.ClientRecord, args []string) {
	note := ""
	if len(args) > 0 {
		var updates map[string]any
		switch hm := normalizeDailyTime(args[0]); {
		case args[0] == "on" || args[0] == "off":
			updates = map[string]any{"daily_enabled": args[0] == "on"}
		case len(args) == 1 && validDailyTime(hm):
			updates = map[string]any{"daily_time": hm, "daily_enabled": true}
		default:
			t.SendMsgToTgbot(chatID, "时间格式为 HH:MM，例如 <code>/daily 21:30</code>（北京时间）。")
			return
		}
		if err := saveDailyPrefs(client, updates); err != nil {
			t.SendMsgToTgbot(chatID, "保存失败，请稍后重试。")
			return
		}
		note = "✅ 已保存"
	}
	t.present(chatID, 0, t.dailyView(client, note))
}

func (t *Tgbot) helpView(tgID int64) botView {
	var b strings.Builder
	b.WriteString("❓ <b>使用帮助</b>\n")
	b.WriteString("/start 主菜单 · /report 用量 · /ips 在线设备 · /daily 日报设置\n\n")
	b.WriteString("• <b>日报</b>：每天在你设定的时间（北京时间）发送剩余流量和今日用量，可在「日报设置」开关或改时间。\n")
	b.WriteString("• <b>到期提醒</b>：到期前 7、3、1 天自动提醒，续费后按新日期计算。\n")
	b.WriteString("• <b>在线设备</b>：同一时间在线的 IP 数有上限；超出时，最久未活动的 IP 会暂停一段时间，到时自动恢复。\n")
	b.WriteString("• <b>续费</b>：请联系管理员。\n")
	b.WriteString("• <b>更换 Telegram 账号</b>：请联系管理员解除绑定后重新绑定。\n\n")
	b.WriteString("📍 <b>手机请务必关闭定位服务，防止节点「送中」</b>：开着定位时，Google 会读到手机的真实位置，把你正在用的节点 IP 判定为中国大陆；节点被送中后，所有人用它访问 Google、YouTube、Gemini 等都会受影响。\n\n")
	b.WriteString("🤖 <b>AI 命令行工具</b>：Droid、Claude Code、Codex 等不走系统代理，请在 Clash 里打开 TUN 模式；或者在终端设置 HTTPS_PROXY=http://127.0.0.1:端口（端口见 Clash 设置，Clash Verge 默认 7897）。\n\n")
	fmt.Fprintf(&b, "🚫 <b>使用规则</b>：禁止批量注册、爬虫、长时间满速占用、无故反复测速、端口扫描、网络攻击、发送垃圾邮件、BT 下载。"+
		"正常下载软件、系统更新不受影响。违规一次封禁 %d 分钟，到时自动恢复；30 天内超过 %d 次，账号停用，需联系管理员。",
		service.AbuseBanMinutes, service.AbuseStrikesLimit)
	back := "pg:home"
	if checkAdmin(tgID) {
		back = "pg:a"
		b.WriteString("\n\n🛠 <b>管理员</b>\n/start 管理菜单 · /users 用户列表 · /user 名字 查找用户\n用户卡片里可以续期、清零流量、停用或启用、解封 IP；「更多工具」是原有的全部管理功能。")
	}
	return botView{b.String(), keyboard([]telego.InlineKeyboardButton{button("⬅️ 返回", back)})}
}

func (t *Tgbot) welcomeView(note string) botView {
	text := "👋 你好！绑定账号后，可以随时查看剩余流量和在线设备，并收到每日用量和到期提醒。\n\n<b>绑定方法</b>：打开用户页面，在「我的激活码」里点「一键绑定 Telegram」；或者直接把激活码发给我。"
	if note != "" {
		text = note + "\n\n" + text
	}
	var rows [][]telego.InlineKeyboardButton
	if url := t.portalURL(); url != "" {
		rows = append(rows, []telego.InlineKeyboardButton{tu.InlineKeyboardButton("🌍 打开用户页面").WithURL(url)})
	}
	return botView{text, keyboard(rows...)}
}

func validDailyTime(value string) bool {
	parsed, err := time.Parse("15:04", value)
	return err == nil && parsed.Format("15:04") == value
}

// Use calendar days in the user's stated time zone. An expired timestamp must
// never produce a pre-expiry message, including on the same date.
func accountReminderDay(now time.Time, expiry int64) int {
	if expiry <= now.UnixMilli() {
		return 0
	}
	days := calendarDaysUntil(expiry, now)
	if days == 7 || days == 3 || days == 1 {
		return days
	}
	return 0
}

func (t *Tgbot) runAccountNotifications(ctx context.Context) {
	defer botWG.Done()
	defer recoverBotPanic()
	// Existing accounts acquire permanent credentials before the first bind.
	var clients []model.ClientRecord
	if err := database.GetDB().Find(&clients).Error; err == nil {
		for i := range clients {
			if _, err := service.EnsureAccountActivation(&clients[i]); err != nil {
				logger.Warning("account credential initialization failed:", err)
			}
		}
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			t.sendAccountNotifications(ctx, now)
		}
	}
}

func notificationSent(key string) bool {
	var count int64
	err := database.GetDB().Model(&model.AccountNotification{}).Where("key = ?", key).Count(&count).Error
	return err != nil || count > 0
}

// notificationSentAt says when a notification went out, if it did.
func notificationSentAt(key string) (time.Time, bool) {
	var row model.AccountNotification
	if err := database.GetDB().Where("key = ?", key).First(&row).Error; err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(row.SentAt), true
}

// Single scheduler goroutine; mark only successful delivery, allowing retries
// after transient failures. Durable keys survive restarts and include new expiry.
func (t *Tgbot) deliverAccountNotification(ctx context.Context, key string, chatID int64, view botView, now time.Time) {
	if notificationSent(key) {
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	params := &telego.SendMessageParams{ChatID: tu.ID(chatID), Text: view.text, ParseMode: telego.ModeHTML}
	if view.keyboard != nil {
		params.ReplyMarkup = view.keyboard
	}
	if _, err := bot.SendMessage(sendCtx, params); err != nil {
		logger.Warning("account notification delivery failed")
		return
	}
	row := model.AccountNotification{Key: key, SentAt: now.UnixMilli()}
	if err := database.GetDB().Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		logger.Warning("account notification receipt failed:", err)
	}
}

// accountPacing keeps the scheduler from repeating work a minute cannot change:
// server expiries are read hourly, the receipts pruned daily.
var accountPacing struct {
	sync.Mutex
	serversAt time.Time
	servers   []service.ProbeServer
	prunedOn  string
}

// probeSnapshot reads the probe's servers; a variable so a test can stand in for Lite.
var probeSnapshot = func(ctx context.Context) (service.ProbeSnapshot, bool, error) {
	return (&service.ProbeService{}).Snapshot(ctx)
}

func (t *Tgbot) probeServers(ctx context.Context, now time.Time) []service.ProbeServer {
	accountPacing.Lock()
	defer accountPacing.Unlock()
	if !accountPacing.serversAt.IsZero() && now.Sub(accountPacing.serversAt) < time.Hour && now.After(accountPacing.serversAt) {
		return accountPacing.servers
	}
	accountPacing.serversAt = now
	snap, configured, err := probeSnapshot(ctx)
	if err != nil || !configured || snap.Error != "" {
		return accountPacing.servers
	}
	accountPacing.servers = snap.Servers
	return snap.Servers
}

func (t *Tgbot) sendAccountNotifications(ctx context.Context, now time.Time) {
	db := database.GetDB()
	local := now.In(accountLocation)
	clock := local.Format("15:04")
	var clients []model.ClientRecord
	if err := db.Where("tg_id > 0").Find(&clients).Error; err != nil {
		return
	}
	prefsByClient := map[int]model.AccountActivation{}
	var rows []model.AccountActivation
	if err := db.Where("client_id IN (SELECT id FROM clients WHERE tg_id > 0)").Find(&rows).Error; err == nil {
		for _, row := range rows {
			prefsByClient[row.ClientId] = row
		}
	}
	for i := range clients {
		if ctx.Err() != nil {
			return
		}
		client := &clients[i]
		prefs, ok := prefsByClient[client.Id]
		if !ok {
			created, err := service.EnsureAccountActivation(client)
			if err != nil {
				continue
			}
			prefs = *created
		}
		if clock < prefs.DailyTime {
			continue
		}
		// Catch up today's report after a restart, without replaying previous dates.
		if key := fmt.Sprintf("daily:%d:%d:%s", client.Id, client.TgID, local.Format("2006-01-02")); prefs.DailyEnabled && !notificationSent(key) {
			if text, err := t.usageText(client, now, "每日用量"); err == nil {
				t.deliverAccountNotification(ctx, key, client.TgID, botView{text, keyboard(
					[]telego.InlineKeyboardButton{button("🌐 在线设备", "pg:ips!"), button("🔔 日报设置", "pg:daily!")},
				)}, now)
			}
		}
		if day := accountReminderDay(now, client.ExpiryTime); day > 0 {
			key := fmt.Sprintf("account:%d:%d:%d:%d", client.Id, client.TgID, client.ExpiryTime, day)
			// Read expiry again immediately before delivery: a renewal suppresses old work.
			var current model.ClientRecord
			if !notificationSent(key) && db.First(&current, client.Id).Error == nil && current.ExpiryTime == client.ExpiryTime && current.TgID == client.TgID {
				t.deliverAccountNotification(ctx, key, client.TgID, t.renewalReminder(client, day), now)
			}
		}
	}
	t.notifyIpBans(ctx, now)
	t.notifyAbuse(ctx, now)
	t.notifyAIPatrol(ctx, now)
	t.notifyRuleSets(ctx, now)
	t.notifyDueUsers(ctx, now)
	t.notifyServerRenewals(ctx, now, clock)
	t.flushLoginFailures(ctx, now)

	accountPacing.Lock()
	prune := accountPacing.prunedOn != local.Format("2006-01-02")
	accountPacing.prunedOn = local.Format("2006-01-02")
	accountPacing.Unlock()
	if prune {
		// Bounded delivery history; reminders cannot recur after their calendar date.
		db.Where("sent_at < ?", now.AddDate(0, 0, -45).UnixMilli()).Delete(&model.AccountNotification{})
	}
}

func (t *Tgbot) renewalReminder(client *model.ClientRecord, day int) botView {
	text := fmt.Sprintf("⏰ <b>续费提醒</b>\n%s 将于 %s 到期（还有 %d 天）。\n续费请联系管理员，续费后按新的到期日提醒。", esc(client.Email), formatClock(client.ExpiryTime), day)
	return botView{text, keyboard([]telego.InlineKeyboardButton{button("📊 查看用量", "pg:use!")})}
}

// notifyIpBans tells a person, and the admins, about each fresh ban of the
// limit's scan; a ban older than a few minutes is not news any more.
func (t *Tgbot) notifyIpBans(ctx context.Context, now time.Time) {
	var bans []model.ClientIpBan
	if err := database.GetDB().Where("banned_at >= ? AND expires_at > ?", now.Add(-10*time.Minute).Unix(), now.Unix()).
		Order("email, banned_at, network").Find(&bans).Error; err != nil || len(bans) == 0 {
		return
	}
	type banGroup struct {
		email    string
		bannedAt int64
		bans     []model.ClientIpBan
	}
	var groups []*banGroup
	for _, ban := range bans {
		if n := len(groups); n > 0 && groups[n-1].email == ban.Email && groups[n-1].bannedAt == ban.BannedAt {
			groups[n-1].bans = append(groups[n-1].bans, ban)
			continue
		}
		groups = append(groups, &banGroup{email: ban.Email, bannedAt: ban.BannedAt, bans: []model.ClientIpBan{ban}})
	}
	admins := adminSnapshot()
	for _, g := range groups {
		var client model.ClientRecord
		if err := database.GetDB().Where("email = ?", g.email).First(&client).Error; err != nil {
			continue
		}
		var lines strings.Builder
		for _, ban := range g.bans {
			lines.WriteString("• " + esc(ban.Network) + " · " + banLeft(ban.ExpiresAt, now) + "\n")
		}
		if client.TgID > 0 && !checkAdmin(client.TgID) {
			text := fmt.Sprintf("⚠️ <b>在线 IP 超出上限</b>\n你的账号同一时间在超过 %d 个 IP 上使用，下面的 IP 暂停使用，到时自动恢复：\n%s如果这些不是你的设备，你的订阅可能被别人使用了，请联系管理员。", client.LimitIP, lines.String())
			t.deliverAccountNotification(ctx, fmt.Sprintf("ipban:%s:%d", g.email, g.bannedAt), client.TgID,
				botView{text, keyboard([]telego.InlineKeyboardButton{button("🌐 查看在线设备", "pg:ips!")})}, now)
		}
		rows := make([][]telego.InlineKeyboardButton, 0, len(g.bans)+1)
		for _, ban := range g.bans {
			rows = append(rows, []telego.InlineKeyboardButton{button("🔓 解封 "+ban.Network, fmt.Sprintf("pg:a:bx:%d:%s!", client.Id, ban.Network))})
		}
		rows = append(rows, []telego.InlineKeyboardButton{button("👤 查看 "+client.Email, fmt.Sprintf("pg:a:c:%d!", client.Id))})
		text := fmt.Sprintf("🚫 <b>%s</b> 超出 IP 上限（%d 个），已暂停：\n%s", esc(client.Email), client.LimitIP, lines.String())
		for _, admin := range admins {
			t.deliverAccountNotification(ctx, fmt.Sprintf("ipban:%s:%d:%d", g.email, g.bannedAt, admin), admin, botView{text, keyboard(rows...)}, now)
		}
	}
}

// notifyServerRenewals reminds the admins of servers the probe says are due,
// at the admin's own daily time when they are a bound user too.
func (t *Tgbot) notifyServerRenewals(ctx context.Context, now time.Time, clock string) {
	admins := adminSnapshot()
	if len(admins) == 0 {
		return
	}
	var due []service.ProbeServer
	for _, server := range t.probeServers(ctx, now) {
		if accountReminderDay(now, server.ExpiryTime) > 0 {
			due = append(due, server)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ExpiryTime < due[j].ExpiryTime })
	for _, admin := range admins {
		at := "20:00"
		if client, bound := t.boundClient(admin); bound {
			if prefs, err := service.EnsureAccountActivation(client); err == nil {
				at = prefs.DailyTime
			}
		}
		if clock < at {
			continue
		}
		for _, server := range due {
			day := accountReminderDay(now, server.ExpiryTime)
			key := fmt.Sprintf("server:%s:%d:%d:%d", server.Id, admin, server.ExpiryTime, day)
			text := fmt.Sprintf("🖥 <b>服务器续费提醒</b>\n%s 将于 %s 到期（还有 %d 天）。\n数据来自探针；续费后请更新探针里的到期日期。", esc(server.Name), formatClock(server.ExpiryTime), day)
			t.deliverAccountNotification(ctx, key, admin, botView{text: text}, now)
		}
	}
}

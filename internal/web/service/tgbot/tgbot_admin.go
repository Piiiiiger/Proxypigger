package tgbot

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

const userListPageSize = 10

// renewChoices are the terms offered to a user whose plan sets none.
var renewChoices = []service.RenewalTerm{{Days: 7}, {Months: 1}, {Months: 3}, {Months: 6}, {Months: 12}}

// termText names a renewal term: years and months by the calendar, or days.
func termText(term service.RenewalTerm) string {
	switch {
	case term.Days > 0:
		return fmt.Sprintf("%d 天", term.Days)
	case term.Months%12 == 0:
		return fmt.Sprintf("%d 年", term.Months/12)
	}
	return fmt.Sprintf("%d 个月", term.Months)
}

// termToken and parseTerm carry a term in a button: "3m" is three months, "7d" a week.
func termToken(term service.RenewalTerm) string {
	if term.Days > 0 {
		return fmt.Sprintf("%dd", term.Days)
	}
	return fmt.Sprintf("%dm", term.Months)
}

func parseTerm(token string) (service.RenewalTerm, bool) {
	if len(token) < 2 {
		return service.RenewalTerm{}, false
	}
	n, err := strconv.Atoi(token[:len(token)-1])
	if err != nil || n < 1 {
		return service.RenewalTerm{}, false
	}
	switch token[len(token)-1] {
	case 'm':
		return service.RenewalTerm{Months: n}, true
	case 'd':
		return service.RenewalTerm{Days: n}, true
	}
	return service.RenewalTerm{}, false
}

// adminRow is one user as the admin menus list them.
type adminRow struct {
	rec    model.ClientRecord
	used   int64
	online int
	bans   int
}

func (r adminRow) label(now time.Time) string {
	emoji, state := clientState(&r.rec, r.used, now)
	parts := []string{r.rec.Email}
	if emoji == "✅" {
		emoji = "⚪"
		if r.online > 0 {
			emoji = "🟢"
		}
		if r.rec.LimitIP > 0 {
			parts = append(parts, ipSlots(r.online, r.rec.LimitIP))
		}
		parts = append(parts, expiryShort(r.rec.ExpiryTime, now))
	} else {
		parts = append(parts, state)
	}
	if r.bans > 0 {
		parts = append(parts, "⛔")
	}
	return emoji + " " + strings.Join(parts, " · ")
}

func (t *Tgbot) adminRows(now time.Time) ([]adminRow, error) {
	var recs []model.ClientRecord
	if err := database.GetDB().Find(&recs).Error; err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool { return strings.ToLower(recs[i].Email) < strings.ToLower(recs[j].Email) })
	used := map[string]int64{}
	if traffics, err := t.inboundService.GetAllClientTraffics(); err == nil {
		for _, tr := range traffics {
			used[tr.Email] = tr.Up + tr.Down
		}
	}
	emails := make([]string, len(recs))
	for i := range recs {
		emails[i] = recs[i].Email
	}
	online, bans, err := (&service.IpLimitService{}).OnlineIpCounts(emails, now)
	if err != nil {
		logger.Warning("tgbot: online counts:", err)
	}
	rows := make([]adminRow, len(recs))
	for i := range recs {
		rows[i] = adminRow{rec: recs[i], used: used[recs[i].Email], online: online[recs[i].Email], bans: bans[recs[i].Email]}
	}
	return rows, nil
}

// needsRenewal is an account the admin should look at soon: due within a week
// or lapsed within the last month, or nine tenths through its quota.
func (r adminRow) needsRenewal(now time.Time) bool {
	if expiry := r.rec.ExpiryTime; expiry > 0 {
		if days := calendarDaysUntil(expiry, now); days <= 7 && days >= -30 {
			return true
		}
	}
	return r.rec.TotalGB > 0 && r.used*10 >= r.rec.TotalGB*9
}

func adminBack() []telego.InlineKeyboardButton {
	return []telego.InlineKeyboardButton{button("⬅️ 管理菜单", "pg:a")}
}

func (t *Tgbot) adminHomeView(tgID int64, now time.Time) botView {
	rows, err := t.adminRows(now)
	if err != nil {
		return botView{"暂时无法读取用户，请稍后重试。", keyboard([]telego.InlineKeyboardButton{button("🔄 重试", "pg:a")})}
	}
	var online, expired, depleted, disabled, renew, banned int
	for _, r := range rows {
		if r.online > 0 {
			online++
		}
		switch emoji, _ := clientState(&r.rec, r.used, now); emoji {
		case "⌛":
			expired++
		case "🈳":
			depleted++
		case "⛔":
			disabled++
		}
		if r.needsRenewal(now) {
			renew++
		}
		banned += r.bans
	}
	var nodes, nodesUp int64
	database.GetDB().Model(&model.Node{}).Where("enable = ?", true).Count(&nodes)
	database.GetDB().Model(&model.Node{}).Where("enable = ? AND status = ?", true, "online").Count(&nodesUp)

	var b strings.Builder
	b.WriteString("🛠 <b>管理菜单</b>\n")
	fmt.Fprintf(&b, "👥 用户 %d · 🟢 在线 %d\n", len(rows), online)
	fmt.Fprintf(&b, "⏳ 需要续期 %d · ⌛ 已到期 %d · 🈳 用尽 %d · ⛔ 停用 %d\n", renew, expired, depleted, disabled)
	fmt.Fprintf(&b, "🚫 IP 封禁中 %d\n", banned)
	if nodes > 0 {
		fmt.Fprintf(&b, "🖥 节点 %d/%d 在线\n", nodesUp, nodes)
	}
	b.WriteString("<i>更新于 " + now.In(accountLocation).Format("01-02 15:04") + "</i>")
	kb := [][]telego.InlineKeyboardButton{
		{button("👥 用户列表", "pg:a:l:0"), button(fmt.Sprintf("⏳ 需要续期（%d）", renew), "pg:a:x")},
	}
	if _, bound := t.boundClient(tgID); bound {
		kb = append(kb, []telego.InlineKeyboardButton{button("📊 我的用量", "pg:use"), button("👤 我的账号", "pg:home")})
	}
	kb = append(kb,
		[]telego.InlineKeyboardButton{button("🧰 更多工具", "pg:a:more"), button("❓ 帮助", "pg:help")},
		[]telego.InlineKeyboardButton{button("🔄 刷新", "pg:a")},
	)
	return botView{b.String(), keyboard(kb...)}
}

func (t *Tgbot) userListView(page int, now time.Time) botView {
	rows, err := t.adminRows(now)
	if err != nil {
		return botView{"暂时无法读取用户，请稍后重试。", keyboard(adminBack())}
	}
	pages := max(1, (len(rows)+userListPageSize-1)/userListPageSize)
	page = min(max(page, 0), pages-1)
	text := fmt.Sprintf("👥 <b>用户列表</b>（%d）· 第 %d/%d 页\n🟢 在线 ⚪ 离线 ⛔ 停用 ⌛ 到期 🈳 用尽", len(rows), page+1, pages)
	kb := make([][]telego.InlineKeyboardButton, 0, userListPageSize+2)
	for _, r := range rows[min(page*userListPageSize, len(rows)):min((page+1)*userListPageSize, len(rows))] {
		kb = append(kb, []telego.InlineKeyboardButton{button(r.label(now), fmt.Sprintf("pg:a:c:%d", r.rec.Id))})
	}
	var nav []telego.InlineKeyboardButton
	if page > 0 {
		nav = append(nav, button("◀️ 上一页", fmt.Sprintf("pg:a:l:%d", page-1)))
	}
	if page < pages-1 {
		nav = append(nav, button("下一页 ▶️", fmt.Sprintf("pg:a:l:%d", page+1)))
	}
	kb = append(kb, nav, adminBack())
	return botView{text, keyboard(kb...)}
}

func (t *Tgbot) attentionView(now time.Time) botView {
	rows, err := t.adminRows(now)
	if err != nil {
		return botView{"暂时无法读取用户，请稍后重试。", keyboard(adminBack())}
	}
	var due []adminRow
	for _, r := range rows {
		if r.needsRenewal(now) {
			due = append(due, r)
		}
	}
	urgency := func(r adminRow) int64 {
		if r.rec.ExpiryTime > 0 && calendarDaysUntil(r.rec.ExpiryTime, now) <= 7 {
			return r.rec.ExpiryTime
		}
		return 1<<62 - r.used*100/max(r.rec.TotalGB, 1)
	}
	sort.SliceStable(due, func(i, j int) bool { return urgency(due[i]) < urgency(due[j]) })
	if len(due) == 0 {
		return botView{"⏳ <b>需要续期</b>\n目前没有需要续期的用户。", keyboard(adminBack())}
	}
	text := "⏳ <b>需要续期</b>\n7 天内到期、已到期，或流量用了九成以上的用户："
	kb := make([][]telego.InlineKeyboardButton, 0, len(due)+1)
	for _, r := range due {
		label := r.label(now)
		if r.rec.TotalGB > 0 && r.used < r.rec.TotalGB && r.used*10 >= r.rec.TotalGB*9 {
			label += fmt.Sprintf(" · 已用 %d%%", r.used*100/r.rec.TotalGB)
		}
		kb = append(kb, []telego.InlineKeyboardButton{button(label, fmt.Sprintf("pg:a:c:%d", r.rec.Id))})
	}
	return botView{text, keyboard(append(kb, adminBack())...)}
}

// userSearchView finds users by part of their name; a single hit opens its card.
func (t *Tgbot) userSearchView(query string, now time.Time) botView {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return botView{"用法：<code>/user 名字</code>，按用户名查找，名字的一部分也可以。", keyboard(adminBack())}
	}
	rows, err := t.adminRows(now)
	if err != nil {
		return botView{"暂时无法读取用户，请稍后重试。", keyboard(adminBack())}
	}
	var hits []adminRow
	for _, r := range rows {
		email := strings.ToLower(r.rec.Email)
		if email == query {
			return t.userCardView(r.rec.Id, now, "")
		}
		if strings.Contains(email, query) {
			hits = append(hits, r)
		}
	}
	switch len(hits) {
	case 0:
		return botView{"没有找到「" + esc(query) + "」。", keyboard([]telego.InlineKeyboardButton{button("👥 用户列表", "pg:a:l:0")}, adminBack())}
	case 1:
		return t.userCardView(hits[0].rec.Id, now, "")
	}
	kb := make([][]telego.InlineKeyboardButton, 0, len(hits)+1)
	for _, r := range hits[:min(len(hits), 20)] {
		kb = append(kb, []telego.InlineKeyboardButton{button(r.label(now), fmt.Sprintf("pg:a:c:%d", r.rec.Id))})
	}
	return botView{fmt.Sprintf("找到 %d 个用户：", len(hits)), keyboard(append(kb, adminBack())...)}
}

func loadClient(id int) (*model.ClientRecord, bool) {
	var rec model.ClientRecord
	if id <= 0 || database.GetDB().First(&rec, id).Error != nil {
		return nil, false
	}
	return &rec, true
}

func goneView() botView {
	return botView{"这个用户已经不存在了。", keyboard([]telego.InlineKeyboardButton{button("👥 用户列表", "pg:a:l:0")}, adminBack())}
}

func cardBack(id int) []telego.InlineKeyboardButton {
	return []telego.InlineKeyboardButton{button("⬅️ 返回", fmt.Sprintf("pg:a:c:%d", id))}
}

func (t *Tgbot) userCardView(id int, now time.Time, note string) botView {
	rec, ok := loadClient(id)
	if !ok {
		return goneView()
	}
	u, err := t.accountUsage(rec, now)
	used := u.up + u.down
	emoji, label := clientState(rec, used, now)
	var b strings.Builder
	if note != "" {
		b.WriteString(note + "\n\n")
	}
	fmt.Fprintf(&b, "👤 <b>%s</b> · %s %s\n", esc(rec.Email), emoji, label)
	if u.planName != "" {
		b.WriteString("套餐：" + esc(u.planName) + "\n")
	}
	switch {
	case err != nil:
		b.WriteString("暂时无法读取用量。\n")
	case rec.TotalGB > 0:
		fmt.Fprintf(&b, "流量：已用 %s / %s（剩 %s）\n%s\n", formatBytes(used), formatBytes(rec.TotalGB), formatBytes(max(0, rec.TotalGB-used)), usageBar(used, rec.TotalGB))
	default:
		fmt.Fprintf(&b, "流量：已用 %s（不限量）\n", formatBytes(used))
	}
	if u.nextReset > 0 {
		b.WriteString("流量清零：" + formatClock(u.nextReset) + "\n")
	}
	b.WriteString("到期：" + expiryText(rec.ExpiryTime, now) + "\n")
	b.WriteString("在线 IP：" + ipSlots(u.ips.Count, u.ips.Limit) + "\n")
	b.WriteString(onlineLines(u.ips, now))
	if rec.TgID != 0 {
		b.WriteString("Telegram：已绑定\n")
	} else {
		b.WriteString("Telegram：未绑定\n")
	}
	status, statusErr := (&service.AbuseService{}).Status(rec.Email, now)
	if line := abuseStatusLine(rec.Email, now); line != "" {
		b.WriteString(line + "\n")
	}
	if rec.Comment != "" {
		b.WriteString("备注：" + esc(rec.Comment) + "\n")
	}
	b.WriteString("<i>更新于 " + now.In(accountLocation).Format("15:04") + "</i>")

	actions := []telego.InlineKeyboardButton{}
	if rec.ExpiryTime != 0 {
		actions = append(actions, button("🔁 续期", fmt.Sprintf("pg:a:r:%d:1", id)))
	}
	actions = append(actions, button("♻️ 清零流量", fmt.Sprintf("pg:a:t:%d", id)))
	switches := []telego.InlineKeyboardButton{button("⛔ 停用", fmt.Sprintf("pg:a:e:%d", id))}
	if !rec.Enable {
		switches[0] = button("✅ 启用", fmt.Sprintf("pg:a:e:%d", id))
	}
	if len(u.ips.Bans) > 0 {
		switches = append(switches, button("🔓 解封 IP", fmt.Sprintf("pg:a:b:%d", id)))
	}
	var abuseRow []telego.InlineKeyboardButton
	if statusErr == nil && status.Ban != nil {
		abuseRow = append(abuseRow, button("🔓 解除封禁", fmt.Sprintf("pg:a:u:%d", id)))
	}
	if statusErr == nil && status.Strikes > 0 {
		abuseRow = append(abuseRow, button("🧹 清除违规", fmt.Sprintf("pg:a:f:%d", id)))
	}
	abuseRow = append(abuseRow, button("📜 封禁记录", fmt.Sprintf("pg:a:h:%d", id)))
	return botView{b.String(), keyboard(actions, switches, abuseRow,
		[]telego.InlineKeyboardButton{button("🔄 刷新", fmt.Sprintf("pg:a:c:%d", id)), button("⬅️ 用户列表", "pg:a:l:0")},
	)}
}

func (t *Tgbot) renewMenuView(id int, reset bool, now time.Time) botView {
	rec, ok := loadClient(id)
	if !ok {
		return goneView()
	}
	if rec.ExpiryTime == 0 {
		return botView{"<b>" + esc(rec.Email) + "</b> 长期有效，不需要续期。", keyboard(cardBack(id))}
	}
	r, toggle := 0, "⬜ 同时清零已用流量"
	if reset {
		r, toggle = 1, "☑️ 同时清零已用流量"
	}
	text := fmt.Sprintf("🔁 为 <b>%s</b> 续期\n当前到期：%s\n从当前到期日往后加，已到期的从现在算；按月续到下个月的同一天。", esc(rec.Email), expiryText(rec.ExpiryTime, now))
	choices := renewChoices
	var plan model.Plan
	if rec.PlanId > 0 && database.GetDB().First(&plan, rec.PlanId).Error == nil {
		if terms := service.PlanTermMonths(&plan); len(terms) > 0 {
			choices = nil
			for _, months := range terms {
				choices = append(choices, service.RenewalTerm{Months: months})
			}
			text += fmt.Sprintf("\n套餐「%s」只按这些周期续费。", esc(plan.Name))
		}
	}
	var rows [][]telego.InlineKeyboardButton
	for i, term := range choices {
		if i%3 == 0 {
			rows = append(rows, nil)
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], button("+"+termText(term), fmt.Sprintf("pg:a:rc:%d:%s:%d", id, termToken(term), r)))
	}
	return botView{text, keyboard(append(rows,
		[]telego.InlineKeyboardButton{button(toggle, fmt.Sprintf("pg:a:r:%d:%d", id, 1-r))},
		cardBack(id),
	)...)}
}

func (t *Tgbot) renewConfirmView(id int, term service.RenewalTerm, reset bool, now time.Time) botView {
	rec, ok := loadClient(id)
	if !ok {
		return goneView()
	}
	r := 0
	if reset {
		r = 1
	}
	next, _, err := service.RenewedExpiry(rec.ExpiryTime, rec.RenewDay, term, now)
	if err != nil {
		return botView{"无法续期：" + esc(err.Error()), keyboard(cardBack(id))}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "确认为 <b>%s</b> 续期 %s？\n到期：%s → %s\n", esc(rec.Email), termText(term), expiryText(rec.ExpiryTime, now), expiryText(next, now))
	if traffic, err := t.inboundService.GetClientTrafficByEmail(rec.Email); err == nil && traffic != nil && reset {
		fmt.Fprintf(&b, "已用流量 %s 将清零。", formatBytes(traffic.Up+traffic.Down))
	} else if reset {
		b.WriteString("已用流量将清零。")
	} else {
		b.WriteString("已用流量保持不变。")
	}
	token := issueActionToken(now)
	return botView{b.String(), keyboard([]telego.InlineKeyboardButton{
		button("✅ 确认续期", fmt.Sprintf("pg:a:rx:%d:%s:%d:%s", id, termToken(term), r, token)),
		button("⬅️ 返回", fmt.Sprintf("pg:a:r:%d:%d", id, r)),
	})}
}

// actionTokens let a confirmed renewal run once: a double tap on 确认 delivers
// the same callback twice, and each would add the days again.
var actionTokens = struct {
	sync.Mutex
	issued map[string]time.Time
}{issued: map[string]time.Time{}}

const actionTokenTTL = 30 * time.Minute

func issueActionToken(now time.Time) string {
	raw := make([]byte, 4)
	_, _ = rand.Read(raw)
	token := hex.EncodeToString(raw)
	actionTokens.Lock()
	defer actionTokens.Unlock()
	for old, at := range actionTokens.issued {
		if now.Sub(at) > actionTokenTTL {
			delete(actionTokens.issued, old)
		}
	}
	actionTokens.issued[token] = now
	return token
}

func consumeActionToken(token string, now time.Time) bool {
	actionTokens.Lock()
	defer actionTokens.Unlock()
	at, ok := actionTokens.issued[token]
	delete(actionTokens.issued, token)
	return ok && now.Sub(at) <= actionTokenTTL
}

func (t *Tgbot) resetConfirmView(id int) botView {
	rec, ok := loadClient(id)
	if !ok {
		return goneView()
	}
	usage := ""
	if traffic, err := t.inboundService.GetClientTrafficByEmail(rec.Email); err == nil && traffic != nil {
		usage = fmt.Sprintf("本期已用 %s（↑ %s · ↓ %s）。", formatBytes(traffic.Up+traffic.Down), formatBytes(traffic.Up), formatBytes(traffic.Down))
	}
	text := fmt.Sprintf("♻️ 清零 <b>%s</b> 的已用流量？\n%s清零后重新计算；因流量用尽而停用的用户会恢复。", esc(rec.Email), usage)
	return botView{text, keyboard([]telego.InlineKeyboardButton{
		button("✅ 确认清零", fmt.Sprintf("pg:a:tx:%d", id)), button("⬅️ 返回", fmt.Sprintf("pg:a:c:%d", id)),
	})}
}

func (t *Tgbot) toggleConfirmView(id int, now time.Time) botView {
	rec, ok := loadClient(id)
	if !ok {
		return goneView()
	}
	if rec.Enable {
		return botView{fmt.Sprintf("⛔ 停用 <b>%s</b>？\n停用后这个用户无法连接，直到重新启用。", esc(rec.Email)), keyboard([]telego.InlineKeyboardButton{
			button("✅ 确认停用", fmt.Sprintf("pg:a:ex:%d:0", id)), button("⬅️ 返回", fmt.Sprintf("pg:a:c:%d", id)),
		})}
	}
	text := fmt.Sprintf("✅ 启用 <b>%s</b>？", esc(rec.Email))
	var used int64
	if traffic, err := t.inboundService.GetClientTrafficByEmail(rec.Email); err == nil && traffic != nil {
		used = traffic.Up + traffic.Down
	}
	if emoji, state := clientState(rec, used, now); emoji == "⌛" || emoji == "🈳" {
		text += "\n⚠️ 这个用户" + state + "，启用后会被再次停用，请先续期或清零流量。"
	}
	return botView{text, keyboard([]telego.InlineKeyboardButton{
		button("✅ 确认启用", fmt.Sprintf("pg:a:ex:%d:1", id)), button("⬅️ 返回", fmt.Sprintf("pg:a:c:%d", id)),
	})}
}

func (t *Tgbot) unbanMenuView(id int, now time.Time) botView {
	rec, ok := loadClient(id)
	if !ok {
		return goneView()
	}
	bans, err := (&service.IpLimitService{}).BansForEmail(rec.Email, now)
	if err != nil || len(bans) == 0 {
		return botView{"<b>" + esc(rec.Email) + "</b> 现在没有被封禁的 IP。", keyboard(cardBack(id))}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🔓 解封 <b>%s</b> 的 IP\n", esc(rec.Email))
	kb := make([][]telego.InlineKeyboardButton, 0, len(bans)+1)
	for _, ban := range bans {
		b.WriteString("• " + esc(ban.Network) + " · " + banLeft(ban.ExpiresAt, now) + "\n")
		kb = append(kb, []telego.InlineKeyboardButton{button("🔓 解封 "+ban.Network, fmt.Sprintf("pg:a:bx:%d:%s", id, ban.Network))})
	}
	b.WriteString("解封后这个 IP 可以马上重新连接；如果仍然超出上限，下一次检查会再次暂停它。")
	return botView{b.String(), keyboard(append(kb, cardBack(id))...)}
}

// answerAdminCallback runs one admin button. Every id comes from the button, so
// each action re-reads the user and works on what is there now.
func (t *Tgbot) answerAdminCallback(q *telego.CallbackQuery, messageID int, rest string) {
	chatID, now := q.Message.GetChat().ID, time.Now()
	action, rest, _ := strings.Cut(rest, ":")
	idText, rest, _ := strings.Cut(rest, ":")
	id, _ := strconv.Atoi(idText)
	args := strings.Split(rest, ":")
	arg := func(i int) int {
		if i >= len(args) {
			return -1
		}
		n, err := strconv.Atoi(args[i])
		if err != nil {
			return -1
		}
		return n
	}
	toast := ""
	var view botView
	switch action {
	case "":
		view = t.adminHomeView(q.From.ID, now)
	case "l":
		view = t.userListView(id, now)
	case "x":
		view = t.attentionView(now)
	case "c":
		view = t.userCardView(id, now, "")
	case "r":
		view = t.renewMenuView(id, arg(0) != 0, now)
	case "rc":
		// A button from before terms carried plain days: offer the menu afresh.
		if term, ok := parseTerm(args[0]); ok {
			view = t.renewConfirmView(id, term, arg(1) == 1, now)
		} else {
			view = t.renewMenuView(id, arg(1) == 1, now)
		}
	case "rx":
		term, _ := parseTerm(args[0])
		toast, view = t.renewNow(id, term, arg(1) == 1, args, now)
	case "t":
		view = t.resetConfirmView(id)
	case "tx":
		toast, view = t.resetNow(id, now)
	case "e":
		view = t.toggleConfirmView(id, now)
	case "ex":
		toast, view = t.setEnableNow(id, arg(0) == 1, now)
	case "b":
		view = t.unbanMenuView(id, now)
	case "bx":
		toast, view = t.unbanNow(id, rest, now)
	case "u":
		toast, view = t.liftAbuseNow(id, now)
	case "f":
		toast, view = t.forgiveAbuseNow(id, now)
	case "h":
		if rec, ok := loadClient(id); ok {
			view = t.banHistoryView(rec.Email, now, fmt.Sprintf("pg:a:c:%d", id))
		} else {
			view = goneView()
		}
	case "more":
		t.sendCallbackAnswerTgBot(q.ID, "")
		t.SendAnswer(chatID, t.I18nBot("tgbot.commands.pleaseChoose"), true)
		return
	default:
		view = t.adminHomeView(q.From.ID, now)
	}
	t.sendCallbackAnswerTgBot(q.ID, toast)
	t.present(chatID, messageID, view)
}

func (t *Tgbot) renewNow(id int, term service.RenewalTerm, reset bool, args []string, now time.Time) (string, botView) {
	rec, ok := loadClient(id)
	if !ok {
		return "", goneView()
	}
	if term == (service.RenewalTerm{}) || len(args) < 3 || !consumeActionToken(args[2], now) {
		return "这个确认已失效，请重新操作", t.userCardView(id, now, "")
	}
	needRestart, err := t.clientService.Renew(&t.inboundService, []string{rec.Email}, term, reset)
	if needRestart {
		t.xrayService.SetToNeedRestart()
	}
	if err != nil {
		logger.Warning("tgbot: renew", rec.Email, "failed:", err)
		return "续期失败", t.userCardView(id, now, "❌ 续期失败："+esc(err.Error()))
	}
	return "已续期 " + termText(term), t.userCardView(id, now, "✅ 已续期 "+termText(term))
}

func (t *Tgbot) resetNow(id int, now time.Time) (string, botView) {
	rec, ok := loadClient(id)
	if !ok {
		return "", goneView()
	}
	if err := t.resetClientTraffic(rec.Email); err != nil {
		logger.Warning("tgbot: reset traffic of", rec.Email, "failed:", err)
		return "清零失败", t.userCardView(id, now, "❌ 清零失败："+esc(err.Error()))
	}
	return "已清零流量", t.userCardView(id, now, "✅ 已清零流量")
}

func (t *Tgbot) setEnableNow(id int, enable bool, now time.Time) (string, botView) {
	rec, ok := loadClient(id)
	if !ok {
		return "", goneView()
	}
	_, needRestart, err := t.clientService.SetClientEnableByEmail(&t.inboundService, rec.Email, enable)
	if needRestart {
		t.xrayService.SetToNeedRestart()
	}
	if err != nil {
		logger.Warning("tgbot: enable", rec.Email, "failed:", err)
		return "操作失败", t.userCardView(id, now, "❌ 操作失败："+esc(err.Error()))
	}
	if enable {
		return "已启用", t.userCardView(id, now, "✅ 已启用")
	}
	return "已停用", t.userCardView(id, now, "⛔ 已停用")
}

func (t *Tgbot) unbanNow(id int, network string, now time.Time) (string, botView) {
	rec, ok := loadClient(id)
	if !ok {
		return "", goneView()
	}
	if err := (&service.IpLimitService{}).Unban(rec.Email, network); err != nil {
		return "这个 IP 已不在封禁中", t.userCardView(id, now, "")
	}
	t.xrayService.SetToNeedRestart()
	return "已解封", t.userCardView(id, now, "✅ 已解封 "+esc(network))
}

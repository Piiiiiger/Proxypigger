package tgbot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// Account and admin views speak Chinese in Beijing time, the zone the account
// reminders have always used and renewals count calendar months in.
var accountLocation = service.AccountLocation

const dayMillis = int64(24 * time.Hour / time.Millisecond)

// botView is one screen of the bot: its text and the buttons under it.
type botView struct {
	text     string
	keyboard *telego.InlineKeyboardMarkup
}

// present shows a view in place of the message its button sits on, or as a new
// message for a typed command or a message too old to edit.
func (t *Tgbot) present(chatID int64, messageID int, v botView) {
	if messageID > 0 && bot != nil {
		params := &telego.EditMessageTextParams{
			ChatID: tu.ID(chatID), MessageID: messageID, Text: v.text, ParseMode: telego.ModeHTML, ReplyMarkup: v.keyboard,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, err := bot.EditMessageText(ctx, params)
		cancel()
		if err == nil || isTelegramNotModifiedError(err) {
			return
		}
		logger.Debug("tgbot: edit failed, sending the view anew:", err)
	}
	if v.keyboard != nil {
		t.SendMsgToTgbot(chatID, v.text, v.keyboard)
		return
	}
	t.SendMsgToTgbot(chatID, v.text)
}

func button(label, data string) telego.InlineKeyboardButton {
	return tu.InlineKeyboardButton(label).WithCallbackData(data)
}

func keyboard(rows ...[]telego.InlineKeyboardButton) *telego.InlineKeyboardMarkup {
	kept := rows[:0:0]
	for _, row := range rows {
		if len(row) > 0 {
			kept = append(kept, row)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return tu.InlineKeyboard(kept...)
}

func formatBytes(n int64) string {
	switch {
	case n <= 0:
		return "0"
	case n >= 1<<40:
		return fmt.Sprintf("%.2f TB", float64(n)/(1<<40))
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
}

// usageBar draws how much of a quota is used in ten blocks; any use shows at
// least one. Views are HTML, so the label holds no "<": Telegram reads it as a tag.
func usageBar(used, total int64) string {
	if total <= 0 {
		return ""
	}
	pct := float64(used) * 100 / float64(total)
	pct = min(max(pct, 0), 100)
	filled := int(pct/10 + 0.5)
	if used > 0 && filled == 0 {
		filled = 1
	}
	label := fmt.Sprintf("%.0f%%", pct)
	if used > 0 && pct < 1 {
		label = "不到 1%"
	}
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled) + " " + label
}

func formatClock(ms int64) string {
	return time.UnixMilli(ms).In(accountLocation).Format("2006-01-02 15:04")
}

// calendarDaysUntil counts Beijing calendar days, as the expiry reminders do.
func calendarDaysUntil(expiry int64, now time.Time) int {
	date := func(t time.Time) time.Time {
		y, m, d := t.In(accountLocation).Date()
		return time.Date(y, m, d, 0, 0, 0, 0, accountLocation)
	}
	return int(date(time.UnixMilli(expiry)).Sub(date(now)).Hours() / 24)
}

// expiryText names an expiry the way a person reads it; a negative expiry is a
// term that starts at the first connection.
func expiryText(expiry int64, now time.Time) string {
	switch {
	case expiry == 0:
		return "长期有效"
	case expiry < 0:
		return fmt.Sprintf("首次连接后 %d 天", -expiry/dayMillis)
	case expiry <= now.UnixMilli():
		return formatClock(expiry) + "（已到期）"
	}
	if days := calendarDaysUntil(expiry, now); days > 0 {
		return fmt.Sprintf("%s（还有 %d 天）", formatClock(expiry), days)
	}
	return formatClock(expiry) + "（今天到期）"
}

// expiryShort is expiryText for a list row.
func expiryShort(expiry int64, now time.Time) string {
	switch {
	case expiry == 0:
		return "长期"
	case expiry < 0:
		return "未开始"
	case expiry <= now.UnixMilli():
		return "已到期"
	}
	if days := calendarDaysUntil(expiry, now); days > 0 {
		return fmt.Sprintf("剩 %d 天", days)
	}
	return "今天到期"
}

func agoText(unixSec int64, now time.Time) string {
	d := now.Sub(time.Unix(unixSec, 0))
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d/time.Minute))
	}
	return fmt.Sprintf("%d 小时前", int(d/time.Hour))
}

// banLeft says how long a ban has to run and when it lifts.
func banLeft(expiresAt int64, now time.Time) string {
	left := time.Unix(expiresAt, 0).Sub(now)
	minutes := int((left + time.Minute - 1) / time.Minute)
	return fmt.Sprintf("还剩 %d 分钟（%s 恢复）", max(minutes, 1), time.Unix(expiresAt, 0).In(accountLocation).Format("15:04"))
}

// clientState is the account's standing, worst first, as the users page shows
// it: an expired or used-up account is off whatever its switch says.
func clientState(rec *model.ClientRecord, used int64, now time.Time) (emoji, label string) {
	switch {
	case rec.ExpiryTime > 0 && rec.ExpiryTime <= now.UnixMilli():
		return "⌛", "已到期"
	case rec.TotalGB > 0 && used >= rec.TotalGB:
		return "🈳", "流量用尽"
	case !rec.Enable:
		return "⛔", "已停用"
	}
	return "✅", "正常"
}

func ipSlots(count, limit int) string {
	if limit <= 0 {
		return fmt.Sprintf("%d（不限）", count)
	}
	return fmt.Sprintf("%d/%d", count, limit)
}

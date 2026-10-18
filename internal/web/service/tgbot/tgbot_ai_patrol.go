package tgbot

import (
	"context"
	"fmt"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func aiPatrolNotice(v model.AIPatrolViolation) botView {
	text := "⚠️ <b>Codex 巡查违规通知</b>\n" + service.AIPatrolNotice
	if v.Action == service.AIPatrolSuspendedAction {
		text = "⛔ <b>账号暂停 24 小时</b>\n你的活动再次被标记为违规，属于收到先前通知后 30 天内发生的另一次独立违规。账号已暂停 24 小时，到期自动恢复。请联系管理员了解详情。"
	}
	return botView{text: text + "\n\n记录时间：" + time.Unix(v.At, 0).In(accountLocation).Format("2006-01-02 15:04") + fmt.Sprintf("\n违规记录编号：%d", v.Id)}
}

// Unlike the transient event feed, pending AI notices have no ten-minute cutoff.
// A failed send stays pending across restarts; success uses durable receipts.
func (t *Tgbot) notifyAIPatrol(ctx context.Context, now time.Time) {
	var pending []model.AIPatrolViolation
	db := database.GetDB()
	if db.Where("notified_at = 0 AND forgiven = ?", false).Order("id").Limit(100).Find(&pending).Error != nil {
		return
	}
	for _, v := range pending {
		var client model.ClientRecord
		if db.Where("email = ?", v.Email).First(&client).Error != nil || client.TgID <= 0 {
			continue
		}
		key := fmt.Sprintf("ai-patrol:%d:%d", v.Id, client.TgID)
		t.deliverAccountNotification(ctx, key, client.TgID, aiPatrolNotice(v), now)
		if sent, ok := notificationSentAt(key); ok {
			db.Model(&model.AIPatrolViolation{}).Where("id = ? AND notified_at = 0", v.Id).Update("notified_at", sent.Unix())
		}
	}
}

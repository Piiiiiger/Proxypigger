package tgbot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func TestAIPatrolNotificationRetriesAfterFailureAndRestartWithoutExpiry(t *testing.T) {
	tb, calls := newLevelTgbot(t)
	now := time.Now()
	good := bot
	v := model.AIPatrolViolation{Email: ownerMail, At: now.Add(-48 * time.Hour).Unix(), Action: service.AIPatrolViolationAction}
	if err := database.GetDB().Create(&v).Error; err != nil {
		t.Fatal(err)
	}
	failure, _ := staleButtonServer(t, map[string]any{"sendMessage": map[string]any{"ok": false, "error_code": 403, "description": "Forbidden"}})
	defer failure.Close()
	swapTestBot(t, failure.URL)
	tb.notifyAIPatrol(context.Background(), now)
	database.GetDB().First(&v, v.Id)
	if v.NotifiedAt != 0 {
		t.Fatal("failed delivery acknowledged")
	}
	bot = good
	// A new scheduler instance still finds and delivers the old pending notice.
	(&Tgbot{}).notifyAIPatrol(context.Background(), now.Add(24*time.Hour))
	database.GetDB().First(&v, v.Id)
	if v.NotifiedAt == 0 || calls("sendMessage") != 1 {
		t.Fatal("pending notice was lost", v.NotifiedAt, calls("sendMessage"))
	}
	(&Tgbot{}).notifyAIPatrol(context.Background(), now.Add(25*time.Hour))
	if calls("sendMessage") != 1 {
		t.Fatal("duplicate notification")
	}
	text := aiPatrolNotice(v).text
	if !strings.Contains(text, "一次违规") || !strings.Contains(text, "请联系管理员了解详情") || !strings.Contains(text, "本次不封禁") {
		t.Fatal(text)
	}
}

func TestAIPatrolSuspensionNoticeUses24HoursAndUnboundNoticeStaysPending(t *testing.T) {
	tb, calls := newLevelTgbot(t)
	now := time.Now()
	database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", ownerMail).Update("tg_id", 0)
	v := model.AIPatrolViolation{Email: ownerMail, At: now.Unix(), Action: service.AIPatrolSuspendedAction}
	database.GetDB().Create(&v)
	tb.notifyAIPatrol(context.Background(), now)
	database.GetDB().First(&v, v.Id)
	if calls("sendMessage") != 0 || v.NotifiedAt != 0 {
		t.Fatal("unbound client incorrectly marked notified")
	}
	text := aiPatrolNotice(v).text
	if !strings.Contains(text, "暂停 24 小时") || strings.Contains(text, "30 分钟") || strings.Contains(text, "永久") {
		t.Fatal(text)
	}
}

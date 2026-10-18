package sub

import (
	"encoding/json"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"net/http"
	"testing"
	"time"
)

func TestPortalAIPatrolNoticeAndAcknowledgementBelongToSignedInAccount(t *testing.T) {
	router, _ := seedPortal(t)
	now := time.Now()
	mine := model.AIPatrolViolation{Email: "pa@e", At: now.Unix(), Action: service.AIPatrolViolationAction, Reason: "reviewed scan"}
	other := model.AIPatrolViolation{Email: "pb@e", At: now.Unix(), Action: service.AIPatrolViolationAction, Reason: "private other user"}
	database.GetDB().Create(&mine)
	database.GetDB().Create(&other)
	body := `{"ids":[` + jsonNumber(mine.Id) + `,` + jsonNumber(other.Id) + `]}`
	if r := portalRequest(router, http.MethodPost, "/sub/portal/ai-patrol/seen", body, "198.51.100.1", nil); r.Code != http.StatusUnauthorized {
		t.Fatal(r.Code)
	}
	cookie := sessionCookie(t, portalLogin(router, "pa@e", "alpha-pass", "198.51.100.1"))
	r := portalRequest(router, http.MethodGet, "/sub/portal/bans", "", "198.51.100.1", cookie)
	var h service.AbuseHistory
	json.Unmarshal(r.Body.Bytes(), &h)
	if len(h.AIPatrolNotices) != 1 || h.AIPatrolNotices[0].Id != mine.Id {
		t.Fatal(r.Body.String())
	}
	if r := portalRequest(router, http.MethodPost, "/sub/portal/ai-patrol/seen", body, "198.51.100.1", cookie); r.Code != http.StatusNoContent {
		t.Fatal(r.Code, r.Body.String())
	}
	database.GetDB().First(&mine, mine.Id)
	database.GetDB().First(&other, other.Id)
	if mine.PortalSeenAt == 0 || other.PortalSeenAt != 0 {
		t.Fatal("cross-account read receipt", mine, other)
	}
}

func jsonNumber(value int) string { data, _ := json.Marshal(value); return string(data) }

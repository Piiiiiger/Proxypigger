package tgbot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// tomorrowAt is the next day at hour, Beijing time, so the users' dates set
// against it lie ahead of the clock the panel checks them with.
func tomorrowAt(hour int) time.Time {
	n := time.Now().In(accountLocation)
	return time.Date(n.Year(), n.Month(), n.Day()+1, hour, 0, 0, 0, accountLocation)
}

func endsAt(totalGB int64, expiry time.Time, enable bool) func(*model.Client) {
	return func(c *model.Client) {
		c.TotalGB, c.ExpiryTime, c.Enable = totalGB<<30, expiry.UnixMilli(), enable
	}
}

func dueNotices(rec *botRecorder) []botCall {
	var out []botCall
	for _, m := range sentTo(rec, adminTgID) {
		if strings.Contains(m.Text, "快到期或流量将尽") {
			out = append(out, m)
		}
	}
	return out
}

// Users close to their quota or their expiry reach the admins in one message, each
// once; users fine, disabled, already out of traffic or already expired do not.
func TestAdminsHearOnceOfUsersCloseToTheirEnd(t *testing.T) {
	tb, rec := newPiggerBot(t)
	withProbe(t)
	ib := seedVlessInbound(t, "a")
	now := tomorrowAt(10)
	month := now.Add(30 * 24 * time.Hour)
	alice := seedClient(t, "alice", []int{ib}, endsAt(50, month, true))
	setUsage(t, "alice", 6<<30, 40<<30)
	bob := seedClient(t, "bob", []int{ib}, endsAt(0, now.Add(3*24*time.Hour), true))
	seedClient(t, "carol", []int{ib}, endsAt(50, month, true))
	seedClient(t, "dave", []int{ib}, endsAt(50, month, false))
	setUsage(t, "dave", 0, 47<<30)
	seedClient(t, "erin", []int{ib}, endsAt(50, month, true))
	setUsage(t, "erin", 0, 50<<30)
	seedClient(t, "frank", []int{ib}, endsAt(0, now.Add(-time.Hour), true))

	ctx := context.Background()
	tb.sendAccountNotifications(ctx, now)
	tb.sendAccountNotifications(ctx, now.Add(time.Minute))
	notices := dueNotices(rec)
	if len(notices) != 1 {
		t.Fatalf("notices = %d, want 1", len(notices))
	}
	text := notices[0].Text
	for _, want := range []string{"alice</b>：流量已用 92%", "bob</b>：", "（还有 3 天）"} {
		if !strings.Contains(text, want) {
			t.Errorf("the notice lacks %q:\n%s", want, text)
		}
	}
	for _, other := range []string{"carol", "dave", "erin", "frank"} {
		if strings.Contains(text, other) {
			t.Errorf("the notice names %s:\n%s", other, text)
		}
	}
	if got := notices[0].dataFor(t, "alice"); got != fmt.Sprintf("pg:a:c:%d", alice.Id) {
		t.Errorf("alice's button = %q, want her card", got)
	}
	if got := notices[0].dataFor(t, "bob"); got != fmt.Sprintf("pg:a:c:%d", bob.Id) {
		t.Errorf("bob's button = %q, want his card", got)
	}
}

// A crossing at night reaches the admins in the morning.
func TestDueNoticesWaitForTheMorning(t *testing.T) {
	tb, rec := newPiggerBot(t)
	withProbe(t)
	ib := seedVlessInbound(t, "a")
	seedClient(t, "alice", []int{ib}, endsAt(50, tomorrowAt(10).Add(30*24*time.Hour), true))
	setUsage(t, "alice", 0, 46<<30)

	ctx := context.Background()
	tb.sendAccountNotifications(ctx, tomorrowAt(2))
	if n := len(dueNotices(rec)); n != 0 {
		t.Fatalf("%d notices at 02:00", n)
	}
	tb.sendAccountNotifications(ctx, tomorrowAt(9))
	if n := len(dueNotices(rec)); n != 1 {
		t.Fatalf("%d notices at 09:00, want 1", n)
	}
}

// Traffic reset, the next crossing is news again; a renewal's new expiry is too,
// once it comes close.
func TestDueNoticesComeBackAfterAResetOrARenewal(t *testing.T) {
	tb, rec := newPiggerBot(t)
	withProbe(t)
	ib := seedVlessInbound(t, "a")
	now := tomorrowAt(10)
	seedClient(t, "alice", []int{ib}, endsAt(50, now.Add(90*24*time.Hour), true))
	setUsage(t, "alice", 0, 46<<30)
	seedClient(t, "bob", []int{ib}, endsAt(0, now.Add(3*24*time.Hour), true))
	ctx := context.Background()
	tb.sendAccountNotifications(ctx, now)

	setUsage(t, "alice", 0, 0)
	tb.sendAccountNotifications(ctx, now.Add(time.Minute))
	setUsage(t, "alice", 0, 48<<30)
	tb.sendAccountNotifications(ctx, now.Add(2*time.Minute))
	if _, err := (&service.ClientService{}).Renew(&service.InboundService{}, []string{"bob"}, service.RenewalTerm{Days: 30}, false); err != nil {
		t.Fatal(err)
	}
	tb.sendAccountNotifications(ctx, now.Add(3*time.Minute))
	tb.sendAccountNotifications(ctx, now.Add(30*24*time.Hour))

	notices := dueNotices(rec)
	if len(notices) != 3 {
		t.Fatalf("notices = %d, want the first, alice after her reset, bob after his renewal", len(notices))
	}
	if !strings.Contains(notices[1].Text, "alice") || strings.Contains(notices[1].Text, "bob") {
		t.Errorf("second notice:\n%s\nwant alice only", notices[1].Text)
	}
	if !strings.Contains(notices[2].Text, "bob") || strings.Contains(notices[2].Text, "alice") {
		t.Errorf("third notice:\n%s\nwant bob only", notices[2].Text)
	}
}

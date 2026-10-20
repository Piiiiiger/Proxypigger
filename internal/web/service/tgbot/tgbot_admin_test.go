package tgbot

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// A renewal from the card adds what its preview promised, a calendar month to the
// same date, clears the usage the box is ticked for, and runs once when pressed twice.
func TestAdminRenewsFromTheCardAsPreviewedAndOnlyOnce(t *testing.T) {
	tb, rec := newPiggerBot(t)
	ib := seedVlessInbound(t, "a")
	expiry := time.Date(2035, time.January, 15, 10, 0, 0, 0, accountLocation).UnixMilli()
	alice := seedClient(t, "alice", []int{ib}, func(c *model.Client) { c.ExpiryTime = expiry; c.TotalGB = 100 << 30 })
	setUsage(t, "alice", 3<<30, 9<<30)

	tb.tap(adminTgID, fmt.Sprintf("pg:a:c:%d", alice.Id))
	tb.tap(adminTgID, rec.last(t).dataFor(t, "续期"))
	tb.tap(adminTgID, rec.last(t).dataFor(t, "+1 个月"))
	confirm := rec.last(t)
	want := time.Date(2035, time.February, 15, 10, 0, 0, 0, accountLocation).UnixMilli()
	if !strings.Contains(confirm.Text, formatClock(want)) {
		t.Errorf("the preview %q does not promise %s", confirm.Text, formatClock(want))
	}
	data := confirm.dataFor(t, "确认续期")
	tb.tap(adminTgID, data)
	tb.tap(adminTgID, data)

	if got := clientRecord(t, "alice").ExpiryTime; got != want {
		t.Errorf("expiry = %s, want %s, added once", formatClock(got), formatClock(want))
	}
	if got := usageOf(t, "alice"); got != 0 {
		t.Errorf("usage = %d after a renewal with the reset ticked, want 0", got)
	}
}

func inboundClientEnable(t *testing.T, inboundId int, email string) any {
	t.Helper()
	var ib model.Inbound
	if err := database.GetDB().First(&ib, inboundId).Error; err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Clients []map[string]any `json:"clients"`
	}
	if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
		t.Fatal(err)
	}
	for _, c := range settings.Clients {
		if c["email"] == email {
			return c["enable"]
		}
	}
	t.Fatalf("%s is not in inbound %d", email, inboundId)
	return nil
}

// Disabling from the card takes the user off every inbound they are on, and
// enabling brings them back on each.
func TestAdminDisablesAndEnablesFromTheCard(t *testing.T) {
	tb, rec := newPiggerBot(t)
	a, b := seedVlessInbound(t, "a"), seedVlessInbound(t, "b")
	alice := seedClient(t, "alice", []int{a, b}, nil)

	tb.tap(adminTgID, fmt.Sprintf("pg:a:e:%d", alice.Id))
	tb.tap(adminTgID, rec.last(t).dataFor(t, "确认停用"))
	if clientRecord(t, "alice").Enable || inboundClientEnable(t, a, "alice") != false || inboundClientEnable(t, b, "alice") != false {
		t.Fatal("alice is still enabled somewhere after the card's disable")
	}
	tb.tap(adminTgID, fmt.Sprintf("pg:a:e:%d", alice.Id))
	tb.tap(adminTgID, rec.last(t).dataFor(t, "确认启用"))
	if !clientRecord(t, "alice").Enable || inboundClientEnable(t, a, "alice") != true || inboundClientEnable(t, b, "alice") != true {
		t.Fatal("alice is still disabled somewhere after the card's enable")
	}
}

// The card lists a running ban and lifts it, an IPv6 /64 with its colons too.
func TestAdminLiftsABanFromTheCard(t *testing.T) {
	tb, rec := newPiggerBot(t)
	ib := seedVlessInbound(t, "a")
	alice := seedClient(t, "alice", []int{ib}, func(c *model.Client) { c.LimitIP = 1 })
	observe(t, time.Now(), "alice", "2001:db8:1:2::7", "203.0.113.9")

	tb.tap(adminTgID, fmt.Sprintf("pg:a:c:%d", alice.Id))
	tb.tap(adminTgID, rec.last(t).dataFor(t, "解封 IP"))
	tb.tap(adminTgID, rec.last(t).dataFor(t, "2001:db8:1:2"))

	if bans, _ := (&service.IpLimitService{}).BansForEmail("alice", time.Now()); len(bans) != 0 {
		t.Errorf("%d bans left after unbanning from the card", len(bans))
	}
	if !strings.Contains(rec.last(t).Text, "已解封") {
		t.Errorf("the card after unbanning says %q", rec.last(t).Text)
	}
}

// The list pages through every user, and /user finds one by part of the name.
func TestAdminListPagesAndFindsUsers(t *testing.T) {
	tb, rec := newPiggerBot(t)
	ib := seedVlessInbound(t, "a")
	for i := range 12 {
		seedClient(t, fmt.Sprintf("user%02d", i), []int{ib}, nil)
	}
	seedClient(t, "carol", []int{ib}, nil)

	tb.send(adminTgID, "/users")
	first := rec.last(t)
	if !strings.Contains(first.Text, "（13）") || len(first.Labels) != 12 || !strings.Contains(first.Labels[0], "carol") {
		t.Fatalf("first page %q has buttons %q, want carol first and 10 users with paging", first.Text, first.Labels)
	}
	tb.tap(adminTgID, first.dataFor(t, "下一页"))
	if second := rec.last(t); len(second.Labels) != 5 || !strings.Contains(second.Labels[2], "user11") {
		t.Errorf("second page buttons %q, want user09 to user11 with paging", second.Labels)
	}
	tb.send(adminTgID, "/user car")
	if card := rec.last(t); !strings.Contains(card.Text, "carol") || !card.hasButton("♻️ 清零流量") {
		t.Errorf("/user car showed %q, want carol's card", card.Text)
	}
	tb.send(adminTgID, "/user user1")
	if hits := rec.last(t); !strings.Contains(hits.Text, "找到 2 个") {
		t.Errorf("/user user1 showed %q, want user10 and user11", hits.Text)
	}
}

// Each admin gets the admin menu in their own chat, everyone else the account
// commands.
func TestCommandMenusAreSetPerScope(t *testing.T) {
	tb, rec := newPiggerBot(t)

	tb.trySetBotCommands(bot)

	var everyone, adminChat []string
	for _, c := range rec.all() {
		if c.Method != "setMyCommands" {
			continue
		}
		var names []string
		commands, _ := c.Payload["commands"].([]any)
		for _, raw := range commands {
			command, _ := raw.(map[string]any)
			names = append(names, fmt.Sprint(command["command"]))
		}
		scope, _ := c.Payload["scope"].(map[string]any)
		switch {
		case scope == nil:
			everyone = names
		case scope["chat_id"] == float64(adminTgID):
			adminChat = names
		}
	}
	if !strings.Contains(strings.Join(everyone, ","), "report") || strings.Contains(strings.Join(everyone, ","), "users") {
		t.Errorf("default commands = %v, want the account set without the admin's", everyone)
	}
	if !strings.Contains(strings.Join(adminChat, ","), "users") {
		t.Errorf("admin chat commands = %v, want the admin set", adminChat)
	}
}

// "需要续期" lists who is due within a week or lapsed this month, never a user
// gone for good nor a healthy one.
func TestAdminRenewalListShowsOnlyUsersDueOrRecentlyLapsed(t *testing.T) {
	tb, rec := newPiggerBot(t)
	ib := seedVlessInbound(t, "a")
	day := 24 * time.Hour
	for name, offset := range map[string]time.Duration{"soon": 3 * day, "lapsed": -10 * day, "gone": -60 * day, "healthy": 60 * day} {
		seedClient(t, name, []int{ib}, func(c *model.Client) { c.ExpiryTime = time.Now().Add(offset).UnixMilli() })
	}

	tb.tap(adminTgID, "pg:a:x")

	listed := strings.Join(rec.last(t).Labels, "|")
	for _, name := range []string{"soon", "lapsed"} {
		if !strings.Contains(listed, name) {
			t.Errorf("%s is missing from the renewal list %q", name, listed)
		}
	}
	for _, name := range []string{"gone", "healthy"} {
		if strings.Contains(listed, name) {
			t.Errorf("%s should not be in the renewal list %q", name, listed)
		}
	}
}

// The renew menu offers only the terms a user's plan is sold by.
func TestRenewMenuOffersOnlyThePlansTerms(t *testing.T) {
	tb, rec := newPiggerBot(t)
	ib := seedVlessInbound(t, "a")
	alice := seedClient(t, "alice", []int{ib}, func(c *model.Client) { c.ExpiryTime = time.Now().Add(5 * 24 * time.Hour).UnixMilli() })
	plan, err := (&service.PlanService{}).Create(service.PlanInput{Name: "SG", InboundIds: []int{ib}, TermMonths: []int{3, 12}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&service.PlanService{}).Assign(&service.InboundService{}, []string{"alice"}, plan.Id); err != nil {
		t.Fatal(err)
	}

	tb.tap(adminTgID, fmt.Sprintf("pg:a:r:%d:0", alice.Id))
	menu := rec.last(t)
	for _, want := range []string{"+3 个月", "+1 年"} {
		if !menu.hasButton(want) {
			t.Errorf("the menu lacks %q: %v", want, menu.Labels)
		}
	}
	for _, other := range []string{"+7 天", "+1 个月", "+6 个月"} {
		if menu.hasButton(other) {
			t.Errorf("the menu offers %q, which the plan is not sold by", other)
		}
	}
}

// A renew button sent before terms carried plain days; tapped now it offers the
// menu again instead of renewing by days.
func TestAnOldRenewButtonReopensTheMenu(t *testing.T) {
	tb, rec := newPiggerBot(t)
	ib := seedVlessInbound(t, "a")
	expiry := time.Date(2035, time.January, 15, 10, 0, 0, 0, accountLocation).UnixMilli()
	alice := seedClient(t, "alice", []int{ib}, func(c *model.Client) { c.ExpiryTime = expiry })
	tb.tap(adminTgID, fmt.Sprintf("pg:a:rc:%d:30:0", alice.Id))
	if menu := rec.last(t); !menu.hasButton("+1 个月") || strings.Contains(menu.Text, "确认") {
		t.Errorf("an old button gave %q %v, want the renew menu", menu.Text, menu.Labels)
	}
	if got := clientRecord(t, "alice").ExpiryTime; got != expiry {
		t.Errorf("expiry moved to %s", formatClock(got))
	}
}

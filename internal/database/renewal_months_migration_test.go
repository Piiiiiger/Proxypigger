package database

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Plans sold by 30/90/180/365 days are sold by 1/3/6/12 calendar months after the
// upgrade, and the day column is gone.
func TestInitDB_MovesPlanTermsToMonths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x-ui.db")
	if err := InitDB(path); err != nil {
		t.Fatal(err)
	}
	seedPlanTermDays(t)
	reopen(t, path)
	assertPlanTermMonths(t)
}

func TestInitDB_MovesPlanTermsToMonths_Postgres(t *testing.T) {
	usePostgresSchema(t)
	if err := InitDB(""); err != nil {
		t.Fatal(err)
	}
	seedPlanTermDays(t)
	reopen(t, "")
	assertPlanTermMonths(t)
}

// Unused codes for 30/90/180/365 days become codes for months once, at the upgrade;
// used codes keep the days they were used with, and a days code made later stays.
func TestInitDB_MovesUnusedMonthlyCodesToMonths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x-ui.db")
	if err := InitDB(path); err != nil {
		t.Fatal(err)
	}
	seedDayCodes(t)
	reopen(t, path)
	assertCodeTerms(t, map[string][2]int{"MONTH": {1, 0}, "YEAR": {12, 0}, "ODD": {0, 92}, "USED": {0, 30}})
	if err := GetDB().Create(&model.ActivationCode{Code: "LATER", PlanId: 1, Days: 30}).Error; err != nil {
		t.Fatal(err)
	}
	reopen(t, path)
	assertCodeTerms(t, map[string][2]int{"MONTH": {1, 0}, "LATER": {0, 30}})
}

func TestInitDB_MovesUnusedMonthlyCodesToMonths_Postgres(t *testing.T) {
	usePostgresSchema(t)
	if err := InitDB(""); err != nil {
		t.Fatal(err)
	}
	seedDayCodes(t)
	reopen(t, "")
	assertCodeTerms(t, map[string][2]int{"MONTH": {1, 0}, "YEAR": {12, 0}, "ODD": {0, 92}, "USED": {0, 30}})
}

func reopen(t *testing.T, path string) {
	t.Helper()
	if err := CloseDB(); err != nil {
		t.Fatal(err)
	}
	if err := InitDB(path); err != nil {
		t.Fatalf("InitDB on the legacy database: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })
}

func seedPlanTermDays(t *testing.T) {
	t.Helper()
	db := GetDB()
	if err := db.Exec("ALTER TABLE plans ADD COLUMN term_days text DEFAULT ''").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range [][2]string{{"Any", ""}, {"All", "[30,90,180,365]"}, {"SG", "[90,365]"}} {
		if err := db.Exec("INSERT INTO plans (name, limit_ip, template_id, sort_index, term_days) VALUES (?, 0, 0, 0, ?)", row[0], row[1]).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func assertPlanTermMonths(t *testing.T) {
	t.Helper()
	want := map[string]string{"Any": "", "All": "[1,3,6,12]", "SG": "[3,12]"}
	var plans []model.Plan
	if err := GetDB().Find(&plans).Error; err != nil {
		t.Fatal(err)
	}
	for _, p := range plans {
		if p.TermMonths != want[p.Name] {
			t.Errorf("plan %s terms = %q, want %q", p.Name, p.TermMonths, want[p.Name])
		}
		delete(want, p.Name)
	}
	if len(want) != 0 {
		t.Errorf("plans missing after the upgrade: %v", want)
	}
	if GetDB().Migrator().HasColumn(&model.Plan{}, "term_days") {
		t.Error("plans still have the term_days column")
	}
}

func seedDayCodes(t *testing.T) {
	t.Helper()
	db := GetDB()
	if err := db.Migrator().DropColumn(&model.ActivationCode{}, "months"); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		code   string
		days   int
		usedAt int64
	}{{"MONTH", 30, 0}, {"YEAR", 365, 0}, {"ODD", 92, 0}, {"USED", 30, 1791172800000}} {
		if err := db.Exec("INSERT INTO activation_codes (code, plan_id, total_gb, days, reset_day, note, used_at, used_by, redeem_day, created_at) "+
			"VALUES (?, 1, 0, ?, 0, '', ?, '', 0, 1791000000000)", row.code, row.days, row.usedAt).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func assertCodeTerms(t *testing.T, want map[string][2]int) {
	t.Helper()
	for code, term := range want {
		var row model.ActivationCode
		if err := GetDB().Where("code = ?", code).First(&row).Error; err != nil {
			t.Fatalf("code %s: %v", code, err)
		}
		if row.Months != term[0] || row.Days != term[1] {
			t.Errorf("code %s = %d months, %d days; want %d months, %d days", code, row.Months, row.Days, term[0], term[1])
		}
	}
}

package database

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPrepareSQLiteForMigration(t *testing.T) {
	t.Run("rejects non-panel database", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "random.db")
		gdb, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		if err := gdb.Exec("CREATE TABLE notes(id integer primary key, body text)").Error; err != nil {
			t.Fatalf("create table: %v", err)
		}
		closeGorm(gdb)

		err = PrepareSQLiteForMigration(dbPath)
		if err == nil {
			t.Fatal("PrepareSQLiteForMigration accepted a non-panel database, want error")
		}
		if !strings.Contains(err.Error(), "not a 3x-ui panel database") {
			t.Fatalf("error = %q, want it to contain %q", err.Error(), "not a 3x-ui panel database")
		}
	})

	t.Run("upgrades old panel schema", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "old.db")
		gdb, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		if err := gdb.AutoMigrate(&model.User{}, &model.Setting{}, &model.Inbound{}); err != nil {
			t.Fatalf("automigrate legacy subset: %v", err)
		}
		closeGorm(gdb)

		if err := PrepareSQLiteForMigration(dbPath); err != nil {
			t.Fatalf("PrepareSQLiteForMigration rejected an old panel database: %v", err)
		}

		check, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			t.Fatalf("reopen sqlite: %v", err)
		}
		defer closeGorm(check)
		sqlDB, err := check.DB()
		if err != nil {
			t.Fatalf("sql db: %v", err)
		}
		for _, table := range []string{"client_global_traffics"} {
			if !sqliteTableExists(sqlDB, table) {
				t.Errorf("table %s was not created by the schema upgrade", table)
			}
		}
	})

	// The copy carries only today's columns, so day terms and day codes must move
	// to months on the source, or a migrated panel sells every plan by any term.
	t.Run("moves day terms and codes to months before the copy", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "old.db")
		gdb, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		if err := gdb.AutoMigrate(&model.User{}, &model.Setting{}, &model.Inbound{}, &model.Plan{}, &model.ActivationCode{}); err != nil {
			t.Fatalf("automigrate: %v", err)
		}
		for _, stmt := range []string{
			"ALTER TABLE plans DROP COLUMN term_months",
			"ALTER TABLE plans ADD COLUMN term_days text DEFAULT ''",
			"ALTER TABLE activation_codes DROP COLUMN months",
			"INSERT INTO plans (name, limit_ip, template_id, sort_index, term_days) VALUES ('All', 0, 0, 0, '[30,90,180,365]')",
			"INSERT INTO activation_codes (code, plan_id, total_gb, days, reset_day, note, used_at, used_by, redeem_day, created_at) VALUES ('MONTH', 1, 0, 30, 0, '', 0, '', 0, 1791000000000)",
		} {
			if err := gdb.Exec(stmt).Error; err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		closeGorm(gdb)

		if err := PrepareSQLiteForMigration(dbPath); err != nil {
			t.Fatalf("PrepareSQLiteForMigration: %v", err)
		}
		check, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			t.Fatalf("reopen sqlite: %v", err)
		}
		defer closeGorm(check)
		var plan model.Plan
		var code model.ActivationCode
		if err := check.Where("name = ?", "All").First(&plan).Error; err != nil || plan.TermMonths != "[1,3,6,12]" {
			t.Errorf("plan terms = %q (%v), want [1,3,6,12]", plan.TermMonths, err)
		}
		if err := check.Where("code = ?", "MONTH").First(&code).Error; err != nil || code.Months != 1 || code.Days != 0 {
			t.Errorf("code = %d months, %d days (%v), want 1 month", code.Months, code.Days, err)
		}
	})
}

package database

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Hits recorded before the hour's traffic was kept load with none, and no NULL is
// left for a sum or a scan to trip on.
func TestAbuseEventBytesColumnMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x-ui.db")
	legacy, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		"CREATE TABLE abuse_events (id integer PRIMARY KEY AUTOINCREMENT, email text NOT NULL, node_id integer, rule text, level text, measure text, count integer, `limit` integer, window integer, samples text, action text, at integer)",
		`INSERT INTO abuse_events (email, node_id, rule, level, measure, count, "limit", window, samples, action, at) VALUES ('alice', 3, 'register', 'strike', 'register-hour', 35, 30, 3600, '["accounts.google.com"]', 'noticed', 1792420447)`,
	} {
		if err := legacy.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	handle, err := legacy.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := InitDB(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDB() })
	var nulls int64
	if err := GetDB().Table("abuse_events").Where("bytes IS NULL").Count(&nulls).Error; err != nil || nulls != 0 {
		t.Fatalf("NULL bytes/error = %d/%v", nulls, err)
	}
	var row model.AbuseEvent
	if err := GetDB().Where("email = ?", "alice").First(&row).Error; err != nil || row.Count != 35 || row.Bytes != 0 {
		t.Fatalf("reading/error = %+v/%v", row, err)
	}
}

package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
)

func testKey(t *testing.T) crypto.Key {
	t.Helper()
	var key crypto.Key
	copy(key[:], []byte("0123456789abcdef0123456789abcdef"))
	return key
}

func TestInitThenOpenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	encPath := filepath.Join(dir, "health.db.enc")
	workPath := filepath.Join(dir, ".health.db.work")
	key := testKey(t)

	if err := Init(encPath, workPath, key); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := os.Stat(workPath); !os.IsNotExist(err) {
		t.Fatalf("working file should not exist after Init, stat err = %v", err)
	}
	if _, err := os.Stat(encPath); err != nil {
		t.Fatalf("encrypted file missing after Init: %v", err)
	}

	// The encrypted file must not be a plain SQLite file (i.e. it's
	// actually encrypted, not just copied).
	raw, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatalf("reading encPath: %v", err)
	}
	if len(raw) >= 16 && string(raw[:16]) == "SQLite format 3\x00" {
		t.Fatal("encrypted file starts with the SQLite header — it isn't encrypted")
	}

	store, err := Open(encPath, workPath, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := os.Stat(workPath); err != nil {
		t.Fatalf("working file missing after Open: %v", err)
	}

	if _, err := store.DB().Exec(
		`INSERT INTO user_profile (id, sex, birth_date) VALUES (1, 'male', '1990-01-01')`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(workPath); !os.IsNotExist(err) {
		t.Fatalf("working file should not exist after Close, stat err = %v", err)
	}

	store2, err := Open(encPath, workPath, key)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer store2.Close()

	var sex string
	if err := store2.DB().QueryRow(`SELECT sex FROM user_profile WHERE id = 1`).Scan(&sex); err != nil {
		t.Fatalf("querying re-opened database: %v", err)
	}
	if sex != "male" {
		t.Errorf("sex = %q, want %q", sex, "male")
	}
}

func TestOpenWrongKeyFails(t *testing.T) {
	dir := t.TempDir()
	encPath := filepath.Join(dir, "health.db.enc")
	workPath := filepath.Join(dir, ".health.db.work")
	key := testKey(t)

	if err := Init(encPath, workPath, key); err != nil {
		t.Fatalf("Init: %v", err)
	}

	var wrongKey crypto.Key
	copy(wrongKey[:], []byte("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"))

	if _, err := Open(encPath, workPath, wrongKey); err == nil {
		t.Error("Open with wrong key: expected error, got none")
	}
}

func TestInitRefusesToOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	encPath := filepath.Join(dir, "health.db.enc")
	workPath := filepath.Join(dir, ".health.db.work")
	key := testKey(t)

	if err := Init(encPath, workPath, key); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := Init(encPath, workPath, key); err == nil {
		t.Error("second Init: expected error, got none")
	}
}

func TestCheckpointPersistsWithoutClosing(t *testing.T) {
	dir := t.TempDir()
	encPath := filepath.Join(dir, "health.db.enc")
	workPath := filepath.Join(dir, ".health.db.work")
	key := testKey(t)

	if err := Init(encPath, workPath, key); err != nil {
		t.Fatalf("Init: %v", err)
	}
	store, err := Open(encPath, workPath, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if _, err := store.DB().Exec(
		`INSERT INTO user_profile (id, sex) VALUES (1, 'female')`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	// Simulate a crash: read the encrypted file straight from disk into a
	// separate store, without going through this store's Close.
	other, err := Open(encPath, filepath.Join(dir, ".health.db.work2"), key)
	if err != nil {
		t.Fatalf("Open (simulated separate process): %v", err)
	}
	defer other.Close()

	var sex string
	if err := other.DB().QueryRow(`SELECT sex FROM user_profile WHERE id = 1`).Scan(&sex); err != nil {
		t.Fatalf("querying checkpointed database: %v", err)
	}
	if sex != "female" {
		t.Errorf("sex = %q, want %q", sex, "female")
	}
}

// TestMigrateUpgradesPreMigrationDatabase simulates a database created
// before Migrations existed (the pre-rename total_calories column, none of
// the newer tables) and confirms Open applies every pending migration in
// order, preserving existing data, rather than requiring a fresh Init.
func TestMigrateUpgradesPreMigrationDatabase(t *testing.T) {
	dir := t.TempDir()
	encPath := filepath.Join(dir, "health.db.enc")
	workPath := filepath.Join(dir, ".health.db.work")
	key := testKey(t)

	const preMigrationSchema = `
		CREATE TABLE watch_daily_summary (
			day TEXT PRIMARY KEY,
			total_calories REAL
		);
		CREATE TABLE watch_sleep_session (
			id INTEGER PRIMARY KEY,
			day TEXT NOT NULL
		);
		CREATE TABLE cronometer_daily_nutrition (
			day TEXT PRIMARY KEY,
			energy_kcal REAL
		);
		INSERT INTO watch_daily_summary (day, total_calories) VALUES ('2026-07-30', 2100.5);
	`

	conn, err := sql.Open("sqlite", workPath)
	if err != nil {
		t.Fatalf("creating pre-migration working file: %v", err)
	}
	if _, err := conn.Exec(preMigrationSchema); err != nil {
		t.Fatalf("applying pre-migration schema: %v", err)
	}
	if _, err := conn.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatalf("setting starting schema version: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("closing pre-migration working file: %v", err)
	}

	store, err := Open(encPath, workPath, key)
	if err != nil {
		t.Fatalf("Open (should auto-migrate): %v", err)
	}
	defer store.Close()

	var kcal float64
	if err := store.DB().QueryRow(`SELECT kcal_burned_google FROM watch_daily_summary WHERE day = '2026-07-30'`).Scan(&kcal); err != nil {
		t.Fatalf("querying renamed column after migration: %v", err)
	}
	if kcal != 2100.5 {
		t.Errorf("kcal_burned_google = %v, want 2100.5 (existing data preserved across rename)", kcal)
	}

	for _, table := range []string{
		"watch_heart_rate_zone_definition", "watch_heart_rate_zone_minutes",
		"watch_calories_by_zone", "watch_blood_glucose_sample", "watch_core_body_temperature_sample",
	} {
		var got string
		if err := store.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&got); err != nil {
			t.Errorf("table %q missing after migration: %v", table, err)
		}
	}

	var version int
	if err := store.DB().QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("reading post-migration schema version: %v", err)
	}
	if version != len(Migrations) {
		t.Errorf("schema version = %d, want %d (all migrations applied)", version, len(Migrations))
	}
}

func TestOpenRecoversFromUncleanShutdown(t *testing.T) {
	dir := t.TempDir()
	encPath := filepath.Join(dir, "health.db.enc")
	workPath := filepath.Join(dir, ".health.db.work")
	key := testKey(t)

	if err := Init(encPath, workPath, key); err != nil {
		t.Fatalf("Init: %v", err)
	}
	store, err := Open(encPath, workPath, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, err := store.DB().Exec(
		`INSERT INTO user_profile (id, sex) VALUES (1, 'female')`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Simulate an unclean shutdown: the process dies with the working file
	// still on disk and never checkpointed back to encPath.
	if err := store.conn.Close(); err != nil {
		t.Fatalf("closing underlying connection: %v", err)
	}

	recovered, err := Open(encPath, workPath, key)
	if err != nil {
		t.Fatalf("Open after unclean shutdown: %v", err)
	}
	defer recovered.Close()

	var sex string
	if err := recovered.DB().QueryRow(`SELECT sex FROM user_profile WHERE id = 1`).Scan(&sex); err != nil {
		t.Fatalf("querying recovered database: %v", err)
	}
	if sex != "female" {
		t.Errorf("sex = %q, want %q (data written before the simulated crash should survive recovery)", sex, "female")
	}
}

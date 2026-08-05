package db

import (
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
		`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x');
		 INSERT INTO user_profile (user_id, sex, birth_date) VALUES (1, 'male', '1990-01-01')`,
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
	if err := store2.DB().QueryRow(`SELECT sex FROM user_profile WHERE user_id = 1`).Scan(&sex); err != nil {
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
		`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x');
		 INSERT INTO user_profile (user_id, sex) VALUES (1, 'female')`,
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
	if err := other.DB().QueryRow(`SELECT sex FROM user_profile WHERE user_id = 1`).Scan(&sex); err != nil {
		t.Fatalf("querying checkpointed database: %v", err)
	}
	if sex != "female" {
		t.Errorf("sex = %q, want %q", sex, "female")
	}
}

// TestCloseLeavesSharedWorkingFileIntactWhenStillOpen is a regression test
// for a real incident (2026-08-05): with two healthd processes sharing one
// root (e.g. "healthd serve" alongside a "healthd mcp" connector instance,
// see ARCHITECTURE.md §11), the second process's Close() used to zero the
// shared working file's bytes in place before attempting to remove it —
// the zero-write silently succeeds even while another process still has
// the file open (Windows only blocks the *remove*, not concurrent writes),
// so a failed remove still meant a live sibling process's database had
// already been destroyed. removeWorkingFile now renames the file to a
// private temp name first, which fails cleanly instead when another
// process still holds it open, leaving the original path untouched.
func TestCloseLeavesSharedWorkingFileIntactWhenStillOpen(t *testing.T) {
	dir := t.TempDir()
	encPath := filepath.Join(dir, "health.db.enc")
	workPath := filepath.Join(dir, ".health.db.work")
	key := testKey(t)

	if err := Init(encPath, workPath, key); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Simulate two processes sharing one root: both Open() the same
	// workPath, each getting its own *sql.DB handle onto the same file
	// (exactly what happens when "healthd serve" is already running and
	// "healthd mcp" opens the same --root — see Open's "recover" branch).
	first, err := Open(encPath, workPath, key)
	if err != nil {
		t.Fatalf("Open (first process): %v", err)
	}
	defer first.Close()

	if _, err := first.DB().Exec(
		`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x');
		 INSERT INTO user_profile (user_id, sex) VALUES (1, 'female')`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := first.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	second, err := Open(encPath, workPath, key)
	if err != nil {
		t.Fatalf("Open (second process, same workPath): %v", err)
	}

	if err := second.Close(); err == nil {
		t.Error("second.Close(): expected an error (workPath still in use by first), got none")
	}
	if _, err := os.Stat(workPath); err != nil {
		t.Fatalf("shared working file should still exist after second.Close(): %v", err)
	}

	// The critical assertion: first (still "running") must still see
	// valid, unzeroed data through its own already-open connection.
	var sex string
	if err := first.DB().QueryRow(`SELECT sex FROM user_profile WHERE user_id = 1`).Scan(&sex); err != nil {
		t.Fatalf("querying via first's still-open connection after second.Close(): %v", err)
	}
	if sex != "female" {
		t.Errorf("sex = %q, want %q — shared working file was corrupted by the other process's Close()", sex, "female")
	}
}

// TestMigrationAddsMCPTokenTable is a regression test for the migration
// mechanism itself (migrations.go's Migrations list was empty until this
// entry — see its doc comment) — simulates a database that predates the
// mcp_token migration by dropping the table Init already created and
// rolling PRAGMA user_version back, then confirms migrate() recreates it.
func TestMigrationAddsMCPTokenTable(t *testing.T) {
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

	if _, err := store.DB().Exec(`DROP TABLE mcp_token`); err != nil {
		t.Fatalf("dropping mcp_token to simulate a pre-migration database: %v", err)
	}
	if _, err := store.DB().Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatalf("resetting schema version: %v", err)
	}

	if err := store.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var name string
	err = store.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'mcp_token'`).Scan(&name)
	if err != nil {
		t.Fatalf("mcp_token table missing after migration: %v", err)
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
		`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x');
		 INSERT INTO user_profile (user_id, sex) VALUES (1, 'female')`,
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
	if err := recovered.DB().QueryRow(`SELECT sex FROM user_profile WHERE user_id = 1`).Scan(&sex); err != nil {
		t.Fatalf("querying recovered database: %v", err)
	}
	if sex != "female" {
		t.Errorf("sex = %q, want %q (data written before the simulated crash should survive recovery)", sex, "female")
	}
}

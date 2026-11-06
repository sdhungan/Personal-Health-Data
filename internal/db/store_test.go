package db

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestMigrateUpgradesFromBaselineAndRejectsOlder covers the migration
// mechanism: a database at migrationBase gets every pending entry applied
// (here the real one, dropping mcp_token), a fresh Init starts at the
// latest version, and a database older than migrationBase is refused
// rather than opened with a schema the code doesn't match.
func TestMigrateUpgradesFromBaselineAndRejectsOlder(t *testing.T) {
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
	conn := store.DB()
	version := func() int {
		var v int
		if err := conn.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	latest := migrationBase + len(Migrations)
	if v := version(); v != latest {
		t.Fatalf("fresh database at version %d, want %d", v, latest)
	}

	// The live database just before these migrations: at the baseline,
	// still carrying mcp_token and RFC3339-text heart-rate timestamps.
	if _, err := conn.Exec(fmt.Sprintf(`CREATE TABLE mcp_token (user_id INTEGER PRIMARY KEY);
		DROP TABLE watch_heart_rate_intraday;
		CREATE TABLE watch_heart_rate_intraday (
		    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE, recorded_at TEXT NOT NULL, bpm REAL NOT NULL,
		    cached_at TEXT NOT NULL DEFAULT (strftime('%%Y-%%m-%%dT%%H:%%M:%%fZ', 'now')), PRIMARY KEY (user_id, recorded_at));
		INSERT INTO watch_heart_rate_intraday (user_id, recorded_at, bpm, cached_at) VALUES (1, '2026-08-05T20:02:25Z', 61, 'c');
		PRAGMA user_version = %d`, migrationBase)); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err != nil {
		t.Fatalf("migrate from baseline: %v", err)
	}
	if v := version(); v != latest {
		t.Errorf("after migrate: version %d, want %d", v, latest)
	}
	var n int
	if err := conn.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'mcp_token'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("mcp_token still present after migrate (count %d, err %v)", n, err)
	}
	var ts any
	var bpm float64
	if err := conn.QueryRow(`SELECT recorded_at, bpm FROM watch_heart_rate_intraday`).Scan(&ts, &bpm); err != nil {
		t.Fatalf("reading migrated heart-rate row: %v", err)
	}
	if want := time.Date(2026, 8, 5, 20, 2, 25, 0, time.UTC).Unix(); ts != want || bpm != 61 {
		t.Errorf("migrated heart-rate row = (%v %T, %v), want (%d int64, 61)", ts, ts, bpm, want)
	}

	if _, err := conn.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, migrationBase-1)); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err == nil {
		t.Error("migrate accepted a database older than migrationBase")
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

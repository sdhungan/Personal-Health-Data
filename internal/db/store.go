package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
)

// Store manages the encrypted-at-rest SQLite database (see ARCHITECTURE.md
// §6 and the pure-Go app-level encryption approach chosen over CGO
// SQLCipher). The database only ever exists in plaintext as a working file
// inside the owner-only db/ directory, for the lifetime of a single Open/
// Close pair; Checkpoint lets a long-running process (the server, the sync
// scheduler) bound how much would be lost to a crash without fully closing.
type Store struct {
	encPath  string
	workPath string
	key      crypto.Key
	conn     *sql.DB
}

// Init creates a brand-new encrypted database containing Schema. It refuses
// to run if a database or working file already exists at the given paths.
func Init(encPath, workPath string, key crypto.Key) error {
	if _, err := os.Stat(encPath); err == nil {
		return fmt.Errorf("refusing to overwrite existing database at %s", encPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking %s: %w", encPath, err)
	}
	if _, err := os.Stat(workPath); err == nil {
		return fmt.Errorf("a working database file already exists at %s; resolve it before running init", workPath)
	}

	conn, err := sql.Open("sqlite", workPath)
	if err != nil {
		return fmt.Errorf("creating working file %s: %w", workPath, err)
	}
	if _, err := conn.Exec(Schema); err != nil {
		conn.Close()
		os.Remove(workPath)
		return fmt.Errorf("applying schema: %w", err)
	}
	// Schema is already at the shape every migration in Migrations produces,
	// so a fresh database starts at the latest version directly rather than
	// replaying history it was never behind on.
	if _, err := conn.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(Migrations))); err != nil {
		conn.Close()
		os.Remove(workPath)
		return fmt.Errorf("setting schema version: %w", err)
	}
	if err := conn.Close(); err != nil {
		return fmt.Errorf("closing new database: %w", err)
	}

	s := &Store{encPath: encPath, workPath: workPath, key: key}
	if err := s.checkpointTo(encPath); err != nil {
		os.Remove(workPath)
		return err
	}
	return removeWorkingFile(workPath)
}

// Open decrypts encPath into workPath and opens it, unless workPath already
// exists — which means healthd did not shut down cleanly last time, so the
// working file (more recent than the last encrypted checkpoint) is opened
// directly instead of being discarded.
func Open(encPath, workPath string, key crypto.Key) (*Store, error) {
	if _, err := os.Stat(workPath); err == nil {
		fmt.Fprintf(os.Stderr,
			"WARNING: found an existing working database file at %s — healthd did not shut down cleanly last time. Recovering from it instead of the last encrypted checkpoint.\n",
			workPath)
		return openWorkingFile(encPath, workPath, key)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("checking working file %s: %w", workPath, err)
	}

	ciphertext, err := os.ReadFile(encPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", encPath, err)
	}
	plaintext, err := crypto.Decrypt(key, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypting %s: %w", encPath, err)
	}
	if err := os.WriteFile(workPath, plaintext, 0o600); err != nil {
		return nil, fmt.Errorf("writing working file %s: %w", workPath, err)
	}

	return openWorkingFile(encPath, workPath, key)
}

func openWorkingFile(encPath, workPath string, key crypto.Key) (*Store, error) {
	conn, err := sql.Open("sqlite", workPath)
	if err != nil {
		return nil, fmt.Errorf("opening working file %s: %w", workPath, err)
	}
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("opening working file %s: %w", workPath, err)
	}
	// WAL lets readers (e.g. dashboard page loads) proceed against a
	// snapshot without blocking on a concurrent writer (e.g. a long-running
	// sync) and vice versa — the "don't lock the DB when fetching"
	// requirement. busy_timeout is a backstop for the moments SQLite still
	// needs a brief exclusive lock (e.g. a WAL checkpoint): callers wait up
	// to 5s instead of failing immediately with "database is locked".
	if _, err := conn.Exec(`PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;`); err != nil {
		conn.Close()
		return nil, fmt.Errorf("configuring working file %s: %w", workPath, err)
	}
	s := &Store{encPath: encPath, workPath: workPath, key: key, conn: conn}
	if err := s.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrating working file %s: %w", workPath, err)
	}
	return s, nil
}

// migrate applies any pending entries from Migrations (see migrations.go),
// tracked via PRAGMA user_version. A database already at the latest version
// (including every fresh Init, which sets it directly) is a no-op.
func (s *Store) migrate() error {
	var version int
	if err := s.conn.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	for i := version; i < len(Migrations); i++ {
		if err := s.applyMigration(i); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(i int) error {
	tx, err := s.conn.Begin()
	if err != nil {
		return fmt.Errorf("beginning migration %d: %w", i, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	if _, err := tx.Exec(Migrations[i]); err != nil {
		return fmt.Errorf("applying migration %d: %w", i, err)
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
		return fmt.Errorf("bumping schema version to %d: %w", i+1, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration %d: %w", i, err)
	}
	return nil
}

// DB returns the underlying *sql.DB for queries.
func (s *Store) DB() *sql.DB { return s.conn }

// Checkpoint re-encrypts the current on-disk state of the working file back
// over the encrypted file, without closing the connection. Callers running
// for extended periods (the server, the sync scheduler) should call this
// periodically so a crash loses at most the interval between checkpoints.
func (s *Store) Checkpoint() error {
	// TRUNCATE folds every committed WAL frame into the main file and
	// empties the WAL, so the plain bytes we're about to read+encrypt are
	// complete — we only ever preserve workPath itself, not its -wal/-shm
	// sidecar files.
	if _, err := s.conn.Exec(`PRAGMA wal_checkpoint(TRUNCATE);`); err != nil {
		return fmt.Errorf("checkpointing WAL for %s: %w", s.workPath, err)
	}
	return s.checkpointTo(s.encPath)
}

func (s *Store) checkpointTo(dest string) error {
	plaintext, err := os.ReadFile(s.workPath)
	if err != nil {
		return fmt.Errorf("reading working file %s: %w", s.workPath, err)
	}
	ciphertext, err := crypto.Encrypt(s.key, plaintext)
	if err != nil {
		return fmt.Errorf("encrypting working file: %w", err)
	}

	tmp := dest + ".new"
	if err := os.WriteFile(tmp, ciphertext, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmp, dest, err)
	}
	return nil
}

// Close checkpoints the working file back to the encrypted file, then
// removes the plaintext working file and closes the connection. After a
// clean Close, no plaintext copy of the database remains on disk.
func (s *Store) Close() error {
	if _, err := s.conn.Exec(`PRAGMA wal_checkpoint(TRUNCATE);`); err != nil {
		return fmt.Errorf("checkpointing WAL for %s: %w", s.workPath, err)
	}
	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("closing database connection: %w", err)
	}
	if err := s.checkpointTo(s.encPath); err != nil {
		return err
	}
	return removeWorkingFile(s.workPath)
}

// Discard closes the connection and removes the working file WITHOUT
// checkpointing back to the encrypted file. Use this for read-only
// operations (e.g. "db decrypt") opened alongside a possibly-running
// server/sync process: checkpointing on Close there would risk clobbering
// whatever that other process has written since this Store was opened.
func (s *Store) Discard() error {
	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("closing database connection: %w", err)
	}
	return removeWorkingFile(s.workPath)
}

// removeWorkingFile best-effort zeroes the plaintext working file before
// removing it. This is not a guarantee against forensic recovery (SSD wear
// leveling, filesystem journaling, etc. can retain copies) — it narrows the
// window, which is the honest limit of the pure-Go, no-CGO approach chosen
// over a page-encrypting SQLCipher driver.
//
// Detaches path under a private temp name FIRST, before touching its
// content, rather than zeroing path in place and only then trying to
// remove it. This isn't just tidiness: it's a safety probe. More than one
// healthd process can legitimately share a root at once now (`healthd
// serve` alongside one or more `healthd mcp` connector instances, see
// ARCHITECTURE.md §11) — each opens its own *sql.DB against the same
// workPath, and SQLite's WAL mode is explicitly chosen to make that safe
// for ordinary reads/writes. But a rename away from path fails on Windows
// exactly when another process still has an open handle to it (the same
// sharing rule that blocks deleting an open file) — unlike a plain
// zero-write, which Windows happily allows concurrently even when a
// rename/delete would be refused. The original code zeroed path in place
// before ever attempting to remove it, so a failed remove (another process
// still had it open) only surfaced *after* that process's live data was
// already destroyed — confirmed by a real incident: a `healthd mcp`
// process exiting while `healthd serve` was still running zeroed the
// shared working file out from under it, and `healthd serve`'s own next
// periodic Checkpoint then faithfully encrypted those zeros over the
// on-disk backup too, leaving nothing to recover. Renaming first turns
// "someone else still needs this" into a clean, harmless error instead —
// path is left completely untouched when that happens.
func removeWorkingFile(path string) error {
	tmp := path + ".removing"
	if err := os.Rename(path, tmp); err != nil {
		return fmt.Errorf("working file %s appears to still be in use by another healthd process (leaving it in place, not touching its contents): %w", path, err)
	}

	if info, err := os.Stat(tmp); err == nil {
		if f, ferr := os.OpenFile(tmp, os.O_WRONLY, 0o600); ferr == nil {
			zeros := make([]byte, info.Size())
			_, _ = f.WriteAt(zeros, 0)
			_ = f.Sync()
			_ = f.Close()
		}
	}
	// WAL mode leaves -wal/-shm sidecar files alongside the main one; only
	// safe to clean those up once the rename above has confirmed no other
	// process still holds the main file (they share the same lock/handle
	// lifecycle under WAL).
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	return os.Remove(tmp)
}

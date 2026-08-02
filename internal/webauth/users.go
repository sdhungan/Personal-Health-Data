// Package webauth is healthd's dashboard login system: account creation,
// password verification, and cookie-backed sessions (see ARCHITECTURE.md's
// multi-user section). It is deliberately separate from provider
// authentication (internal/googleauth, internal/cronometer) — a dashboard
// login only gates *viewing* the data; it has no bearing on whether the
// background sync scheduler can keep that user's data up to date.
package webauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/paths"
)

// User is the minimal identity every handler/sync job needs.
type User struct {
	ID       int64
	Username string
}

// ErrInvalidCredentials covers both "no such username" and "wrong
// password" — deliberately the same error either way so a login form can't
// be used to enumerate valid usernames.
var ErrInvalidCredentials = errors.New("invalid username or password")

// ErrUsernameTaken is returned by CreateUser when username already exists.
var ErrUsernameTaken = errors.New("username is already taken")

// CreateUser creates a new account: a bcrypt hash of password for login
// verification (users.password_hash), and — independently — an Argon2id
// key derived from the same plaintext password with its own fresh salt
// (internal/crypto.GenerateAndSaveKey, the exact call "healthd db init"
// already makes for the root DB key), cached to this user's own keyfile
// under p.UserKeyFile(id). That keyfile is what encrypts this user's
// Google/Cronometer provider credentials — see internal/googleauth and
// internal/cronometer — and is cached to disk (not held only in memory) so
// the background sync scheduler can decrypt and sync this user's data
// unattended, regardless of whether they are currently logged in.
func CreateUser(ctx context.Context, db *sql.DB, p *paths.Paths, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return User{}, errors.New("username must not be empty")
	}
	if len(password) < 8 {
		return User{}, errors.New("password must be at least 8 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hashing password: %w", err)
	}

	res, err := db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash) VALUES (?, ?)
	`, username, string(hash))
	if err != nil {
		if isUniqueConstraintErr(err) {
			return User{}, ErrUsernameTaken
		}
		return User{}, fmt.Errorf("creating user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("reading new user id: %w", err)
	}

	if err := p.EnsureUserDir(id); err != nil {
		return User{}, fmt.Errorf("creating per-user directories: %w", err)
	}
	if _, err := crypto.GenerateAndSaveKey(password, p.UserKeyFile(id)); err != nil {
		return User{}, fmt.Errorf("deriving credential-encryption key: %w", err)
	}

	return User{ID: id, Username: username}, nil
}

// Authenticate verifies username/password against the stored bcrypt hash.
func Authenticate(ctx context.Context, db *sql.DB, username, password string) (User, error) {
	username = strings.TrimSpace(username)

	var u User
	var hash string
	err := db.QueryRowContext(ctx, `
		SELECT id, username, password_hash FROM users WHERE username = ?
	`, username).Scan(&u.ID, &u.Username, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("looking up user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

// CredentialKey loads the per-user key that encrypts this user's Google/
// Cronometer provider credentials (see CreateUser's doc comment).
func CredentialKey(p *paths.Paths, userID int64) (crypto.Key, error) {
	return crypto.LoadKey(p.UserKeyFile(userID))
}

// isUniqueConstraintErr reports whether err is a SQLite UNIQUE constraint
// violation (modernc.org/sqlite doesn't export a typed sentinel for this,
// so — same discipline as everywhere else in this codebase that touches
// driver-specific errors — this checks the message text, not the value).
func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// userDataTables lists every table scoped by user_id (see schema.sql's own
// header comment), deleted explicitly by DeleteUser rather than relying on
// schema.sql's "ON DELETE CASCADE" on every one of these tables' foreign
// key: SQLite's foreign-key enforcement is a per-connection PRAGMA, and
// Go's *sql.DB pools multiple physical connections, so there's no guarantee
// the connection a given statement lands on ever had that PRAGMA set —
// explicit deletes are correct regardless. watch_sleep_stage isn't listed
// here: it has no user_id of its own (scoped transitively through
// watch_sleep_session, see schema.sql's comment on that table) so DeleteUser
// deletes it via a subquery instead, before its parent.
var userDataTables = []string{
	"web_session", "user_profile", "sync_state",
	"watch_daily_summary", "watch_active_minutes_by_level", "watch_active_zone_minutes_by_zone",
	"watch_steps_hourly", "watch_heart_rate_intraday", "watch_blood_glucose_sample",
	"watch_core_body_temperature_sample", "watch_activity_level_segment",
	"watch_exercise_session", "watch_ecg_reading",
	"watch_heart_rate_zone_definition", "watch_heart_rate_zone_minutes", "watch_calories_by_zone",
	"body_measurement",
	"cronometer_daily_nutrition", "cronometer_serving", "cronometer_exercise", "cronometer_biometric", "cronometer_note",
	"daily_journal", "daily_tag",
}

// DeleteUser permanently deletes userID's account: every row of their data
// (see userDataTables), the users row itself, and their on-disk per-user
// credential directory and keyfile (see paths.UserDir/UserKeyFile) —
// leaving no trace of the account behind. This is irreversible; callers are
// responsible for confirming with the account holder before calling it (see
// internal/web's handleAccountDelete, which re-checks the account password
// first) and for tearing down any in-memory state keyed by userID (cached
// syncers, the caller's own session cookie) afterward, since DeleteUser only
// owns the database and on-disk credential state.
func DeleteUser(ctx context.Context, db *sql.DB, p *paths.Paths, userID int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM watch_sleep_stage
		WHERE sleep_session_id IN (SELECT id FROM watch_sleep_session WHERE user_id = ?)
	`, userID); err != nil {
		return fmt.Errorf("deleting watch_sleep_stage: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM watch_sleep_session WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("deleting watch_sleep_session: %w", err)
	}
	for _, table := range userDataTables {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE user_id = ?`, table), userID); err != nil {
			return fmt.Errorf("deleting %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}

	if err := os.RemoveAll(p.UserDir(userID)); err != nil {
		return fmt.Errorf("removing per-user config directory: %w", err)
	}
	if err := os.Remove(p.UserKeyFile(userID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing per-user key: %w", err)
	}
	return nil
}

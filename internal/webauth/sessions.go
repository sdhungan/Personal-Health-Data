package webauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// SessionDuration is the sliding inactivity window: a session is valid as
// long as it has been touched (see LookupSession) within this long. It is
// not a fixed absolute expiry — every authenticated request pushes it back
// out another 24h.
const SessionDuration = 24 * time.Hour

// tokenBytes is the raw entropy of a session token before base64url
// encoding (32 bytes = 256 bits, well above what's needed to make guessing
// infeasible).
const tokenBytes = 32

// timeLayout matches the format internal/syncengine already uses for its
// own app-set (not schema-default) timestamps, for consistency.
const timeLayout = time.RFC3339Nano

// CreateSession issues a new session for userID and returns the raw token
// to set as the browser's cookie value. Only sha256(token) is ever written
// to the database (see schema.sql's web_session comment) — the raw token
// is not recoverable from a database dump.
func CreateSession(ctx context.Context, db *sql.DB, userID int64) (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	now := time.Now().UTC().Format(timeLayout)
	_, err := db.ExecContext(ctx, `
		INSERT INTO web_session (token_hash, user_id, created_at, last_seen_at)
		VALUES (?, ?, ?, ?)
	`, hashToken(token), userID, now, now)
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	return token, nil
}

// LookupSession validates token, reporting the owning user's id/username if
// it's present and has been seen within SessionDuration. A valid lookup
// also touches (slides forward) last_seen_at — this is what makes the 24h
// window inactivity-based rather than a fixed expiry from login time.
// username is looked up in the same query (joined against users) so
// callers needing it for display (e.g. the header's "logged in as ..."
// control) don't need a second round trip.
func LookupSession(ctx context.Context, db *sql.DB, token string) (userID int64, username string, ok bool, err error) {
	if token == "" {
		return 0, "", false, nil
	}
	hash := hashToken(token)

	var lastSeenStr string
	err = db.QueryRowContext(ctx, `
		SELECT web_session.user_id, users.username, web_session.last_seen_at
		FROM web_session JOIN users ON users.id = web_session.user_id
		WHERE token_hash = ?
	`, hash).Scan(&userID, &username, &lastSeenStr)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, fmt.Errorf("looking up session: %w", err)
	}

	lastSeen, err := time.Parse(timeLayout, lastSeenStr)
	if err != nil {
		return 0, "", false, fmt.Errorf("parsing session last_seen_at %q: %w", lastSeenStr, err)
	}
	if time.Since(lastSeen) > SessionDuration {
		// Expired: remove it so it doesn't linger forever, and report "no
		// session" rather than surfacing this as an error to the caller.
		_, _ = db.ExecContext(ctx, `DELETE FROM web_session WHERE token_hash = ?`, hash)
		return 0, "", false, nil
	}

	now := time.Now().UTC().Format(timeLayout)
	if _, err := db.ExecContext(ctx, `UPDATE web_session SET last_seen_at = ? WHERE token_hash = ?`, now, hash); err != nil {
		return 0, "", false, fmt.Errorf("touching session: %w", err)
	}
	return userID, username, true, nil
}

// DeleteSession removes a session (logout). Deleting a token that doesn't
// exist is a no-op, not an error.
func DeleteSession(ctx context.Context, db *sql.DB, token string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM web_session WHERE token_hash = ?`, hashToken(token))
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

// CleanupExpired removes every session that has been inactive for longer
// than SessionDuration. Called opportunistically alongside the existing
// periodic DB checkpoint (see internal/web.Server.Start) rather than on its
// own goroutine/timer — expired rows are harmless to leave around briefly
// (LookupSession already refuses them), this just keeps the table tidy.
func CleanupExpired(ctx context.Context, db *sql.DB) error {
	cutoff := time.Now().UTC().Add(-SessionDuration).Format(timeLayout)
	_, err := db.ExecContext(ctx, `DELETE FROM web_session WHERE last_seen_at < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("cleaning up expired sessions: %w", err)
	}
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

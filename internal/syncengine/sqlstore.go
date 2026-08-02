package syncengine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SQLStore implements StateStore against the sync_state table (see
// internal/db/schema.sql). It works for any source value the table's
// CHECK constraint allows — google_health and cronometer both use the
// same implementation, just with different source strings. UserID scopes
// every query to one user's rows — one SQLStore is constructed per user
// per sync pass (see internal/cli's scheduler fan-out), never shared.
type SQLStore struct {
	DB     *sql.DB
	UserID int64
}

var _ StateStore = (*SQLStore)(nil)

func (s *SQLStore) EnsurePending(ctx context.Context, source, day string) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO sync_state (user_id, source, day, status)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, source, day) DO NOTHING
	`, s.UserID, source, day, StatusPending)
	if err != nil {
		return fmt.Errorf("ensuring sync_state row for user %d %s/%s: %w", s.UserID, source, day, err)
	}
	return nil
}

func (s *SQLStore) SetStatus(ctx context.Context, source, day, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `
		UPDATE sync_state SET status = ?, last_synced_at = ?, updated_at = ?
		WHERE user_id = ? AND source = ? AND day = ?
	`, status, now, now, s.UserID, source, day)
	if err != nil {
		return fmt.Errorf("setting sync_state status for user %d %s/%s: %w", s.UserID, source, day, err)
	}
	return nil
}

func (s *SQLStore) LatestDay(ctx context.Context, source string) (string, bool, error) {
	var day string
	err := s.DB.QueryRowContext(ctx, `
		SELECT day FROM sync_state WHERE user_id = ? AND source = ? ORDER BY day DESC LIMIT 1
	`, s.UserID, source).Scan(&day)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("finding latest sync_state day for user %d %s: %w", s.UserID, source, err)
	}
	return day, true, nil
}

func (s *SQLStore) UnresolvedDays(ctx context.Context, source, beforeDay string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT day FROM sync_state
		WHERE user_id = ? AND source = ? AND day < ? AND status IN (?, ?)
		ORDER BY day ASC
	`, s.UserID, source, beforeDay, StatusPending, StatusPartial)
	if err != nil {
		return nil, fmt.Errorf("listing unresolved sync_state days for user %d %s: %w", s.UserID, source, err)
	}
	defer rows.Close()

	var days []string
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			return nil, fmt.Errorf("scanning unresolved day: %w", err)
		}
		days = append(days, day)
	}
	return days, rows.Err()
}

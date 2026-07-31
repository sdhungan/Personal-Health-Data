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
// same implementation, just with different source strings.
type SQLStore struct {
	DB *sql.DB
}

var _ StateStore = (*SQLStore)(nil)

func (s *SQLStore) EnsurePending(ctx context.Context, source, day string) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO sync_state (source, day, status)
		VALUES (?, ?, ?)
		ON CONFLICT(source, day) DO NOTHING
	`, source, day, StatusPending)
	if err != nil {
		return fmt.Errorf("ensuring sync_state row for %s/%s: %w", source, day, err)
	}
	return nil
}

func (s *SQLStore) SetStatus(ctx context.Context, source, day, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `
		UPDATE sync_state SET status = ?, last_synced_at = ?, updated_at = ?
		WHERE source = ? AND day = ?
	`, status, now, now, source, day)
	if err != nil {
		return fmt.Errorf("setting sync_state status for %s/%s: %w", source, day, err)
	}
	return nil
}

func (s *SQLStore) LatestDay(ctx context.Context, source string) (string, bool, error) {
	var day string
	err := s.DB.QueryRowContext(ctx, `
		SELECT day FROM sync_state WHERE source = ? ORDER BY day DESC LIMIT 1
	`, source).Scan(&day)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("finding latest sync_state day for %s: %w", source, err)
	}
	return day, true, nil
}

func (s *SQLStore) UnresolvedDays(ctx context.Context, source, beforeDay string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT day FROM sync_state
		WHERE source = ? AND day < ? AND status IN (?, ?)
		ORDER BY day ASC
	`, source, beforeDay, StatusPending, StatusPartial)
	if err != nil {
		return nil, fmt.Errorf("listing unresolved sync_state days for %s: %w", source, err)
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

package web

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"time"

	"github.com/yuin/goldmark"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

func fetchJournal(ctx context.Context, db *sql.DB, day string) (views.JournalData, error) {
	var content sql.NullString
	err := db.QueryRowContext(ctx, `SELECT notes FROM daily_journal WHERE day = ?`, day).Scan(&content)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return views.JournalData{}, fmt.Errorf("querying daily_journal for %s: %w", day, err)
	}

	j := views.JournalData{Day: day, AutoSave: true}
	if content.Valid {
		j.Content = content.String
	}
	j.ContentHTML = renderMarkdown(j.Content)
	return j, nil
}

func saveJournal(ctx context.Context, db *sql.DB, day, content string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx, `
		INSERT INTO daily_journal (day, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(day) DO UPDATE SET notes = excluded.notes, updated_at = excluded.updated_at
	`, day, content, now, now)
	if err != nil {
		return fmt.Errorf("saving journal for %s: %w", day, err)
	}
	return nil
}

// renderMarkdown converts basic markdown to HTML for the preview pane. A
// conversion failure (goldmark practically never errors on plain text,
// but we don't trust that blindly) falls back to escaped plain text
// rather than either crashing or emitting unescaped user input as HTML.
func renderMarkdown(src string) string {
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(src), &buf); err != nil {
		return "<pre>" + html.EscapeString(src) + "</pre>"
	}
	return buf.String()
}

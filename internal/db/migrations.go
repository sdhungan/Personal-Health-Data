package db

// Migrations is the ordered list of incremental schema changes applied to a
// database that predates them (see Store.Migrate). Each entry is one
// migration's SQL — possibly several statements — applied inside its own
// transaction; PRAGMA user_version tracks how many have been applied so
// Migrate only ever runs the ones a given database hasn't seen yet.
//
// A fresh database created by Init applies Schema directly (already at the
// shape every migration below produces) and sets user_version to
// len(Migrations), skipping this list entirely — these are only for
// upgrading a database that already has data in it.
//
// First real migration (2026-08-05): adds mcp_token, needed for the MCP
// connector's move from a spawned stdio subprocess to an HTTP route on the
// always-on server (see internal/webauth/mcptoken.go, internal/web/mcp.go).
// Unlike the watch_* schema redesign noted below, this one runs as a real
// migration rather than "just re-init a fresh root" — a root can already
// have real synced data and user accounts in it by the time this ships.
var Migrations = []string{
	`CREATE TABLE mcp_token (
		user_id    INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
		token_hash TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`,
	`CREATE TABLE dashboard_tile_layout (
		user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
		kind     TEXT NOT NULL,
		position INTEGER,
		width    INTEGER CHECK (width BETWEEN 1 AND 5),
		PRIMARY KEY (user_id, kind)
	)`,
	`ALTER TABLE dashboard_tile_layout ADD COLUMN height INTEGER CHECK (height BETWEEN 120 AND 1200)`,
	// The dashboard moved from an auto-fill grid (5 columns at full width)
	// to a fixed 12-column one: rebuild the table (SQLite can't alter a
	// CHECK constraint) and rescale saved widths from fifths to twelfths.
	`CREATE TABLE dashboard_tile_layout_new (
		user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
		kind     TEXT NOT NULL,
		position INTEGER,
		width    INTEGER CHECK (width BETWEEN 1 AND 12),
		height   INTEGER CHECK (height BETWEEN 120 AND 1200),
		PRIMARY KEY (user_id, kind)
	);
	INSERT INTO dashboard_tile_layout_new (user_id, kind, position, width, height)
		SELECT user_id, kind, position, CAST(ROUND(width * 12.0 / 5) AS INTEGER), height FROM dashboard_tile_layout;
	DROP TABLE dashboard_tile_layout;
	ALTER TABLE dashboard_tile_layout_new RENAME TO dashboard_tile_layout`,
	// Per-account open/closed state, NULL = the tile's built-in default.
	`ALTER TABLE dashboard_tile_layout ADD COLUMN expanded INTEGER`,
	// Exercise GPS routes (see schema.sql's watch_exercise_session).
	`ALTER TABLE watch_exercise_session ADD COLUMN data_point_id TEXT;
	ALTER TABLE watch_exercise_session ADD COLUMN has_gps INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE watch_exercise_session ADD COLUMN route_json TEXT`,
	// Data fix, not a schema change: distance, floors, and active minutes
	// were decoded from the wrong API field names and stored as 0 for every
	// day (see googlehealth.DistanceRollup/FloorsRollup/ActiveMinutesByLevel).
	// Re-open every finished Google Health day so the scheduler's next pass
	// re-syncs history with the corrected parsing.
	`UPDATE sync_state SET status = 'pending' WHERE source = 'google_health' AND status IN ('complete', 'missing')`,
	// Data fix: sleep moved from the day a session starts to the day it
	// ends (wake-up day). Rows filed under the old day would linger next to
	// the re-filed ones, so drop them (sync owns these tables outright; no
	// UI writes them) and clear the stale daily totals — every Google
	// Health day is already pending re-sync (previous migration), which
	// rebuilds both.
	`DELETE FROM watch_sleep_stage;
	DELETE FROM watch_sleep_session;
	UPDATE watch_daily_summary SET sleep_duration_minutes = NULL`,
}

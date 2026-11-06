package db

// Migrations are incremental schema/data changes for a database that already
// has data in it, applied in order inside their own transaction each (see
// Store.migrate). PRAGMA user_version holds the absolute version a database
// is at; Migrations[i] upgrades it from migrationBase+i to migrationBase+i+1.
//
// A fresh database created by Init applies Schema directly (already the
// shape every migration produces) and starts at the latest version, so this
// list is only ever for upgrading existing databases. Once every database
// that matters has applied an entry, fold it into schema.sql, drop it here
// and raise migrationBase by the same count.
var Migrations = []string{
	// The MCP connector's bearer-token table, left over from the reverted
	// move of the connector to an HTTP route; nothing reads it.
	`DROP TABLE IF EXISTS mcp_token`,

	// watch_heart_rate_intraday.recorded_at: RFC3339 text -> INTEGER Unix
	// seconds. Lossless: the sync only ever wrote whole seconds. A value
	// that doesn't parse yields NULL, which the NOT NULL column rejects,
	// failing (and rolling back) the whole migration rather than dropping
	// the row. Values already INTEGER (a fresh schema) are kept as-is.
	`CREATE TABLE watch_heart_rate_intraday_new (
	    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	    recorded_at INTEGER NOT NULL,
	    bpm         REAL NOT NULL,
	    cached_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	    PRIMARY KEY (user_id, recorded_at)
	);
	INSERT INTO watch_heart_rate_intraday_new (user_id, recorded_at, bpm, cached_at)
	    SELECT user_id, CASE typeof(recorded_at) WHEN 'integer' THEN recorded_at ELSE CAST(strftime('%s', recorded_at) AS INTEGER) END, bpm, cached_at
	    FROM watch_heart_rate_intraday;
	DROP TABLE watch_heart_rate_intraday;
	ALTER TABLE watch_heart_rate_intraday_new RENAME TO watch_heart_rate_intraday;
	CREATE INDEX idx_watch_heart_rate_intraday_cached_at ON watch_heart_rate_intraday (cached_at);`,

	// watch_heart_rate_intraday: drop cached_at and its index (nothing ever
	// pruned by it) and make the table WITHOUT ROWID, so the rows live in
	// the primary-key b-tree itself instead of a rowid table plus a
	// separate primary-key index. Dropping the old table drops its index.
	`CREATE TABLE watch_heart_rate_intraday_new (
	    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	    recorded_at INTEGER NOT NULL,
	    bpm         REAL NOT NULL,
	    PRIMARY KEY (user_id, recorded_at)
	) WITHOUT ROWID;
	INSERT INTO watch_heart_rate_intraday_new (user_id, recorded_at, bpm)
	    SELECT user_id, recorded_at, bpm FROM watch_heart_rate_intraday;
	DROP TABLE watch_heart_rate_intraday;
	ALTER TABLE watch_heart_rate_intraday_new RENAME TO watch_heart_rate_intraday;`,

	// Sleep was fetched with an unfiltered list() that only returns the
	// newest page, so after the wake-up-day re-file only recent sleep came
	// back. The sync now filters by end time; re-open every resolved Google
	// day with no sleep session so the scheduler re-syncs it. Days that
	// truly had no sleep just resolve again.
	`UPDATE sync_state SET status = 'pending'
	WHERE source = 'google_health' AND status IN ('complete', 'missing')
	  AND NOT EXISTS (SELECT 1 FROM watch_sleep_session s
	                  WHERE s.user_id = sync_state.user_id AND s.day = sync_state.day)`,
}

// migrationBase is the version every database had reached when its earlier
// migrations were folded into schema.sql (2026-10-27: mcp_token, the
// dashboard tile layout table and its later columns, exercise GPS route
// columns, and the one-off distance/floors/active-minutes and sleep-day
// data fixes). A database below it predates those and can't be upgraded by
// this binary — see Store.migrate.
const migrationBase = 8

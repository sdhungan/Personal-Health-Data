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
}

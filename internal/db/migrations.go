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
}

// migrationBase is the version every database had reached when its earlier
// migrations were folded into schema.sql (2026-10-27: mcp_token, the
// dashboard tile layout table and its later columns, exercise GPS route
// columns, and the one-off distance/floors/active-minutes and sleep-day
// data fixes). A database below it predates those and can't be upgraded by
// this binary — see Store.migrate.
const migrationBase = 8

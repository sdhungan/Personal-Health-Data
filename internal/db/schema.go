// Package db holds the healthd database schema and (eventually) the
// raw_value/override_value access layer described in ARCHITECTURE.md §3.
package db

import _ "embed"

// Schema is the initial database schema, executed once against a fresh
// database by "healthd db init". Later structural changes should become
// versioned migration files once a migration story is wired in; this is
// deliberately the single up-front schema for a project with no data yet.
//
//go:embed schema.sql
var Schema string

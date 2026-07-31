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
// Empty as of the watch_* schema redesign (see cronometer-integration.md):
// this project is still in early development with no real deployed
// database to preserve, so that redesign went straight into schema.sql
// instead of an upgrade path — re-run "healthd db init" against a fresh
// root rather than migrating an old one. The mechanism itself stays ready
// for real migrations once this matters (a real user's data on disk).
var Migrations = []string{}

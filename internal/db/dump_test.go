package db

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestDumpRoundTripsThroughFreshDatabase(t *testing.T) {
	dir := t.TempDir()
	encPath := filepath.Join(dir, "health.db.enc")
	workPath := filepath.Join(dir, ".health.db.work")
	key := testKey(t)

	if err := Init(encPath, workPath, key); err != nil {
		t.Fatalf("Init: %v", err)
	}
	store, err := Open(encPath, workPath, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, err := store.DB().Exec(
		`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x')`,
	); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	if _, err := store.DB().Exec(
		`INSERT INTO user_profile (user_id, sex, birth_date) VALUES (1, 'male', '1990-05-17')`,
	); err != nil {
		t.Fatalf("insert user_profile: %v", err)
	}
	if _, err := store.DB().Exec(
		`INSERT INTO body_measurement (user_id, day, weight_kg_raw, waist_cm, neck_cm) VALUES (1, '2026-07-30', 79.4, 84.0, NULL)`,
	); err != nil {
		t.Fatalf("insert body_measurement: %v", err)
	}
	if _, err := store.DB().Exec(
		`INSERT INTO daily_tag (user_id, day, tag) VALUES (1, '2026-07-30', 'travel')`,
	); err != nil {
		t.Fatalf("insert daily_tag: %v", err)
	}

	var buf bytes.Buffer
	if err := Dump(store.DB(), &buf); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if err := store.Discard(); err != nil {
		t.Fatalf("Discard: %v", err)
	}

	fresh, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open fresh sqlite: %v", err)
	}
	defer fresh.Close()

	if _, err := fresh.Exec(buf.String()); err != nil {
		t.Fatalf("executing dumped SQL: %v\n--- dump ---\n%s", err, buf.String())
	}

	var sex, birthDate string
	if err := fresh.QueryRow(`SELECT sex, birth_date FROM user_profile WHERE user_id = 1`).Scan(&sex, &birthDate); err != nil {
		t.Fatalf("querying restored user_profile: %v", err)
	}
	if sex != "male" || birthDate != "1990-05-17" {
		t.Errorf("user_profile = (%q, %q), want (male, 1990-05-17)", sex, birthDate)
	}

	var weight, waist float64
	var neck sql.NullFloat64
	err = fresh.QueryRow(`SELECT weight_kg_raw, waist_cm, neck_cm FROM body_measurement WHERE day = '2026-07-30'`).
		Scan(&weight, &waist, &neck)
	if err != nil {
		t.Fatalf("querying restored body_measurement: %v", err)
	}
	if weight != 79.4 || waist != 84.0 || neck.Valid {
		t.Errorf("body_measurement = (%v, %v, valid=%v), want (79.4, 84, NULL)", weight, waist, neck.Valid)
	}

	var tag string
	if err := fresh.QueryRow(`SELECT tag FROM daily_tag WHERE day = '2026-07-30'`).Scan(&tag); err != nil {
		t.Fatalf("querying restored daily_tag: %v", err)
	}
	if tag != "travel" {
		t.Errorf("tag = %q, want %q", tag, "travel")
	}
}

func TestDiscardDoesNotCheckpoint(t *testing.T) {
	dir := t.TempDir()
	encPath := filepath.Join(dir, "health.db.enc")
	workPath := filepath.Join(dir, ".health.db.work")
	key := testKey(t)

	if err := Init(encPath, workPath, key); err != nil {
		t.Fatalf("Init: %v", err)
	}
	before, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatalf("reading encPath: %v", err)
	}

	store, err := Open(encPath, workPath, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := store.DB().Exec(
		`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x');
		 INSERT INTO user_profile (user_id, sex) VALUES (1, 'male')`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.Discard(); err != nil {
		t.Fatalf("Discard: %v", err)
	}

	after, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatalf("reading encPath: %v", err)
	}
	if string(before) != string(after) {
		t.Error("Discard modified the encrypted file on disk; it must leave writes uncheckpointed")
	}
}

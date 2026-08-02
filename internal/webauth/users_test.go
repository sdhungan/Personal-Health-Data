package webauth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/sdhungan/Personal-Health-Data/internal/paths"
)

func testPaths(t *testing.T) *paths.Paths {
	t.Helper()
	p, err := paths.Resolve(filepath.Join(t.TempDir(), "root"))
	if err != nil {
		t.Fatalf("resolving test root: %v", err)
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	return p
}

func TestCreateUserThenAuthenticate(t *testing.T) {
	conn := newTestDB(t) // has user id=1 "test"/"x" already seeded; fine to also create more
	p := testPaths(t)
	ctx := context.Background()

	u, err := CreateUser(ctx, conn, p, "alice", "correcthorsebattery")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.Username != "alice" {
		t.Errorf("Username = %q, want alice", u.Username)
	}

	got, err := Authenticate(ctx, conn, "alice", "correcthorsebattery")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("Authenticate returned id %d, want %d", got.ID, u.ID)
	}

	if _, err := Authenticate(ctx, conn, "alice", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate with wrong password: err = %v, want ErrInvalidCredentials", err)
	}
	if _, err := Authenticate(ctx, conn, "nobody", "whatever"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate with unknown username: err = %v, want ErrInvalidCredentials", err)
	}
}

func TestCreateUserRejectsDuplicateUsername(t *testing.T) {
	conn := newTestDB(t)
	p := testPaths(t)
	ctx := context.Background()

	if _, err := CreateUser(ctx, conn, p, "bob", "correcthorsebattery"); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	if _, err := CreateUser(ctx, conn, p, "bob", "different-password"); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("second CreateUser: err = %v, want ErrUsernameTaken", err)
	}
}

func TestCreateUserDerivesAPerUserCredentialKey(t *testing.T) {
	conn := newTestDB(t)
	p := testPaths(t)
	ctx := context.Background()

	u, err := CreateUser(ctx, conn, p, "carol", "correcthorsebattery")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	key, err := CredentialKey(p, u.ID)
	if err != nil {
		t.Fatalf("CredentialKey: %v", err)
	}
	var zero [32]byte
	if [32]byte(key) == zero {
		t.Error("CredentialKey returned an all-zero key")
	}
}

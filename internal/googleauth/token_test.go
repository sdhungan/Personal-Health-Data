package googleauth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
)

func testKey() crypto.Key {
	var key crypto.Key
	copy(key[:], []byte("0123456789abcdef0123456789abcdef"))
	return key
}

func TestSaveLoadTokenRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "google_oauth.json.enc")
	key := testKey()

	want := &oauth2.Token{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour).Truncate(time.Second),
	}

	if err := SaveToken(path, key, want); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	got, err := LoadToken(path, key)
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}

	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || got.TokenType != want.TokenType {
		t.Errorf("LoadToken = %+v, want %+v", got, want)
	}
	if !got.Expiry.Equal(want.Expiry) {
		t.Errorf("Expiry = %v, want %v", got.Expiry, want.Expiry)
	}
}

func TestLoadTokenWrongKeyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "google_oauth.json.enc")
	key := testKey()

	if err := SaveToken(path, key, &oauth2.Token{AccessToken: "x"}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	var wrongKey crypto.Key
	copy(wrongKey[:], []byte("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"))

	if _, err := LoadToken(path, wrongKey); err == nil {
		t.Error("LoadToken with wrong key: expected error, got none")
	}
}

// fakeTokenSource cycles through a fixed list of tokens, one per call, to
// simulate a refresh happening on some calls but not others.
type fakeTokenSource struct {
	tokens []*oauth2.Token
	i      int
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	tok := f.tokens[f.i]
	if f.i < len(f.tokens)-1 {
		f.i++
	}
	return tok, nil
}

func TestSavingTokenSourceOnlyPersistsOnChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "google_oauth.json.enc")
	key := testKey()

	tokenA := &oauth2.Token{AccessToken: "a"}
	tokenB := &oauth2.Token{AccessToken: "b"}

	src := &savingTokenSource{
		base:     &fakeTokenSource{tokens: []*oauth2.Token{tokenA, tokenA, tokenB, tokenB}},
		path:     path,
		key:      key,
		lastSeen: "",
	}

	for i, want := range []string{"a", "a", "b", "b"} {
		tok, err := src.Token()
		if err != nil {
			t.Fatalf("call %d: Token(): %v", i, err)
		}
		if tok.AccessToken != want {
			t.Fatalf("call %d: AccessToken = %q, want %q", i, tok.AccessToken, want)
		}

		saved, err := LoadToken(path, key)
		if err != nil {
			t.Fatalf("call %d: LoadToken: %v", i, err)
		}
		if saved.AccessToken != want {
			t.Errorf("call %d: persisted AccessToken = %q, want %q", i, saved.AccessToken, want)
		}
	}
}

func TestIsInvalidGrant(t *testing.T) {
	grantErr := &oauth2.RetrieveError{ErrorCode: "invalid_grant", ErrorDescription: "Token has been expired or revoked."}
	wrapped := fmt.Errorf("calling GET https://x: %w", fmt.Errorf("refreshing Google access token: %w", grantErr))
	if !IsInvalidGrant(wrapped) {
		t.Error("IsInvalidGrant(wrapped invalid_grant) = false, want true")
	}

	other := &oauth2.RetrieveError{ErrorCode: "invalid_client"}
	if IsInvalidGrant(other) {
		t.Error("IsInvalidGrant(invalid_client) = true, want false")
	}
	if IsInvalidGrant(errors.New("some other failure")) {
		t.Error("IsInvalidGrant(plain error) = true, want false")
	}
	if IsInvalidGrant(nil) {
		t.Error("IsInvalidGrant(nil) = true, want false")
	}
}

// failingTokenSource always fails a refresh with the given error.
type failingTokenSource struct{ err error }

func (f *failingTokenSource) Token() (*oauth2.Token, error) { return nil, f.err }

func TestSavingTokenSourceDeletesFileOnInvalidGrant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "google_oauth.json.enc")
	key := testKey()
	if err := SaveToken(path, key, &oauth2.Token{AccessToken: "stale"}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	src := &savingTokenSource{
		base: &failingTokenSource{err: &oauth2.RetrieveError{ErrorCode: "invalid_grant"}},
		path: path,
		key:  key,
	}
	if _, err := src.Token(); err == nil {
		t.Fatal("Token(): expected error, got none")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("token file still exists after invalid_grant, stat err = %v", err)
	}
}

func TestSavingTokenSourceKeepsFileOnOtherErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "google_oauth.json.enc")
	key := testKey()
	if err := SaveToken(path, key, &oauth2.Token{AccessToken: "still-good"}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	src := &savingTokenSource{
		base: &failingTokenSource{err: errors.New("temporary network blip")},
		path: path,
		key:  key,
	}
	if _, err := src.Token(); err == nil {
		t.Fatal("Token(): expected error, got none")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("token file should survive a non-invalid_grant error, stat err = %v", err)
	}
}

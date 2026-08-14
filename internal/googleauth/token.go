package googleauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"

	"golang.org/x/oauth2"

	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
)

// SaveToken encrypts token with key and writes it to path (see
// ARCHITECTURE.md §5: the refresh token is the sensitive long-lived
// credential here, so it gets the same protection as the health data).
func SaveToken(path string, key crypto.Key, token *oauth2.Token) error {
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshaling token: %w", err)
	}
	blob, err := crypto.Encrypt(key, data)
	if err != nil {
		return fmt.Errorf("encrypting token: %w", err)
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return os.Chmod(path, 0o600)
}

// LoadToken reads and decrypts a token previously written by SaveToken.
func LoadToken(path string, key crypto.Key) (*oauth2.Token, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	data, err := crypto.Decrypt(key, blob)
	if err != nil {
		return nil, fmt.Errorf("decrypting %s: %w", path, err)
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parsing token from %s: %w", path, err)
	}
	return &token, nil
}

// savingTokenSource wraps an oauth2.TokenSource and persists whenever the
// access token changes (i.e. a refresh actually happened), so an
// unattended sync run that silently refreshes never loses the new token.
type savingTokenSource struct {
	mu       sync.Mutex
	base     oauth2.TokenSource
	path     string
	key      crypto.Key
	lastSeen string
}

// IsInvalidGrant reports whether err is (or wraps, per errors.As) an oauth2
// invalid_grant response — Google's signal that a refresh token is dead
// (expired, revoked from myaccount.google.com, or issued to an OAuth client
// that no longer exists) rather than a transient failure. There is no retry
// that fixes this; the only way forward is a fresh consent flow. Callers
// that cache a built syncer/client keyed on "token file present" (see
// web.Server's googleSyncers) must evict that cache on this signal too, not
// just react to the error — otherwise they keep reporting "connected" for a
// token that will never work again.
func IsInvalidGrant(err error) bool {
	var retrieveErr *oauth2.RetrieveError
	return errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "invalid_grant"
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := s.base.Token()
	if err != nil {
		// "connected" elsewhere (userGoogleSyncer) just means "a token file
		// exists and parses" — harmless until Google actually confirms the
		// refresh token itself is dead (invalid_grant), at which point
		// leaving the file in place is actively wrong: every future check
		// would keep reporting "connected" for a token that will never
		// work again. Delete it so that check goes stale-but-safe instead
		// of stale-but-wrong — best-effort, a delete failure shouldn't mask
		// the real refresh error.
		if IsInvalidGrant(err) {
			_ = os.Remove(s.path)
		}
		return nil, fmt.Errorf("refreshing Google access token (you may need to re-run \"healthd auth google\"): %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if tok.AccessToken != s.lastSeen {
		if err := SaveToken(s.path, s.key, tok); err != nil {
			return nil, fmt.Errorf("persisting refreshed token: %w", err)
		}
		s.lastSeen = tok.AccessToken
	}
	return tok, nil
}

// HTTPClient loads the saved token and returns an *http.Client that
// authenticates Google Health API requests, transparently refreshing (and
// re-persisting) the access token as needed. clientJSON is the app-wide
// OAuth client (see LoadClientJSON); callbackPort is config.yaml's
// google.callback_port — both are only used to rebuild the oauth2.Config
// a refresh needs, the callback listener itself isn't involved here.
func HTTPClient(ctx context.Context, tokenPath string, key crypto.Key, clientJSON []byte, callbackPort int) (*http.Client, error) {
	oauthCfg, err := OAuthConfig(clientJSON, callbackPort)
	if err != nil {
		return nil, err
	}

	token, err := LoadToken(tokenPath, key)
	if err != nil {
		return nil, fmt.Errorf("loading saved Google token (run \"healthd auth google\" first?): %w", err)
	}

	source := &savingTokenSource{
		base:     oauthCfg.TokenSource(ctx, token),
		path:     tokenPath,
		key:      key,
		lastSeen: token.AccessToken,
	}
	return oauth2.NewClient(ctx, source), nil
}

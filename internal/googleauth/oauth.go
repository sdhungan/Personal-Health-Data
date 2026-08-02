package googleauth

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/oauth2"
	xgoogle "golang.org/x/oauth2/google"

	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
)

// ErrMissingClientCredentials is returned when no Google OAuth client JSON
// has been uploaded yet (see SaveClientJSON) — this is an app-wide setting
// (one Google Cloud OAuth client legitimately serves every account's own
// Google login through Google's own consent screen), not a per-user
// credential, so it's set once for the whole healthd instance rather than
// by each account individually.
var ErrMissingClientCredentials = errors.New(
	"no Google OAuth client credentials uploaded yet\n" +
		"  Register a project at https://console.cloud.google.com, enable the Google Health API,\n" +
		"  create an OAuth client, add yourself as a test user, download its client JSON, and\n" +
		"  upload it from the dashboard's Google settings page (or \"healthd google-client set <path>\").")

// LoadClientJSON reads and decrypts the Google OAuth client JSON previously
// written by SaveClientJSON. A missing file is reported as
// ErrMissingClientCredentials so callers can show a clear, actionable
// message instead of a raw os.ErrNotExist.
func LoadClientJSON(path string, key crypto.Key) ([]byte, error) {
	blob, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrMissingClientCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	data, err := crypto.Decrypt(key, blob)
	if err != nil {
		return nil, fmt.Errorf("decrypting %s: %w", path, err)
	}
	return data, nil
}

// SaveClientJSON validates that data actually parses as a Google OAuth
// client JSON (the standard client_secret_*.json format downloaded from
// Google Cloud Console — either "installed"/Desktop or "web" client type)
// before encrypting and saving it to path, so an accidental wrong-file
// upload fails immediately with a clear error rather than silently
// breaking every account's "Connect Google Health" button later.
func SaveClientJSON(path string, key crypto.Key, data []byte) error {
	if _, err := xgoogle.ConfigFromJSON(data, Scopes...); err != nil {
		return fmt.Errorf("not a valid Google OAuth client JSON: %w", err)
	}

	blob, err := crypto.Encrypt(key, data)
	if err != nil {
		return fmt.Errorf("encrypting client JSON: %w", err)
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return os.Chmod(path, 0o600)
}

// OAuthConfig builds the oauth2.Config used for the Google Health API from
// clientJSON (see LoadClientJSON). The redirect URL is always overridden to
// point at healthd's local callback listener: for a Desktop-type client
// (redirect_uris: ["http://localhost"], no port), Google accepts any
// localhost port/path at redirect time.
func OAuthConfig(clientJSON []byte, callbackPort int) (*oauth2.Config, error) {
	oauthCfg, err := xgoogle.ConfigFromJSON(clientJSON, Scopes...)
	if err != nil {
		return nil, fmt.Errorf("parsing Google OAuth client JSON: %w", err)
	}
	oauthCfg.RedirectURL = fmt.Sprintf("http://localhost:%d/callback", callbackPort)

	return oauthCfg, nil
}

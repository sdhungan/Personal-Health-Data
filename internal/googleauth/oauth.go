package googleauth

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	xgoogle "golang.org/x/oauth2/google"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
)

// ErrMissingClientCredentials is returned when config.yaml has no Google
// OAuth credentials file configured yet.
var ErrMissingClientCredentials = errors.New(
	"google.credentials_file is not set in config.yaml\n" +
		"  Register a project at https://console.cloud.google.com, enable the Google Health API,\n" +
		"  create an OAuth client, add yourself as a test user, download its client JSON, and\n" +
		"  point google.credentials_file at it.")

// OAuthConfig builds the oauth2.Config used for the Google Health API from
// the user's own registered OAuth client (the standard client_secret_*.json
// downloaded from Google Cloud Console — either "installed"/Desktop or
// "web" type; google.ConfigFromJSON handles both). The redirect URL is
// always overridden to point at healthd's local callback listener: for a
// Desktop-type client (redirect_uris: ["http://localhost"], no port),
// Google accepts any localhost port/path at redirect time.
func OAuthConfig(cfg *config.Config) (*oauth2.Config, error) {
	if cfg.Google.CredentialsFile == "" {
		return nil, ErrMissingClientCredentials
	}

	data, err := os.ReadFile(cfg.Google.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("reading Google credentials file %s: %w", cfg.Google.CredentialsFile, err)
	}

	oauthCfg, err := xgoogle.ConfigFromJSON(data, Scopes...)
	if err != nil {
		return nil, fmt.Errorf("parsing Google credentials file %s: %w", cfg.Google.CredentialsFile, err)
	}
	oauthCfg.RedirectURL = fmt.Sprintf("http://localhost:%d/callback", cfg.Google.CallbackPort)

	return oauthCfg, nil
}

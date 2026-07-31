// Package config loads config.yaml (see ARCHITECTURE.md §4): ports, the
// sync interval, and the Google Cloud OAuth client the user must obtain
// themselves. Cronometer credentials are deliberately not here — see
// "healthd auth cronometer" — they go straight to an encrypted file, never
// plaintext config.yaml.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the parsed contents of config.yaml, merged over Default().
type Config struct {
	Port                int          `yaml:"port"`
	SyncIntervalMinutes int          `yaml:"sync_interval_minutes"`
	Google              GoogleConfig `yaml:"google"`
}

// GoogleConfig points at the OAuth client credentials for the Google
// Health API. These come from a Google Cloud project the user registers
// themselves (Google Health API scopes are Restricted, so this can't be a
// credential healthd ships with) — see the "auth google" command's error
// message for setup pointers.
type GoogleConfig struct {
	// CredentialsFile is the path to the OAuth client JSON downloaded from
	// Google Cloud Console ("Download JSON" on the client's page) — the
	// standard client_secret_*.json format, parsed with
	// golang.org/x/oauth2/google.ConfigFromJSON. Relative paths resolve
	// against the current working directory.
	CredentialsFile string `yaml:"credentials_file"`
	// CallbackPort is the local port healthd's callback listener binds to.
	// For a Desktop/"installed"-type OAuth client (redirect_uris:
	// ["http://localhost"] with no port), Google accepts any localhost
	// port/path at redirect time, so this doesn't need to be registered
	// anywhere — it just needs to stay free on this machine.
	CallbackPort int `yaml:"callback_port"`
}

// Default returns the configuration used when config.yaml is absent or a
// field is left unset.
func Default() *Config {
	return &Config{
		Port:                8080,
		SyncIntervalMinutes: 30,
		Google: GoogleConfig{
			CallbackPort: 9876,
		},
	}
}

// Load reads config.yaml at path, if present, and overlays it onto
// Default(). A missing file is not an error — commands that need
// credentials the defaults don't provide (e.g. "auth google") are
// responsible for checking for and reporting that themselves.
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return cfg, nil
}

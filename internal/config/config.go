// Package config loads config.yaml (see ARCHITECTURE.md §4): ports and the
// sync interval. Neither the Google OAuth client nor Cronometer credentials
// live here — see internal/googleauth.SaveClientJSON / "healthd auth
// cronometer" — they go straight to an encrypted file, never plaintext
// config.yaml.
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

// GoogleConfig holds config.yaml-level Google settings that aren't secret
// material — the OAuth client itself (client_id/client_secret) is uploaded
// through the dashboard's settings page instead and stored encrypted (see
// internal/googleauth.SaveClientJSON, internal/paths.GoogleClientSecretFile).
type GoogleConfig struct {
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

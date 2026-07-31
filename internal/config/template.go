package config

import (
	"fmt"
	"os"
)

// template is written by "healthd db init" the first time, so there's an
// actual file to edit when a command (e.g. "auth google") reports missing
// credentials.
const template = `# healthd configuration (see ARCHITECTURE.md §4)

port: 8080
sync_interval_minutes: 30

# Google Health API OAuth client. Register a project at
# https://console.cloud.google.com, enable the Google Health API, create an
# OAuth client, add yourself as a test user, download its client JSON, and
# point credentials_file at it (keep it out of version control).
google:
  credentials_file: ""
  callback_port: 9876

# Cronometer credentials are not stored here -- run "healthd auth
# cronometer" instead, which encrypts them at rest the same way the Google
# OAuth token is protected.
`

// WriteTemplateIfMissing writes the starter config.yaml to path unless a
// file already exists there.
func WriteTemplateIfMissing(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking %s: %w", path, err)
	}
	return os.WriteFile(path, []byte(template), 0o600)
}

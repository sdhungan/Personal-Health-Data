package cronometer

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
)

// Credentials is the Cronometer username/password, encrypted at rest the
// same way internal/googleauth encrypts the Google OAuth token (see
// ARCHITECTURE.md §5) — there's no OAuth here, so the raw credentials
// themselves are the long-lived secret a background sync needs to re-login
// with whenever the cached Session expires.
type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Session is the short-lived result of a successful login — cached
// separately from Credentials so a sync run doesn't have to log in (and
// risk Cronometer's per-account login rate limit) on every pass.
type Session struct {
	UserID   int    `json:"user_id"`
	Token    string `json:"token"`
	Timezone string `json:"timezone"`
}

// SaveCredentials encrypts creds with key and writes it to path.
func SaveCredentials(path string, key crypto.Key, creds *Credentials) error {
	return saveEncrypted(path, key, creds)
}

// LoadCredentials reads and decrypts credentials previously written by
// SaveCredentials.
func LoadCredentials(path string, key crypto.Key) (*Credentials, error) {
	var creds Credentials
	if err := loadEncrypted(path, key, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// SaveSession encrypts sess with key and writes it to path.
func SaveSession(path string, key crypto.Key, sess *Session) error {
	return saveEncrypted(path, key, sess)
}

// LoadSession reads and decrypts a session previously written by
// SaveSession. A missing file is reported via the wrapped os error so
// callers can distinguish "never logged in yet" (errors.Is(err,
// fs.ErrNotExist)) from a genuine read/decrypt failure.
func LoadSession(path string, key crypto.Key) (*Session, error) {
	var sess Session
	if err := loadEncrypted(path, key, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func saveEncrypted(path string, key crypto.Key, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshaling %T: %w", v, err)
	}
	blob, err := crypto.Encrypt(key, data)
	if err != nil {
		return fmt.Errorf("encrypting %T: %w", v, err)
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return os.Chmod(path, 0o600)
}

func loadEncrypted(path string, key crypto.Key, v any) error {
	blob, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	data, err := crypto.Decrypt(key, blob)
	if err != nil {
		return fmt.Errorf("decrypting %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}

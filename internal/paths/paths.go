// Package paths resolves the healthd root directory (see ARCHITECTURE.md
// §4) and provides the only place in the codebase that builds filesystem
// paths under it, so every caller — sync job, CLI, DB layer — agrees on
// where things live without reimplementing path handling.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// dirPerm is applied to every directory healthd creates under its root.
// Everything under root is either health data, credentials, or key
// material, so the default posture is owner-only rather than picking
// permissions per directory.
const dirPerm = 0o700

// Paths resolves and provides access to every file and directory the
// healthd binary owns under its root directory.
type Paths struct {
	root string
}

// Resolve determines the root directory to use. An empty root falls back
// to "~/.healthd" via os.UserHomeDir, which resolves correctly on Linux,
// Darwin, and Windows without any OS-specific branching here. A leading
// "~" in an explicit root is expanded against the home directory too,
// since not every shell expands it for us (notably Windows'). The result
// is always cleaned and absolute.
func Resolve(root string) (*Paths, error) {
	root = strings.TrimSpace(root)

	switch {
	case root == "":
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolving home directory: %w", err)
		}
		root = filepath.Join(home, ".healthd")
	case root == "~":
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolving home directory: %w", err)
		}
		root = home
	default:
		if rest, ok := cutTilde(root); ok {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("resolving home directory: %w", err)
			}
			root = filepath.Join(home, rest)
		}
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root %q: %w", root, err)
	}

	return &Paths{root: filepath.Clean(abs)}, nil
}

// cutTilde reports whether root begins with a "~/" or "~\" prefix and
// returns the remainder if so.
func cutTilde(root string) (string, bool) {
	if rest, ok := strings.CutPrefix(root, "~/"); ok {
		return rest, true
	}
	if rest, ok := strings.CutPrefix(root, `~\`); ok {
		return rest, true
	}
	return "", false
}

// expandUser resolves a leading "~" (alone, or as "~/..." / `~\...`)
// against the user's home directory. Inputs without a leading "~" are
// returned unchanged.
func expandUser(input string) (string, error) {
	if input == "~" {
		return os.UserHomeDir()
	}
	if rest, ok := cutTilde(input); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home directory: %w", err)
		}
		return filepath.Join(home, rest), nil
	}
	return input, nil
}

// Root returns the resolved, absolute root directory.
func (p *Paths) Root() string { return p.root }

// Directory accessors (see ARCHITECTURE.md §4).
func (p *Paths) DBDir() string      { return filepath.Join(p.root, "db") }
func (p *Paths) ConfigDir() string  { return filepath.Join(p.root, "config") }
func (p *Paths) LogsDir() string    { return filepath.Join(p.root, "logs") }
func (p *Paths) KeysDir() string    { return filepath.Join(p.root, "keys") }
func (p *Paths) ServiceDir() string { return filepath.Join(p.root, "service") }

// File accessors (see ARCHITECTURE.md §4).
func (p *Paths) DBFile() string { return filepath.Join(p.DBDir(), "health.db.enc") }

// DBWorkingFile is the plaintext SQLite file healthd decrypts health.db.enc
// into for the duration of a run (see internal/db.Store) and re-encrypts on
// checkpoint/close. It lives inside the same owner-only db/ directory. Its
// presence at startup signals an unclean shutdown last time.
func (p *Paths) DBWorkingFile() string { return filepath.Join(p.DBDir(), ".health.db.work") }
func (p *Paths) ConfigFile() string    { return filepath.Join(p.ConfigDir(), "config.yaml") }
func (p *Paths) GoogleOAuthFile() string {
	return filepath.Join(p.ConfigDir(), "google_oauth.json.enc")
}

// GoogleClientSecretFile holds the Google Cloud OAuth *client* (client_id +
// client_secret) — the app-wide credential registered once per Google
// Cloud project, encrypted with the root DB key (crypto.LoadKey(p.DBKeyFile())),
// not any one user's per-user key. This is a genuinely different secret
// from a user's own OAuth token (GoogleOAuthFile/UserGoogleOAuthFile): one
// client JSON is shared by every account's own "Connect Google Health"
// consent flow, uploaded via the dashboard's settings page instead of a
// config.yaml file path (see internal/googleauth.SaveClientJSON).
func (p *Paths) GoogleClientSecretFile() string {
	return filepath.Join(p.ConfigDir(), "google_oauth_client.json.enc")
}

// CronometerCredentialsFile holds the encrypted Cronometer username/password
// (see ARCHITECTURE.md §5 — Cronometer has no OAuth, so this is the
// long-term encrypted store the credentials live in instead of plaintext
// config.yaml; "healthd auth cronometer" writes it).
func (p *Paths) CronometerCredentialsFile() string {
	return filepath.Join(p.ConfigDir(), "cronometer_credentials.json.enc")
}

// CronometerSessionFile caches the (short-lived) Cronometer session token so
// a sync run doesn't have to log in from scratch every time — Cronometer
// throttles repeated logins per account.
func (p *Paths) CronometerSessionFile() string {
	return filepath.Join(p.ConfigDir(), "cronometer_session.json.enc")
}
func (p *Paths) DBKeyFile() string { return filepath.Join(p.KeysDir(), "db.key") }

// UsersKeysDir/UsersConfigDir group every per-user secret under its own
// subdirectory of the existing keys/ and config/ trees, parallel to the
// root-level layout (see ARCHITECTURE.md's multi-user section) rather than
// introducing a third top-level directory.
func (p *Paths) UsersKeysDir() string   { return filepath.Join(p.KeysDir(), "users") }
func (p *Paths) UsersConfigDir() string { return filepath.Join(p.ConfigDir(), "users") }

// UserDir is where one user's encrypted provider-credential files live.
func (p *Paths) UserDir(userID int64) string {
	return filepath.Join(p.UsersConfigDir(), strconv.FormatInt(userID, 10))
}

// UserKeyFile is the per-user credential-encryption keyfile: the same
// {salt, key} JSON shape crypto.GenerateAndSaveKey/LoadKey already produce
// for keys/db.key, just derived from that user's own account password
// instead of the root passphrase, and cached here (owner-only permissions)
// so the background sync scheduler can decrypt this user's Google/
// Cronometer credentials unattended, regardless of whether they're
// currently logged into the dashboard (see ARCHITECTURE.md).
func (p *Paths) UserKeyFile(userID int64) string {
	return filepath.Join(p.UsersKeysDir(), strconv.FormatInt(userID, 10)+".key")
}

func (p *Paths) UserGoogleOAuthFile(userID int64) string {
	return filepath.Join(p.UserDir(userID), "google_oauth.json.enc")
}

func (p *Paths) UserCronometerCredentialsFile(userID int64) string {
	return filepath.Join(p.UserDir(userID), "cronometer_credentials.json.enc")
}

func (p *Paths) UserCronometerSessionFile(userID int64) string {
	return filepath.Join(p.UserDir(userID), "cronometer_session.json.enc")
}

// EnsureUserDir creates (and, off Windows, tightens permissions on) the
// per-user keys/config directories the first time a given user needs one —
// at signup, or the first time they connect a provider — mirroring
// EnsureDirs's MkdirAll+Chmod pattern for the root-level directories.
func (p *Paths) EnsureUserDir(userID int64) error {
	dirs := []string{p.UsersKeysDir(), p.UsersConfigDir(), p.UserDir(userID)}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(dir, dirPerm); err != nil {
				return fmt.Errorf("setting permissions on %s: %w", dir, err)
			}
		}
	}
	return nil
}

// EnsureDirs creates the full root folder structure if missing, and
// tightens permissions on every directory to owner-only. Chmod is skipped
// on Windows, where POSIX modes don't map onto ACLs and MkdirAll's mode
// argument is already ignored by the runtime.
func (p *Paths) EnsureDirs() error {
	dirs := []string{
		p.root,
		p.DBDir(),
		p.ConfigDir(),
		p.LogsDir(),
		p.KeysDir(),
		p.ServiceDir(),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(dir, dirPerm); err != nil {
				return fmt.Errorf("setting permissions on %s: %w", dir, err)
			}
		}
	}
	return nil
}

// EnsureParentDir creates the parent directory of path if it does not
// already exist. Useful before writing to a caller-supplied file path
// (e.g. the destination of "healthd db decrypt").
func EnsureParentDir(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	return nil
}

// ExternalOutputPath resolves a user-supplied destination path that is
// deliberately meant to live outside root, such as the target of
// "healthd db decrypt". It expands a leading "~", cleans and absolutizes
// the result, and refuses to point at a path healthd itself manages, so an
// errant decrypt can't silently clobber the encrypted DB or key material
// it just read from.
func (p *Paths) ExternalOutputPath(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", errors.New("output path must not be empty")
	}

	expanded, err := expandUser(input)
	if err != nil {
		return "", err
	}

	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolving output path %q: %w", input, err)
	}
	abs = filepath.Clean(abs)

	managed := []string{p.DBFile(), p.DBWorkingFile(), p.DBKeyFile(), p.GoogleOAuthFile(), p.GoogleClientSecretFile(), p.ConfigFile(), p.CronometerCredentialsFile(), p.CronometerSessionFile()}
	for _, m := range managed {
		if abs == m {
			return "", fmt.Errorf("refusing to write to managed path %q", abs)
		}
	}

	return abs, nil
}

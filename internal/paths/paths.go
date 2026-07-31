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
func (p *Paths) SyncLogFile() string   { return filepath.Join(p.LogsDir(), "sync.log") }
func (p *Paths) ServerLogFile() string { return filepath.Join(p.LogsDir(), "server.log") }
func (p *Paths) DBKeyFile() string     { return filepath.Join(p.KeysDir(), "db.key") }

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

// SafeJoin joins base and name, guaranteeing the result stays inside base.
// It rejects absolute inputs and any ".." segment that would escape base.
// Use this wherever a path component comes from external input (config
// values, future per-record attachment names, etc.) rather than being one
// of the fixed accessors above.
func SafeJoin(base, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("path must not be empty")
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("path %q must be relative", name)
	}

	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolving base %q: %w", base, err)
	}
	joined := filepath.Join(baseAbs, name)

	rel, err := filepath.Rel(baseAbs, joined)
	if err != nil {
		return "", fmt.Errorf("resolving %q relative to %q: %w", name, base, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes base directory %q", name, base)
	}

	return joined, nil
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

	managed := []string{p.DBFile(), p.DBWorkingFile(), p.DBKeyFile(), p.GoogleOAuthFile(), p.ConfigFile()}
	for _, m := range managed {
		if abs == m {
			return "", fmt.Errorf("refusing to write to managed path %q", abs)
		}
	}

	return abs, nil
}

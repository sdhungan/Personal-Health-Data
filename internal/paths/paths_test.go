package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveDefaultRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	p, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve(\"\"): %v", err)
	}

	want := filepath.Clean(filepath.Join(home, ".healthd"))
	if p.Root() != want {
		t.Errorf("Root() = %q, want %q", p.Root(), want)
	}
}

func TestResolveTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare tilde", "~", home},
		{"tilde slash", "~/custom-root", filepath.Join(home, "custom-root")},
		{"tilde nested", "~/a/b/c", filepath.Join(home, "a", "b", "c")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Resolve(tt.in)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.in, err)
			}
			want := filepath.Clean(tt.want)
			if p.Root() != want {
				t.Errorf("Root() = %q, want %q", p.Root(), want)
			}
		})
	}
}

func TestResolveCleansExplicitRoot(t *testing.T) {
	dir := t.TempDir()
	messy := filepath.Join(dir, "a", "..", "b")

	p, err := Resolve(messy)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", messy, err)
	}

	want := filepath.Clean(filepath.Join(dir, "b"))
	if p.Root() != want {
		t.Errorf("Root() = %q, want %q", p.Root(), want)
	}
}

func TestAccessorsNestUnderRoot(t *testing.T) {
	p, err := Resolve(filepath.Join(t.TempDir(), "healthd"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	cases := map[string]string{
		"DBDir":                     p.DBDir(),
		"ConfigDir":                 p.ConfigDir(),
		"LogsDir":                   p.LogsDir(),
		"KeysDir":                   p.KeysDir(),
		"ServiceDir":                p.ServiceDir(),
		"DBFile":                    p.DBFile(),
		"ConfigFile":                p.ConfigFile(),
		"GoogleOAuthFile":           p.GoogleOAuthFile(),
		"CronometerCredentialsFile": p.CronometerCredentialsFile(),
		"CronometerSessionFile":     p.CronometerSessionFile(),
		"DBKeyFile":                 p.DBKeyFile(),
	}

	for name, got := range cases {
		rel, err := filepath.Rel(p.Root(), got)
		if err != nil {
			t.Errorf("%s: filepath.Rel error: %v", name, err)
			continue
		}
		if rel == ".." || len(rel) >= 2 && rel[:2] == ".." {
			t.Errorf("%s = %q escapes root %q", name, got, p.Root())
		}
	}

	if p.DBFile() != filepath.Join(p.DBDir(), "health.db.enc") {
		t.Errorf("DBFile() = %q, want under DBDir()", p.DBFile())
	}
	if p.DBKeyFile() != filepath.Join(p.KeysDir(), "db.key") {
		t.Errorf("DBKeyFile() = %q, want under KeysDir()", p.DBKeyFile())
	}
}

func TestEnsureDirsCreatesStructureWithOwnerOnlyPerms(t *testing.T) {
	root := filepath.Join(t.TempDir(), "healthd")
	p, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	dirs := []string{p.Root(), p.DBDir(), p.ConfigDir(), p.LogsDir(), p.KeysDir(), p.ServiceDir()}
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
		if runtime.GOOS != "windows" {
			if perm := info.Mode().Perm(); perm != dirPerm {
				t.Errorf("%s perm = %o, want %o", dir, perm, dirPerm)
			}
		}
	}

	// Calling it again on an already-populated root must not error.
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs (second call): %v", err)
	}
}

func TestExternalOutputPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "healthd")
	p, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	external := filepath.Join(t.TempDir(), "backup.sql")
	got, err := p.ExternalOutputPath(external)
	if err != nil {
		t.Fatalf("ExternalOutputPath(%q): %v", external, err)
	}
	if got != filepath.Clean(external) {
		t.Errorf("ExternalOutputPath = %q, want %q", got, filepath.Clean(external))
	}

	managedPaths := []string{p.DBFile(), p.DBKeyFile(), p.GoogleOAuthFile(), p.ConfigFile(), p.CronometerCredentialsFile(), p.CronometerSessionFile()}
	for _, m := range managedPaths {
		if _, err := p.ExternalOutputPath(m); err == nil {
			t.Errorf("ExternalOutputPath(%q): expected error for managed path, got none", m)
		}
	}

	if _, err := p.ExternalOutputPath(""); err == nil {
		t.Error("ExternalOutputPath(\"\"): expected error, got none")
	}
}

func TestEnsureParentDir(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nested", "deeper", "out.sql")

	if err := EnsureParentDir(target); err != nil {
		t.Fatalf("EnsureParentDir: %v", err)
	}

	info, err := os.Stat(filepath.Dir(target))
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("parent of %s is not a directory", target)
	}
}

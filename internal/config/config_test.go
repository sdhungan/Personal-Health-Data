package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *cfg != *Default() {
		t.Errorf("Load(missing) = %+v, want Default() %+v", cfg, Default())
	}
}

func TestLoadNonPositiveSyncIntervalFallsBackToDefault(t *testing.T) {
	// A real incident this guards against: time.NewTicker (service.go)
	// panics outright on a non-positive duration, taking the whole merged
	// process down over one bad config.yaml edit — see Load's own comment.
	for _, interval := range []string{"0", "-5"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("sync_interval_minutes: "+interval+"\n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load(%q): %v", interval, err)
		}
		if cfg.SyncIntervalMinutes != Default().SyncIntervalMinutes {
			t.Errorf("Load(sync_interval_minutes: %s).SyncIntervalMinutes = %d, want default %d",
				interval, cfg.SyncIntervalMinutes, Default().SyncIntervalMinutes)
		}
	}
}

func TestLoadPreservesValidOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := "port: 9999\nsync_interval_minutes: 15\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9999 || cfg.SyncIntervalMinutes != 15 {
		t.Errorf("Load() = %+v, want Port=9999 SyncIntervalMinutes=15", cfg)
	}
	// Untouched fields still come from Default().
	if cfg.Google.CallbackPort != Default().Google.CallbackPort {
		t.Errorf("Google.CallbackPort = %d, want default %d", cfg.Google.CallbackPort, Default().Google.CallbackPort)
	}
}

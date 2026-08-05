package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriterArchivesAfterMaxLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "healthd.log")

	w, err := newRotatingWriter(path)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer w.Close()

	for i := 0; i < maxLines+5; i++ {
		if _, err := w.Write([]byte("line\n")); err != nil {
			t.Fatalf("Write (line %d): %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var archived, current int
	for _, e := range entries {
		if e.Name() == "healthd.log" {
			current++
		} else if strings.HasPrefix(e.Name(), "healthd.log.") {
			archived++
		}
	}
	if archived != 1 {
		t.Errorf("archived files = %d, want 1", archived)
	}
	if current != 1 {
		t.Errorf("current log files = %d, want 1", current)
	}

	// The active file should hold only what was written after rotation
	// triggered on write #1001 (the one that pushed the count past
	// maxLines): 4 more writes follow (#1002-#1005 of the 1005 total).
	if w.lines != 4 {
		t.Errorf("post-rotation line count = %d, want 4", w.lines)
	}
}

func TestNewReopensExistingFileWithoutLosingLineCount(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")

	logger, closeFn, err := New(logsDir, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	logger.Info("first line")
	if err := closeFn(); err != nil {
		t.Fatalf("closeFn: %v", err)
	}

	// Reopen (simulating a restart) and confirm the existing line is
	// counted rather than resetting to zero and over-shooting maxLines
	// later than it should.
	w, err := newRotatingWriter(filepath.Join(logsDir, "healthd.log"))
	if err != nil {
		t.Fatalf("newRotatingWriter on reopen: %v", err)
	}
	defer w.Close()
	if w.lines != 1 {
		t.Errorf("lines after reopening a 1-line file = %d, want 1", w.lines)
	}
}

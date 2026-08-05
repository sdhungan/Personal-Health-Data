// Package applog builds healthd's structured logger (zap), writing to
// <root>/logs/healthd.log with simple line-count-based rotation: once the
// active file exceeds maxLines, it's archived under a timestamp suffix and
// a fresh file is started. This is deliberately a line count, not the
// byte-size threshold most log rotation libraries (e.g. lumberjack) use —
// that's what was asked for, and healthd's log volume (one process,
// startup milestones plus occasional sync/connector errors) never
// approaches a scale where a line-count check's minor overhead matters.
package applog

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// maxLines is when the active log file gets archived and replaced with a
// fresh one.
const maxLines = 1000

// rotatingWriter is a zapcore.WriteSyncer that archives path (renamed with
// a UTC timestamp suffix) and reopens it fresh once more than maxLines
// lines have been written to it — checked on every write, not on a
// separate timer, so a burst of log lines can't overshoot the limit by
// more than one write's worth of lines.
type rotatingWriter struct {
	mu    sync.Mutex
	path  string
	file  *os.File
	lines int
}

func newRotatingWriter(path string) (*rotatingWriter, error) {
	w := &rotatingWriter{path: path}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening log file %s: %w", w.path, err)
	}
	lines, err := countLines(w.path)
	if err != nil {
		f.Close()
		return err
	}
	w.file = f
	w.lines = lines
	return nil
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("counting existing lines in %s: %w", path, err)
	}
	defer f.Close()

	n := 0
	scanner := bufio.NewScanner(f)
	// A single log line can exceed bufio.Scanner's 64KiB default (a JSON
	// stack trace field, for instance) — grow the buffer rather than have
	// countLines silently under-count and delay rotation.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		n++
	}
	return n, scanner.Err()
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n, err := w.file.Write(p)
	if err != nil {
		return n, err
	}
	w.lines += bytes.Count(p, []byte("\n"))
	if w.lines > maxLines {
		if rerr := w.rotate(); rerr != nil {
			// Losing rotation for one write is not worth failing the
			// caller's log call over — surface it directly on stderr
			// instead, since the logger itself is what would normally
			// report this.
			fmt.Fprintln(os.Stderr, "warning: rotating log file:", rerr)
		}
	}
	return n, nil
}

func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("closing %s before rotation: %w", w.path, err)
	}
	archived := w.path + "." + time.Now().UTC().Format("20060102T150405Z")
	if err := os.Rename(w.path, archived); err != nil {
		return fmt.Errorf("archiving %s to %s: %w", w.path, archived, err)
	}
	return w.open()
}

func (w *rotatingWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Sync()
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// New builds a zap logger writing JSON-structured logs to
// <logsDir>/healthd.log (see rotatingWriter for the rotation policy).
// alsoStderr additionally tees every entry to stderr in human-readable
// form — used for interactive runs (a real terminal), so "healthd" started
// directly still shows logs live instead of only landing in the file;
// false for a hosted OS service, which has no attached console to write to
// anyway. The returned close func flushes and closes the log file; callers
// should defer it.
func New(logsDir string, alsoStderr bool) (*zap.Logger, func() error, error) {
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("creating logs directory %s: %w", logsDir, err)
	}
	path := filepath.Join(logsDir, "healthd.log")
	w, err := newRotatingWriter(path)
	if err != nil {
		return nil, nil, err
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderCfg), w, zap.InfoLevel)
	if alsoStderr {
		consoleCfg := encoderCfg
		consoleCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
		stderrCore := zapcore.NewCore(zapcore.NewConsoleEncoder(consoleCfg), zapcore.AddSync(os.Stderr), zap.InfoLevel)
		core = zapcore.NewTee(core, stderrCore)
	}

	return zap.New(core), w.Close, nil
}

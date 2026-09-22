// Package logging builds the CLI's running log: the same JSON lines that go to
// stderr, also teed to a rotating file under ~/.fs/logs. The file is
// best-effort — a write that fails is swallowed to a single WARN on stderr and
// never fails the command. Nothing reads the file back, so it is never
// load-bearing for a decision; it is what the process did, kept.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// maxBytes is the size at which fs.log rotates: 4 MiB.
const maxBytes = 4 << 20

// acceptedLevels is what ParseLevel refuses with, so a bad FS_LOG_LEVEL names
// the values it accepts instead of falling back silently.
const acceptedLevels = "debug, info, warn, error"

// ParseLevel parses an FS_LOG_LEVEL value. It is case-insensitive and treats
// empty as the default, INFO.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("invalid log level %q (accepted: %s)", s, acceptedLevels)
}

// ResolveLevel picks this process's level: the environment's value (empty means
// INFO), overridden by verbose — the -v/--verbose flag — which turns it up to
// DEBUG. The flag wins over the environment.
func ResolveLevel(env string, verbose bool) (slog.Level, error) {
	level, err := ParseLevel(env)
	if err != nil {
		return level, err
	}
	if verbose {
		level = slog.LevelDebug
	}
	return level, nil
}

// Sink is an io.Writer that appends lines to fs.log under dir, rotating it at
// maxBytes and keeping at most three files (fs.log, fs.log.1, fs.log.2). Every operation is local and best-effort: a
// failure is returned to the caller (which downgrades it to a WARN) and never
// exits or panics.
type Sink struct {
	dir  string
	mu   sync.Mutex
	file *os.File
}

// NewSink returns a sink rooted at dir. Nothing is created until the first
// write, so a command that never logs touches nothing on disk.
func NewSink(dir string) *Sink { return &Sink{dir: dir} }

// Write appends p to fs.log, rotating first when the file is already at or
// over the cap. A rotation that fails still writes p to fs.log when it can, so
// the line is not lost; only a failure to open or write the file itself is
// returned.
func (s *Sink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.rotate(); err != nil {
		return 0, err
	}
	return s.file.Write(p)
}

// rotate opens the file on first use, then rolls it over when it has reached
// the cap. It is best-effort: if the rollover cannot complete, the pending
// line is still written to whatever fs.log is reachable, and only a failure to
// open or stat the file is returned.
func (s *Sink) rotate() error {
	if s.file == nil {
		if err := s.open(); err != nil {
			return err
		}
	}

	fi, err := s.file.Stat()
	if err != nil {
		return err
	}
	if fi.Size() < maxBytes {
		return nil
	}

	base := filepath.Join(s.dir, "fs.log")
	if err := s.file.Close(); err != nil {
		// Keep the handle: the over-cap file still accepts the line.
		return nil
	}
	s.file = nil

	// fs.log.2 is discarded; fs.log.1 becomes fs.log.2; fs.log becomes
	// fs.log.1. Rename errors are ignored so a partial rollover still leaves a
	// writable fs.log for the pending line.
	_ = os.Remove(base + ".2")
	_ = os.Rename(base+".1", base+".2")
	_ = os.Rename(base, base+".1")

	return s.open()
}

// open creates the log directory and opens fs.log for append.
func (s *Sink) open() error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "fs.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	s.file = f
	return nil
}

// teeState is the shared state of a teeHandler and its WithAttrs/WithGroup
// children: whether the sink failure has already been warned about once.
type teeState struct {
	mu     sync.Mutex
	warned bool
}

// teeHandler writes every record to the rotating file (best-effort) and to
// stderr (always), keeping stderr's output identical to before the file
// existed.
type teeHandler struct {
	stderr slog.Handler
	file   slog.Handler
	state  *teeState
}

// NewLogger returns the CLI's structured logger. Records are JSON to stderr as
// before, and the same records go to the rotating sink under dir. level is the
// process's level, fixed for its life.
func NewLogger(dir string, level slog.Level) *slog.Logger {
	lv := &slog.LevelVar{}
	lv.Set(level)
	opts := &slog.HandlerOptions{Level: lv}
	return slog.New(&teeHandler{
		stderr: slog.NewJSONHandler(os.Stderr, opts),
		file:   slog.NewJSONHandler(NewSink(dir), opts),
		state:  &teeState{},
	})
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.stderr.Enabled(ctx, level)
}

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	if err := h.file.Handle(ctx, r); err != nil {
		h.warn(err)
	}
	return h.stderr.Handle(ctx, r)
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &teeHandler{stderr: h.stderr.WithAttrs(attrs), file: h.file.WithAttrs(attrs), state: h.state}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	return &teeHandler{stderr: h.stderr.WithGroup(name), file: h.file.WithGroup(name), state: h.state}
}

// warn reports a sink failure once, as a WARN on stderr. It never fails the
// command and never writes through the sink (which is what just failed).
func (h *teeHandler) warn(err error) {
	h.state.mu.Lock()
	if h.state.warned {
		h.state.mu.Unlock()
		return
	}
	h.state.warned = true
	h.state.mu.Unlock()

	rec := slog.NewRecord(time.Now(), slog.LevelWarn, "log write failed", 0)
	rec.AddAttrs(slog.String("error", err.Error()))
	_ = h.stderr.Handle(context.Background(), rec)
}

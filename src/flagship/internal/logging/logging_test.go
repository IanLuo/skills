package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{" Info ", slog.LevelInfo},
		{"debug", slog.LevelDebug},
		{"Debug", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.in)
		if err != nil {
			t.Errorf("ParseLevel(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseLevelRefusesNamingAcceptedValues(t *testing.T) {
	for _, in := range []string{"bogus", "verbose", "warnin", "1"} {
		_, err := ParseLevel(in)
		if err == nil {
			t.Errorf("ParseLevel(%q) = nil error, want refusal", in)
			continue
		}
		if !strings.Contains(err.Error(), "accepted: debug, info, warn, error") {
			t.Errorf("ParseLevel(%q) error %q does not name the accepted values", in, err)
		}
	}
}

func TestResolveLevelFlagWinsOverEnv(t *testing.T) {
	cases := []struct {
		env     string
		verbose bool
		want    slog.Level
	}{
		{"", false, slog.LevelInfo},
		{"", true, slog.LevelDebug},
		{"debug", false, slog.LevelDebug},
		{"warn", true, slog.LevelDebug}, // flag overrides the environment
		{"error", false, slog.LevelError},
	}
	for _, c := range cases {
		got, err := ResolveLevel(c.env, c.verbose)
		if err != nil {
			t.Errorf("ResolveLevel(%q, %v) error: %v", c.env, c.verbose, err)
			continue
		}
		if got != c.want {
			t.Errorf("ResolveLevel(%q, %v) = %v, want %v", c.env, c.verbose, got, c.want)
		}
	}

	if _, err := ResolveLevel("bogus", false); err == nil {
		t.Error("ResolveLevel(bogus) = nil error, want refusal")
	}
}

// TestSinkRotation writes past two full rotations and asserts the three files,
// that the newest content is intact in fs.log, and that nothing beyond .2 is
// kept.
func TestSinkRotation(t *testing.T) {
	dir := t.TempDir()
	s := NewSink(dir)

	// maxBytes is 4 MiB and one line is 1 KiB, so 4096 lines fill a file
	// exactly. Two rotations need 2*4096 + 1 writes.
	const lineSize = 1024
	const linesPerFile = maxBytes / lineSize
	total := 2*linesPerFile + 1

	last := []byte(nil)
	for i := 0; i < total; i++ {
		line := bytes.Repeat([]byte{byte('a' + i%26)}, lineSize)
		last = line
		if _, err := s.Write(line); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	for _, name := range []string{"fs.log", "fs.log.1", "fs.log.2"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "fs.log.3")); !os.IsNotExist(err) {
		t.Fatalf("fs.log.3 exists, want at most 3 files: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "fs.log"))
	if err != nil {
		t.Fatalf("read fs.log: %v", err)
	}
	if !bytes.Equal(got, last) {
		t.Fatalf("fs.log does not hold the newest line (len %d vs %d)", len(got), len(last))
	}
}

// TestSinkRotatesBeforeFirstWrite seeds an already-over-cap file and asserts the
// first write rotates it away rather than appending to it.
func TestSinkRotatesBeforeFirstWrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := bytes.Repeat([]byte("s"), maxBytes+100)
	if err := os.WriteFile(filepath.Join(dir, "fs.log"), seed, 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewSink(dir)
	line := []byte("newest line")
	if _, err := s.Write(line); err != nil {
		t.Fatalf("write: %v", err)
	}

	rotated, err := os.ReadFile(filepath.Join(dir, "fs.log.1"))
	if err != nil {
		t.Fatalf("fs.log.1 missing after rotation: %v", err)
	}
	if !bytes.Equal(rotated, seed) {
		t.Fatalf("fs.log.1 does not hold the seeded content (len %d vs %d)", len(rotated), len(seed))
	}

	fresh, err := os.ReadFile(filepath.Join(dir, "fs.log"))
	if err != nil {
		t.Fatalf("read fs.log: %v", err)
	}
	if !bytes.Equal(fresh, line) {
		t.Fatalf("fs.log = %q, want %q", fresh, line)
	}
}

// TestSinkUnwritableDirReturnsError pins the sink's contract: a failed write is
// returned to the caller (which downgrades it to a WARN), never a panic or exit.
func TestSinkUnwritableDirReturnsError(t *testing.T) {
	dir := t.TempDir()
	// The sink's directory is a regular file, so MkdirAll cannot succeed.
	blocker := filepath.Join(dir, "logs")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewSink(blocker)
	if _, err := s.Write([]byte("line")); err == nil {
		t.Fatal("Write to an unwritable sink returned nil error, want error")
	}
}

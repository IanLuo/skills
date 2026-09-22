package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runFSCapture runs the fs binary with an explicit HOME and extra environment,
// returning stdout, stderr and the exit code separately — what the unwritable-
// sink property (AC6) is measured against, where runFS's stdout-only parse is
// not enough.
func runFSCapture(t *testing.T, bin, dir, home string, extraEnv []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "HOME="+home), extraEnv...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("exec %v: %v", args, err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// setupDispatchRoot registers a project whose root passes the code-entry gate's
// two checks, so a dispatch resolves a plan and reaches the plan-resolved DEBUG
// line.
func setupDispatchRoot(t *testing.T, bin, home, name string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# agents\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "prd.md"), []byte("<!-- specs:locked: prd -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runFSCapture(t, bin, root, home, nil, "project", "create", "--name", name, "--root", root); code != 0 {
		t.Fatalf("project create %s exit %d", name, code)
	}
	return root
}

// A dispatch stderr that carries the plan-resolved DEBUG line proves the level
// moved; the same run without -v must not.
func TestCLIVerboseWritesDebugLines(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)
	writeKBPlaybook(t, home, "code-entry", dispatchPlaybook)

	setupDispatchRoot(t, bin, home, "verbosedefault")
	_, defaultStderr, code := runFSCapture(t, bin, t.TempDir(), home, nil,
		"dispatch", "--project", "verbosedefault", "--type", "dev-task", "--goal", "sample")
	if code != 0 {
		t.Fatalf("default dispatch exit %d: %s", code, defaultStderr)
	}
	if strings.Contains(defaultStderr, `"level":"DEBUG"`) || strings.Contains(defaultStderr, "plan resolved") {
		t.Errorf("default dispatch wrote a DEBUG line:\n%s", defaultStderr)
	}

	setupDispatchRoot(t, bin, home, "verboseflag")
	_, verboseStderr, code := runFSCapture(t, bin, t.TempDir(), home, nil,
		"dispatch", "-v", "--project", "verboseflag", "--type", "dev-task", "--goal", "sample", "--allow-unresolved")
	if code != 0 {
		t.Fatalf("-v dispatch exit %d: %s", code, verboseStderr)
	}
	if !strings.Contains(verboseStderr, `"level":"DEBUG"`) || !strings.Contains(verboseStderr, "plan resolved") {
		t.Errorf("-v dispatch did not write the plan-resolved DEBUG line:\n%s", verboseStderr)
	}
}

// FS_LOG_LEVEL=debug turns the level up without the flag.
func TestCLIFSLogLevelEnvWritesDebug(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)
	writeKBPlaybook(t, home, "code-entry", dispatchPlaybook)

	setupDispatchRoot(t, bin, home, "envdebug")
	_, stderr, code := runFSCapture(t, bin, t.TempDir(), home, []string{"FS_LOG_LEVEL=debug"},
		"dispatch", "--project", "envdebug", "--type", "dev-task", "--goal", "sample")
	if code != 0 {
		t.Fatalf("dispatch exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "plan resolved") {
		t.Errorf("FS_LOG_LEVEL=debug dispatch did not write the DEBUG line:\n%s", stderr)
	}
}

// The flag overrides the environment: FS_LOG_LEVEL=error with -v still writes
// DEBUG.
func TestCLIFlagOverridesEnvLevel(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)
	writeKBPlaybook(t, home, "code-entry", dispatchPlaybook)

	setupDispatchRoot(t, bin, home, "flagwins")
	_, stderr, code := runFSCapture(t, bin, t.TempDir(), home, []string{"FS_LOG_LEVEL=error"},
		"dispatch", "-v", "--project", "flagwins", "--type", "dev-task", "--goal", "sample")
	if code != 0 {
		t.Fatalf("dispatch exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "plan resolved") {
		t.Errorf("flag did not override FS_LOG_LEVEL=error:\n%s", stderr)
	}
}

// An unparseable FS_LOG_LEVEL is refused, naming the accepted values, rather
// than silently falling back.
func TestCLIUnparseableLogLevelRefused(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)

	stdout, _, code := runFSCapture(t, bin, t.TempDir(), home, []string{"FS_LOG_LEVEL=bogus"}, "status")
	if code != 1 {
		t.Fatalf("exit %d, want 1 (refusal)", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("stdout is not an envelope: %v\n%s", err, stdout)
	}
	if resp["ok"] != false {
		t.Fatalf("ok = %v, want false", resp["ok"])
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "accepted: debug, info, warn, error") {
		t.Errorf("error %q does not name the accepted values", errMsg)
	}
}

// AC6: a log write failure must never fail a command. Pointing the sink at an
// unwritable path leaves the command's exit code and stdout unchanged; the
// worst that reaches stderr is a WARN.
func TestCLIUnwritableSinkDoesNotChangeExitOrStdout(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	cleanHome := t.TempDir()
	cleanStdout, _, cleanCode := runFSCapture(t, bin, dir, cleanHome, nil, "unfinished")
	if cleanCode != 0 {
		t.Fatalf("clean unfinished exit %d: %s", cleanCode, cleanStdout)
	}

	blockedHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(blockedHome, ".fs"), 0o755); err != nil {
		t.Fatal(err)
	}
	// ~/.fs/logs is a regular file, so the sink cannot create the directory.
	if err := os.WriteFile(filepath.Join(blockedHome, ".fs", "logs"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runFSCapture(t, bin, dir, blockedHome, nil, "unfinished")
	if code != cleanCode {
		t.Fatalf("exit %d, want %d (unchanged)", code, cleanCode)
	}
	if stdout != cleanStdout {
		t.Errorf("stdout changed under an unwritable sink:\nblocked: %s\nclean:   %s", stdout, cleanStdout)
	}
	if !strings.Contains(stderr, `"level":"INFO"`) {
		t.Errorf("stderr lost its normal lines under an unwritable sink:\n%s", stderr)
	}
	if strings.Contains(stderr, `"level":"ERROR"`) {
		t.Errorf("stderr carries an ERROR under an unwritable sink, want at most a WARN:\n%s", stderr)
	}
}

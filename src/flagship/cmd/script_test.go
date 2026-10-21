package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runFSRaw runs the binary and returns its combined output and exit status. It
// is runFSEnv without the JSON unmarshal: `fs script`'s stdout is the script's
// own, so neither half of that contract is the envelope every other subcommand
// writes.
func runFSRaw(t *testing.T, bin, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+testHome(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("exec: %v", err)
		}
		return string(out), ee.ExitCode()
	}
	return string(out), 0
}

// fs script runs a script the binary ships, and the argument passing is the
// whole of the difficulty: `bash -c "$body" x` makes x the shell's $0, so $1 is
// empty and the script silently receives no arguments at all. A test that passes
// none proves nothing — so the case below is resolve-path.sh, whose answer
// DEPENDS on the argument it is given and which exits 2 when it is given none.
func TestScriptRunsAShippedScriptWithItsArguments(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()

	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}

	// The link reaches the script as $1, and the script answers with the one
	// spelling of the directory it names.
	out, code := runFSRaw(t, bin, root, "script", "resolve-path.sh", link)
	if code != 0 {
		t.Fatalf("a symlinked path must exit 0 through fs script, exit %d: %s", code, out)
	}
	if strings.TrimSpace(out) != resolved {
		t.Errorf("output = %q, want the resolved %q", strings.TrimSpace(out), resolved)
	}

	// No argument at all: the script's own usage refusal, which is what the
	// missing $0 placeholder would have produced for the case above too.
	out, code = runFSRaw(t, bin, root, "script", "resolve-path.sh")
	if code != 2 {
		t.Fatalf("no argument must be the script's own exit 2, exit %d: %s", code, out)
	}
	if !strings.Contains(out, "usage") {
		t.Errorf("output = %q, want the usage refusal", out)
	}
}

// The regression this change is most likely to cause: a script that stops being
// able to fail. A non-zero status from the script must be fs's own status, and
// the script's output must reach the caller — otherwise every gate a script
// carries would silently stop gating. Both directions are asserted here, through
// fs script and not through the file.
func TestScriptPassesThroughAFailingExitStatus(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()

	// A path whose last component exists resolves and exits 0.
	out, code := runFSRaw(t, bin, root, "script", "resolve-path.sh", root)
	if code != 0 {
		t.Fatalf("an existing path must pass through fs script, exit %d: %s", code, out)
	}

	// A path whose directory does not exist cannot be resolved, and the script
	// says so with exit 1 — the caller must hear it.
	missing := filepath.Join(root, "nosuchdir", "child")
	out, code = runFSRaw(t, bin, root, "script", "resolve-path.sh", missing)
	if code != 1 {
		t.Fatalf("an unresolvable path must exit 1 through fs script, exit %d: %s", code, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("stdout = %q, want nothing: an unresolvable path prints no spelling, so a caller matching nothing reads it as a refusal rather than as a miss", out)
	}
}

// A step that names a script the binary no longer ships must fail loudly, and say
// what it could have named instead: the refusal lists the set that exists, so a
// renamed script is a message rather than a silent no-op.
func TestScriptRefusesANameTheBinaryDoesNotShip(t *testing.T) {
	bin := getFS(t)

	out, code := runFSRaw(t, bin, t.TempDir(), "script", "nosuch.sh")
	if code == 0 {
		t.Fatalf("an unknown script name must refuse, got exit 0: %s", out)
	}
	for _, want := range []string{"nosuch.sh", "branch-decision.sh", "resolve-path.sh"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal %q must name %q", out, want)
		}
	}
}

// Nothing is materialised. The scripts used to be written to ~/.fs/scripts and
// addressed as paths; a script is now read out of the binary, so a run must touch
// no directory at all — the regression here is a helpful future change that
// decides to cache them on disk again.
func TestScriptWritesNothingToDisk(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)
	root := t.TempDir()

	out, code := runFSRaw(t, bin, root, "script", "resolve-path.sh", root)
	if code != 0 {
		t.Fatalf("resolving an existing path must exit 0, exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".fs")); !os.IsNotExist(err) {
		t.Errorf("fs script touched %s/.fs (%v); a script is read out of the binary and nothing is materialised", home, err)
	}
}

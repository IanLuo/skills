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
// whole of the difficulty: `bash -c "$body" x,y` makes x,y the shell's $0, so
// $1 is empty and the script silently receives no arguments at all. A test that
// passes none proves nothing — so the case below is check-parallel.sh, whose
// answer DEPENDS on the argument it is given, and the three statuses here are
// exactly what the missing $0 placeholder cannot produce: it makes all three
// the "no cards were named" refusal.
func TestScriptRunsAShippedScriptWithItsArguments(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	writeFileIn(t, root, "a.md", "# A\n\n## Files\nsrc/a.go\n")
	writeFileIn(t, root, "b.md", "# B\n\n## Files\nsrc/b.go\n")
	writeFileIn(t, root, "c.md", "# C\n\n## Files\nsrc/a.go\n")

	// Disjoint cards: the script read the list it was handed.
	out, code := runFSRaw(t, bin, root, "script", "check-parallel.sh", "a.md,b.md")
	if code != 0 {
		t.Fatalf("disjoint cards must exit 0 through fs script, exit %d: %s", code, out)
	}
	if !strings.Contains(out, "2 cards, no overlapping paths") {
		t.Errorf("output = %q, want the two cards it intersected", out)
	}

	// Overlapping cards: the same script, a different answer, exit 1.
	out, code = runFSRaw(t, bin, root, "script", "check-parallel.sh", "a.md,c.md")
	if code != 1 {
		t.Fatalf("overlapping cards must exit 1 through fs script, exit %d: %s", code, out)
	}
	if !strings.Contains(out, "src/a.go") {
		t.Errorf("output = %q, want the shared path", out)
	}

	// Named no cards: the script's own third answer, and the one a lost
	// argument would have produced for all three cases above.
	out, code = runFSRaw(t, bin, root, "script", "check-parallel.sh")
	if code != 2 {
		t.Fatalf("no cards must be the script's own exit 2, exit %d: %s", code, out)
	}
	if !strings.Contains(out, "no cards were named") {
		t.Errorf("output = %q, want the no-cards refusal", out)
	}
}

// The regression this change is most likely to cause: a gate that stops being
// able to fail. check-specs.sh is the gate that keeps every locked doc honest,
// and it runs inside fs now — so if a non-zero status were swallowed, or the
// output streamed while 0 was returned, every locked doc would silently stop
// being checked and every dispatch would still report green. Both directions
// are asserted here, through fs script and not through the file.
func TestScriptPassesThroughAFailingExitStatus(t *testing.T) {
	bin := getFS(t)
	root := t.TempDir()
	goGit(t, root, "init", "-q", "-b", "main")
	writeFileIn(t, root, "src/a.txt", "a\n")
	goGit(t, root, "add", "src/a.txt")
	goGit(t, root, "-c", "user.email=t@example.com", "-c", "user.name=Test", "commit", "-q", "-m", "one")
	sha := gitOut(t, root, "rev-parse", "--short", "HEAD")
	writeFileIn(t, root, ".agents/specs/one.md",
		"<!-- specs:locked:"+sha+" 2026-01-01 type=spec -->\n\nbody\n")

	// The doc names HEAD: the lock is true, so the gate passes.
	out, code := runFSRaw(t, bin, root, "script", "check-specs.sh")
	if code != 0 {
		t.Fatalf("a doc locked at HEAD must pass through fs script, exit %d: %s", code, out)
	}

	// The covered code moves past the lock, and the same command must now fail.
	writeFileIn(t, root, "src/b.txt", "b\n")
	goGit(t, root, "add", "src/b.txt")
	goGit(t, root, "-c", "user.email=t@example.com", "-c", "user.name=Test", "commit", "-q", "-m", "two")

	out, code = runFSRaw(t, bin, root, "script", "check-specs.sh")
	if code != 1 {
		t.Fatalf("a doc behind its code must exit 1 through fs script, exit %d: %s", code, out)
	}
	if !strings.Contains(out, sha) {
		t.Errorf("output = %q, must name the commit the doc is locked at (%s)", out, sha)
	}
}

// A step that names a script the binary no longer ships must fail loudly, and
// say what it could have named instead: the refusal lists the set that exists,
// so a renamed script is a message rather than a silent no-op.
func TestScriptRefusesANameTheBinaryDoesNotShip(t *testing.T) {
	bin := getFS(t)

	out, code := runFSRaw(t, bin, t.TempDir(), "script", "nosuch.sh")
	if code == 0 {
		t.Fatalf("an unknown script name must refuse, got exit 0: %s", out)
	}
	for _, want := range []string{"nosuch.sh", "check-parallel.sh", "check-specs.sh"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal %q must name %q", out, want)
		}
	}
}

// Nothing is materialised. The scripts used to be written to ~/.fs/scripts and
// addressed as paths; a script is now read out of the binary, so a run must
// touch no directory at all — the regression here is a helpful future change
// that decides to cache them on disk again.
func TestScriptWritesNothingToDisk(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)
	root := t.TempDir()

	out, code := runFSRaw(t, bin, root, "script", "check-specs.sh")
	if code != 0 {
		t.Fatalf("a project with no docs directory must exit 0, exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".fs")); !os.IsNotExist(err) {
		t.Errorf("fs script touched %s/.fs (%v); a script is read out of the binary and nothing is materialised", home, err)
	}
}

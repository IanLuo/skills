package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// version_test.go — what the binary says about the build it is.
//
// A build is evidence about one tree, and a binary that cannot name that tree
// gives a reader holding the wrong one nothing to check a conclusion against.
// That is not hypothetical here: three builds of this tool sat on one machine,
// all different, and a fix that worked was concluded twice to have failed
// because the build under test predated it. So `fs version` reports the revision
// the flake stamped, whether the tree it was built from was dirty, and its own
// path — the last because "which file is the tool" had three answers and no way
// to ask the question.
//
// Both states are tested. A plain `go build` — what a developer runs — has no
// stamp and must say so rather than invent a revision; a build linked with the
// flake's ldflags must report exactly what it was stamped with.

// buildFSStamped builds the CLI with a stamp, which is the only way to test one:
// the revision is a link-time value, so nothing short of linking produces it.
func buildFSStamped(t *testing.T, revision, dirty string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fs")
	cmd := exec.Command("go", "build",
		"-ldflags", "-X main.buildRevision="+revision+" -X main.buildDirty="+dirty,
		"-o", bin, ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build with stamp failed: %v\n%s", err, out)
	}
	return bin
}

// sameFile reports whether two paths name the same file on disk, so the
// assertion is about the file and not about how the path was spelled.
func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		t.Fatalf("stat %s: %v", a, err)
	}
	fb, err := os.Stat(b)
	if err != nil {
		t.Fatalf("stat %s: %v", b, err)
	}
	return os.SameFile(fa, fb)
}

func TestCLIVersionReportsAnUnstampedBuild(t *testing.T) {
	bin := getFS(t)

	resp, code := runFS(t, bin, t.TempDir(), "version")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; response = %v", code, resp)
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("data = %v, want an object", resp["data"])
	}
	if got := data["revision"]; got != "unknown" {
		t.Errorf("revision = %v, want \"unknown\" for a build with no stamp", got)
	}
	if got := data["dirty"]; got != "unknown" {
		t.Errorf("dirty = %v, want \"unknown\" for a build with no stamp", got)
	}
	path, _ := data["path"].(string)
	if path == "" {
		t.Fatal("path is empty; the run's own file is the answer to which file is the tool")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("path = %q, want an absolute path", path)
	}
	if !sameFile(t, path, bin) {
		t.Errorf("path = %q, want the binary that was run (%s)", path, bin)
	}
}

func TestCLIVersionReportsTheStampItWasBuiltWith(t *testing.T) {
	bin := buildFSStamped(t, "3f83fb5a1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f", "true")

	resp, code := runFS(t, bin, t.TempDir(), "version")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; response = %v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if got := data["revision"]; got != "3f83fb5a1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f" {
		t.Errorf("revision = %v, want the stamped revision", got)
	}
	if got := data["dirty"]; got != "true" {
		t.Errorf("dirty = %v, want \"true\" for a build stamped dirty", got)
	}

	clean := buildFSStamped(t, "a69d2e32", "false")
	resp, code = runFS(t, clean, t.TempDir(), "version")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; response = %v", code, resp)
	}
	data, _ = resp["data"].(map[string]any)
	if got := data["dirty"]; got != "false" {
		t.Errorf("dirty = %v, want \"false\" for a build stamped clean", got)
	}
}

func TestCLIVersionRefusesAStrayArgument(t *testing.T) {
	bin := getFS(t)

	// A flag or a positional it does not take is a usage error, not a silent
	// no-op: exit 2, which is what every other subcommand does with one.
	_, code := runFS(t, bin, t.TempDir(), "version", "extra")
	if code != 2 {
		t.Errorf("exit = %d, want 2 for a stray positional", code)
	}
}

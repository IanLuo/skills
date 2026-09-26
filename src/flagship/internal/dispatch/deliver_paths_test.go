package dispatch

import (
	"os"
	"path/filepath"
	"testing"
)

// sameFile is the question the worktree pane lookup asks, and these are its
// edges. Answering "same" for two paths it cannot both stat would start a worker
// at a checkout that is not there; answering "different" for one directory is
// the bug the lookup was fixed for. Both wrong answers are refusals of one kind
// or another, so the edges are worth stating exactly.
func TestSameFileAnswersOnlyForPathsItCanBothSee(t *testing.T) {
	dir := t.TempDir()

	if !sameFile(dir, dir) {
		t.Error("a directory is not reported as the same as itself")
	}
	if sameFile(dir, filepath.Join(dir, "absent")) {
		t.Error("a path that does not exist is reported as the same as one that does")
	}
	if sameFile("", dir) || sameFile(dir, "") {
		t.Error("an empty path is reported as the same as a directory")
	}
	if sameFile(dir, t.TempDir()) {
		t.Error("two different directories are reported as the same")
	}

	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sameFile(dir, file) {
		t.Error("a file in a directory is reported as the same as the directory")
	}
}

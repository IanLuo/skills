package trace

import (
	"os"
	"path/filepath"
	"testing"
)

// The trace is a record of a dispatch, so it is filed flat, keyed by the node
// alone, and the project it acted on is a field on every line — never a
// directory in the path.
func TestAppendFilesFlatAndCarriesProject(t *testing.T) {
	dir := t.TempDir()
	lg := Open(dir)

	ln := Line{
		Kind:     KindPlan,
		Dispatch: "t-abc",
		Tags:     map[string]any{"code": true},
	}
	if err := lg.Append("skills", ln); err != nil {
		t.Fatalf("append: %v", err)
	}

	// The file is ~/.fs/traces/<node>.jsonl, not under the project.
	want := filepath.Join(dir, "t-abc.jsonl")
	if got := lg.Path("t-abc"); got != want {
		t.Errorf("Path = %q, want flat %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("trace not at flat path: %v", err)
	}

	// The reader locates it from the node alone.
	lines, err := lg.Read("t-abc")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if lines[0].Project != "skills" {
		t.Errorf("line project = %q, want %q", lines[0].Project, "skills")
	}
}

// Append is the one place the project is set, so a call site cannot forget it —
// and a project-less line is refused rather than written without provenance.
func TestAppendRefusesEmptyProject(t *testing.T) {
	lg := Open(t.TempDir())
	if err := lg.Append("", Line{Kind: KindPlan, Dispatch: "t-abc"}); err == nil {
		t.Fatal("an empty project must refuse the write")
	}
}

// Read takes the node alone: absence is still an error naming the node, never
// an empty timeline that reads as success.
func TestReadByNodeReportsAbsence(t *testing.T) {
	lg := Open(t.TempDir())
	if _, err := lg.Read("t-ghost"); err == nil {
		t.Fatal("a node with no trace must be an error")
	}
}

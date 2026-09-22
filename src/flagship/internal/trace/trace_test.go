package trace

import (
	"os"
	"path/filepath"
	"testing"
)

// The trace is a record of a dispatch, so it is filed flat. The project leads
// the name — <project>.<node>.jsonl — because a person browsing the directory
// should recognise the file, and a dot separates the halves because both
// contain hyphens. The project stays a field on every line as well.
func TestAppendNamesTraceWithProjectLeading(t *testing.T) {
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

	// The file is ~/.fs/traces/skills.t-abc.jsonl: project first, dot separator.
	want := filepath.Join(dir, "skills.t-abc.jsonl")
	if got := lg.Path("skills", "t-abc"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("trace not at the project-led path: %v", err)
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

// The reader globs the node. If a glob matches more than one file — the same
// node id filed under two projects — it refuses naming them rather than picking
// one.
func TestReadRefusesWhenNodeMatchesMoreThanOneTrace(t *testing.T) {
	lg := Open(t.TempDir())
	for _, project := range []string{"skills", "other"} {
		if err := lg.Append(project, Line{Kind: KindPlan, Dispatch: "t-dup"}); err != nil {
			t.Fatalf("append %s: %v", project, err)
		}
	}
	if _, err := lg.Read("t-dup"); err == nil {
		t.Fatal("a node matching two traces must refuse")
	}
}

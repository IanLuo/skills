package trace

import (
	"os"
	"path/filepath"
	"strings"
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

// A step line says `moment`, the same word the plan line uses for the sections a
// playbook serves: one file that called the plan's list `moments` and each
// step's own moment `phase` disagreed with itself. Lines written before the
// rename carry `phase`, and the reader still accepts them — tolerance for a
// historical record, not a second spelling going forward.
func TestReadAcceptsTheLegacyPhaseField(t *testing.T) {
	dir := t.TempDir()
	lg := Open(dir)
	legacy := `{"ts":"2026-09-21T12:00:01Z","id":"t-legacy/code/1","dispatch":"t-legacy",` +
		`"project":"skills","playbook":"code","phase":"entry","step":1,"kind":"check","status":"pass"}`
	if err := os.WriteFile(filepath.Join(dir, "skills.t-legacy.jsonl"), []byte(legacy+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, err := lg.Read("t-legacy")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(lines) != 1 || lines[0].Moment != "entry" {
		t.Errorf("a legacy line's moment = %q, want %q", lines[0].Moment, "entry")
	}
}

// What is written says `moment` and does not also say `phase`: the legacy field
// is read, never written, so a new trace file is internally consistent.
func TestAppendWritesMomentAndNotPhase(t *testing.T) {
	dir := t.TempDir()
	lg := Open(dir)
	if err := lg.Append("skills", Line{
		ID: "t-step/code/1", Dispatch: "t-step", Playbook: "code",
		Moment: "work", Step: 1, Kind: KindLoaded, Status: StatusLoaded,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "skills.t-step.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"moment":"work"`) {
		t.Errorf("a step line must carry moment:\n%s", raw)
	}
	if strings.Contains(string(raw), "phase") {
		t.Errorf("a step line must not carry phase:\n%s", raw)
	}
}

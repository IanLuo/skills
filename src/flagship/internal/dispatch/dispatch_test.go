package dispatch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/registry"
	"github.com/flagship-dev/flagship/internal/store"
)

const devPlaybook = `name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - read AGENTS.md at the project root
  - find locked docs on disk with grep -rl for the specs:locked and design:locked markers
  - a locked PRD is present; if absent, ask the user before proceeding
  - if the locked PRD covers more than one deliverable, the user has named the slice
  - if design-system.md exists its first line carries a design:locked marker; absent means run design-task first
  - the acceptance check for this deliverable is stated
`

// fixture wires a handler, registry, and knowledge center isolated in temp dirs.
type fixture struct {
	h       *command.Handler
	reg     *registry.Registry
	kc      *knowledge.Center
	storeDB string
	kbDir   string
}

func newFixture(t *testing.T, root string) *fixture {
	t.Helper()
	dir := t.TempDir()

	dbPath := filepath.Join(dir, "store.db")
	h, err := command.NewHandler(dbPath)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	t.Cleanup(func() { h.Close() })

	reg, err := registry.Open(filepath.Join(dir, "registry.db"))
	if err != nil {
		t.Fatalf("registry.Open: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	if err := reg.Register("skills", root); err != nil {
		t.Fatalf("Register: %v", err)
	}

	kbDir := filepath.Join(dir, "kb")
	kc, err := knowledge.Open(kbDir)
	if err != nil {
		t.Fatalf("knowledge.Open: %v", err)
	}

	return &fixture{h: h, reg: reg, kc: kc, storeDB: dbPath, kbDir: kbDir}
}

func (f *fixture) writePlaybook(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.kbDir, name+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareMissingPlaybook(t *testing.T) {
	f := newFixture(t, t.TempDir())

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "no-such-type", "x")
	if resp.OK {
		t.Fatal("expected failure for a missing playbook")
	}
	if !strings.Contains(resp.Error, "no-such-type-prerequisites") {
		t.Errorf("error must name the missing playbook, got %q", resp.Error)
	}
	if !strings.Contains(resp.Error, "never improvise") {
		t.Errorf("error must say to stop, not improvise, got %q", resp.Error)
	}
}

func TestPrepareBriefListsEveryStep(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# agents\n")
	writeFile(t, filepath.Join(root, "docs", "prd.md"), "<!-- specs:locked: prd -->\n")

	f := newFixture(t, root)
	f.writePlaybook(t, "dev-task-prerequisites", devPlaybook)

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample")
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief, ok := resp.Data.(*dispatch.Brief)
	if !ok {
		t.Fatalf("data type %T, want *dispatch.Brief", resp.Data)
	}

	if brief.Goal != "sample" || brief.Project != "skills" || brief.RootPath != root {
		t.Errorf("brief header = %+v", brief)
	}
	if brief.Playbook != "dev-task-prerequisites" {
		t.Errorf("playbook = %q", brief.Playbook)
	}
	if len(brief.Checklist) != 6 {
		t.Fatalf("checklist has %d items, want one per playbook step (6)", len(brief.Checklist))
	}

	byStatus := map[string]string{}
	for _, item := range brief.Checklist {
		byStatus[item.Step] = item.Status
	}
	if got := byStatus["read AGENTS.md at the project root"]; got != "pass" {
		t.Errorf("AGENTS.md step status = %q, want pass", got)
	}
	grepStep := "find locked docs on disk with grep -rl for the specs:locked and design:locked markers"
	if got := byStatus[grepStep]; got != "pass" {
		t.Errorf("locked-doc grep step status = %q, want pass", got)
	}
	if got := byStatus["the acceptance check for this deliverable is stated"]; got != "?" {
		t.Errorf("cap-only step status = %q, want ?", got)
	}

	if len(brief.LockedDocs) != 1 || brief.LockedDocs[0] != filepath.Join("docs", "prd.md") {
		t.Errorf("locked_docs = %v, want [docs/prd.md]", brief.LockedDocs)
	}
	if len(brief.Notes) == 0 || !strings.Contains(strings.Join(brief.Notes, " "), "false positive") {
		t.Errorf("notes must flag the known grep false positive, got %v", brief.Notes)
	}
	if !strings.Contains(brief.NextCommand, "herdr pane split") ||
		!strings.Contains(brief.NextCommand, "herdr agent start") ||
		!strings.Contains(brief.NextCommand, "herdr agent prompt") {
		t.Errorf("next_command must be the herdr split/start/prompt line, got %q", brief.NextCommand)
	}
}

func TestPrepareReportsFailingMechanicalChecks(t *testing.T) {
	f := newFixture(t, t.TempDir()) // empty root: no AGENTS.md, no locked docs
	f.writePlaybook(t, "dev-task-prerequisites", devPlaybook)

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample")
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)

	for _, item := range brief.Checklist {
		if item.Status == "pass" {
			t.Errorf("step %q passed against an empty root", item.Step)
		}
	}
	if len(brief.LockedDocs) != 0 {
		t.Errorf("locked_docs = %v, want empty", brief.LockedDocs)
	}
}

func TestPrepareCreatesCapNode(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, root)
	f.writePlaybook(t, "dev-task-prerequisites", devPlaybook)

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample")
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)
	if brief.CapNodeID == "" {
		t.Fatal("cap_node_id is empty")
	}

	// The cap scope is implicit: it is a project_id, never a registry row.
	if _, err := f.reg.Get("cap"); err == nil {
		t.Error("cap must never be registered as a project")
	}

	s, err := store.Open(f.storeDB)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	events, err := s.Replay("cap", nil)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("cap scope has %d events, want task-created + decision-recorded", len(events))
	}
	if events[0].Type != store.TaskCreated {
		t.Errorf("event 0 = %s, want task-created", events[0].Type)
	}
	if events[0].NodeID == nil || *events[0].NodeID != brief.CapNodeID {
		t.Errorf("task-created node_id = %v, want %s", events[0].NodeID, brief.CapNodeID)
	}
	if !strings.Contains(string(events[0].Payload), "dispatch dev-task: sample") {
		t.Errorf("task-created payload = %s, want goal 'dispatch dev-task: sample'", events[0].Payload)
	}
	if events[1].Type != store.DecisionRecorded {
		t.Errorf("event 1 = %s, want decision-recorded", events[1].Type)
	}
	if !strings.Contains(string(events[1].Payload), "skills") {
		t.Errorf("decision payload = %s, want the target project named", events[1].Payload)
	}
}

func TestPrepareRequiresArgs(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", devPlaybook)

	for _, tc := range []struct{ project, taskType, goal string }{
		{"", "dev-task", "x"},
		{"skills", "", "x"},
		{"skills", "dev-task", ""},
	} {
		resp := dispatch.Prepare(f.h, f.reg, f.kc, tc.project, tc.taskType, tc.goal)
		if resp.OK {
			t.Errorf("Prepare(%q, %q, %q) should fail", tc.project, tc.taskType, tc.goal)
		}
	}
}

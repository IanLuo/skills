package dispatch_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
  - check: test -f AGENTS.md
  - check: grep -rl -e 'specs:locked' -e 'design:locked' --include='*.md' .
  - ask: a locked PRD is present, and it names this one deliverable
  - ask: is the acceptance check for this deliverable stated
`

const passingPlaybook = `name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: true
`

// failFastPlaybook passes, then fails, then touches a sentinel. A failing check
// must stop the run, so the sentinel is the proof that check 3 never ran.
func failFastPlaybook(sentinel string) string {
	return fmt.Sprintf(`name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: true
  - check: false
  - check: touch %s
`, sentinel)
}

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

	// Tests create cap-scope dispatch nodes, and one left unresolved makes later
	// dispatches refuse. Close them, so the suite never becomes order-dependent.
	t.Cleanup(func() { resolveCapScope(h) })

	return &fixture{h: h, reg: reg, kc: kc, storeDB: dbPath, kbDir: kbDir}
}

// resolveCapScope marks every node in the cap scope done. Best-effort: cleanup
// must not fail a test that already passed.
func resolveCapScope(h *command.Handler) {
	nodes, err := h.UnfinishedIn("cap")
	if err != nil {
		return
	}
	for _, node := range nodes {
		h.TaskUpdate("cap", node.NodeID, "done", "test cleanup", nil)
	}
}

// capDecisions returns the decisions recorded on one cap-scope node, via the
// same derived state fs status reports.
func capDecisions(t *testing.T, f *fixture, nodeID string) []string {
	t.Helper()
	task, ok := scopeTask(t, f, "cap", nodeID)
	if !ok {
		t.Fatalf("cap node %s not found", nodeID)
	}
	return task.Decisions
}

func (f *fixture) writePlaybook(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.kbDir, name+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// close runs the close-out gate against this fixture's store, registry, and
// knowledge center, so tests exercise the real signature without repeating it.
func (f *fixture) close(hc dispatch.HerdrCLI, req dispatch.CloseRequest) command.Response {
	return dispatch.Close(f.h, hc, f.reg, f.kc, req)
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

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "no-such-type", "x", false)
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

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", false)
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
	if len(brief.Checklist) != 4 {
		t.Fatalf("checklist has %d items, want the 2 checks + 2 asks", len(brief.Checklist))
	}

	byBody := map[string]dispatch.Item{}
	for _, item := range brief.Checklist {
		byBody[item.Body] = item
	}

	agentsCheck := byBody["test -f AGENTS.md"]
	if agentsCheck.Kind != knowledge.KindCheck || agentsCheck.Status != "pass" {
		t.Errorf("AGENTS.md check = %+v, want a passing check", agentsCheck)
	}
	if agentsCheck.Output != "" {
		t.Errorf("AGENTS.md check output = %q, want empty", agentsCheck.Output)
	}

	grepBody := "grep -rl -e 'specs:locked' -e 'design:locked' --include='*.md' ."
	grepCheck := byBody[grepBody]
	if grepCheck.Kind != knowledge.KindCheck || grepCheck.Status != "pass" {
		t.Errorf("locked-doc check = %+v, want a passing check", grepCheck)
	}
	if !strings.Contains(grepCheck.Output, filepath.Join("docs", "prd.md")) {
		t.Errorf("locked-doc check output = %q, want the matched doc", grepCheck.Output)
	}

	for _, ask := range []string{
		"a locked PRD is present, and it names this one deliverable",
		"is the acceptance check for this deliverable stated",
	} {
		item, ok := byBody[ask]
		if !ok {
			t.Fatalf("ask step %q missing from the checklist", ask)
		}
		if item.Kind != knowledge.KindAsk || item.Status != "?" {
			t.Errorf("ask %q = %+v, want kind ask and status ?", ask, item)
		}
	}

	if len(brief.LockedDocs) != 1 || brief.LockedDocs[0] != filepath.Join("docs", "prd.md") {
		t.Errorf("locked_docs = %v, want [docs/prd.md]", brief.LockedDocs)
	}
	if len(brief.Notes) == 0 || !strings.Contains(strings.Join(brief.Notes, " "), "false positive") {
		t.Errorf("notes must flag the known grep false positive, got %v", brief.Notes)
	}
	if !strings.Contains(brief.NextCommand, "herdr tab create") ||
		!strings.Contains(brief.NextCommand, "herdr agent start") ||
		!strings.Contains(brief.NextCommand, "herdr agent prompt") {
		t.Errorf("next_command must be the herdr tab/start/prompt line, got %q", brief.NextCommand)
	}
}

func TestPrepareStopsAtFirstFailingCheck(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "check3-ran")
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", failFastPlaybook(sentinel))

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", false)
	if resp.OK {
		t.Fatal("a failing check must refuse, not prepare a brief")
	}
	for _, want := range []string{"prerequisite check failed", `"false"`, "tell the user", "--confirm"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("check 3 ran despite check 2 failing: stat err = %v", err)
	}

	// A refusal must leave nothing behind: a cap node here would make every
	// later dispatch refuse.
	nodes, err := f.h.UnfinishedIn("cap")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("cap scope has %d nodes after a refusal, want 0", len(nodes))
	}
}

func TestPrepareConfirmRunsEveryCheckAndRecordsOverride(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "check3-ran")
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", failFastPlaybook(sentinel))

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", true)
	if !resp.OK {
		t.Fatalf("--confirm must prepare despite the failure: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)
	if len(brief.Checklist) != 3 {
		t.Fatalf("checklist has %d items, want all 3 checks", len(brief.Checklist))
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("--confirm must run every check; check 3 did not run: %v", err)
	}

	want := "user confirmed proceeding past a failing check: false"
	if decisions := capDecisions(t, f, brief.CapNodeID); !slices.Contains(decisions, want) {
		t.Errorf("cap node decisions = %v, want %q", decisions, want)
	}
}

func TestPrepareRefusesWhileAnUnresolvedDispatchExists(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", passingPlaybook)

	first := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "first", false)
	if !first.OK {
		t.Fatalf("first dispatch: %s", first.Error)
	}
	firstID := first.Data.(*dispatch.Brief).CapNodeID

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "second", false)
	if resp.OK {
		t.Fatal("expected refusal while a dispatch is unresolved")
	}
	for _, want := range []string{"unresolved dispatches", firstID, "--confirm"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}

	confirmed := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "second", true)
	if !confirmed.OK {
		t.Fatalf("--confirm dispatch: %s", confirmed.Error)
	}
	brief := confirmed.Data.(*dispatch.Brief)
	want := "user confirmed proceeding with unresolved dispatches: " + firstID
	if decisions := capDecisions(t, f, brief.CapNodeID); !slices.Contains(decisions, want) {
		t.Errorf("cap node decisions = %v, want %q", decisions, want)
	}
}

// The cap scope holds long-lived non-dispatch work (the cap loop). The gate is
// about dispatches, so that work must never block a new dispatch.
func TestPrepareIgnoresNonDispatchCapBacklog(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", passingPlaybook)

	added := f.h.TaskAdd("cap", "cap loop", nil)
	if !added.OK {
		t.Fatalf("task add: %s", added.Error)
	}
	capLoopID := added.Data.(command.EventData).NodeID
	if resp := f.h.TaskBlock("cap", capLoopID, "waiting on the user"); !resp.OK {
		t.Fatalf("task block: %s", resp.Error)
	}

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", false)
	if !resp.OK {
		t.Fatalf("a non-dispatch cap node must not gate: %s", resp.Error)
	}
}

// Regression for the original defect: meaning comes from the declared kind, not
// from words in the body. A check no keyword could match still runs.
func TestPrepareRunsCheckWhateverItsWording(t *testing.T) {
	const reworded = `name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: echo reworded
`
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", reworded)

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", false)
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)

	if len(brief.Checklist) != 1 {
		t.Fatalf("checklist has %d items, want 1", len(brief.Checklist))
	}
	item := brief.Checklist[0]
	if item.Kind != knowledge.KindCheck || item.Body != "echo reworded" {
		t.Errorf("item = %+v, want the check as declared", item)
	}
	if item.Status != "pass" {
		t.Errorf("status = %q, want pass", item.Status)
	}
	if item.Output != "reworded" {
		t.Errorf("output = %q, want reworded", item.Output)
	}
	if !strings.Contains(brief.NextCommand, "reworded") {
		t.Errorf("the brief handed to the worker must carry the check output, got %q", brief.NextCommand)
	}
}

// A say step is worker context, not a gate item: it never appears in the
// checklist and never carries a status.
func TestPrepareSayIsContextNotGate(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# agents\n")
	const procedure = `name: dev-task-prerequisites
type: procedure
trigger: dev-task
steps:
  - say: read AGENTS.md before editing
  - check: test -f AGENTS.md
`
	f := newFixture(t, root)
	f.writePlaybook(t, "dev-task-prerequisites", procedure)

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", false)
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)

	if len(brief.Checklist) != 1 || brief.Checklist[0].Kind != knowledge.KindCheck {
		t.Errorf("checklist = %+v, want only the check", brief.Checklist)
	}
	if len(brief.Context) != 1 || brief.Context[0] != "read AGENTS.md before editing" {
		t.Errorf("context = %v, want the say body", brief.Context)
	}
	if !strings.Contains(brief.NextCommand, "read AGENTS.md before editing") {
		t.Errorf("the brief handed to the worker must carry the say step, got %q", brief.NextCommand)
	}
}

func TestPrepareCreatesCapNode(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# agents\n")
	writeFile(t, filepath.Join(root, "docs", "prd.md"), "<!-- specs:locked: prd -->\n")
	f := newFixture(t, root)
	f.writePlaybook(t, "dev-task-prerequisites", devPlaybook)

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", false)
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
		resp := dispatch.Prepare(f.h, f.reg, f.kc, tc.project, tc.taskType, tc.goal, false)
		if resp.OK {
			t.Errorf("Prepare(%q, %q, %q) should fail", tc.project, tc.taskType, tc.goal)
		}
	}
}

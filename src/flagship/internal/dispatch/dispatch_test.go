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
	"github.com/flagship-dev/flagship/internal/query"
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
// A test that cares what the exit gate does writes the playbook it wants with
// writePlaybook.
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

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "no-such-type", "x", nil, "", false, false)
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

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", false, false)
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
	if !strings.Contains(brief.NextCommand, "herdr pane split") ||
		!strings.Contains(brief.NextCommand, "herdr agent start") ||
		!strings.Contains(brief.NextCommand, "herdr agent prompt") {
		t.Errorf("next_command must be the herdr split/start/prompt line, got %q", brief.NextCommand)
	}
	// The standing worker obligation travels in the brief the command sends, so
	// a worker learns how a gap outlives it before it starts.
	if !strings.Contains(brief.NextCommand, "--kind gap") ||
		!strings.Contains(brief.NextCommand, "your final message is not read") {
		t.Errorf("next_command must carry the worker obligation, got %q", brief.NextCommand)
	}
}

func TestPrepareStopsAtFirstFailingCheck(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "check3-ran")
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", failFastPlaybook(sentinel))

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", false, false)
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

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", true, false)
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

	want := `--confirm overrode the failing check "false" (output: (no output))`
	if decisions := capDecisions(t, f, brief.CapNodeID); !slices.Contains(decisions, want) {
		t.Errorf("cap node decisions = %v, want %q", decisions, want)
	}
}

func TestPrepareRefusesWhileAnUnresolvedDispatchExists(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", passingPlaybook)

	first := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "first", nil, "", false, false)
	if !first.OK {
		t.Fatalf("first dispatch: %s", first.Error)
	}
	firstID := first.Data.(*dispatch.Brief).CapNodeID

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "second", nil, "", false, false)
	if resp.OK {
		t.Fatal("expected refusal while a dispatch is unresolved")
	}
	for _, want := range []string{"unresolved dispatches", firstID, "--allow-unresolved"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}

	confirmed := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "second", nil, "", false, true)
	if !confirmed.OK {
		t.Fatalf("--allow-unresolved dispatch: %s", confirmed.Error)
	}
	brief := confirmed.Data.(*dispatch.Brief)
	want := "--allow-unresolved proceeded with unresolved dispatches: " + firstID
	if decisions := capDecisions(t, f, brief.CapNodeID); !slices.Contains(decisions, want) {
		t.Errorf("cap node decisions = %v, want %q", decisions, want)
	}
}

// Each override belongs to its own gate, and to nothing else. Answering "go
// ahead with the other dispatch open" must not wave through a failing check the
// cap never saw, and answering "go ahead despite the failing check" must not
// wave through an unresolved dispatch — the two failures the single --confirm
// flag had.
func TestPrepareOverridesDoNotCoverEachOther(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", passingPlaybook)
	first := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "first", nil, "", false, false)
	if !first.OK {
		t.Fatalf("first dispatch: %s", first.Error)
	}
	firstID := first.Data.(*dispatch.Brief).CapNodeID

	// Now the checks fail: the fixture holds an unresolved dispatch and a
	// failing prerequisite at the same time.
	f.writePlaybook(t, "dev-task-prerequisites", failFastPlaybook(filepath.Join(t.TempDir(), "check3-ran")))

	byUnresolved := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "second", nil, "", false, true)
	if byUnresolved.OK {
		t.Fatal("--allow-unresolved must not override a failing check")
	}
	for _, want := range []string{"prerequisite check failed", "--confirm"} {
		if !strings.Contains(byUnresolved.Error, want) {
			t.Errorf("--allow-unresolved refusal %q must mention %q", byUnresolved.Error, want)
		}
	}

	byConfirm := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "second", nil, "", true, false)
	if byConfirm.OK {
		t.Fatal("--confirm must not override an unresolved dispatch")
	}
	for _, want := range []string{"unresolved dispatches", firstID, "--allow-unresolved"} {
		if !strings.Contains(byConfirm.Error, want) {
			t.Errorf("--confirm refusal %q must mention %q", byConfirm.Error, want)
		}
	}

	// Neither refusal proceeded: the only cap node is still the first dispatch.
	nodes, err := f.h.UnfinishedIn("cap")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != firstID {
		t.Errorf("cap nodes = %v, want only the first dispatch %s", nodes, firstID)
	}
}

// The dispatch gate reads the node's kind, not its goal, so a dispatch whose
// goal was rewritten still blocks a new dispatch.
func TestPrepareRefusesForADispatchWhoseGoalWasEdited(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", passingPlaybook)

	first := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "first", nil, "", false, false)
	if !first.OK {
		t.Fatalf("first dispatch: %s", first.Error)
	}
	firstID := first.Data.(*dispatch.Brief).CapNodeID
	if resp := f.h.TaskEdit("cap", firstID, "no longer says dispatch", "", nil); !resp.OK {
		t.Fatalf("task edit: %s", resp.Error)
	}

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "second", nil, "", false, false)
	if resp.OK {
		t.Fatal("a dispatch whose goal was rewritten must still gate")
	}
	if !strings.Contains(resp.Error, firstID) {
		t.Errorf("error %q must name the edited dispatch %s", resp.Error, firstID)
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

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", false, false)
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

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", false, false)
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

// A check runs with the dispatch's own inputs in its environment, named FS_*.
//
// This is what makes the parallel gate possible at all: the batch's cards exist
// nowhere a static shell string can read, so a prerequisite over them could not
// be written without it. printenv rather than echo is deliberate — it fails on
// an absent variable, so passing proves each one is set, while an empty FS_CARDS
// must be visible as set-and-empty rather than mistaken for a missing env.
func TestPrepareGivesEveryCheckTheDispatchEnv(t *testing.T) {
	const envPlaybook = `name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: printenv FS_PROJECT; printenv FS_TYPE; printenv FS_GOAL; printenv FS_CARDS
`
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", envPlaybook)

	withCards := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", []string{"a.md", "b.md"}, "", false, false)
	if !withCards.OK {
		t.Fatalf("Prepare with cards: %s", withCards.Error)
	}
	brief := withCards.Data.(*dispatch.Brief)
	if len(brief.Checklist) != 1 {
		t.Fatalf("checklist = %+v, want the one check", brief.Checklist)
	}
	if got, want := brief.Checklist[0].Output, "skills\ndev-task\nsample\na.md,b.md"; got != want {
		t.Errorf("check saw %q, want %q", got, want)
	}

	// FS_CARDS is exported empty rather than left absent, so a gate that reads
	// it can tell "this dispatch named no cards" from "this step has no env".
	// The first dispatch has to be resolved first: an outstanding one gates the
	// next unless --confirm overrides it.
	if resp := f.h.TaskUpdate("cap", brief.CapNodeID, "done", "test cleanup", nil); !resp.OK {
		t.Fatalf("resolving the first dispatch: %s", resp.Error)
	}
	noCards := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", false, false)
	if !noCards.OK {
		t.Fatalf("Prepare without cards: %s", noCards.Error)
	}
	if got, want := noCards.Data.(*dispatch.Brief).Checklist[0].Output, "skills\ndev-task\nsample"; got != want {
		t.Errorf("check saw %q, want %q", got, want)
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

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", false, false)
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

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", false, false)
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
		resp := dispatch.Prepare(f.h, f.reg, f.kc, tc.project, tc.taskType, tc.goal, nil, "", false, false)
		if resp.OK {
			t.Errorf("Prepare(%q, %q, %q) should fail", tc.project, tc.taskType, tc.goal)
		}
	}
}

// A playbook's trigger must agree with the task type its name selects it for.
// dispatch loads "<type>-prerequisites", so a trigger naming another type is a
// contradiction: refuse, naming both values.
func TestPrepareRefusesAPlaybookWhoseTriggerContradictsItsType(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", `name: dev-task-prerequisites
type: prerequisite
trigger: nonsense
steps:
  - check: true
`)

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", false, false)
	if resp.OK {
		t.Fatal("a contradictory trigger must refuse, not prepare a brief")
	}
	for _, want := range []string{"nonsense", `"dev-task"`, "empty"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}

	// The refusal is a config error, not a gate: --confirm must not override it.
	confirmed := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", true, false)
	if confirmed.OK {
		t.Fatal("--confirm must not override a contradictory trigger")
	}

	// And nothing was left behind to gate the next dispatch.
	nodes, err := f.h.UnfinishedIn("cap")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("cap scope has %d nodes after a refusal, want 0", len(nodes))
	}
}

// The field is optional. The name is what selects a playbook, so an absent
// trigger is a silence, not a contradiction — the shipped playbooks rely on it.
func TestPrepareAcceptsAPlaybookWithNoTrigger(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", `name: dev-task-prerequisites
type: prerequisite
steps:
  - check: true
`)

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", false, false)
	if !resp.OK {
		t.Fatalf("an empty trigger must be accepted: %s", resp.Error)
	}
}

// passingIntegratePlaybook is the integrate entry gate in its simplest honest
// form: a gate test is about the pipework, not about the shipped wording.
const passingIntegratePlaybook = `name: integrate-prerequisites
type: prerequisite
trigger: integrate
steps:
  - check: true
`

// --integrates names the member this integration merges. The link is written
// into the integration's task-created payload, and events are immutable, so a
// link that names nothing — or names a node that is not a dispatch — must be
// refused, naming it.
func TestPrepareRefusesAnIntegratesThatIsNotADispatchNode(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "integrate-prerequisites", passingIntegratePlaybook)

	gap := addCapKindNode(t, f, "a gap, never dispatched", query.KindGap)
	for _, tc := range []struct{ ref, want string }{
		{gap, "not a dispatch node"},
		{"t-00000000", "no node t-00000000 in scope cap"},
		{"other:t-1", "names scope other"},
		{"cap:", "names no node"},
	} {
		resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "integrate", "merge", nil, tc.ref, false, false)
		if resp.OK {
			t.Errorf("--integrates %s must be refused", tc.ref)
			continue
		}
		if !strings.Contains(resp.Error, tc.want) {
			t.Errorf("refusal for %q = %q, want it to mention %q", tc.ref, resp.Error, tc.want)
		}
		if !strings.Contains(resp.Error, "dispatch:") {
			t.Errorf("refusal for %q = %q, want it to say which command refused", tc.ref, resp.Error)
		}
	}

	// A refusal leaves nothing behind to gate the next dispatch.
	nodes, err := f.h.UnfinishedIn("cap")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 { // only the gap node
		t.Errorf("cap scope has %d nodes after refusals, want only the gap", len(nodes))
	}
}

// A valid link is recorded on the integration's own task-created payload — the
// structural statement of which member this dispatch integrates — and is
// readable from the node without parsing any goal text.
func TestPrepareRecordsTheMemberOnTheIntegrationNode(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "integrate-prerequisites", passingIntegratePlaybook)

	member := addCapKindNode(t, f, "dispatch parallel: member", query.KindDispatch)
	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "integrate", "merge member", nil, member, false, false)
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)

	task, ok := scopeTask(t, f, "cap", brief.CapNodeID)
	if !ok || task.Integrates != member {
		t.Errorf("integration node integrates = %q, want %q", task.Integrates, member)
	}
	if task.Kind != query.KindDispatch {
		t.Errorf("integration node kind = %q, want dispatch", task.Kind)
	}

	// The payload itself carries it: this is what a reader of the event sees.
	payload := string(capEventsOf(t, f, brief.CapNodeID)[0].Payload)
	if !strings.Contains(payload, `"integrates":"`+member+`"`) {
		t.Errorf("task-created payload = %s, want the member link", payload)
	}
}

// The member's dispatch is closed only after the integration it waits on is
// done, so a member is expected to be unresolved while its integration is being
// prepared. It is the subject of this dispatch, not other open work piled on
// top of it: `--allow-unresolved` must not be needed to integrate a member.
// Every other open dispatch still refuses.
func TestPrepareGatesOnOtherOpenDispatchesButNotOnTheMemberItIntegrates(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "integrate-prerequisites", passingIntegratePlaybook)

	member := addCapKindNode(t, f, "dispatch parallel: member one", query.KindDispatch)

	// Only the named member is open: the integration prepares without an override.
	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "integrate", "merge one", nil, member, false, false)
	if !resp.OK {
		t.Fatalf("the member it integrates must not trip the unresolved gate: %s", resp.Error)
	}

	// A second, unrelated dispatch is open now, so the same call refuses and
	// names it — and the member it names is still not the reason.
	other := addCapKindNode(t, f, "dispatch parallel: member two", query.KindDispatch)
	resp = dispatch.Prepare(f.h, f.reg, f.kc, "skills", "integrate", "merge one", nil, member, false, false)
	if resp.OK {
		t.Fatal("another open dispatch must still trip the gate")
	}
	if !strings.Contains(resp.Error, other) || !strings.Contains(resp.Error, "--allow-unresolved") {
		t.Errorf("refusal %q must name %s and the override", resp.Error, other)
	}

	// The override is still the way past it, and it is recorded.
	resp = dispatch.Prepare(f.h, f.reg, f.kc, "skills", "integrate", "merge one", nil, member, false, true)
	if !resp.OK {
		t.Fatalf("--allow-unresolved must proceed: %s", resp.Error)
	}
}

// The entry gate sees the member this dispatch integrates, so it can be a gate
// about this integration and not about the batch.
func TestPrepareGivesChecksTheIntegratesLink(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "integrate-prerequisites", `name: integrate-prerequisites
type: prerequisite
trigger: integrate
steps:
  - check: printenv FS_INTEGRATES
  - check: printenv FS_CARDS
`)

	member := addCapKindNode(t, f, "dispatch parallel: member", query.KindDispatch)
	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "integrate", "merge", nil, member, false, false)
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)
	if len(brief.Checklist) != 2 {
		t.Fatalf("checklist = %+v, want the two checks", brief.Checklist)
	}
	// printenv fails on an absent variable, so both passing proves both are set;
	// FS_CARDS is set-and-empty rather than missing.
	if got := brief.Checklist[0].Output; got != member {
		t.Errorf("FS_INTEGRATES = %q, want the member %s", got, member)
	}
	if got := brief.Checklist[1].Output; got != "" {
		t.Errorf("FS_CARDS = %q, want empty-and-set", got)
	}
}

// A dispatch that integrates nothing records no link, and its checks see an
// empty FS_INTEGRATES rather than a missing one.
func TestPrepareWithoutIntegratesRecordsNoLink(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "dev-task-prerequisites", passingPlaybook)

	resp := dispatch.Prepare(f.h, f.reg, f.kc, "skills", "dev-task", "sample", nil, "", false, false)
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)
	task, ok := scopeTask(t, f, "cap", brief.CapNodeID)
	if !ok || task.Integrates != "" {
		t.Errorf("integrates = %q, want none for a dispatch that integrates nothing", task.Integrates)
	}
}

// capEventsOf returns every event appended to one node, in order.
func capEventsOf(t *testing.T, f *fixture, nodeID string) []store.Event {
	t.Helper()
	s, err := store.Open(f.storeDB)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	events, err := s.Replay("cap", &store.ReplayFilter{NodeID: &nodeID})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return events
}

// fs integrated answers the member's question. A pending integration does not
// count — the member is not integrated until the integration's own gate has
// passed and its node is done — and a scope with no such integration says so
// with what to do instead.
func TestIntegratedAnswersWithTheDoneIntegrationThatNamesTheMember(t *testing.T) {
	f := newFixture(t, t.TempDir())
	member := addCapKindNode(t, f, "dispatch parallel: member", query.KindDispatch)
	other := addCapKindNode(t, f, "dispatch parallel: other member", query.KindDispatch)

	link := addIntegrationNode(t, f, member)
	unrelated := addIntegrationNode(t, f, other)

	resp := dispatch.Integrated(f.h, member)
	if resp.OK {
		t.Fatal("a pending integration must not count as integrating the member")
	}
	if !strings.Contains(resp.Error, member) {
		t.Errorf("refusal %q must name the member", resp.Error)
	}

	// A done integration that does not name this member does not answer for it.
	if r := f.h.TaskUpdate("cap", unrelated, "done", "merged something else", nil); !r.OK {
		t.Fatal(r.Error)
	}
	if resp := dispatch.Integrated(f.h, member); resp.OK {
		t.Error("an integration naming another member must not answer for this one")
	}

	if r := f.h.TaskUpdate("cap", link, "done", "merged the member", nil); !r.OK {
		t.Fatal(r.Error)
	}
	resp = dispatch.Integrated(f.h, member)
	if !resp.OK {
		t.Fatalf("Integrated: %s", resp.Error)
	}
	result := resp.Data.(dispatch.IntegratedResult)
	if result.Member != member || result.IntegratedBy != link {
		t.Errorf("result = %+v, want member %s integrated by %s", result, member, link)
	}
}

// A member with no integration at all — the hand-merge case — is refused, with
// the dispatched route named. This is what the member's exit gate rests on: an
// outcome check could not tell a hand merge from a dispatched one.
func TestIntegratedRefusesAMemberNoIntegrationNames(t *testing.T) {
	f := newFixture(t, t.TempDir())
	member := addCapKindNode(t, f, "dispatch parallel: member", query.KindDispatch)

	resp := dispatch.Integrated(f.h, member)
	if resp.OK {
		t.Fatal("a member with no integration must be refused")
	}
	for _, want := range []string{member, "--integrates", "never by hand"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("refusal %q must mention %q", resp.Error, want)
		}
	}
}

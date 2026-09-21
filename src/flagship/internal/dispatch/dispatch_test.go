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
	"github.com/flagship-dev/flagship/internal/trace"
)

// devPlaybook is the entry gate for `code` work: two checks and two asks,
// carrying `applies_when: code` so only a dispatch that declares --tag code
// selects it. A playbook's name no longer selects anything.
const devPlaybook = `name: code-entry
phase: entry
applies_when: code
order: 10
steps:
  - check: test -f AGENTS.md
  - check: grep -rl -e 'specs:locked' -e 'design:locked' --include='*.md' .
  - ask: a locked PRD is present, and it names this one deliverable
  - ask: is the acceptance check for this deliverable stated
`

// codeWork is the same dispatch's work playbook: a `say` step that becomes an
// obligation and a `use` step that becomes a hint. Both are text in a file a
// user can edit, which is the model's whole point.
const codeWork = `name: code-work
phase: work
applies_when: code
order: 20
steps:
  - say: run the suite before you claim the work is done
  - use: tdd
`

const passingEntry = `name: code-entry
phase: entry
applies_when: code
steps:
  - check: true
`

// failFastEntryPlaybook passes, then fails, then touches a sentinel. A failing
// check must stop the run, so the sentinel is the proof that check 3 never ran.
func failFastEntryPlaybook(sentinel string) string {
	return fmt.Sprintf(`name: code-entry
phase: entry
applies_when: code
steps:
  - check: true
  - check: false
  - check: touch %s
`, sentinel)
}

// integrationEntryPlaybook is an integration's entry gate. `type=integrate` is a
// derived tag — the dispatch's own --type sets it — so this playbook loads
// without any declared --tag.
const integrationEntryPlaybook = `name: integration-entry
phase: entry
applies_when: type=integrate
steps:
  - check: true
`

// fixture wires a handler, registry, knowledge center, and trace store isolated
// in temp dirs. fsRoot is the temp HOME's `~/.fs`: both scopes hang off it, so
// nothing a test does can reach the operator's real knowledge center or logs.
type fixture struct {
	h       *command.Handler
	reg     *registry.Registry
	kc      *knowledge.Center
	lg      *trace.Log
	storeDB string
	fsRoot  string
	kbDir   string
}

func newFixture(t *testing.T, root string) *fixture {
	t.Helper()
	fsRoot := t.TempDir()

	dbPath := filepath.Join(fsRoot, "store.db")
	h, err := command.NewHandler(dbPath)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	t.Cleanup(func() { h.Close() })

	reg, err := registry.Open(filepath.Join(fsRoot, "registry.db"))
	if err != nil {
		t.Fatalf("registry.Open: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	if err := reg.Register("skills", root); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// knowledge.Open takes the fs root, not the playbooks directory: it is the
	// scope above `kb/` that a project's own playbooks are named against.
	kc, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatalf("knowledge.Open: %v", err)
	}

	lg := trace.Open(filepath.Join(fsRoot, "projects"))

	// Tests create cap-scope dispatch nodes, and one left unresolved makes later
	// dispatches refuse. Close them, so the suite never becomes order-dependent.
	t.Cleanup(func() { resolveCapScope(h) })

	return &fixture{
		h: h, reg: reg, kc: kc, lg: lg,
		storeDB: dbPath, fsRoot: fsRoot, kbDir: filepath.Join(fsRoot, "kb"),
	}
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

// devReq is the ordinary dispatch these tests prepare: a dev-task into skills
// that declares the `code` tag, which is what selects the fixtures above.
func devReq(goal string) dispatch.PrepareRequest {
	return dispatch.PrepareRequest{
		Project: "skills", TaskType: "dev-task", Goal: goal, Tags: []string{"code"},
	}
}

// prepare runs the dispatch gate against this fixture's store, registry,
// knowledge center, and trace log, so tests exercise the real signature without
// repeating it.
func (f *fixture) prepare(req dispatch.PrepareRequest) command.Response {
	return dispatch.Prepare(f.h, f.reg, f.kc, f.lg, req)
}

// close runs the close-out gate the same way.
func (f *fixture) close(hc dispatch.HerdrCLI, req dispatch.CloseRequest) command.Response {
	return dispatch.Close(f.h, hc, f.reg, f.kc, f.lg, req)
}

// writePlaybook puts one playbook in the global scope, the way fs bootstrap or a
// user's editor would.
func (f *fixture) writePlaybook(t *testing.T, name, content string) {
	t.Helper()
	writeFile(t, filepath.Join(f.kbDir, name+".yaml"), content)
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

// traceOf reads a dispatch's trace, so a test can assert what was recorded and
// not only what was returned.
func traceOf(t *testing.T, f *fixture, project, node string) []trace.Line {
	t.Helper()
	lines, err := f.lg.Read(project, node)
	if err != nil {
		t.Fatalf("read trace for %s: %v", node, err)
	}
	return lines
}

// The brief carries the plan the tags resolved — each playbook with its phase
// and the order it declared — alongside the entry gate's checklist and the work
// playbook's own obligations and hints.
func TestPrepareBriefCarriesThePlanAndEveryStep(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# agents\n")
	writeFile(t, filepath.Join(root, "docs", "prd.md"), "<!-- specs:locked: prd -->\n")

	f := newFixture(t, root)
	f.writePlaybook(t, "code-entry", devPlaybook)
	f.writePlaybook(t, "code-work", codeWork)

	resp := f.prepare(devReq("sample"))
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

	// The plan is the resolution of the tags, recorded in order.
	wantPlan := []trace.PlanEntry{
		{Name: "code-entry", Phase: knowledge.PhaseEntry, Order: 10},
		{Name: "code-work", Phase: knowledge.PhaseWork, Order: 20},
	}
	if !slices.Equal(brief.Plan, wantPlan) {
		t.Errorf("plan = %+v, want %+v", brief.Plan, wantPlan)
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

	// The worker's obligations and hints are the work playbook's own steps, and
	// they travel in the text the cap sends — not a string compiled into the
	// binary. This is what makes the brief something a user can edit.
	if !slices.Equal(brief.Obligations, []string{"run the suite before you claim the work is done"}) {
		t.Errorf("obligations = %v, want the work playbook's say step", brief.Obligations)
	}
	if !slices.Equal(brief.Skills, []string{"tdd"}) {
		t.Errorf("skills = %v, want the work playbook's use step", brief.Skills)
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
	for _, want := range []string{
		"run the suite before you claim the work is done",
		"- tdd",
		"- code-entry (entry, order 10)",
		"- code-work (work, order 20)",
	} {
		if !strings.Contains(brief.NextCommand, want) {
			t.Errorf("the brief handed to the worker must carry %q, got %q", want, brief.NextCommand)
		}
	}
}

func TestPrepareStopsAtFirstFailingCheck(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "check3-ran")
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "code-entry", failFastEntryPlaybook(sentinel))

	resp := f.prepare(devReq("sample"))
	if resp.OK {
		t.Fatal("a failing check must refuse, not prepare a brief")
	}
	for _, want := range []string{"entry check", `"false"`, "code-entry", "step 2", "tell the user", "--confirm"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("check 3 ran despite check 2 failing: stat err = %v", err)
	}

	// The nodes come before the entry gate — the trace is named after the node,
	// and an entry step's outcome belongs on the record — so a refusal rolls the
	// node back rather than leaving it out. Nothing outstanding is left to gate
	// the next dispatch, and the attempt is legible.
	unfinished, err := f.h.UnfinishedIn("cap")
	if err != nil {
		t.Fatal(err)
	}
	if len(unfinished) != 0 {
		t.Errorf("cap scope has %d open nodes after a refusal, want 0", len(unfinished))
	}
	nodes := scopeTasks(t, f, "cap")
	if len(nodes) != 1 {
		t.Fatalf("cap scope has %d nodes, want the one rolled-back attempt", len(nodes))
	}
	if nodes[0].Status != "done" {
		t.Errorf("refused dispatch status = %q, want done", nodes[0].Status)
	}
	if !strings.Contains(strings.Join(nodes[0].Decisions, " "), "dispatch refused at entry") {
		t.Errorf("refused dispatch decisions = %v, want the refusal recorded", nodes[0].Decisions)
	}

	// Both steps ran to the point of failure, and both are on the trace: the
	// passing one, and the failing one the refusal follows.
	lines := traceOf(t, f, "skills", nodes[0].NodeID)
	var steps []trace.Line
	for _, ln := range lines {
		if ln.Kind != trace.KindPlan {
			steps = append(steps, ln)
		}
	}
	if len(steps) != 2 {
		t.Fatalf("trace has %d step lines, want the two steps that ran: %+v", len(steps), steps)
	}
	if steps[0].Status != trace.StatusPass || steps[1].Status != trace.StatusFail {
		t.Errorf("trace statuses = %q, %q, want pass then fail", steps[0].Status, steps[1].Status)
	}
}

func TestPrepareConfirmRunsEveryCheckAndRecordsOverride(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "check3-ran")
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "code-entry", failFastEntryPlaybook(sentinel))

	req := devReq("sample")
	req.Confirm = true
	resp := f.prepare(req)
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
	f.writePlaybook(t, "code-entry", passingEntry)

	first := f.prepare(devReq("first"))
	if !first.OK {
		t.Fatalf("first dispatch: %s", first.Error)
	}
	firstID := first.Data.(*dispatch.Brief).CapNodeID

	resp := f.prepare(devReq("second"))
	if resp.OK {
		t.Fatal("expected refusal while a dispatch is unresolved")
	}
	for _, want := range []string{"unresolved dispatches", firstID, "--allow-unresolved"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}

	override := devReq("second")
	override.AllowUnresolved = true
	confirmed := f.prepare(override)
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
	f.writePlaybook(t, "code-entry", passingEntry)
	first := f.prepare(devReq("first"))
	if !first.OK {
		t.Fatalf("first dispatch: %s", first.Error)
	}
	firstID := first.Data.(*dispatch.Brief).CapNodeID

	// Now the checks fail: the fixture holds an unresolved dispatch and a
	// failing entry check at the same time.
	f.writePlaybook(t, "code-entry", failFastEntryPlaybook(filepath.Join(t.TempDir(), "check3-ran")))

	byUnresolved := devReq("second")
	byUnresolved.AllowUnresolved = true
	resp := f.prepare(byUnresolved)
	if resp.OK {
		t.Fatal("--allow-unresolved must not override a failing check")
	}
	for _, want := range []string{"entry check", "--confirm"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("--allow-unresolved refusal %q must mention %q", resp.Error, want)
		}
	}

	byConfirm := devReq("second")
	byConfirm.Confirm = true
	resp = f.prepare(byConfirm)
	if resp.OK {
		t.Fatal("--confirm must not override an unresolved dispatch")
	}
	for _, want := range []string{"unresolved dispatches", firstID, "--allow-unresolved"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("--confirm refusal %q must mention %q", resp.Error, want)
		}
	}

	// Neither refusal proceeded: the second attempt was rolled back, so the only
	// open node is still the first dispatch.
	unfinished, err := f.h.UnfinishedIn("cap")
	if err != nil {
		t.Fatal(err)
	}
	if len(unfinished) != 1 || unfinished[0].NodeID != firstID {
		t.Errorf("open cap nodes = %v, want only the first dispatch %s", unfinished, firstID)
	}
}

// The dispatch gate reads the node's kind, not its goal, so a dispatch whose
// goal was rewritten still blocks a new dispatch.
func TestPrepareRefusesForADispatchWhoseGoalWasEdited(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "code-entry", passingEntry)

	first := f.prepare(devReq("first"))
	if !first.OK {
		t.Fatalf("first dispatch: %s", first.Error)
	}
	firstID := first.Data.(*dispatch.Brief).CapNodeID
	if resp := f.h.TaskEdit("cap", firstID, "no longer says dispatch", "", nil); !resp.OK {
		t.Fatalf("task edit: %s", resp.Error)
	}

	resp := f.prepare(devReq("second"))
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
	f.writePlaybook(t, "code-entry", passingEntry)

	added := f.h.TaskAdd("cap", "cap loop", nil)
	if !added.OK {
		t.Fatalf("task add: %s", added.Error)
	}
	capLoopID := added.Data.(command.EventData).NodeID
	if resp := f.h.TaskBlock("cap", capLoopID, "waiting on the user"); !resp.OK {
		t.Fatalf("task block: %s", resp.Error)
	}

	resp := f.prepare(devReq("sample"))
	if !resp.OK {
		t.Fatalf("a non-dispatch cap node must not gate: %s", resp.Error)
	}
}

// Regression for the original defect: meaning comes from the declared kind, not
// from words in the body. A check no keyword could match still runs.
func TestPrepareRunsCheckWhateverItsWording(t *testing.T) {
	const reworded = `name: code-entry
phase: entry
applies_when: code
steps:
  - check: echo reworded
`
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "code-entry", reworded)

	resp := f.prepare(devReq("sample"))
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
// This is what makes a gate over the dispatch itself possible at all: which
// cards, which type, which member exist nowhere a static shell string can read.
// printenv rather than echo is deliberate — it fails on an absent variable, so
// passing proves each one is set, while an empty FS_CARDS must be visible as
// set-and-empty rather than mistaken for a missing env.
func TestPrepareGivesEveryCheckTheDispatchEnv(t *testing.T) {
	const envPlaybook = `name: code-entry
phase: entry
applies_when: code
steps:
  - check: printenv FS_PROJECT; printenv FS_TYPE; printenv FS_GOAL; printenv FS_CARDS
`
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "code-entry", envPlaybook)

	withCards := devReq("sample")
	withCards.Cards = []string{"a.md", "b.md"}
	resp := f.prepare(withCards)
	if !resp.OK {
		t.Fatalf("Prepare with cards: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)
	if len(brief.Checklist) != 1 {
		t.Fatalf("checklist = %+v, want the one check", brief.Checklist)
	}
	if got, want := brief.Checklist[0].Output, "skills\ndev-task\nsample\na.md,b.md"; got != want {
		t.Errorf("check saw %q, want %q", got, want)
	}

	// FS_CARDS is exported empty rather than left absent, so a gate that reads
	// it can tell "this dispatch named no cards" from "this step has no env".
	// The first dispatch has to be resolved first: an outstanding one gates the
	// next.
	if resp := f.h.TaskUpdate("cap", brief.CapNodeID, "done", "test cleanup", nil); !resp.OK {
		t.Fatalf("resolving the first dispatch: %s", resp.Error)
	}
	noCards := f.prepare(devReq("sample"))
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
	const procedure = `name: code-entry
phase: entry
applies_when: code
steps:
  - say: read AGENTS.md before editing
  - check: test -f AGENTS.md
`
	f := newFixture(t, root)
	f.writePlaybook(t, "code-entry", procedure)

	resp := f.prepare(devReq("sample"))
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
	f.writePlaybook(t, "code-entry", devPlaybook)

	resp := f.prepare(devReq("sample"))
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
	// Three events, in the order Prepare appends them: the node, the decision
	// naming the project it was dispatched to, and the plan it is measured
	// against — which the exit gate reads rather than re-deriving it.
	if len(events) != 3 {
		t.Fatalf("cap scope has %d events, want task-created + decision-recorded + plan-recorded", len(events))
	}
	for i, want := range []store.EventType{store.TaskCreated, store.DecisionRecorded, store.PlanRecorded} {
		if events[i].Type != want {
			t.Errorf("event %d = %s, want %s", i, events[i].Type, want)
		}
	}
	if events[0].NodeID == nil || *events[0].NodeID != brief.CapNodeID {
		t.Errorf("task-created node_id = %v, want %s", events[0].NodeID, brief.CapNodeID)
	}
	if !strings.Contains(string(events[0].Payload), "dispatch dev-task: sample") {
		t.Errorf("task-created payload = %s, want goal 'dispatch dev-task: sample'", events[0].Payload)
	}
	if !strings.Contains(string(events[1].Payload), "skills") {
		t.Errorf("decision payload = %s, want the target project named", events[1].Payload)
	}
	for _, want := range []string{`"name":"code-entry"`, `"phase":"entry"`, `"tags":{"code":true`} {
		if !strings.Contains(string(events[2].Payload), want) {
			t.Errorf("plan payload = %s, want it to carry %s", events[2].Payload, want)
		}
	}

	// The same plan is the trace's first line, so `fs trace` shows what the
	// dispatch was measured against before any step has an outcome. It comes
	// before the entry steps' own lines, which are written as they run.
	lines := traceOf(t, f, "skills", brief.CapNodeID)
	if len(lines) == 0 || lines[0].Kind != trace.KindPlan {
		t.Fatalf("trace = %+v, want the plan line first", lines)
	}
	if len(lines[0].Plan) != 1 || lines[0].Plan[0].Name != "code-entry" ||
		lines[0].Plan[0].Phase != "entry" || lines[0].Plan[0].Order != 10 {
		t.Errorf("plan line = %+v, want the resolved plan", lines[0])
	}
	if lines[0].Dispatch != brief.CapNodeID {
		t.Errorf("plan line dispatch = %q, want %q", lines[0].Dispatch, brief.CapNodeID)
	}
}

func TestPrepareRequiresArgs(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "code-entry", devPlaybook)

	for _, tc := range []struct{ project, taskType, goal string }{
		{"", "dev-task", "x"},
		{"skills", "", "x"},
		{"skills", "dev-task", ""},
	} {
		resp := f.prepare(dispatch.PrepareRequest{
			Project: tc.project, TaskType: tc.taskType, Goal: tc.goal, Tags: []string{"code"},
		})
		if resp.OK {
			t.Errorf("Prepare(%q, %q, %q) should fail", tc.project, tc.taskType, tc.goal)
		}
	}
}

// A dispatch whose tags select no playbook is legal: the plan is empty, and both
// the brief and the trace say so rather than leaving the absence to be read as a
// missing record. An empty plan the exit gate can see is what makes "nothing was
// declared" different from "nothing was recorded".
func TestPrepareWithNoMatchingPlaybookIsALegalEmptyPlan(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "code-entry", devPlaybook)

	// The playbook is tagged `code`; this dispatch declares nothing.
	req := dispatch.PrepareRequest{Project: "skills", TaskType: "research", Goal: "explore"}
	resp := f.prepare(req)
	if !resp.OK {
		t.Fatalf("an empty plan must not refuse: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)
	if len(brief.Plan) != 0 {
		t.Errorf("plan = %+v, want none", brief.Plan)
	}
	if !strings.Contains(brief.NextCommand, "plan: empty") {
		t.Errorf("the brief must say the plan is empty, got %q", brief.NextCommand)
	}

	lines := traceOf(t, f, "skills", brief.CapNodeID)
	recorded, ok := trace.PlanOf(lines)
	if !ok {
		t.Fatal("an empty plan must still be recorded: a trace with no plan line cannot be gated")
	}
	if len(recorded.Plan) != 0 {
		t.Errorf("recorded plan = %+v, want an empty plan with its tags", recorded.Plan)
	}
	if recorded.Tags["type"] != "research" {
		t.Errorf("recorded tags = %v, want the dispatch's own type", recorded.Tags)
	}
}

// `applies_when` is optional, and an absent one is a silence rather than a
// contradiction: a playbook that declares no terms loads for every dispatch.
func TestPrepareSelectsAPlaybookWithNoAppliesWhen(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "worker", `name: worker
phase: work
steps:
  - say: note your work on the node you were given
`)

	resp := f.prepare(devReq("sample"))
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)
	if len(brief.Plan) != 1 || brief.Plan[0].Name != "worker" {
		t.Fatalf("plan = %+v, want the playbook that declares no applies_when", brief.Plan)
	}
	if !strings.Contains(brief.NextCommand, "note your work on the node you were given") {
		t.Errorf("its say step must reach the worker, got %q", brief.NextCommand)
	}
}

// A playbook is held to the schema its phase declares before it can run half its
// steps. This is a configuration error, not a gate: --confirm must not override
// it, and nothing is left behind.
func TestPrepareRefusesAPlaybookWithAStepItsPhaseForbids(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "code-entry", `name: code-entry
phase: entry
applies_when: code
steps:
  - do: touch unexpected
`)

	resp := f.prepare(devReq("sample"))
	if resp.OK {
		t.Fatal("a step the phase forbids must refuse, not prepare a brief")
	}
	for _, want := range []string{"code-entry", "step 1", `kind "do"`, "allowed kinds: check, ask, say, include"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}

	confirmed := devReq("sample")
	confirmed.Confirm = true
	if resp := f.prepare(confirmed); resp.OK {
		t.Fatal("--confirm must not override a playbook that is not legal for its phase")
	}

	nodes, err := f.h.UnfinishedIn("cap")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("cap scope has %d nodes after a refusal, want 0", len(nodes))
	}
}

// Seeding never deletes, so after an upgrade the knowledge center still holds
// the playbooks the removed schema named. They are refused rather than skipped:
// a dispatch that silently dropped a protocol would be a dispatch that silently
// changed what it gates. This is why the operator migration after `fs bootstrap`
// is not cosmetic — one file left behind refuses every dispatch, not just its
// own.
func TestPrepareRefusesWhileAPlaybookFromTheRemovedSchemaIsOnDisk(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "code-entry", passingEntry)
	f.writePlaybook(t, "dev-task-prerequisites", `name: dev-task-prerequisites
type: prerequisite
trigger: dev-task
steps:
  - check: true
`)

	resp := f.prepare(devReq("sample"))
	if resp.OK {
		t.Fatal("a playbook from the removed schema must refuse the dispatch, not be skipped")
	}
	for _, want := range []string{"dev-task-prerequisites", "removed field", "fs kb remove dev-task-prerequisites"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}

	nodes, err := f.h.UnfinishedIn("cap")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("cap scope has %d nodes after a refusal, want 0", len(nodes))
	}
}

// --integrates names the member this integration merges. The link is written
// into the integration's task-created payload, and events are immutable, so a
// link that names nothing — or names a node that is not a dispatch — must be
// refused, naming it.
func TestPrepareRefusesAnIntegratesThatIsNotADispatchNode(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "integration-entry", integrationEntryPlaybook)

	gap := addCapKindNode(t, f, "a gap, never dispatched", query.KindGap)
	for _, tc := range []struct{ ref, want string }{
		{gap, "not a dispatch node"},
		{"t-00000000", "no node t-00000000 in scope cap"},
		{"other:t-1", "names scope other"},
		{"cap:", "names no node"},
	} {
		resp := f.prepare(dispatch.PrepareRequest{
			Project: "skills", TaskType: "integrate", Goal: "merge", Integrates: tc.ref,
		})
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
	f.writePlaybook(t, "integration-entry", integrationEntryPlaybook)

	member := addCapKindNode(t, f, "dispatch parallel: member", query.KindDispatch)
	resp := f.prepare(dispatch.PrepareRequest{
		Project: "skills", TaskType: "integrate", Goal: "merge member", Integrates: member,
	})
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
	f.writePlaybook(t, "integration-entry", integrationEntryPlaybook)

	member := addCapKindNode(t, f, "dispatch parallel: member one", query.KindDispatch)
	intReq := func() dispatch.PrepareRequest {
		return dispatch.PrepareRequest{
			Project: "skills", TaskType: "integrate", Goal: "merge one", Integrates: member,
		}
	}

	// Only the named member is open: the integration prepares without an override.
	resp := f.prepare(intReq())
	if !resp.OK {
		t.Fatalf("the member it integrates must not trip the unresolved gate: %s", resp.Error)
	}

	// A second, unrelated dispatch is open now, so the same call refuses and
	// names it — and the member it names is still not the reason.
	other := addCapKindNode(t, f, "dispatch parallel: member two", query.KindDispatch)
	resp = f.prepare(intReq())
	if resp.OK {
		t.Fatal("another open dispatch must still trip the gate")
	}
	if !strings.Contains(resp.Error, other) || !strings.Contains(resp.Error, "--allow-unresolved") {
		t.Errorf("refusal %q must name %s and the override", resp.Error, other)
	}

	// The override is still the way past it.
	override := intReq()
	override.AllowUnresolved = true
	if resp := f.prepare(override); !resp.OK {
		t.Fatalf("--allow-unresolved must proceed: %s", resp.Error)
	}
}

// The entry gate sees the member this dispatch integrates, so it can be a gate
// about this integration and not about the batch.
func TestPrepareGivesChecksTheIntegratesLink(t *testing.T) {
	f := newFixture(t, t.TempDir())
	f.writePlaybook(t, "integration-entry", `name: integration-entry
phase: entry
applies_when: type=integrate
steps:
  - check: printenv FS_INTEGRATES
  - check: printenv FS_CARDS
`)

	member := addCapKindNode(t, f, "dispatch parallel: member", query.KindDispatch)
	resp := f.prepare(dispatch.PrepareRequest{
		Project: "skills", TaskType: "integrate", Goal: "merge", Integrates: member,
	})
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
	f.writePlaybook(t, "code-entry", passingEntry)

	resp := f.prepare(devReq("sample"))
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

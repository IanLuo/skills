package dispatch_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

// The brief is the whole instruction a worker receives, so it is where the node
// it owns, every worker's standing obligations and the launch config have to
// appear. The launch config is frozen in code (contract R8): a worker started
// without it dies on a missing provider route, and nothing says so.
func TestPrepareBriefCarriesTheNodeTheObligationsAndTheLaunchConfig(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# agents\n")
	writeFile(t, filepath.Join(root, "docs", "prd.md"), "<!-- specs:locked: prd -->\n")

	f := newFixture(t, root)
	resp := f.prepare(devReq("sample"))
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)

	if brief.CapNodeID == "" {
		t.Fatal("cap_node_id is empty")
	}
	if brief.Goal != "sample" || brief.Project != "skills" || brief.RootPath != root {
		t.Errorf("brief = %+v, want the goal and the project it was dispatched into", brief)
	}
	if brief.Engine != dispatch.LaunchEngine || brief.Agent != dispatch.LaunchAgent {
		t.Errorf("launch config = %s/%s, want %s/%s", brief.Engine, brief.Agent, dispatch.LaunchEngine, dispatch.LaunchAgent)
	}
	if got, want := strings.Join(brief.AgentArgs, " "), "--provider magpie --model opencode-go/deepseek-v4.1-flash"; got != want {
		t.Errorf("agent args = %q, want %q", got, want)
	}
	if len(brief.Obligations) == 0 {
		t.Error("brief carries no obligations: a worker with no standing rules has nothing to write its node with")
	}
	if len(brief.LockedDocs) != 1 || brief.LockedDocs[0] != filepath.Join("docs", "prd.md") {
		t.Errorf("locked docs = %v, want the one locked doc the tree carries", brief.LockedDocs)
	}
	if !strings.Contains(brief.NextCommand, "--deliver") {
		t.Errorf("next command = %q, want the instruction that hands the brief to a worker", brief.NextCommand)
	}
}

func TestPrepareRequiresArgs(t *testing.T) {
	f := newFixture(t, t.TempDir())

	for _, tc := range []struct{ project, taskType, goal string }{
		{"", "dev-task", "x"},
		{"skills", "", "x"},
		{"skills", "dev-task", ""},
	} {
		resp := f.prepare(dispatch.PrepareRequest{
			Project: tc.project, TaskType: tc.taskType, Goal: tc.goal,
		})
		if resp.OK {
			t.Errorf("Prepare(%q, %q, %q) should fail", tc.project, tc.taskType, tc.goal)
		}
	}
}

// A dispatch records two events and nothing else: the node itself, and the
// decision naming the project the work went to. There is no plan event — there
// is no plan.
func TestPrepareCreatesCapNode(t *testing.T) {
	f := newFixture(t, t.TempDir())

	resp := f.prepare(devReq("sample"))
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	brief := resp.Data.(*dispatch.Brief)

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
	for i, want := range []store.EventType{store.TaskCreated, store.DecisionRecorded} {
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
}

// The unresolved-dispatch read is the same scope filter as fs pending's, and it
// failed the same way once: a dispatch standing in a project's own scope did not
// stop the next one. The read asks about every dispatch that is not done,
// wherever it lives.
func TestPrepareRefusesADispatchOpenOutsideTheCapScope(t *testing.T) {
	f := newFixture(t, t.TempDir())

	added := f.h.TaskAddKind("skills", "dispatch dev-task: open in the project", query.KindDispatch, "", "", "", "", "", nil)
	if !added.OK {
		t.Fatalf("task add: %s", added.Error)
	}
	open := added.Data.(command.EventData).NodeID

	resp := f.prepare(devReq("second"))
	if resp.OK {
		t.Fatal("a dispatch open in a project scope must refuse the next dispatch")
	}
	for _, want := range []string{"unresolved dispatches", open, "--allow-unresolved"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q must mention %q", resp.Error, want)
		}
	}
}

func TestPrepareRefusesWhileAnUnresolvedDispatchExists(t *testing.T) {
	f := newFixture(t, t.TempDir())

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
	if decisions := capDecisions(t, f, brief.CapNodeID); !contains(decisions, want) {
		t.Errorf("cap node decisions = %v, want %q", decisions, want)
	}
}

// The dispatch read looks at the node's kind, not its goal, so a dispatch whose
// goal was rewritten still blocks a new dispatch.
func TestPrepareRefusesForADispatchWhoseGoalWasEdited(t *testing.T) {
	f := newFixture(t, t.TempDir())

	first := f.prepare(devReq("first"))
	if !first.OK {
		t.Fatalf("first dispatch: %s", first.Error)
	}
	firstID := first.Data.(*dispatch.Brief).CapNodeID
	if resp := f.h.TaskEdit("cap", firstID, "no longer says dispatch", "", "", nil); !resp.OK {
		t.Fatalf("task edit: %s", resp.Error)
	}

	resp := f.prepare(devReq("second"))
	if resp.OK {
		t.Fatal("a dispatch whose goal was rewritten must still block")
	}
	if !strings.Contains(resp.Error, firstID) {
		t.Errorf("error %q must name the edited dispatch %s", resp.Error, firstID)
	}
}

// The cap scope holds long-lived non-dispatch work (the cap loop). The read is
// about dispatches, so that work must never block a new dispatch.
func TestPrepareIgnoresNonDispatchCapBacklog(t *testing.T) {
	f := newFixture(t, t.TempDir())

	added := f.h.TaskAdd("cap", "cap loop", nil)
	if !added.OK {
		t.Fatalf("task add: %s", added.Error)
	}
	capLoopID := added.Data.(command.EventData).NodeID
	if resp := f.h.TaskBlock("cap", capLoopID, "waiting on the user"); !resp.OK {
		t.Fatalf("task block: %s", resp.Error)
	}

	if resp := f.prepare(devReq("sample")); !resp.OK {
		t.Fatalf("a non-dispatch cap node must not block: %s", resp.Error)
	}
}

// --integrates names the member this integration merges. The link is written
// into the integration's task-created payload, and events are immutable, so a
// link that names nothing — or names a node that is not a dispatch — must be
// refused, naming it.
func TestPrepareRefusesAnIntegratesThatIsNotADispatchNode(t *testing.T) {
	f := newFixture(t, t.TempDir())

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

	// A refusal leaves nothing behind to block the next dispatch.
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

// --reviews is a different question from --integrates, and it is asked the same
// way: a review names a dispatch node, recorded on its own payload.
func TestPrepareRefusesAReviewsThatIsNotADispatchNode(t *testing.T) {
	f := newFixture(t, t.TempDir())

	gap := addCapKindNode(t, f, "a gap, never dispatched", query.KindGap)
	resp := f.prepare(dispatch.PrepareRequest{
		Project: "skills", TaskType: "review-task", Goal: "read the change", Reviews: gap,
	})
	if resp.OK {
		t.Fatal("--reviews naming a gap node must be refused")
	}
	for _, want := range []string{gap, "not a dispatch node", "--reviews"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("refusal %q must mention %q", resp.Error, want)
		}
	}

	work := addCapKindNode(t, f, "dispatch dev-task: the change", query.KindDispatch)
	resp = f.prepare(dispatch.PrepareRequest{
		Project: "skills", TaskType: "review-task", Goal: "read the change", Reviews: work,
	})
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	task, ok := scopeTask(t, f, "cap", resp.Data.(*dispatch.Brief).CapNodeID)
	if !ok || task.Reviews != work {
		t.Errorf("review node reviews = %q, want %q", task.Reviews, work)
	}
}

// The member's dispatch is closed only after the integration it waits on is
// done, so a member is expected to be unresolved while its integration is being
// prepared. It is the subject of this dispatch, not other open work piled on top
// of it: --allow-unresolved must not be needed to integrate a member. Every
// other open dispatch still refuses.
func TestPrepareGatesOnOtherOpenDispatchesButNotOnTheMemberItIntegrates(t *testing.T) {
	f := newFixture(t, t.TempDir())

	member := addCapKindNode(t, f, "dispatch parallel: member one", query.KindDispatch)
	intReq := func() dispatch.PrepareRequest {
		return dispatch.PrepareRequest{
			Project: "skills", TaskType: "integrate", Goal: "merge one", Integrates: member,
		}
	}

	// Only the named member is open: the integration prepares without an override.
	if resp := f.prepare(intReq()); !resp.OK {
		t.Fatalf("the member it integrates must not trip the unresolved read: %s", resp.Error)
	}

	// A second, unrelated dispatch is open now, so the same call refuses and
	// names it — and the member it names is still not the reason.
	other := addCapKindNode(t, f, "dispatch parallel: member two", query.KindDispatch)
	resp := f.prepare(intReq())
	if resp.OK {
		t.Fatal("another open dispatch must still trip the read")
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

// A blocking review finding is fixed while the change it was found in is still
// open, so the fix round has to be dispatchable without --allow-unresolved: the
// dispatches it repairs are its subjects, not open work piled on top of it.
//
// The exemption is the flag, and it is repeatable because the ordinary fix round
// answers two: the change it repairs and the review whose finding it fixes. The
// same dispatch without --fixes — even one whose goal names the open nodes — is
// refused.
func TestPrepareGatesOnOtherOpenDispatchesButNotOnTheOnesItFixes(t *testing.T) {
	f := newFixture(t, t.TempDir())

	work := addCapKindNode(t, f, "dispatch dev-task: the change", query.KindDispatch)
	review := addCapKindNode(t, f, "dispatch review: read the change", query.KindDispatch)

	fix := func(goal string, fixes ...string) dispatch.PrepareRequest {
		return dispatch.PrepareRequest{
			Project: "skills", TaskType: "dev-task", Goal: goal, Fixes: fixes,
		}
	}

	// Naming only one of the two open dispatches is not enough: the other is
	// still open work, and the refusal names it.
	resp := f.prepare(fix("fix the review finding in "+work, work))
	if resp.OK {
		t.Fatal("an open dispatch the fix round does not name must still trip the read")
	}
	if !strings.Contains(resp.Error, review) || !strings.Contains(resp.Error, "--allow-unresolved") {
		t.Errorf("refusal %q must name the open review %s and the override", resp.Error, review)
	}

	// Naming both is what the protocol does, and it needs no override.
	resp = f.prepare(fix("fix what the review found", work, review))
	if !resp.OK {
		t.Fatalf("--fixes must exempt the dispatches it names: %s", resp.Error)
	}

	// The relation is recorded on the fix round's own node, so a reader sees
	// which dispatches the fix answers.
	round := resp.Data.(*dispatch.Brief).CapNodeID
	if decisions := capDecisions(t, f, round); !contains(decisions,
		"fixes "+work+", "+review+": this dispatch repairs what they found, and is dispatched while they are open for that reason") {
		t.Errorf("the fix round records %v, want the links to %s and %s", decisions, work, review)
	}

	// And the flag is the declaration: a goal that merely names the open nodes
	// grants nothing.
	if resp := f.prepare(fix("fix the review finding in " + work)); resp.OK {
		t.Fatal("a node id in the goal text must not exempt anything")
	}
}

// A fix round names a dispatch, not any node at all: the same two refusals
// --integrates and --reviews make, made the same way.
func TestPrepareRefusesAFixOfANodeThatIsNotADispatch(t *testing.T) {
	f := newFixture(t, t.TempDir())
	gap := addCapKindNode(t, f, "a gap, never dispatched", query.KindGap)

	resp := f.prepare(dispatch.PrepareRequest{
		Project: "skills", TaskType: "dev-task", Goal: "fix it", Fixes: []string{gap},
	})
	if resp.OK {
		t.Fatal("--fixes naming a gap node must be refused")
	}
	for _, want := range []string{gap, "not a dispatch node", "--fixes"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("refusal %q must mention %q", resp.Error, want)
		}
	}

	resp = f.prepare(dispatch.PrepareRequest{
		Project: "skills", TaskType: "dev-task", Goal: "fix it", Fixes: []string{"t-00000000"},
	})
	if resp.OK {
		t.Fatal("--fixes naming no node must be refused")
	}
	if !strings.Contains(resp.Error, "t-00000000") {
		t.Errorf("refusal %q must name the node", resp.Error)
	}
}

// A dispatch that integrates nothing records no link.
func TestPrepareWithoutIntegratesRecordsNoLink(t *testing.T) {
	f := newFixture(t, t.TempDir())

	resp := f.prepare(devReq("sample"))
	if !resp.OK {
		t.Fatalf("Prepare: %s", resp.Error)
	}
	task, ok := scopeTask(t, f, "cap", resp.Data.(*dispatch.Brief).CapNodeID)
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

// fs reviews is a read over a link, never a gate over a plan: a done review
// dispatch that names this node is the whole answer.
func TestReviewsAcceptsADoneReviewAndNamesAnOpenOne(t *testing.T) {
	f := newFixture(t, t.TempDir())
	work := addCapKindNode(t, f, "dispatch dev-task: the change", query.KindDispatch)

	resp := dispatch.Reviews(f.h, work)
	if resp.OK {
		t.Fatal("a dispatch no review names must be refused")
	}
	for _, want := range []string{work, "--reviews"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("refusal %q must mention %q", resp.Error, want)
		}
	}

	added := f.h.TaskAddKind("cap", "dispatch review: read the change", query.KindDispatch, "", "", work, "", "", nil)
	if !added.OK {
		t.Fatalf("task add: %s", added.Error)
	}
	review := added.Data.(command.EventData).NodeID

	// An open review is named as open: "not reviewed" and "reviewed, still open"
	// are different facts.
	resp = dispatch.Reviews(f.h, work)
	if resp.OK {
		t.Fatal("an open review must not satisfy the read")
	}
	if !strings.Contains(resp.Error, review) || !strings.Contains(resp.Error, "not done") {
		t.Errorf("refusal %q must name the open review %s as not done", resp.Error, review)
	}

	if r := f.h.TaskUpdate("cap", review, "done", "reviewed", nil); !r.OK {
		t.Fatal(r.Error)
	}
	resp = dispatch.Reviews(f.h, work)
	if !resp.OK {
		t.Fatalf("Reviews: %s", resp.Error)
	}
	result := resp.Data.(dispatch.ReviewsResult)
	if result.Node != work || result.ReviewedBy != review {
		t.Errorf("result = %+v, want %s reviewed by %s", result, work, review)
	}
}

// A review reads a dispatch, not any node at all.
func TestReviewsRefusesANodeThatIsNotADispatch(t *testing.T) {
	f := newFixture(t, t.TempDir())
	gap := addCapKindNode(t, f, "a gap, never dispatched", query.KindGap)

	resp := dispatch.Reviews(f.h, gap)
	if resp.OK {
		t.Fatal("a gap node must not be reviewed as a dispatch")
	}
	if !strings.Contains(resp.Error, "not a dispatch node") {
		t.Errorf("refusal %q must say what the kind is", resp.Error)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

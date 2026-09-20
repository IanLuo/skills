package dispatch_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

// recordDeliveryAs records the binding the way Deliver does, naming the agent
// the way Deliver names it: after the worker's node.
func recordDeliveryAs(t *testing.T, f *fixture, capNode, agent, workerID string) {
	t.Helper()
	resp := f.h.RecordDelivery("cap", capNode, command.DeliveryRecord{
		PaneID: "w1:p9", TabID: "w1:t9", Agent: agent, Engine: "herdr",
		Project: "skills", Node: workerID,
	})
	if !resp.OK {
		t.Fatalf("RecordDelivery: %s", resp.Error)
	}
}

func pendingOf(t *testing.T, resp command.Response) dispatch.PendingResult {
	t.Helper()
	if !resp.OK {
		t.Fatalf("Pending: %s", resp.Error)
	}
	result, ok := resp.Data.(dispatch.PendingResult)
	if !ok {
		t.Fatalf("Pending data = %#v, want dispatch.PendingResult", resp.Data)
	}
	return result
}

func pendingEntryFor(t *testing.T, result dispatch.PendingResult, capNode string) dispatch.PendingEntry {
	t.Helper()
	for _, entry := range result.Pending {
		if entry.CapNode == capNode {
			return entry
		}
	}
	t.Fatalf("pending %+v has no entry for %s", result.Pending, capNode)
	return dispatch.PendingEntry{}
}

// A blocked dispatch is still a dispatch waiting on the cap, so fs pending
// re-runs the check its block recorded: while the condition holds it reads plain
// blocked, and once it is over the entry says so instead of repeating an expired
// reason as current truth. Nothing is unblocked.
func TestPendingReRunsABlockedDispatchCheck(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	marker := filepath.Join(t.TempDir(), "marker")
	check := "test -f " + marker
	if resp := f.h.TaskBlockWithCheck("cap", capNode, "waiting on the marker", check); !resp.OK {
		t.Fatalf("block: %s", resp.Error)
	}

	before := scopeEventCount(t, f, "cap")
	entry := pendingEntryFor(t, pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{})), capNode)
	if entry.Status != "blocked" {
		t.Errorf("status while the condition holds = %q, want blocked", entry.Status)
	}
	if got := scopeEventCount(t, f, "cap"); got != before {
		t.Errorf("cap events = %d, want %d: fs pending must write nothing", got, before)
	}

	if err := os.WriteFile(marker, []byte("here"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := "blocked (condition no longer holds — " + check + " exited 0)"
	entry = pendingEntryFor(t, pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{})), capNode)
	if entry.Status != want {
		t.Errorf("status once the condition is over = %q, want %q", entry.Status, want)
	}
}

// A block with no check is prose: fs pending makes no claim about it, however
// stale the reason is.
func TestPendingLeavesAChecklessBlockAsProse(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	if resp := f.h.TaskBlock("cap", capNode, "waiting on the user"); !resp.OK {
		t.Fatalf("block: %s", resp.Error)
	}

	entry := pendingEntryFor(t, pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{})), capNode)
	if entry.Status != "blocked" {
		t.Errorf("status = %q, want plain blocked", entry.Status)
	}
}

// scopeEventCount counts the events in a scope, so a test can prove a command
// wrote nothing.
func scopeEventCount(t *testing.T, f *fixture, scope string) int {
	t.Helper()
	s, err := store.Open(f.storeDB)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	events, err := s.Replay(scope, nil)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return len(events)
}

// The classification, one dispatch at a time. The worker's node is the truth
// about whether the work is done; herdr only says when to look.
func TestPendingClassifiesEachDispatch(t *testing.T) {
	cases := []struct {
		name   string
		worker string // the worker node's status
		herdr  string // what herdr reports for the agent; "" = herdr has no such agent
		want   string
	}{
		{"a done worker is ready however herdr reads", "done", "working", dispatch.StateReady},
		{"a working agent is running", "active", "working", dispatch.StateRunning},
		{"an idle agent is still running", "active", "idle", dispatch.StateRunning},
		{"herdr finished it unseen", "active", "done", dispatch.StateUnseen},
		{"herdr has no such agent", "active", "", dispatch.StateGone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, t.TempDir())
			capNode := addCapNode(t, f, "dispatch dev-task: sample")
			workerID := addWorkerNode(t, f, tc.worker)
			agent := "dispatch-dev-task-" + workerID
			recordDeliveryAs(t, f, capNode, agent, workerID)

			agents := map[string]string{}
			if tc.herdr != "" {
				agents[agent] = tc.herdr
			}
			result := pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{agents: agents}))

			entry := pendingEntryFor(t, result, capNode)
			if entry.State != tc.want {
				t.Errorf("state = %q, want %q", entry.State, tc.want)
			}
			if entry.WorkerNode != workerID || entry.Project != "skills" ||
				entry.Tab != "w1:t9" || entry.Agent != agent {
				t.Errorf("entry = %+v, want the recorded binding", entry)
			}
			if len(result.Pending) != 1 || result.Counts[tc.want] != 1 {
				t.Errorf("result = %+v, want one %s dispatch", result, tc.want)
			}
		})
	}
}

// A record written before the link existed carries no worker node. There is
// nothing to read, so it is unlinked rather than the worker looking gone.
func TestPendingReportsALegacyRecordUnlinked(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	recordLegacyDelivery(t, f, capNode, "w1:p9")

	result := pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{}))

	entry := pendingEntryFor(t, result, capNode)
	if entry.State != dispatch.StateUnlinked {
		t.Errorf("state = %q, want %q", entry.State, dispatch.StateUnlinked)
	}
	if entry.WorkerNode != "" {
		t.Errorf("worker_node = %q, want none on a record that carries no node", entry.WorkerNode)
	}
	if result.Counts[dispatch.StateUnlinked] != 1 {
		t.Errorf("counts = %v, want one unlinked", result.Counts)
	}
}

// A prepared dispatch that was never delivered has no record at all, and is
// unlinked for the same reason: closing it needs --abandoned or --worker.
func TestPendingReportsAMissingRecordUnlinked(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")

	result := pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{}))

	entry := pendingEntryFor(t, result, capNode)
	if entry.State != dispatch.StateUnlinked {
		t.Errorf("state = %q, want %q", entry.State, dispatch.StateUnlinked)
	}
	if entry.Agent != "" || entry.Tab != "" || entry.Project != "" {
		t.Errorf("entry = %+v, want no binding for a never-delivered dispatch", entry)
	}
}

// The cap's own backlog is not dispatch work, so it is not something waiting on
// the cap to close a dispatch.
func TestPendingIgnoresNonDispatchCapNodes(t *testing.T) {
	f := newFixture(t, t.TempDir())
	addCapKindNode(t, f, "cap loop", query.KindWork)
	addCapNode(t, f, "dispatch dev-task: abandoned")

	result := pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{}))

	if len(result.Pending) != 1 {
		t.Fatalf("pending = %+v, want only the dispatch node", result.Pending)
	}
	if result.Pending[0].State != dispatch.StateUnlinked {
		t.Errorf("state = %q, want the one dispatch unlinked", result.Pending[0].State)
	}
}

// The regression for the retired convention: what makes a node a dispatch is its
// kind, so rewriting its goal cannot turn it into something the cap stops seeing.
// The prefix got this exactly wrong.
func TestPendingSeesADispatchWhoseGoalWasEdited(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "active")
	agent := "dispatch-dev-task-" + workerID
	recordDeliveryAs(t, f, capNode, agent, workerID)

	// Rewrite the goal so the prose no longer says "dispatch ..." at all.
	if resp := f.h.TaskEdit("cap", capNode, "reworded entirely", "", nil); !resp.OK {
		t.Fatalf("task edit: %s", resp.Error)
	}

	result := pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{
		agents: map[string]string{agent: "working"},
	}))

	if entry := pendingEntryFor(t, result, capNode); entry.State != dispatch.StateRunning {
		t.Errorf("state = %q, want %q", entry.State, dispatch.StateRunning)
	}
}

// fs pending reports the backlog's size, so the cap does not have to remember
// it: open gaps are counted, and a resolved one is not.
func TestPendingReportsTheUndispatchedGapCount(t *testing.T) {
	f := newFixture(t, t.TempDir())
	addCapKindNode(t, f, "an open gap", query.KindGap)
	resolved := addCapKindNode(t, f, "a resolved gap", query.KindGap)
	if resp := f.h.TaskUpdate("cap", resolved, "done", "fixed", nil); !resp.OK {
		t.Fatalf("task update: %s", resp.Error)
	}
	addCapKindNode(t, f, "ordinary work", query.KindWork)

	result := pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{}))

	if result.UndispatchedGaps != 1 {
		t.Errorf("undispatched_gaps = %d, want 1", result.UndispatchedGaps)
	}
}

// Old records collided on one agent name: fs pending must report both, not fall
// over on the duplicate.
func TestPendingToleratesTwoRecordsSharingAnAgentName(t *testing.T) {
	f := newFixture(t, t.TempDir())
	finished := addCapNode(t, f, "dispatch dev-task: finished")
	unfinished := addCapNode(t, f, "dispatch dev-task: unfinished")
	doneWorker := addWorkerNode(t, f, "done")
	activeWorker := addWorkerNode(t, f, "active")
	// Both records name the same agent, the way --deliver used to.
	recordDeliveryAs(t, f, finished, "dispatch-dev-task", doneWorker)
	recordDeliveryAs(t, f, unfinished, "dispatch-dev-task", activeWorker)

	result := pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{
		agents: map[string]string{"dispatch-dev-task": "working"},
	}))

	if len(result.Pending) != 2 {
		t.Fatalf("pending = %+v, want both dispatches", result.Pending)
	}
	if got := pendingEntryFor(t, result, finished).State; got != dispatch.StateReady {
		t.Errorf("finished dispatch = %q, want %q", got, dispatch.StateReady)
	}
	if got := pendingEntryFor(t, result, unfinished).State; got != dispatch.StateRunning {
		t.Errorf("unfinished dispatch = %q, want %q", got, dispatch.StateRunning)
	}
}

// herdr being unreadable is not the same as the agent being gone, so it must not
// read as gone: the states are reported as still running and the reason is a
// warning, the way fs close reports an unreachable herdr.
func TestPendingWarnsWhenHerdrIsUnavailable(t *testing.T) {
	f := newFixture(t, t.TempDir())
	open := addCapNode(t, f, "dispatch dev-task: open")
	workerID := addWorkerNode(t, f, "active")
	recordDeliveryAs(t, f, open, "dispatch-dev-task-"+workerID, workerID)

	herdrErr := errors.New("herdr: server is down")
	result := pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{agentsErr: herdrErr}))

	if entry := pendingEntryFor(t, result, open); entry.State != dispatch.StateRunning {
		t.Errorf("state = %q, want %q for a worker that is not done", entry.State, dispatch.StateRunning)
	}
	for _, want := range []string{"server is down", dispatch.StateRunning} {
		if !strings.Contains(result.Warning, want) {
			t.Errorf("warning %q must mention %q", result.Warning, want)
		}
	}
}

// A done worker is ready even when herdr cannot be asked: the node is the truth,
// and nothing about the classification depends on the dispatcher being up.
func TestPendingReadsADoneWorkerEvenWithHerdrDown(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordDeliveryAs(t, f, capNode, "dispatch-dev-task-"+workerID, workerID)

	result := pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{agentsErr: errors.New("herdr: no such file")}))

	if entry := pendingEntryFor(t, result, capNode); entry.State != dispatch.StateReady {
		t.Errorf("state = %q, want %q", entry.State, dispatch.StateReady)
	}
}

// fs pending answers a question; it is not an action. It must append no event in
// any scope it reads.
func TestPendingIsARead(t *testing.T) {
	f := newFixture(t, t.TempDir())
	done := addCapNode(t, f, "dispatch dev-task: done")
	workerID := addWorkerNode(t, f, "done")
	recordDeliveryAs(t, f, done, "dispatch-dev-task-"+workerID, workerID)
	legacy := addCapNode(t, f, "dispatch dev-task: legacy")
	recordLegacyDelivery(t, f, legacy, "w1:p8")

	capBefore := scopeEventCount(t, f, "cap")
	skillsBefore := scopeEventCount(t, f, "skills")

	pendingOf(t, dispatch.Pending(f.h, &fakeHerdr{agents: map[string]string{}}))

	if got := scopeEventCount(t, f, "cap"); got != capBefore {
		t.Errorf("cap events = %d, want %d: fs pending must write nothing", got, capBefore)
	}
	if got := scopeEventCount(t, f, "skills"); got != skillsBefore {
		t.Errorf("skills events = %d, want %d: fs pending must write nothing", got, skillsBefore)
	}
}

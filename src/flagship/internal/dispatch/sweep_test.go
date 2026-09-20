package dispatch_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/query"
)

// sweepOf runs fs sweep and returns its data payload.
func sweepOf(t *testing.T, resp command.Response) dispatch.SweepResult {
	t.Helper()
	if !resp.OK {
		t.Fatalf("Sweep: %s", resp.Error)
	}
	result, ok := resp.Data.(dispatch.SweepResult)
	if !ok {
		t.Fatalf("Sweep data = %#v, want dispatch.SweepResult", resp.Data)
	}
	return result
}

// sweepDecisionFor is the exact decision a sweep records for one pane.
func sweepDecisionFor(paneID, workerRef string) string {
	return "pane " + paneID + " closed by sweep (worker node " + workerRef + " is done)"
}

// A done worker's pane is the resource this command exists for: close it, and
// record why on the cap's node — without touching the node's status.
func TestSweepClosesThePaneOfADoneWorker(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordTypedDelivery(t, f, capNode, "w1:p9", workerID, "dev-task")
	hc := &fakeHerdr{}

	result := sweepOf(t, dispatch.Sweep(f.h, hc))

	if len(result.Closed) != 1 {
		t.Fatalf("closed = %+v, want the done worker's pane", result.Closed)
	}
	closed := result.Closed[0]
	if closed.CapNode != capNode || closed.PaneID != "w1:p9" || closed.WorkerNode != "skills:"+workerID {
		t.Errorf("closed = %+v, want the recorded pane and worker node", closed)
	}
	if !slices.Equal(hc.closed, []string{"w1:p9"}) {
		t.Errorf("herdr closed = %v, want [w1:p9]", hc.closed)
	}
	if result.Warning != "" {
		t.Errorf("warning = %q, want none", result.Warning)
	}

	// The record is the cap's: a sweep closes the resource, never the node.
	if status := nodeStatus(t, f, "cap", capNode); status == "done" {
		t.Errorf("cap node is %q; a sweep must never close the record", status)
	}
	want := sweepDecisionFor("w1:p9", "skills:"+workerID)
	if decisions := capDecisions(t, f, capNode); !slices.Contains(decisions, want) {
		t.Errorf("decisions = %v, want %q", decisions, want)
	}
}

// Live work's pane must stay: that is the rule, not an oversight.
func TestSweepLeavesALiveWorkersPaneAlone(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "active")
	recordTypedDelivery(t, f, capNode, "w1:p9", workerID, "dev-task")
	hc := &fakeHerdr{agents: map[string]string{"dispatch-dev-task": "working"}}

	before := scopeEventCount(t, f, "cap")
	result := sweepOf(t, dispatch.Sweep(f.h, hc))

	if len(result.Closed) != 0 || len(hc.closed) != 0 {
		t.Fatalf("closed = %+v / %v, want a live worker's pane left alone", result.Closed, hc.closed)
	}
	if len(result.Kept) != 1 || result.Kept[0].PaneID != "w1:p9" || result.Kept[0].State != dispatch.StateRunning {
		t.Errorf("kept = %+v, want the live worker's pane as %q", result.Kept, dispatch.StateRunning)
	}
	if got := scopeEventCount(t, f, "cap"); got != before {
		t.Errorf("cap events = %d, want %d: a sweep that closes nothing writes nothing", got, before)
	}
	if decisions := capDecisions(t, f, capNode); len(decisions) != 0 {
		t.Errorf("decisions = %v, want none for a live worker", decisions)
	}
}

// Running it twice is the same as running it once: the decision on the node is
// the record that this pane was already handled.
func TestSweepIsIdempotent(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordTypedDelivery(t, f, capNode, "w1:p9", workerID, "dev-task")
	hc := &fakeHerdr{}

	first := sweepOf(t, dispatch.Sweep(f.h, hc))
	if len(first.Closed) != 1 {
		t.Fatalf("first sweep closed = %+v, want the pane", first.Closed)
	}
	events := scopeEventCount(t, f, "cap")

	second := sweepOf(t, dispatch.Sweep(f.h, hc))
	if len(second.Closed) != 0 || len(second.Kept) != 0 {
		t.Errorf("second sweep = %+v, want nothing left to do", second)
	}
	if second.Warning != "" {
		t.Errorf("second sweep warning = %q, want none", second.Warning)
	}
	if got := scopeEventCount(t, f, "cap"); got != events {
		t.Errorf("cap events = %d after a second sweep, want %d: it must change nothing", got, events)
	}
	if got := len(capDecisions(t, f, capNode)); got != 1 {
		t.Errorf("decisions = %d, want the one the first sweep recorded", got)
	}
}

// A pane already gone is the goal state, not a warning — the same rule fs close
// follows — and it is still recorded as handled.
func TestSweepTreatsAnAlreadyGonePaneAsSuccess(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordTypedDelivery(t, f, capNode, "w1:p9", workerID, "dev-task")
	hc := &fakeHerdr{closeErr: dispatch.ErrPaneGone}

	result := sweepOf(t, dispatch.Sweep(f.h, hc))

	if len(result.Closed) != 1 {
		t.Errorf("closed = %+v, want the gone pane reported as closed", result.Closed)
	}
	if result.Warning != "" {
		t.Errorf("warning = %q, want none: a pane already gone is success", result.Warning)
	}
	if decisions := capDecisions(t, f, capNode); len(decisions) != 1 {
		t.Errorf("decisions = %v, want the one close recorded", decisions)
	}
}

// A pane that will not close is a warning and no decision: the pane is still
// there, so it is not handled, and the next sweep must try it again.
func TestSweepWarnsAndLeavesNoDecisionWhenThePaneWillNotClose(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordTypedDelivery(t, f, capNode, "w1:p9", workerID, "dev-task")
	hc := &fakeHerdr{closeErr: errors.New("herdr: socket closed")}

	result := sweepOf(t, dispatch.Sweep(f.h, hc))

	if result.Warning == "" || !strings.Contains(result.Warning, "w1:p9") {
		t.Errorf("warning = %q, want one naming the pane it could not close", result.Warning)
	}
	if len(result.Closed) != 0 {
		t.Errorf("closed = %+v, want none: the pane is still there", result.Closed)
	}
	if decisions := capDecisions(t, f, capNode); len(decisions) != 0 {
		t.Errorf("decisions = %v, want none for a pane that would not close", decisions)
	}

	// Once herdr works, the next sweep closes it and records the decision once.
	hc.closeErr = nil
	next := sweepOf(t, dispatch.Sweep(f.h, hc))
	if len(next.Closed) != 1 || len(next.Kept) != 0 {
		t.Errorf("next sweep = %+v, want the pane closed", next)
	}
	if decisions := capDecisions(t, f, capNode); len(decisions) != 1 {
		t.Errorf("decisions = %v, want the one close recorded", decisions)
	}
}

// A done worker is the truth even when herdr cannot be asked: the node decides,
// and the dispatcher being down only costs the kept states their detail.
func TestSweepClosesADoneWorkerEvenWhenHerdrAgentsAreUnavailable(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordTypedDelivery(t, f, capNode, "w1:p9", workerID, "dev-task")
	hc := &fakeHerdr{agentsErr: errors.New("herdr: server is down")}

	result := sweepOf(t, dispatch.Sweep(f.h, hc))

	if len(result.Closed) != 1 {
		t.Errorf("closed = %+v, want the done worker's pane closed", result.Closed)
	}
	if !strings.Contains(result.Warning, "server is down") {
		t.Errorf("warning = %q, want it to say herdr could not be asked", result.Warning)
	}
}

// A pane whose worker node nobody can name is left alone: a sweep closes a pane
// because the node justifies it, never because herdr has no agent for it.
func TestSweepKeepsThePaneOfARecordWithNoWorkerNode(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	recordLegacyDelivery(t, f, capNode, "w1:p9")
	hc := &fakeHerdr{}

	result := sweepOf(t, dispatch.Sweep(f.h, hc))

	if len(result.Closed) != 0 || len(hc.closed) != 0 {
		t.Fatalf("closed = %+v / %v, want a pane no worker node justifies left alone", result.Closed, hc.closed)
	}
	if len(result.Kept) != 1 || result.Kept[0].State != dispatch.StateUnlinked {
		t.Errorf("kept = %+v, want the unlinked dispatch kept", result.Kept)
	}
}

// A dispatch that was prepared and never delivered left no pane, so there is no
// resource for a sweep to report: it is about panes.
func TestSweepReportsNothingForANeverDeliveredDispatch(t *testing.T) {
	f := newFixture(t, t.TempDir())
	addCapNode(t, f, "dispatch dev-task: prepared, never delivered")
	hc := &fakeHerdr{}

	result := sweepOf(t, dispatch.Sweep(f.h, hc))

	if len(result.Closed) != 0 || len(result.Kept) != 0 || len(hc.closed) != 0 {
		t.Errorf("sweep = %+v / %v, want nothing for a dispatch with no pane", result, hc.closed)
	}
}

// Only dispatch nodes are the cap's dispatches; ordinary cap work is not
// something a sweep acts on.
func TestSweepIgnoresCapNodesThatAreNotDispatches(t *testing.T) {
	f := newFixture(t, t.TempDir())
	work := addCapKindNode(t, f, "ordinary cap work", query.KindWork)
	workerID := addWorkerNode(t, f, "done")
	recordTypedDelivery(t, f, work, "w1:p9", workerID, "dev-task")
	hc := &fakeHerdr{}

	result := sweepOf(t, dispatch.Sweep(f.h, hc))

	if len(result.Closed) != 0 || len(hc.closed) != 0 {
		t.Errorf("closed = %+v / %v, want ordinary cap work untouched", result.Closed, hc.closed)
	}
}

// It writes exactly one decision per pane it closed, and no other event.
func TestSweepWritesExactlyOneDecisionPerClosedPane(t *testing.T) {
	f := newFixture(t, t.TempDir())
	first := addCapNode(t, f, "dispatch dev-task: one")
	second := addCapNode(t, f, "dispatch dev-task: two")
	live := addCapNode(t, f, "dispatch dev-task: live")
	one := addWorkerNode(t, f, "done")
	two := addWorkerNode(t, f, "done")
	liveWorker := addWorkerNode(t, f, "active")
	recordTypedDelivery(t, f, first, "w1:p9", one, "dev-task")
	recordTypedDelivery(t, f, second, "w1:p10", two, "dev-task")
	recordTypedDelivery(t, f, live, "w1:p11", liveWorker, "dev-task")

	hc := &fakeHerdr{agents: map[string]string{"dispatch-dev-task": "working"}}
	before := scopeEventCount(t, f, "cap")
	sweepOf(t, dispatch.Sweep(f.h, hc))

	if got := scopeEventCount(t, f, "cap") - before; got != 2 {
		t.Errorf("cap events written = %d, want exactly one per closed pane", got)
	}
	for _, node := range []string{first, second} {
		if got := len(capDecisions(t, f, node)); got != 1 {
			t.Errorf("node %s decisions = %d, want 1", node, got)
		}
	}
	if got := len(capDecisions(t, f, live)); got != 0 {
		t.Errorf("live dispatch decisions = %d, want 0", got)
	}
}

// It starts nothing, so it is not a second dispatcher.
func TestSweepStartsNothing(t *testing.T) {
	f := newFixture(t, t.TempDir())
	capNode := addCapNode(t, f, "dispatch dev-task: sample")
	workerID := addWorkerNode(t, f, "done")
	recordTypedDelivery(t, f, capNode, "w1:p9", workerID, "dev-task")
	hc := &fakeHerdr{}

	sweepOf(t, dispatch.Sweep(f.h, hc))

	if len(hc.created) != 0 || len(hc.started) != 0 || len(hc.prompts) != 0 {
		t.Errorf("sweep split/started/prompted: %v/%v/%v — it must start no work",
			hc.created, hc.started, hc.prompts)
	}
}

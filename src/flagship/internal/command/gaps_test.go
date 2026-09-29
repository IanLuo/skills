package command_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/query"
)

// The backlog is flat: every gap has the same weight and shape, so nothing can
// be triaged without reading all of it. These are the three mechanical fixes —
// weight as a field, a merge that records both sides, and a header that says
// what the list is showing. (The fourth, the cap's triage rule, is prose in
// defaults/kb/cap.yaml and is verified by tests/playbooks.)

// gapOf adds a gap and returns its node id.
func gapOf(t *testing.T, h *command.Handler, project, goal string, severity query.Severity) string {
	t.Helper()
	resp := h.TaskAddKind(project, goal, query.KindGap, "", "", "", "", severity, nil)
	if !resp.OK {
		t.Fatalf("TaskAddKind: %s", resp.Error)
	}
	return resp.Data.(command.EventData).NodeID
}

func gapsOf(t *testing.T, h *command.Handler) []command.GapNode {
	t.Helper()
	resp := h.Gaps(nil)
	if !resp.OK {
		t.Fatalf("Gaps: %s", resp.Error)
	}
	return resp.Data.(map[string]any)["gaps"].([]command.GapNode)
}

// nodeInfo returns one node's derived state as fs status renders it.
func nodeInfo(t *testing.T, h *command.Handler, project, nodeID string) command.TaskInfo {
	t.Helper()
	for _, task := range h.Status(project).Data.(command.StatusResult).Tasks {
		if task.NodeID == nodeID {
			return task
		}
	}
	t.Fatalf("no node %s in project %s", nodeID, project)
	return command.TaskInfo{}
}

// A gap's weight is a field, so fs gaps sorts the heaviest first instead of
// making a reader read 78 KB of prose to find them. Unweighed gaps go last:
// unset makes no claim about weight, so putting it among the levels would be a
// claim the field never made.
func TestGapSeveritySortsHeaviestFirst(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	// The weights are assigned by node id, ascending from the lightest, so the
	// order a sort that ignored the weight would fall back to — id ascending —
	// is the exact opposite of the one asserted here. Node ids are random, so
	// this is what makes the assertion bind whichever ids came out: whatever id
	// sorts first is the one holding the lightest weight.
	var ids []string
	for i := 0; i < 4; i++ {
		ids = append(ids, gapOf(t, h, "proj", "a gap", ""))
	}
	sort.Strings(ids)
	byID := []query.Severity{query.SeverityProse, query.SeverityDefect, query.SeveritySevere, query.SeverityUnset}
	for i, id := range ids {
		if byID[i] == query.SeverityUnset {
			continue
		}
		if resp := h.TaskEdit("proj", id, "", "", byID[i], nil); !resp.OK {
			t.Fatalf("TaskEdit: %s", resp.Error)
		}
	}

	var got []string
	for _, gap := range gapsOf(t, h) {
		got = append(got, gap.Severity)
	}
	want := []string{"severe", "defect", "prose", "unset"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("fs gaps weights in order = %v, want the heaviest first and unweighed last: %v", got, want)
	}
}

// A gap nobody has weighed reads as unset, never as the lowest weight — a
// severity that defaults to prose is a triage that decides by omission.
func TestGapWithNoSeverityReadsUnset(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	unset := gapOf(t, h, "proj", "nobody has weighed this", "")
	light := gapOf(t, h, "proj", "a stale comment", query.SeverityProse)

	for _, gap := range gapsOf(t, h) {
		if gap.NodeID == unset && gap.Severity != "unset" {
			t.Errorf("unweighed gap severity = %q, want %q", gap.Severity, "unset")
		}
		if gap.NodeID == light && gap.Severity != "prose" {
			t.Errorf("prose gap severity = %q, want %q", gap.Severity, "prose")
		}
	}
	if nodeInfo(t, h, "proj", unset).Severity != "" {
		t.Errorf("an unweighed gap must store no severity, got %q", nodeInfo(t, h, "proj", unset).Severity)
	}
}

func TestTaskAddRefusesSeverityOnANonGap(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	resp := h.TaskAddKind("proj", "ordinary work", query.KindWork, "", "", "", "", query.SeveritySevere, nil)
	if resp.OK {
		t.Fatal("a work node accepted a severity; the vocabulary is about triaging a backlog, and work is not in one")
	}
}

func TestTaskAddRefusesAnUnknownSeverity(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	resp := h.TaskAddKind("proj", "a gap", query.KindGap, "", "", "", "", query.Severity("critical"), nil)
	if resp.OK {
		t.Fatal("an unknown severity was stored; a typo must fail where it can still be corrected")
	}
}

func TestTaskEditSetsSeverity(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	gap := gapOf(t, h, "proj", "a gap nobody weighed", "")
	if resp := h.TaskEdit("proj", gap, "", "", query.Severity("critical"), nil); resp.OK {
		t.Fatal("TaskEdit stored an unknown severity")
	}
	if resp := h.TaskEdit("proj", gap, "", "", query.SeveritySevere, nil); !resp.OK {
		t.Fatalf("TaskEdit: %s", resp.Error)
	}
	if got := nodeInfo(t, h, "proj", gap).Severity; got != "severe" {
		t.Errorf("severity = %q, want severe", got)
	}
	// A goal-only edit leaves the weight alone: an empty severity means
	// "leave it", the same as an empty goal and an empty kind.
	if resp := h.TaskEdit("proj", gap, "a reworded gap", "", "", nil); !resp.OK {
		t.Fatalf("TaskEdit: %s", resp.Error)
	}
	if got := nodeInfo(t, h, "proj", gap).Severity; got != "severe" {
		t.Errorf("severity after a goal edit = %q, want it left alone", got)
	}
}

func TestTaskEditRefusesSeverityOnANonGap(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	work := h.TaskAdd("proj", "ordinary work", nil).Data.(command.EventData).NodeID
	if resp := h.TaskEdit("proj", work, "", "", query.SeveritySevere, nil); resp.OK {
		t.Fatal("a work node accepted a severity")
	}
}

// A weight is a gap's and only a gap's, on this path too. A kind edit away from
// gap therefore takes the weight with it: the node's kind and its weight are
// one decision, and a node that is not a gap has no weight to carry. Refusing
// the edit instead would strand the node, because an empty --severity means
// "leave it alone" — a gap that could not be re-kinded is a state no command
// undoes. The clear is an event like any other edit, so the weight it dropped
// is on the record, and setting the node back is two commands.
func TestTaskEditKindAwayFromGapClearsTheSeverity(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	gap := gapOf(t, h, "proj", "a gap with a weight", query.SeveritySevere)
	if resp := h.TaskEdit("proj", gap, "", query.KindWork, "", nil); !resp.OK {
		t.Fatalf("TaskEdit: %s", resp.Error)
	}
	got := nodeInfo(t, h, "proj", gap)
	if got.Kind != query.KindWork {
		t.Errorf("kind = %q, want work", got.Kind)
	}
	if got.Severity != "" {
		t.Errorf("a work node carries the weight %q; a severity is a gap's, and a node that is not a gap has none", got.Severity)
	}

	// Restorable in two commands: back to gap, then weighed again.
	if resp := h.TaskEdit("proj", gap, "", query.KindGap, "", nil); !resp.OK {
		t.Fatalf("TaskEdit: %s", resp.Error)
	}
	if resp := h.TaskEdit("proj", gap, "", "", query.SeverityProse, nil); !resp.OK {
		t.Fatalf("TaskEdit: %s", resp.Error)
	}
	if got := nodeInfo(t, h, "proj", gap).Severity; got != "prose" {
		t.Errorf("severity = %q, want the node to be weighable again once it is a gap", got)
	}
}

// fs gaps prints the done ones and the open ones in one list, so the header has
// to say what the reader is looking at — the cap has already had to check each
// by hand to be sure none of them was open.
func TestGapsHeaderCountsWhatItIsShowing(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.ProjectCreate("other", "/tmp/other")

	done := gapOf(t, h, "proj", "a gap already resolved", query.SeveritySevere)
	if resp := h.TaskUpdate("proj", done, "done", "fixed", nil); !resp.OK {
		t.Fatalf("TaskUpdate: %s", resp.Error)
	}
	gapOf(t, h, "proj", "a severe one", query.SeveritySevere)
	gapOf(t, h, "proj", "a prose one", query.SeverityProse)
	gapOf(t, h, "other", "an unweighed one", "")

	resp := h.Gaps(nil)
	if !resp.OK {
		t.Fatalf("Gaps: %s", resp.Error)
	}
	counts := resp.Data.(map[string]any)["counts"].(command.GapCounts)
	if counts.Total != 4 || counts.Open != 3 || counts.Done != 1 {
		t.Errorf("counts = %+v, want total 4, open 3, done 1", counts)
	}
	bySeverity := counts.OpenBySeverity
	if bySeverity.Severe != 1 || bySeverity.Defect != 0 || bySeverity.Prose != 1 || bySeverity.Unset != 1 {
		t.Errorf("open by severity = %+v, want severe 1, defect 0, prose 1, unset 1", bySeverity)
	}
}

// Merging two gaps about one defect is compensating events, never a deletion:
// the absorbed node is done and names what took it in, and the survivor alone
// says what it took in — a reader of one node must not need the other.
func TestTaskMergeRecordsBothSides(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	survivor := gapOf(t, h, "proj", "the branch step deletes the wrong branch", query.SeveritySevere)
	first := gapOf(t, h, "proj", "the teardown names a branch that is not the delivery's", query.SeverityProse)

	resp := h.TaskMerge("proj", first, survivor, "one defect, two sightings", nil)
	if !resp.OK {
		t.Fatalf("TaskMerge: %s", resp.Error)
	}

	absorbed := nodeInfo(t, h, "proj", first)
	if absorbed.Status != "done" {
		t.Errorf("absorbed status = %q, want done", absorbed.Status)
	}
	if !strings.Contains(strings.Join(absorbed.Decisions, "\n"), "proj:"+survivor) {
		t.Errorf("absorbed decisions = %v, want one naming the node that absorbed it (proj:%s)", absorbed.Decisions, survivor)
	}

	kept := nodeInfo(t, h, "proj", survivor)
	if kept.Status != "pending" {
		t.Errorf("survivor status = %q, want it still open and still the backlog entry", kept.Status)
	}
	if len(kept.Absorbed) != 1 || kept.Absorbed[0] != first {
		t.Errorf("survivor absorbed = %v, want [%s] read from the survivor alone", kept.Absorbed, first)
	}

	// A second merge accumulates rather than replacing the first.
	second := gapOf(t, h, "proj", "the same branch step, seen from the other side", query.SeverityProse)
	if resp := h.TaskMerge("proj", second, survivor, "same defect again", nil); !resp.OK {
		t.Fatalf("TaskMerge: %s", resp.Error)
	}
	if got := nodeInfo(t, h, "proj", survivor).Absorbed; len(got) != 2 || got[0] != first || got[1] != second {
		t.Errorf("survivor absorbed = %v, want both, in the order they were folded in", got)
	}
}

// An absorbed node is done, and nothing merges into a done node: that is what
// stops one gap being folded in twice, and stops a node that was closed being
// reopened by a merge. It is not what stops a chain — a survivor stays open —
// which is the rule below it.
func TestTaskMergeRefusesADoneNode(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	first := gapOf(t, h, "proj", "a gap", "")
	second := gapOf(t, h, "proj", "another gap", "")
	third := gapOf(t, h, "proj", "a third gap", "")
	if resp := h.TaskMerge("proj", first, second, "folded", nil); !resp.OK {
		t.Fatalf("TaskMerge: %s", resp.Error)
	}

	if resp := h.TaskMerge("proj", third, first, "into an absorbed node", nil); resp.OK {
		t.Error("merged into an absorbed node; an absorbed node is done, and a fold folds open nodes")
	}
	if resp := h.TaskMerge("proj", first, third, "an already absorbed node", nil); resp.OK {
		t.Error("merged a node that is already done")
	}
}

// A fold is a fold and not a chain, and the done check alone does not make that
// true: the survivor of a fold stays open, so it is exactly the node a second
// merge would absorb — A into B, B into C — and the final survivor would then
// name only B, leaving A reachable by reading B. Refusing to absorb a node that
// has itself absorbed is what makes the survivor's list the whole fold, so a
// reader of the final survivor sees every node that became one. The direction
// triage needs is untouched: a fresh gap still folds into an existing survivor.
func TestTaskMergeRefusesToAbsorbASurvivor(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	first := gapOf(t, h, "proj", "a gap", "")
	survivor := gapOf(t, h, "proj", "the same gap, seen again", "")
	third := gapOf(t, h, "proj", "the same gap, once more", "")
	if resp := h.TaskMerge("proj", first, survivor, "one defect, two sightings", nil); !resp.OK {
		t.Fatalf("TaskMerge: %s", resp.Error)
	}

	if resp := h.TaskMerge("proj", survivor, third, "the whole fold, onto a third node", nil); resp.OK {
		t.Error("a survivor was absorbed into a third node; that is the A into B, B into C chain, and the final survivor would show only part of the fold")
	}
	if resp := h.TaskMerge("proj", third, survivor, "same defect again", nil); !resp.OK {
		t.Fatalf("TaskMerge: %s", resp.Error)
	}
	if got := nodeInfo(t, h, "proj", survivor).Absorbed; len(got) != 2 || got[0] != first || got[1] != third {
		t.Errorf("survivor absorbed = %v, want the whole fold, in the order the nodes were folded in", got)
	}
}

func TestTaskMergeRefusesItselfAndUnknownNodes(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.ProjectCreate("other", "/tmp/other")

	gap := gapOf(t, h, "proj", "a gap", "")
	other := gapOf(t, h, "other", "a gap in another scope", "")

	if resp := h.TaskMerge("proj", gap, gap, "", nil); resp.OK {
		t.Error("a node absorbed itself")
	}
	if resp := h.TaskMerge("proj", gap, "t-nothing", "", nil); resp.OK {
		t.Error("merged into a node that does not exist; the link would point at nothing forever")
	}
	if resp := h.TaskMerge("proj", gap, "other:t-nothing", "", nil); resp.OK {
		t.Error("merged into an id that names nothing in the scope its address names")
	}
	// A bare id takes the command's own scope, so another scope's node is simply
	// not found there: the qualifier is what says which scope, and without one
	// there is only the one the command resolved.
	if resp := h.TaskMerge("proj", other, gap, "", nil); resp.OK {
		t.Error("a bare id from another scope resolved; an unqualified reference takes the command's own scope")
	}
	// A fold across scopes is not in this list any more: it is the case triage
	// needs — one defect filed in the cap's scope and noticed in the worker's —
	// and it is asserted in address_test.go. What an address still has to be is
	// real, and in the scope it names.
	if resp := h.TaskMerge("proj", gap, "", "", nil); resp.OK {
		t.Error("merged into nothing")
	}
}

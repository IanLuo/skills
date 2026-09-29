package command_test

import (
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

// The tool prints "<project>:<node>" in every brief, every report line and every
// card, so a path that names a node has to accept that form. These are the two
// that did not: fs log --node, which handed the reference to the reader raw, and
// task merge --into, which resolved both halves in the one scope --project
// names. Both failed silently or confusingly, which is what makes them one
// class: a node reference resolved by the wrong rule, in the wrong scope.

// SplitAddress is the one resolver. It locates — the scope the reference names,
// or the default when it names none — and does not judge whether that scope may
// be used here. That judgement is the caller's, and it differs per path.
func TestSplitAddress(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dflt      string
		ref       string
		wantScope string
		wantID    string
		wantErr   string
	}{
		{name: "a bare id takes the default scope", dflt: "skills", ref: "t-1a2b3c4d", wantScope: "skills", wantID: "t-1a2b3c4d"},
		{name: "a qualified address names its own scope", dflt: "skills", ref: "cap:t-1a2b3c4d", wantScope: "cap", wantID: "t-1a2b3c4d"},
		{name: "a qualified address with no default", dflt: "", ref: "cap:t-1a2b3c4d", wantScope: "cap", wantID: "t-1a2b3c4d"},
		{name: "a qualifier naming no node", dflt: "skills", ref: "cap:", wantErr: "names no node"},
		{name: "an empty reference", dflt: "skills", ref: "", wantErr: "required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, id, err := command.SplitAddress(tc.dflt, tc.ref)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("SplitAddress(%q, %q) = (%q, %q), want an error mentioning %q", tc.dflt, tc.ref, scope, id, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("SplitAddress(%q, %q) error = %q, want it to mention %q", tc.dflt, tc.ref, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("SplitAddress(%q, %q): %v", tc.dflt, tc.ref, err)
			}
			if scope != tc.wantScope || id != tc.wantID {
				t.Errorf("SplitAddress(%q, %q) = (%q, %q), want (%q, %q)", tc.dflt, tc.ref, scope, id, tc.wantScope, tc.wantID)
			}
		})
	}
}

// fs log --node took the reference raw, so "<project>:<node>" matched no node_id
// and the answer was zero events and exit 0 — indistinguishable from a node with
// no history, and from a typo.
func TestLogAcceptsAQualifiedNodeAddress(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	nodeID := h.TaskAdd("proj", "task one", nil).Data.(command.EventData).NodeID
	h.TaskAdd("proj", "task two", nil)

	qualified := "proj:" + nodeID
	resp := h.Log("proj", &qualified, nil)
	if !resp.OK {
		t.Fatalf("Log(%q): %s", qualified, resp.Error)
	}
	events := resp.Data.(command.LogResult).Events
	if len(events) != 1 {
		t.Fatalf("Log(%q) returned %d events, want the node's own 1", qualified, len(events))
	}
	if events[0].NodeID == nil || *events[0].NodeID != nodeID {
		t.Errorf("Log(%q) returned events for %v, want %s", qualified, events[0].NodeID, nodeID)
	}

	// The form that already worked must keep working — same answer, same events.
	bare := h.Log("proj", &nodeID, nil)
	if !bare.OK {
		t.Fatalf("Log(%q): %s", nodeID, bare.Error)
	}
	if got := bare.Data.(command.LogResult).Events; len(got) != len(events) {
		t.Errorf("bare id returned %d events, qualified returned %d — the two forms must mean the same node", len(got), len(events))
	}
}

// The worse half of the same defect: an id that resolves to nothing answered
// with zero events and exit 0. A reader cannot tell "no events" from "that is
// not a node", so the empty answer is a claim the store cannot support.
func TestLogRefusesAnIdThatNamesNoNode(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.TaskAdd("proj", "task one", nil)

	for _, ref := range []string{"t-00000000", "proj:t-00000000"} {
		resp := h.Log("proj", &ref, nil)
		if resp.OK {
			t.Errorf("Log(%q) succeeded with %d events; an id that names no node must refuse",
				ref, len(resp.Data.(command.LogResult).Events))
			continue
		}
		if !strings.Contains(resp.Error, "t-00000000") || !strings.Contains(resp.Error, "proj") {
			t.Errorf("Log(%q) error = %q, want it to name the node and the scope it was looked for in", ref, resp.Error)
		}
	}
}

// An address naming a scope the command is not reading is a contradiction the
// caller stated twice, and it is refused naming both — the rule resolveWriteNode
// has applied since the identifier-path card. It is not resolved by the address:
// an explicit scope is the stronger statement, and quietly reading another one
// is how a typo becomes a wrong answer instead of a refusal.
func TestLogRefusesAnAddressNamingAnotherScope(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.ProjectCreate("other", "/tmp/other")
	otherID := h.TaskAdd("other", "a node in another scope", nil).Data.(command.EventData).NodeID

	resp := h.Log("proj", strPtr("other:"+otherID), nil)
	if resp.OK {
		t.Fatal("Log read another scope's node; the caller named proj and other:t-... in one breath")
	}
	if !strings.Contains(resp.Error, "proj") || !strings.Contains(resp.Error, "other") {
		t.Errorf("error = %q, want it to name both scopes so the contradiction is visible", resp.Error)
	}
}

// --type narrows which of a node's events to show. It is not a second existence
// test: a node with no event of the requested type is a node with no event of
// the requested type, and the empty list there is an answer rather than a
// refusal.
func TestLogTypeFilterDoesNotTurnANodeIntoAMissingOne(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	nodeID := h.TaskAdd("proj", "task one", nil).Data.(command.EventData).NodeID

	et := store.DecisionRecorded
	resp := h.Log("proj", &nodeID, &et)
	if !resp.OK {
		t.Fatalf("Log(%s, decision-recorded): %s", nodeID, resp.Error)
	}
	if got := resp.Data.(command.LogResult).Events; len(got) != 0 {
		t.Errorf("got %d decision events, want 0", len(got))
	}
}

// The fold that motivated all of this: every backlog family pairs a finding in
// the cap's scope with one in the worker's, so triage needs a merge whose two
// ends are in two scopes. Resolving both in --project refused it.
func TestTaskMergeFoldsAcrossScopes(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("cap", "/tmp/cap")
	h.ProjectCreate("skills", "/tmp/skills")

	capGap := gapOf(t, h, "cap", "the finding, filed in the cap's scope", query.SeveritySevere)
	skillsGap := gapOf(t, h, "skills", "the same finding, noticed in the worker's scope", query.SeverityProse)

	// Fully qualified: neither operand takes the command's own scope.
	resp := h.TaskMerge("skills", "cap:"+capGap, "skills:"+skillsGap, "one defect, two sightings", nil)
	if !resp.OK {
		t.Fatalf("TaskMerge across scopes: %s", resp.Error)
	}

	absorbed := nodeInfo(t, h, "cap", capGap)
	if absorbed.Status != "done" {
		t.Errorf("absorbed status = %q, want done", absorbed.Status)
	}
	if !strings.Contains(strings.Join(absorbed.Decisions, "\n"), "skills:"+skillsGap) {
		t.Errorf("absorbed decisions = %v, want one naming the survivor by its full address (skills:%s)", absorbed.Decisions, skillsGap)
	}

	kept := nodeInfo(t, h, "skills", skillsGap)
	if kept.Status != "pending" {
		t.Errorf("survivor status = %q, want it still open", kept.Status)
	}
	// The survivor's list has to be readable from the survivor: a bare id from
	// another scope names nothing there, which is the defect this card is about.
	if len(kept.Absorbed) != 1 || kept.Absorbed[0] != "cap:"+capGap {
		t.Errorf("survivor absorbed = %v, want [cap:%s] — an id from another scope must carry its scope or it resolves nowhere", kept.Absorbed, capGap)
	}

	// The absorbed node's event really is in its own scope, not the survivor's.
	logResp := h.Log("cap", &capGap, nil)
	if !logResp.OK {
		t.Fatalf("Log(cap, %s): %s", capGap, logResp.Error)
	}
	if got := logResp.Data.(command.LogResult).Events; len(got) != 3 {
		t.Errorf("cap scope has %d events for the absorbed node, want 3 (created, merged-into, done)", len(got))
	}
}

// A qualified operand is resolved by the address it carries, so the same fold
// works the other way round, and a same-scope fold still records bare ids the
// way it always has.
func TestTaskMergeResolvesAQualifiedOperandInItsOwnScope(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")

	survivor := gapOf(t, h, "proj", "the gap that stays", query.SeveritySevere)
	first := gapOf(t, h, "proj", "the gap that folds in", query.SeverityProse)

	resp := h.TaskMerge("proj", "proj:"+first, "proj:"+survivor, "one defect, two sightings", nil)
	if !resp.OK {
		t.Fatalf("TaskMerge with qualified operands: %s", resp.Error)
	}
	kept := nodeInfo(t, h, "proj", survivor)
	if len(kept.Absorbed) != 1 || kept.Absorbed[0] != first {
		t.Errorf("survivor absorbed = %v, want [%s] — a same-scope fold is unchanged", kept.Absorbed, first)
	}
}

// A parent is a node in the child's own scope, one scope, so an address that
// names another is refused rather than followed: the tree has no cross-scope
// edges.
func TestTaskAddKindResolvesAQualifiedParent(t *testing.T) {
	h := setup(t)
	h.ProjectCreate("proj", "/tmp/proj")
	h.ProjectCreate("other", "/tmp/other")
	parent := gapOf(t, h, "proj", "the parent", "")
	otherID := gapOf(t, h, "other", "a gap in another scope", "")

	qualified := "proj:" + parent
	resp := h.TaskAddKind("proj", "a child", "work", "", "", "", "", "", &qualified)
	if !resp.OK {
		t.Fatalf("TaskAddKind with a qualified parent: %s", resp.Error)
	}
	child := resp.Data.(command.EventData).NodeID
	if info := nodeInfo(t, h, "proj", child); info.ParentNodeID != parent {
		t.Errorf("child parent = %q, want the bare %s — the qualifier is an address, not part of the id", info.ParentNodeID, parent)
	}

	across := "other:" + otherID
	if resp := h.TaskAddKind("proj", "a child", "work", "", "", "", "", "", &across); resp.OK {
		t.Error("a parent in another scope was accepted; the node would be unreachable in its own tree")
	}
	if resp := h.TaskAddKind("proj", "a child", "work", "", "", "", "", "", strPtr("t-00000000")); resp.OK {
		t.Error("a parent that does not exist was accepted")
	}
}

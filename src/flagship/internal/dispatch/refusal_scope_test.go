package dispatch

import (
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/store"
)

// The refusal's scope, and the rule that a field derived across scopes says so
// rather than borrowing the read's own scope. A count has no scope, and naming
// one is a claim the number does not support.
func TestRefusalScopeNamesTheRightScope(t *testing.T) {
	answer := pendingAnswer{
		Dispatches: []pendingDispatch{
			{Node: command.UnfinishedNode{NodeID: "t-1"}, Delivery: &command.DeliveryRecord{Project: "skills", Node: "t-2"}},
			{Node: command.UnfinishedNode{NodeID: "t-3"}},
		},
		GapScopes: map[string]int{"cap": 1, "skills": 2},
	}
	cases := []struct {
		field    string
		want     string
		spanning bool
	}{
		{"gap_scopes.skills", "skills", false},
		{"gap_scopes.cap", "cap", false},
		{"undispatched_gaps", "cap, skills", true},
		{"dispatches[0].worker.status", "skills", false},
		{"dispatches[0].reports[0].line", "skills", false},
		{"dispatches[0].node.status", "cap", false},
		{"dispatches[0].delivery.agent", "cap", false},
		{"cap.state", "cap", false},
	}
	for _, tc := range cases {
		got, spanning := refusalScope(answer, answer, tc.field)
		if got != tc.want || spanning != tc.spanning {
			t.Errorf("refusalScope(%q) = (%q, spanning=%v), want (%q, spanning=%v)", tc.field, got, spanning, tc.want, tc.spanning)
		}
	}

	// The message itself: one prefix (the caller's), a spanning field said to be
	// spanning, and the contributing scopes named.
	spanning := (&stateRefusal{
		read: "pending", scope: refusalScopesFor(answer, "undispatched_gaps"), spanning: true,
		diff: &store.JSONDiff{Field: "undispatched_gaps", A: "1", B: "2"},
	}).Error()
	if !strings.Contains(spanning, "across scopes (cap, skills)") {
		t.Errorf("spanning refusal = %q, want it to say across scopes and name them", spanning)
	}
	if strings.HasPrefix(spanning, "pending") {
		t.Errorf("refusal %q names the read; the caller names it once and two prefixes read as pending: pending:", spanning)
	}

	scoped := (&stateRefusal{
		read: "pending", scope: "skills", diff: &store.JSONDiff{Field: "dispatches[0].worker.status", A: "active", B: "done"},
	}).Error()
	if !strings.Contains(scoped, "on scope skills") {
		t.Errorf("scoped refusal = %q, want it to name the scope", scoped)
	}
	if strings.HasPrefix(scoped, "pending") {
		t.Errorf("refusal %q carries its own read prefix", scoped)
	}
}

// refusalScopesFor is refusalScope's scope half, for the message test above.
func refusalScopesFor(answer pendingAnswer, field string) string {
	scope, _ := refusalScope(answer, answer, field)
	return scope
}

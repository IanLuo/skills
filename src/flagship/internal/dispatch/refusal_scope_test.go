package dispatch

import (
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/store"
)

// The refusal's scope: one scope's value names that scope, and everything that
// is not one scope's value says it spans scopes rather than borrowing the read's
// own. A count has no scope, and naming one is a claim the value does not
// support.
func TestRefusalScopeNamesTheRightScope(t *testing.T) {
	answer := pendingAnswer{
		Dispatches: []pendingDispatch{
			{Node: command.UnfinishedNode{NodeID: "t-1"}, Delivery: &command.DeliveryRecord{Project: "skills", Node: "t-2"}},
			{Node: command.UnfinishedNode{NodeID: "t-3"}},
		},
		GapScopes: map[string]int{"cap": 1, "skills": 2},
	}
	differing := pendingAnswer{
		Dispatches: answer.Dispatches,
		GapScopes:  map[string]int{"cap": 1, "skills": 3},
	}
	cases := []struct {
		field    string
		want     string
		spanning bool
	}{
		// One scope's own value.
		{"gap_scopes.skills", "skills", false},
		{"gap_scopes.cap", "cap", false},
		{"dispatches[0].worker.status", "skills", false},
		{"dispatches[0].reports[0].line", "skills", false},
		{"dispatches[0].node.status", "cap", false},
		{"dispatches[0].delivery.agent", "cap", false},
		{"cap.state", "cap", false},
		{"cap", "cap", false},

		// Aggregates over scopes: the total, the whole map (a scope added to it
		// or removed from it arrives as this bare field, because an empty map is
		// omitted), the dispatch list, and a field this function has never seen.
		// Spanning is the default, so the next aggregate cannot inherit a scope
		// it does not have.
		{"undispatched_gaps", "skills", true},
		{"gap_scopes", "skills", true},
		{"dispatches", "skills", true},
		{"a_field_added_later", "skills", true},
	}
	for _, tc := range cases {
		got, spanning := refusalScope(answer, differing, tc.field)
		if got != tc.want || spanning != tc.spanning {
			t.Errorf("refusalScope(%q) = (%q, spanning=%v), want (%q, spanning=%v)", tc.field, got, spanning, tc.want, tc.spanning)
		}
	}

	// Two answers that agree contribute nothing: the count spans scopes, but no
	// scope moved.
	if got := contributingScopes(answer, answer); got != "" {
		t.Errorf("contributingScopes of two equal answers = %q, want none", got)
	}
	if got := contributingScopes(answer, differing); got != "skills" {
		t.Errorf("contributingScopes = %q, want only the scope that moved", got)
	}
	if got := spanningScope(answer, answer); got != "every scope" {
		t.Errorf("spanningScope of two equal answers = %q, want every scope", got)
	}

	// The message itself: one prefix (the caller's), a spanning field said to be
	// spanning with its contributing scopes named, and a scoped field named as
	// one scope.
	spanning := (&stateRefusal{
		read: "pending", scope: spanningScope(answer, differing), spanning: true,
		diff: &store.JSONDiff{Field: "gap_scopes", A: `{"cap":1,"skills":2}`, B: `{"cap":1,"skills":3}`},
	}).Error()
	if !strings.Contains(spanning, "across scopes (skills)") {
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

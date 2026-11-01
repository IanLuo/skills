package coord_test

import (
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/coord"
	"github.com/flagship-dev/flagship/internal/memory"
)

// TestSessionBoundToACannotTouchB is AC3. Session S claims A; then every
// task-affecting verb against B is refused, and — the part that matters — refused
// before it writes. Row counts and row identities are compared, not just the errors,
// because a refusal that already appended something has not refused.
func TestSessionBoundToACannotTouchB(t *testing.T) {
	s, _ := open(t)
	a := add(t, s, "p")
	b := add(t, s, "p")
	claim(t, s, "p", "s-1", a.Tid)

	before := snapshot(t, s, "p")

	attempts := map[string]func() error{
		"claim": func() error {
			_, err := coord.Claim(s, coord.ClaimInput{Project: "p", Session: "s-1", Task: b.Tid})
			return err
		},
		"report": func() error {
			_, err := coord.Report(s, coord.ReportInput{Project: "p", Session: "s-1", Task: b.Tid, State: "progress"})
			return err
		},
		"report a check": func() error {
			_, err := coord.Report(s, coord.ReportInput{
				Project: "p", Session: "s-1", Task: b.Tid, State: "pass", Role: "review", Command: "true", Exit: exit(0),
			})
			return err
		},
		"done": func() error {
			_, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-1", Task: b.Tid})
			return err
		},
		"status of another project": func() error {
			_, err := coord.Status(s, coord.StatusInput{Project: "other", Session: "s-1"})
			return err
		},
	}
	for name, attempt := range attempts {
		err := attempt()
		if err == nil {
			t.Fatalf("%s on another task was allowed", name)
		}
		if !strings.Contains(err.Error(), a.Tid) && !strings.Contains(err.Error(), "s-1") {
			t.Fatalf("%s refusal %q does not name the existing binding %s", name, err, a.Tid)
		}
	}

	if after := snapshot(t, s, "p"); after != before {
		t.Fatalf("rows changed after cross-task refusals:\nbefore %v\nafter  %v", before, after)
	}
}

// TestBindingSurvivesCompletion: A finishing does not free S to take B. One
// identity holds one task for its life, so a fresh session is the only way to new
// work.
func TestBindingSurvivesCompletion(t *testing.T) {
	s, _ := open(t)
	a := add(t, s, "p")
	b := add(t, s, "p")
	review := addReview(t, s, "p", a.Tid)

	claim(t, s, "p", "s-dev", a.Tid)
	claim(t, s, "p", "s-rev", review.Tid)
	runCheck(t, s, "p", "s-rev", review.Tid, a.Checks[0], 0)
	if _, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-rev", Task: review.Tid}); err != nil {
		t.Fatalf("done: %v", err)
	}

	if _, err := coord.Claim(s, coord.ClaimInput{Project: "p", Session: "s-dev", Task: b.Tid}); err == nil {
		t.Fatal("a session moved to a second task after its first completed")
	}
	// A fresh session takes B, which is still eligible.
	got := claim(t, s, "p", "s-fresh", b.Tid)
	if got.Tid != b.Tid {
		t.Fatalf("fresh session claimed %s, want %s", got.Tid, b.Tid)
	}
}

// TestSecondTaskForOneSessionIsRefusedAtTheBind: the refusal is memstore's duplicate,
// not a check fs performs. The listing is deliberately read before the second claim,
// so a protocol that decided from that read would let the session through.
func TestSecondTaskForOneSessionIsRefusedAtTheBind(t *testing.T) {
	s, _ := open(t)
	a := add(t, s, "p")
	b := add(t, s, "p")
	claim(t, s, "p", "s-1", a.Tid)

	rs := rows(t, s, "p") // a stale-in-time listing is irrelevant to the bind's key
	if _, ok := memory.NewIndex(rs).Binding("s-1"); !ok {
		t.Fatal("the session was not bound after claiming")
	}
	_, err := coord.Claim(s, coord.ClaimInput{Project: "p", Session: "s-1", Task: b.Tid})
	if err == nil || !strings.Contains(err.Error(), a.Tid) {
		t.Fatalf("second claim = %v, want a refusal naming %s", err, a.Tid)
	}
	// Task B itself is the only row that names it: no bind, no claim, no event.
	for _, r := range rows(t, s, "p") {
		if r.Kind != memory.KindTask && r.Task() == b.Tid {
			t.Fatalf("a refused claim wrote %q about task %s", r.OriginID, b.Tid)
		}
	}
}

// TestUnboundSessionCannotWrite: a session that has claimed nothing holds nothing,
// so a report or a completion is refused rather than silently accepted.
func TestUnboundSessionCannotWrite(t *testing.T) {
	s, _ := open(t)
	task := add(t, s, "p")
	if _, err := coord.Report(s, coord.ReportInput{Project: "p", Session: "s-x", Task: task.Tid, State: "progress"}); err == nil {
		t.Fatal("an unbound session reported against a task")
	}
	if _, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-x", Task: task.Tid}); err == nil {
		t.Fatal("an unbound session completed a task")
	}
	if n := len(rows(t, s, "p")); n != 1 {
		t.Fatalf("%d rows after two refusals, want only the task", n)
	}
}

// snapshot is the listing as comparable text: it is how "refused before writing"
// becomes an observation rather than a claim.
func snapshot(t *testing.T, s *memory.Store, project string) string {
	t.Helper()
	var b strings.Builder
	for _, r := range rows(t, s, project) {
		b.WriteString(r.OriginID + "|" + r.Extra + "|" + r.Text + "\n")
	}
	return b.String()
}

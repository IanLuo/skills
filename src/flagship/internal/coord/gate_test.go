package coord_test

import (
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/coord"
	"github.com/flagship-dev/flagship/internal/memory"
)

// reviewFixture is a development task, its review task, and both held: the state
// every gate case starts from.
func reviewFixture(t *testing.T) (*memory.Store, coord.Task, coord.Task) {
	s, _ := open(t)
	dev := add(t, s, "p")
	review := addReview(t, s, "p", dev.Tid)
	claim(t, s, "p", "s-dev", dev.Tid)
	claim(t, s, "p", "s-rev", review.Tid)
	return s, dev, review
}

// TestDoneRefusesWithoutAReviewTask is the "missing reviewer" case: a worker cannot
// complete its own work, and saying so is a refusal, not a warning.
func TestDoneRefusesWithoutAReviewTask(t *testing.T) {
	s, _ := open(t)
	dev := add(t, s, "p")
	claim(t, s, "p", "s-dev", dev.Tid)
	report(t, s, coord.ReportInput{Project: "p", Session: "s-dev", Task: dev.Tid, State: "dev-done"})

	_, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-dev", Task: dev.Tid})
	if err == nil {
		t.Fatal("a worker completed its own development task")
	}
	if !strings.Contains(err.Error(), "review") {
		t.Fatalf("refusal %q does not say a review task is required", err)
	}
	assertOpen(t, s, dev.Tid)
}

// TestDoneRefusesWithoutReviewerRunChecks is the "missing required check" case: the
// developer's own report is not evidence of an independent check.
func TestDoneRefusesWithoutReviewerRunChecks(t *testing.T) {
	s, dev, review := reviewFixture(t)
	// The developer runs the check and reports it. It is not evidence: the run has
	// to come from the reviewer, on the review task.
	report(t, s, coord.ReportInput{
		Project: "p", Session: "s-dev", Task: dev.Tid, State: "pass",
		Command: dev.Checks[0], Exit: exit(0),
	})

	_, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-rev", Task: review.Tid})
	if err == nil {
		t.Fatal("completion passed with no reviewer-run check")
	}
	if !strings.Contains(err.Error(), dev.Checks[0]) {
		t.Fatalf("refusal %q does not name the missing check %q", err, dev.Checks[0])
	}
	assertOpen(t, s, dev.Tid)
	assertOpen(t, s, review.Tid)
}

// TestDoneRefusesAFailingCheck: a non-zero result leaves the task incomplete and the
// record keeps the exact command and exit code that refused it.
func TestDoneRefusesAFailingCheck(t *testing.T) {
	s, dev, review := reviewFixture(t)
	runCheck(t, s, "p", "s-rev", review.Tid, dev.Checks[0], 1)

	_, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-rev", Task: review.Tid})
	if err == nil {
		t.Fatal("completion passed with a failing check")
	}
	if !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("refusal %q does not record the exit code", err)
	}
	assertOpen(t, s, dev.Tid)

	// The refusal is recorded: the next reader does not have to re-derive why.
	st, err := coord.Status(s, coord.StatusInput{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, task := range st.Projects[0].Tasks {
		if task.Task != review.Tid {
			continue
		}
		for _, ev := range task.Evidence {
			if ev.State == "fail" && ev.Command == "" && strings.Contains(ev.Note, "completion refused") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the refusal was not recorded on the review task")
	}
}

// TestDoneRefusesAPartialCheckSet: every declared check is required, not one of
// them. A task that declared two cannot be completed on the strength of one.
func TestDoneRefusesAPartialCheckSet(t *testing.T) {
	s, _ := open(t)
	dev, err := coord.Add(s, coord.AddInput{
		Project: "p", Goal: "two checks", Checks: []string{"go test ./...", "go vet ./..."},
	})
	if err != nil {
		t.Fatal(err)
	}
	review := addReview(t, s, "p", dev.Tid)
	claim(t, s, "p", "s-dev", dev.Tid)
	claim(t, s, "p", "s-rev", review.Tid)
	runCheck(t, s, "p", "s-rev", review.Tid, dev.Checks[0], 0)

	if _, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-rev", Task: review.Tid}); err == nil {
		t.Fatalf("completion passed with %q unrun", dev.Checks[1])
	} else if !strings.Contains(err.Error(), dev.Checks[1]) {
		t.Fatalf("refusal %q does not name the unrun check %q", err, dev.Checks[1])
	}
}

// TestDoneRequiresADifferentSession checks independence at the gate rather than
// trusting the binding layer for it. Through fs this state cannot arise — one
// identity holds one task — so the row is written directly: a harness that reused an
// identity must be refused here, not three layers down.
func TestDoneRequiresADifferentSession(t *testing.T) {
	s, _ := open(t)
	dev := add(t, s, "p")
	review := addReview(t, s, "p", dev.Tid)
	claim(t, s, "p", "s-same", review.Tid)
	if err := s.PutClaim(memory.Scope("p"), dev.Tid, "s-same", 1); err != nil {
		t.Fatalf("forge the reused identity's claim: %v", err)
	}
	runCheck(t, s, "p", "s-same", review.Tid, dev.Checks[0], 0)

	_, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-same", Task: review.Tid})
	if err == nil {
		t.Fatal("one session developed and reviewed the same work")
	}
	if !strings.Contains(err.Error(), "different session") {
		t.Fatalf("refusal %q does not say the reviewer must be a different session", err)
	}
}

// TestDonePassesOnAPassingIndependentCheckSet is the only case that completes
// anything: the reviewer ran every declared check, each exited zero, and the record
// keeps the command, the exit code and the session.
func TestDonePassesOnAPassingIndependentCheckSet(t *testing.T) {
	s, dev, review := reviewFixture(t)
	runCheck(t, s, "p", "s-rev", review.Tid, dev.Checks[0], 0)

	result, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-rev", Task: review.Tid})
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if result.Dev != dev.Tid || result.Review != review.Tid {
		t.Fatalf("result = %+v, want both tasks named", result)
	}
	if len(result.Checks) != 1 || result.Checks[0].Command != dev.Checks[0] ||
		result.Checks[0].Exit != 0 || result.Checks[0].Session != "s-rev" {
		t.Fatalf("checks = %+v, want the exact command, exit and reviewer session", result.Checks)
	}
	// Both rows are closed, and stay closed: completion is terminal.
	rs := rows(t, s, "p")
	for _, r := range rs {
		if r.Kind == memory.KindTask && (r.Task() == dev.Tid || r.Task() == review.Tid) && !r.Closed {
			t.Fatalf("task %s is not closed after completion", r.Task())
		}
	}
	if _, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-rev", Task: review.Tid}); err == nil {
		t.Fatal("a completed task was completed again; it must never be reopened")
	}
	if _, err := coord.Claim(s, coord.ClaimInput{Project: "p", Session: "s-fresh", Task: dev.Tid}); err == nil {
		t.Fatal("a closed task was claimable")
	}
}

// TestDoneRerunsTheNewestResult: a reviewer that ran a check, fixed the cause and
// re-ran it reports the rerun, not the first failure.
func TestDoneRerunsTheNewestResult(t *testing.T) {
	s, _ := open(t)
	dev, err := coord.Add(s, coord.AddInput{Project: "p", Goal: "rerun", Checks: []string{"flake"}})
	if err != nil {
		t.Fatal(err)
	}
	review2 := addReview(t, s, "p", dev.Tid)
	claim(t, s, "p", "s-dev2", dev.Tid)
	claim(t, s, "p", "s-rev2", review2.Tid)
	runCheck(t, s, "p", "s-rev2", review2.Tid, dev.Checks[0], 1)
	runCheck(t, s, "p", "s-rev2", review2.Tid, dev.Checks[0], 0)

	if _, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-rev2", Task: review2.Tid}); err != nil {
		t.Fatalf("the newest run was not the one the gate read: %v", err)
	}
}

// assertOpen fails unless a task is still open.
func assertOpen(t *testing.T, s *memory.Store, tid string) {
	t.Helper()
	for _, r := range rows(t, s, "p") {
		if r.Kind == memory.KindTask && r.Task() == tid {
			if r.Closed {
				t.Fatalf("task %s was closed by a refusal", tid)
			}
			return
		}
	}
	t.Fatalf("no task row for %s", tid)
}

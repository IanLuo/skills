package coord_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/flagship-dev/flagship/internal/coord"
	"github.com/flagship-dev/flagship/internal/memory"
)

// open returns a store on a scratch memstore file with a controllable clock. A
// test that needs to watch a lease lapse moves the clock rather than waiting.
func open(t *testing.T) (*memory.Store, *time.Time) {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s.Clock = func() time.Time { return now }
	return s, &now
}

// add records a development task with one declared check.
func add(t *testing.T, s *memory.Store, project string) coord.Task {
	t.Helper()
	task, err := coord.Add(s, coord.AddInput{
		Project: project,
		Goal:    "make the thing work",
		Checks:  []string{"go test ./..."},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	return task
}

// addReview records the review task for a development task.
func addReview(t *testing.T, s *memory.Store, project, dev string) coord.Task {
	t.Helper()
	task, err := coord.Add(s, coord.AddInput{
		Project:  project,
		Goal:     "review " + dev,
		ReviewOf: dev,
	})
	if err != nil {
		t.Fatalf("add review: %v", err)
	}
	return task
}

// claim takes a task for a session and fails the test if it cannot.
func claim(t *testing.T, s *memory.Store, project, session, task string) coord.Task {
	t.Helper()
	got, err := coord.Claim(s, coord.ClaimInput{Project: project, Session: session, Task: task})
	if err != nil {
		t.Fatalf("claim %s for %s: %v", task, session, err)
	}
	return got
}

// report records one report and fails the test if it is refused.
func report(t *testing.T, s *memory.Store, in coord.ReportInput) coord.Evidence {
	t.Helper()
	ev, err := coord.Report(s, in)
	if err != nil {
		t.Fatalf("report %s on %s: %v", in.State, in.Task, err)
	}
	return ev
}

// exit is the pointer form the report and evidence types take.
func exit(n int) *int { return &n }

// rows reads one project's listing.
func rows(t *testing.T, s *memory.Store, project string) []memory.Row {
	t.Helper()
	got, err := s.Rows(memory.Scope(project))
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	return got
}

// countKind counts the rows of one kind in a listing.
func countKind(rs []memory.Row, kind string) int {
	n := 0
	for _, r := range rs {
		if r.Kind == kind {
			n++
		}
	}
	return n
}

// runCheck records a check as a reviewer would: role=review, the exact command and
// the exit code it produced.
func runCheck(t *testing.T, s *memory.Store, project, session, task, command string, code int) {
	t.Helper()
	state := "fail"
	if code == 0 {
		state = "pass"
	}
	report(t, s, coord.ReportInput{
		Project: project, Session: session, Task: task, Role: "review",
		State: state, Command: command, Exit: exit(code),
	})
}

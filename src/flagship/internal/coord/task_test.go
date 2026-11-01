package coord_test

import (
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/coord"
	"github.com/flagship-dev/flagship/internal/memory"
)

// TestAddRecordsATask is AC1's happy path: a valid scope and a non-empty goal with
// acceptance criteria become one row, and the caller gets its identifier.
func TestAddRecordsATask(t *testing.T) {
	s, _ := open(t)
	task, err := coord.Add(s, coord.AddInput{
		Project: "p", Goal: "ship the rebuild", Checks: []string{"go test ./...", "go vet ./..."},
		Path: "/tmp/wt",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if task.Tid == "" || task.Kind != coord.KindDev {
		t.Fatalf("task = %+v, want a dev task with an id", task)
	}
	if len(task.Checks) != 2 || task.GoalHash == "" {
		t.Fatalf("task = %+v, want two checks and a goal digest", task)
	}
	rs := rows(t, s, "p")
	if countKind(rs, memory.KindTask) != 1 {
		t.Fatalf("task rows = %d, want 1", countKind(rs, memory.KindTask))
	}
}

// TestAddRefusesEmptyRecords is AC1's other half, and the rule from the design's
// data model: an empty record is refused before any write, so a refusal leaves no
// trace to clean up.
func TestAddRefusesEmptyRecords(t *testing.T) {
	s, _ := open(t)
	cases := []struct {
		name string
		in   coord.AddInput
		want string
	}{
		{"no goal", coord.AddInput{Project: "p", Checks: []string{"true"}}, "no goal"},
		{"blank goal", coord.AddInput{Project: "p", Goal: "   ", Checks: []string{"true"}}, "no goal"},
		{"no project", coord.AddInput{Goal: "g", Checks: []string{"true"}}, "project"},
		{"no check", coord.AddInput{Project: "p", Goal: "g"}, "no declared check"},
		{"blank check", coord.AddInput{Project: "p", Goal: "g", Checks: []string{" "}}, "empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := coord.Add(s, c.in)
			if err == nil {
				t.Fatal("refused record was accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %q, want it to name %q", err, c.want)
			}
		})
	}
	rs, err := s.AllRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 0 {
		t.Fatalf("%d rows exist after every add was refused; a refusal must write nothing", len(rs))
	}
}

// TestReviewTaskRunsTheDeclaredChecks: a review is a distinct task in the same
// scope whose checks are the development task's, not a set it chose for itself.
func TestReviewTaskRunsTheDeclaredChecks(t *testing.T) {
	s, _ := open(t)
	dev := add(t, s, "p")

	review := addReview(t, s, "p", dev.Tid)
	if review.Kind != coord.KindReview || review.ParentDev != dev.Tid {
		t.Fatalf("review = %+v, want a review task naming %s", review, dev.Tid)
	}
	if len(review.Checks) != 1 || review.Checks[0] != dev.Checks[0] {
		t.Fatalf("review checks = %v, want the development task's %v", review.Checks, dev.Checks)
	}
	if review.Tid == dev.Tid {
		t.Fatal("the review reused the development task's identity")
	}

	// A review may not pick its own checks: two check sets would drift.
	if _, err := coord.Add(s, coord.AddInput{
		Project: "p", Goal: "review", ReviewOf: dev.Tid, Checks: []string{"something else"},
	}); err == nil {
		t.Fatal("a review task was allowed to declare its own checks")
	}
	// A review of a review is a category error.
	if _, err := coord.Add(s, coord.AddInput{Project: "p", Goal: "review again", ReviewOf: review.Tid}); err == nil {
		t.Fatal("a review of a review task was accepted")
	}
	// A review of a task in another project does not exist.
	if _, err := coord.Add(s, coord.AddInput{Project: "other", Goal: "review", ReviewOf: dev.Tid}); err == nil {
		t.Fatal("a review was created for a task in another project")
	}
}

// TestAddRefusesAReviewOfClosedWork: a completed task is never reopened, so new
// work is a new task even when the same goal comes round again.
func TestAddRefusesAReviewOfClosedWork(t *testing.T) {
	s, _ := open(t)
	dev := add(t, s, "p")
	review := addReview(t, s, "p", dev.Tid)
	if _, err := coord.Claim(s, coord.ClaimInput{Project: "p", Session: "s-dev", Task: dev.Tid}); err != nil {
		t.Fatal(err)
	}
	claim(t, s, "p", "s-rev", review.Tid)
	runCheck(t, s, "p", "s-rev", review.Tid, dev.Checks[0], 0)
	if _, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-rev", Task: review.Tid}); err != nil {
		t.Fatalf("done: %v", err)
	}
	if _, err := coord.Add(s, coord.AddInput{Project: "p", Goal: "again", ReviewOf: dev.Tid}); err == nil {
		t.Fatal("a closed development task accepted a new review; a completed task is never reopened")
	}
}

package coord

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/flagship-dev/flagship/internal/memory"
)

// DoneInput is a reviewer asking to complete a review.
type DoneInput struct {
	Project string
	Session string
	Task    string
	Note    string
}

// CheckResult is one required check as the completion record keeps it: the exact
// command, its exit result, and the session that ran it.
type CheckResult struct {
	Command string `json:"command"`
	Exit    int    `json:"exit"`
	Session string `json:"session"`
}

// DoneResult names both tasks a passing review closes.
type DoneResult struct {
	Review string        `json:"review"`
	Dev    string        `json:"dev"`
	Checks []CheckResult `json:"checks"`
}

// Done is the completion gate.
//
// Completion is requested on a review task, by the reviewer, and only a passing
// independent check set closes anything. All four of these leave the task
// incomplete and record why: no review task (a worker cannot complete its own
// work), a development task already closed, no developer to be independent from,
// and a required check that is missing or did not pass.
//
// The developer's own report of completion is never enough on its own: it is one
// input, and this gate is the other.
func Done(s *memory.Store, in DoneInput) (DoneResult, error) {
	if err := memory.ValidProject(in.Project); err != nil {
		return DoneResult{}, err
	}
	if err := memory.ValidID("session", in.Session); err != nil {
		return DoneResult{}, err
	}
	if err := memory.ValidID("task", in.Task); err != nil {
		return DoneResult{}, err
	}

	scope := memory.Scope(in.Project)
	rows, err := s.Rows(scope)
	if err != nil {
		return DoneResult{}, err
	}
	now := s.Now()
	ix := memory.NewIndex(rows)
	if err := hold(ix, in.Session, in.Task, now); err != nil {
		return DoneResult{}, err
	}

	review, err := loadTask(rows, in.Task)
	if err != nil {
		return DoneResult{}, err
	}
	if review.ParentDev == "" {
		return DoneResult{}, fmt.Errorf(
			"%s is development work: completion requires a review task, claimed by a fresh session, that runs the checks — a worker cannot complete its own work", review.Tid)
	}
	dev, err := loadTask(rows, review.ParentDev)
	if err != nil {
		return DoneResult{}, err
	}
	if dev.done {
		return DoneResult{}, fmt.Errorf("%s is already closed; a completed task is never reopened", dev.Tid)
	}
	developer, ok := ix.Claim(dev.Tid)
	if !ok {
		return DoneResult{}, fmt.Errorf("%s was never claimed, so there is no developer to be independent from", dev.Tid)
	}
	if developer.Session == in.Session {
		return DoneResult{}, fmt.Errorf("session %s holds %s; a reviewer must be a different session from the developer", in.Session, dev.Tid)
	}

	// Every check the development task declared, against the newest run of that
	// exact command by this reviewer. A later run is the current truth, so a
	// reviewer that fixed and re-ran reports the rerun.
	events := eventsFor(ix, review.Tid, now)
	var (
		passed  []CheckResult
		missing []string
		failed  []string
	)
	for _, check := range dev.Checks {
		ev, ok := newestCheck(events, in.Session, check)
		if !ok || ev.Exit == nil {
			missing = append(missing, check)
			continue
		}
		if *ev.Exit != 0 {
			failed = append(failed, fmt.Sprintf("%s (exit %d)", check, *ev.Exit))
			continue
		}
		passed = append(passed, CheckResult{Command: check, Exit: *ev.Exit, Session: ev.Session})
	}
	if len(missing) > 0 || len(failed) > 0 {
		reason := refusalReason(missing, failed)
		// The task stays incomplete and the review records the reason: a refusal
		// with no trace would make the next reader re-derive it from nothing.
		extra, err := json.Marshal(eventExtra{
			State: "fail", Role: "review", Note: clip(reason, maxNote),
		})
		if err != nil {
			return DoneResult{}, err
		}
		if err := s.PutEvent(scope, review.Tid, in.Session,
			ix.MaxEvent(review.Tid, in.Session)+1,
			reason, string(extra)); err != nil {
			return DoneResult{}, err
		}
		return DoneResult{}, errors.New(reason)
	}

	// Passing. Close the review first, then the development task it reviewed. That
	// order is the recoverable one: a failure between the two leaves the
	// development task open, and a rerun closes it — while closing the development
	// task first would leave a review task that can never be completed, because
	// the gate refuses a closed development task.
	if err := s.Retire(review.rowID); err != nil {
		return DoneResult{}, err
	}
	if err := s.Retire(dev.rowID); err != nil {
		return DoneResult{}, err
	}
	return DoneResult{Review: review.Tid, Dev: dev.Tid, Checks: passed}, nil
}

// newestCheck returns the newest review-run result for one exact command by the
// session that must have run it. A run by anyone else is not evidence of an
// independent check.
func newestCheck(events []Evidence, sid, command string) (Evidence, bool) {
	for _, ev := range events {
		if ev.Role != "review" || ev.Session != sid || ev.Command != command {
			continue
		}
		return ev, true
	}
	return Evidence{}, false
}

// refusalReason says exactly what is missing or failing, in the order a reader
// can act on it.
func refusalReason(missing, failed []string) string {
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "no recorded reviewer run of: "+strings.Join(missing, "; "))
	}
	if len(failed) > 0 {
		parts = append(parts, "failed: "+strings.Join(failed, "; "))
	}
	return "completion refused: " + strings.Join(parts, " — ") + "; the task stays incomplete"
}

// clip bounds a stored string. Evidence is bounded by design: no transcript, no
// unbounded tool output, nothing a secret could hide in.
func clip(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}

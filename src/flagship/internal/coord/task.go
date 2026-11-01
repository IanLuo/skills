// Package coord is fs's coordination policy: the task shape, the claim protocol,
// the completion gate and status derivation. It refuses to import memstore —
// every persistent read and write goes through internal/memory, which is the one
// place keys are built and liveness is computed.
package coord

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/flagship-dev/flagship/internal/memory"
)

// The two task kinds. There is no kind column: a task is a review task exactly
// when it names the development task it reviews, which is also the fact a
// completion is gated on.
const (
	KindDev    = "dev"
	KindReview = "review"
)

// bounded evidence. Nothing here stores a transcript, a secret or unbounded tool
// output — only what a reader needs to decide whether a declared check passed.
const (
	maxNote    = 2000
	maxDigest  = 200
	maxCommand = 500
)

// Task is one unit of work: the row's identity plus the fields the row carries.
// Session and Round are derived — who holds it now — and are empty on a task
// nobody holds.
type Task struct {
	Tid         string   `json:"task"`
	Kind        string   `json:"kind"`
	Goal        string   `json:"goal"`
	Checks      []string `json:"checks"`
	ProjectPath string   `json:"project_path,omitempty"`
	ParentDev   string   `json:"parent_dev,omitempty"`
	GoalHash    string   `json:"goal_hash"`
	Session     string   `json:"session,omitempty"`
	Round       int      `json:"round,omitempty"`

	rowID int64
	at    time.Time
	done  bool
}

// taskExtra is a task row's extra: the four fields the design names, no more.
type taskExtra struct {
	Goal        string   `json:"goal"`
	Checks      []string `json:"checks"`
	ProjectPath string   `json:"project_path,omitempty"`
	ParentDev   string   `json:"parent_dev,omitempty"`
	GoalHash    string   `json:"goal_hash"`
}

// AddInput is a user's new task. The user owns intent: goal, project scope,
// acceptance criteria and the checks a reviewer must run.
type AddInput struct {
	Project  string
	Goal     string
	Checks   []string
	Path     string
	ReviewOf string
}

// Add stores one task row and returns it.
//
// Empty and invalid records are refused before any write: no goal, no project
// scope, no declared check. A task no reviewer can check could never be completed
// under the gate, so it is refused here rather than discovered there.
func Add(s *memory.Store, in AddInput) (Task, error) {
	if err := memory.ValidProject(in.Project); err != nil {
		return Task{}, err
	}
	goal := strings.TrimSpace(in.Goal)
	if goal == "" {
		return Task{}, errors.New("no goal: --goal is required")
	}
	scope := memory.Scope(in.Project)

	// A review is a task whose checks are the development task's: the reviewer
	// runs what the task declared, not a set it chose for itself.
	var parent Task
	if in.ReviewOf != "" {
		if err := memory.ValidID("task", in.ReviewOf); err != nil {
			return Task{}, err
		}
		if len(in.Checks) > 0 {
			return Task{}, errors.New("a review task runs the task's declared checks: drop --check, or add a task of its own")
		}
		rows, err := s.Rows(scope)
		if err != nil {
			return Task{}, err
		}
		parent, err = loadTask(rows, in.ReviewOf)
		if err != nil {
			return Task{}, err
		}
		if parent.ParentDev != "" {
			return Task{}, fmt.Errorf("%s is itself a review task; a review reviews development work", parent.Tid)
		}
		if parent.done {
			return Task{}, fmt.Errorf("%s is closed; re-work is a new task", parent.Tid)
		}
	}

	checks, err := cleanChecks(in.Checks)
	if err != nil {
		return Task{}, err
	}
	if parent.Tid != "" {
		checks = parent.Checks
	}
	if len(checks) == 0 {
		return Task{}, errors.New("no declared check: --check is required, because a task no reviewer can check cannot be completed")
	}

	task := Task{
		Tid:         newTid(),
		Kind:        KindDev,
		Goal:        goal,
		Checks:      checks,
		ProjectPath: strings.TrimSpace(in.Path),
		ParentDev:   parent.Tid,
		GoalHash:    hash(goal),
	}
	if task.ParentDev != "" {
		task.Kind = KindReview
	}
	extra, err := json.Marshal(taskExtra{
		Goal: task.Goal, Checks: task.Checks, ProjectPath: task.ProjectPath,
		ParentDev: task.ParentDev, GoalHash: task.GoalHash,
	})
	if err != nil {
		return Task{}, err
	}
	if err := s.PutTask(scope, task.Tid, task.Goal, string(extra)); err != nil {
		if errors.Is(err, memory.ErrDuplicate) {
			return Task{}, fmt.Errorf("task id %s is already taken; retry", task.Tid)
		}
		return Task{}, err
	}
	task.at = s.Now()
	return task, nil
}

// cleanChecks trims the declared checks and refuses an entry that is not one, so
// a blank --check cannot stand in for a check the reviewer would run.
func cleanChecks(checks []string) ([]string, error) {
	out := make([]string, 0, len(checks))
	for _, c := range checks {
		c = strings.TrimSpace(c)
		if c == "" {
			return nil, errors.New("a declared check is empty")
		}
		if len(c) > maxCommand {
			return nil, fmt.Errorf("declared check is %d bytes; the cap is %d", len(c), maxCommand)
		}
		out = append(out, c)
	}
	return out, nil
}

// decodeTask turns one task row into a task.
func decodeTask(r memory.Row) (Task, error) {
	tid, ok := memory.ParseTask(r.OriginID)
	if !ok {
		return Task{}, fmt.Errorf("task row %q names no task", r.OriginID)
	}
	var ex taskExtra
	if err := json.Unmarshal([]byte(r.Extra), &ex); err != nil {
		return Task{}, fmt.Errorf("task %s: unreadable row: %w", tid, err)
	}
	return Task{
		Tid:         tid,
		Kind:        kindOf(ex.ParentDev),
		Goal:        ex.Goal,
		Checks:      ex.Checks,
		ProjectPath: ex.ProjectPath,
		ParentDev:   ex.ParentDev,
		GoalHash:    ex.GoalHash,
		rowID:       r.ID,
		at:          r.Ts,
		done:        r.Closed,
	}, nil
}

// loadTask finds and decodes one task row.
func loadTask(rows []memory.Row, tid string) (Task, error) {
	for _, r := range rows {
		if r.Kind != memory.KindTask {
			continue
		}
		if got, _ := memory.ParseTask(r.OriginID); got == tid {
			return decodeTask(r)
		}
	}
	return Task{}, fmt.Errorf("no task %s in this project", tid)
}

// allTasks decodes every task row in a listing, oldest first, so an eligible
// queue is first-come. One pass: a listing is decoded once, not once per task.
func allTasks(rows []memory.Row) []Task {
	var out []Task
	for _, r := range rows {
		if r.Kind != memory.KindTask {
			continue
		}
		t, err := decodeTask(r)
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].at.Equal(out[j].at) {
			return out[i].Tid < out[j].Tid
		}
		return out[i].at.Before(out[j].at)
	})
	return out
}

// kindOf names a task's kind from the one fact that decides it.
func kindOf(parentDev string) string {
	if parentDev != "" {
		return KindReview
	}
	return KindDev
}

// newTid mints a task identity. It is random rather than sequential because two
// users of the same memstore must never mint the same one.
func newTid() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A weak id would be worse than no task: crypto/rand failing means the
		// process cannot name new work safely, so this is a refusal, not a
		// fallback to the clock.
		panic("coord: no randomness for a task id: " + err.Error())
	}
	return "t-" + hex.EncodeToString(b[:])
}

// hash is the goal's digest, recorded so two tasks claiming one goal are visible
// as such without copying the text twice.
func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

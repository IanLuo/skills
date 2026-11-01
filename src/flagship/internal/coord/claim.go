package coord

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/flagship-dev/flagship/internal/memory"
)

// claimAttempts bounds the retry loop. A retry exists for one reason only: a
// lost race on the claim key means the read that preceded it was stale, and a
// fresher read may show a different fact. Three attempts is enough to lose to a
// real holder and say so.
const claimAttempts = 3

// ClaimInput is one agent asking for one task.
type ClaimInput struct {
	Project string
	Session string
	Task    string
}

// Claim binds a session to one task and holds it.
//
// The protocol, in order, and the order is the safety argument:
//
//  0. Reads only: which task, and is it claimable? A session that names a task that
//     does not exist, or one already closed, is refused here — before the bind — so
//     a typo does not burn an identity. Nothing here decides the claim.
//  1. Put `session:<sid>`. Duplicate means the session is spent: refuse, write
//     nothing else, name the task it is already bound to.
//  2. Read the task's rows. The current round is the highest seen.
//  3. Live by another session: refuse, naming the holder and when it lapses.
//  4. Live by me: already mine, success — the report that follows keeps it alive.
//  5. Otherwise Put `claim:<tid>:<round+1>`. Duplicate means another racer won;
//     retry from 2. A stale read may cost a refusal and a retry; it can never
//     produce two winners, because the decision is memstore's refusal and not a
//     comparison fs makes.
//
// A failure after step 1 leaves the session burned and holding nothing. That is
// safe and accepted: the harness starts a new session.
func Claim(s *memory.Store, in ClaimInput) (Task, error) {
	if err := memory.ValidProject(in.Project); err != nil {
		return Task{}, err
	}
	if err := memory.ValidID("session", in.Session); err != nil {
		return Task{}, err
	}
	if in.Task != "" {
		if err := memory.ValidID("task", in.Task); err != nil {
			return Task{}, err
		}
	}
	scope := memory.Scope(in.Project)

	for attempt := 0; attempt < claimAttempts; attempt++ {
		rows, err := s.Rows(scope)
		if err != nil {
			return Task{}, err
		}
		ix := memory.NewIndex(rows)
		now := s.Now()

		// Which task, and is it claimable at all? These are reads: they decide
		// nothing, and asking them before the bind keeps a mistyped task id from
		// spending a session on work that does not exist.
		tid := in.Task
		bound, isBound := ix.Binding(in.Session)
		if isBound {
			if tid != "" && tid != bound {
				return Task{}, fmt.Errorf("session %s is bound to task %s; it cannot touch %s", in.Session, bound, tid)
			}
			tid = bound
		} else if tid == "" {
			t, ok := eligible(rows, ix, now)
			if !ok {
				return Task{}, fmt.Errorf("no eligible task in %s: every task there is held or closed", scope)
			}
			tid = t.Tid
		}
		task, err := loadTask(rows, tid)
		if err != nil {
			return Task{}, err
		}
		if task.done {
			return Task{}, fmt.Errorf("task %s is closed; re-work is a new task", tid)
		}

		// Step 1 — the bind. A session is spent by construction: this row's key is
		// unique inside memstore's transaction, so a second task for one identity
		// is refused even under a race, with no check for fs to forget.
		if !isBound {
			if err := s.PutBind(scope, in.Session, tid); err != nil {
				if errors.Is(err, memory.ErrDuplicate) {
					other, _ := ix.Binding(in.Session)
					return Task{}, fmt.Errorf("session %s is already spent on task %s; one identity holds one task", in.Session, other)
				}
				return Task{}, err
			}
			// The bind is a write, and a liveness answer is made of rows: re-read
			// rather than reason about a listing that predates it.
			if rows, err = s.Rows(scope); err != nil {
				return Task{}, err
			}
			ix = memory.NewIndex(rows)
		}

		// Steps 2–4 — who holds it, and is that holder still writing?
		round := 0
		if held, ok := ix.Claim(tid); ok {
			round = held.Round
			if held.Live(now) {
				if held.Session == in.Session {
					task.Session, task.Round = in.Session, held.Round
					return task, nil
				}
				return Task{}, fmt.Errorf("task %s is held by session %s until %s",
					tid, held.Session, held.Expires().UTC().Format(time.RFC3339))
			}
		}

		// Step 5 — the arbiter.
		if err := s.PutClaim(scope, tid, in.Session, round+1); err != nil {
			if errors.Is(err, memory.ErrDuplicate) {
				continue
			}
			return Task{}, err
		}
		task.Session, task.Round = in.Session, round+1
		return task, nil
	}
	return Task{}, fmt.Errorf("task %s: lost the claim three times to other sessions; nothing was held", in.Task)
}

// eligible picks the oldest open task with no live claim. Selection is a read and
// carries no authority: whatever it picks, the claim that follows is what decides,
// and a racer that picked the same task is refused by the claim key.
func eligible(rows []memory.Row, ix *memory.Index, now time.Time) (Task, bool) {
	for _, t := range allTasks(rows) {
		if t.done {
			continue
		}
		if held, ok := ix.Claim(t.Tid); ok && held.Live(now) {
			continue
		}
		return t, true
	}
	return Task{}, false
}

// hold refuses unless session sid is bound to tid and still holds it. Every
// task-affecting agent verb runs this before it writes, so a session bound to A
// cannot write to B, and a session whose lease lapsed cannot write at all.
func hold(ix *memory.Index, sid, tid string, now time.Time) error {
	bound, ok := ix.Binding(sid)
	if !ok {
		return fmt.Errorf("session %s holds no task; claim one first", sid)
	}
	if bound != tid {
		return fmt.Errorf("session %s is bound to task %s; it cannot write to %s", sid, bound, tid)
	}
	held, ok := ix.Claim(tid)
	if !ok {
		return fmt.Errorf("task %s has no claim; nothing holds it", tid)
	}
	if held.Session != sid {
		return fmt.Errorf("task %s is held by session %s", tid, held.Session)
	}
	if !held.Live(now) {
		return fmt.Errorf("session %s's lease on task %s expired at %s; it may no longer write",
			sid, tid, held.Expires().UTC().Format(time.RFC3339))
	}
	return nil
}

// ReportInput is one progress report from the session that holds the task —
// including a reviewer recording the result of a check it ran itself.
type ReportInput struct {
	Project string
	Session string
	Task    string
	State   string
	Role    string
	Command string
	Exit    *int
	Digest  string
	Note    string
}

// The states a report may carry, and the roles that may report. Both are closed
// sets: a typo is refused rather than stored as a state no reader understands.
var (
	states = map[string]bool{
		"progress": true, // work is moving
		"dev-done": true, // the development work is finished; the task is not complete
		"pass":     true, // a check was run and passed
		"fail":     true, // a check was run and failed
		"blocked":  true, // something outside the task stops it
	}
	roles = map[string]bool{
		"dev":     true, // the session working the task
		"review":  true, // an independent reviewer's run of a check
		"runtime": true, // a harness observing process lifecycle
	}
)

// eventExtra is a report row's extra: the six fields the design names.
type eventExtra struct {
	State   string `json:"state"`
	Role    string `json:"role"`
	Command string `json:"command,omitempty"`
	Exit    *int   `json:"exit,omitempty"`
	Digest  string `json:"digest,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Evidence is one recorded report as a reader sees it: what happened, who said
// so, when, and how stale that is.
type Evidence struct {
	State   string     `json:"state"`
	Role    string     `json:"role"`
	Command string     `json:"command,omitempty"`
	Exit    *int       `json:"exit,omitempty"`
	Digest  string     `json:"digest,omitempty"`
	Note    string     `json:"note,omitempty"`
	Session string     `json:"session"`
	At      *time.Time `json:"at,omitempty"`
	Age     *float64   `json:"age_seconds,omitempty"`

	// index is the row's per-session report number. It orders two reports that
	// share a timestamp, so "the newest run of a check" is a fact and not an
	// accident of sort order.
	index int
}

// Report appends one report for the session that holds the task. The write is
// also the keepalive: there is no renew verb and no heartbeat row, so a session
// that is working is a session that is writing.
func Report(s *memory.Store, in ReportInput) (Evidence, error) {
	if err := memory.ValidProject(in.Project); err != nil {
		return Evidence{}, err
	}
	if err := memory.ValidID("session", in.Session); err != nil {
		return Evidence{}, err
	}
	if err := memory.ValidID("task", in.Task); err != nil {
		return Evidence{}, err
	}
	if !states[in.State] {
		return Evidence{}, fmt.Errorf("unknown state %q: one of progress, dev-done, pass, fail, blocked", in.State)
	}
	if in.Role == "" {
		in.Role = "dev"
	}
	if !roles[in.Role] {
		return Evidence{}, fmt.Errorf("unknown role %q: one of dev, review, runtime", in.Role)
	}
	if in.Command != "" && in.Exit == nil {
		return Evidence{}, fmt.Errorf("check %q has no exit code: a check without its result is not evidence", in.Command)
	}
	if in.Command == "" && in.Exit != nil {
		return Evidence{}, fmt.Errorf("an exit code needs the command it came from")
	}
	if in.Exit != nil && (*in.Exit < 0 || *in.Exit > 255) {
		return Evidence{}, fmt.Errorf("exit %d is not a process exit code", *in.Exit)
	}
	if len(in.Command) > maxCommand {
		return Evidence{}, fmt.Errorf("command is %d bytes; the cap is %d", len(in.Command), maxCommand)
	}
	if len(in.Digest) > maxDigest {
		return Evidence{}, fmt.Errorf("digest is %d bytes; the cap is %d", len(in.Digest), maxDigest)
	}
	if len(in.Note) > maxNote {
		return Evidence{}, fmt.Errorf("note is %d bytes; the cap is %d", len(in.Note), maxNote)
	}

	scope := memory.Scope(in.Project)
	rows, err := s.Rows(scope)
	if err != nil {
		return Evidence{}, err
	}
	now := s.Now()
	ix := memory.NewIndex(rows)
	if err := hold(ix, in.Session, in.Task, now); err != nil {
		return Evidence{}, err
	}
	extra, err := json.Marshal(eventExtra{
		State: in.State, Role: in.Role, Command: in.Command,
		Exit: in.Exit, Digest: in.Digest, Note: in.Note,
	})
	if err != nil {
		return Evidence{}, err
	}
	text := in.State
	if in.Command != "" {
		text = fmt.Sprintf("%s: %s (exit %d)", in.State, in.Command, *in.Exit)
	} else if in.Note != "" {
		text = in.State + ": " + in.Note
	}
	if err := s.PutEvent(scope, in.Task, in.Session, ix.MaxEvent(in.Task, in.Session)+1, text, string(extra)); err != nil {
		if errors.Is(err, memory.ErrDuplicate) {
			return Evidence{}, fmt.Errorf("this report was already recorded; re-run the command rather than repeating it")
		}
		return Evidence{}, err
	}
	at := now
	return Evidence{
		State: in.State, Role: in.Role, Command: in.Command, Exit: in.Exit,
		Digest: in.Digest, Note: in.Note, Session: in.Session, At: &at,
	}, nil
}

// eventsFor decodes every report on one task, newest first, with each one's age
// against now. Bounded: a listing carries at most maxEvidence of them.
func eventsFor(ix *memory.Index, tid string, now time.Time) []Evidence {
	var out []Evidence
	for _, r := range ix.Events(tid) {
		var ex eventExtra
		if err := json.Unmarshal([]byte(r.Extra), &ex); err != nil {
			continue
		}
		sid, _ := memory.ParseSession(r.OriginID)
		idx, _ := memory.ParseEventIndex(r.OriginID)
		at := r.Ts
		age := now.Sub(at).Seconds()
		out = append(out, Evidence{
			State: ex.State, Role: ex.Role, Command: ex.Command, Exit: ex.Exit,
			Digest: ex.Digest, Note: ex.Note, Session: sid, At: &at, Age: &age,
			index: idx,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(*out[j].At) {
			return out[i].At.After(*out[j].At)
		}
		if out[i].index != out[j].index {
			return out[i].index > out[j].index
		}
		return out[i].Session < out[j].Session
	})
	if len(out) > maxEvidence {
		out = out[:maxEvidence]
	}
	return out
}

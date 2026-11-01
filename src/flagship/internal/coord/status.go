package coord

import (
	"fmt"
	"sort"
	"time"

	"github.com/flagship-dev/flagship/internal/memory"
)

// maxEvidence bounds the reports a status listing carries per task. Status is a
// summary; the full set stays in memstore for a reader that asks for it.
const maxEvidence = 20

// StatusInput is one status question: a project's work, an agent's own task, or
// everything fs is coordinating.
type StatusInput struct {
	Project string
	Session string
}

// Observed is one status as its source reported it, with how stale it is.
//
// A stale observation is reported as unknown. Expiry never reads as live, idle or
// done: an agent that stopped writing is an agent whose state nobody knows, and
// saying otherwise invents a fact the evidence does not carry.
type Observed struct {
	State   string     `json:"state"`
	Session string     `json:"session,omitempty"`
	Role    string     `json:"role,omitempty"`
	Source  string     `json:"source"`
	At      *time.Time `json:"at,omitempty"`
	Age     *float64   `json:"age_seconds,omitempty"`
	Fresh   bool       `json:"fresh"`
}

// TaskStatus is one task with everything a reader needs to act on it.
type TaskStatus struct {
	Task         string     `json:"task"`
	Kind         string     `json:"kind"`
	State        string     `json:"state"`
	Goal         string     `json:"goal"`
	Checks       []string   `json:"checks"`
	ProjectPath  string     `json:"project_path,omitempty"`
	ParentDev    string     `json:"parent_dev,omitempty"`
	Session      string     `json:"session,omitempty"`
	Round        int        `json:"round,omitempty"`
	LeaseExpires *time.Time `json:"lease_expires,omitempty"`
	LeaseLive    bool       `json:"lease_live"`
	Agent        Observed   `json:"agent"`
	Runtime      Observed   `json:"runtime"`
	Evidence     []Evidence `json:"evidence,omitempty"`
}

// ProjectStatus is one project's tasks.
type ProjectStatus struct {
	Project string       `json:"project"`
	Tasks   []TaskStatus `json:"tasks"`
}

// StatusReport is the answer to a status question: one entry per project in view.
type StatusReport struct {
	Projects []ProjectStatus `json:"projects"`
}

// Status derives what fs knows from rows alone. Nothing here is cached and
// nothing is persisted: a longer history costs read latency, not a store to
// repair.
//
// Every read is one fs-scoped listing plus an in-process filter. A session's view
// is limited to what its binding authorizes; the aggregate view across projects
// is the user's.
func Status(s *memory.Store, in StatusInput) (StatusReport, error) {
	if in.Session != "" {
		if err := memory.ValidID("session", in.Session); err != nil {
			return StatusReport{}, err
		}
	}
	if in.Project != "" {
		if err := memory.ValidProject(in.Project); err != nil {
			return StatusReport{}, err
		}
	}
	rows, err := s.AllRows()
	if err != nil {
		return StatusReport{}, err
	}
	ix := memory.NewIndex(rows)
	now := s.Now()

	// A session's authority comes from its binding, and the binding is a row in
	// exactly one scope. Locate it first: a bound session reads one task, an
	// unbound one may read the project it names and nothing more.
	scopes := map[string]bool{}
	var onlyTask string
	if in.Session != "" {
		bound, isBound := ix.Binding(in.Session)
		if isBound {
			if in.Project != "" && memory.Scope(in.Project) != scopeOf(rows, in.Session) {
				return StatusReport{}, fmt.Errorf("session %s is bound in %s, not %s: an agent reads only the project its binding names",
					in.Session, projectOf(rows, in.Session), in.Project)
			}
			scope := scopeOf(rows, in.Session)
			if scope == "" {
				return StatusReport{}, fmt.Errorf("session %s is bound to task %s, which has no row in view", in.Session, bound)
			}
			scopes[scope] = true
			onlyTask = bound
		} else {
			// Unbound: no authority beyond discovery of the named project, and no
			// aggregate view at all.
			if in.Project == "" {
				return StatusReport{}, fmt.Errorf("session %s holds no task: name --project to see what is available", in.Session)
			}
			scopes[memory.Scope(in.Project)] = true
		}
	} else if in.Project != "" {
		scopes[memory.Scope(in.Project)] = true
	} else {
		for _, r := range rows {
			scopes[r.Scope] = true
		}
	}

	byScope := map[string][]memory.Row{}
	for _, r := range rows {
		if !scopes[r.Scope] {
			continue
		}
		byScope[r.Scope] = append(byScope[r.Scope], r)
	}

	out := StatusReport{Projects: []ProjectStatus{}}
	for _, scope := range sortedKeys(scopes) {
		project, ok := memory.Project(scope)
		if !ok {
			continue
		}
		entry := ProjectStatus{Project: project, Tasks: []TaskStatus{}}
		for _, task := range allTasks(byScope[scope]) {
			if onlyTask != "" && task.Tid != onlyTask {
				continue
			}
			entry.Tasks = append(entry.Tasks, statusOf(task, ix, now))
		}
		out.Projects = append(out.Projects, entry)
	}
	return out, nil
}

// statusOf derives one task's state, who holds it, what was reported about it and
// how stale each of those facts is.
func statusOf(task Task, ix *memory.Index, now time.Time) TaskStatus {
	st := TaskStatus{
		Task:        task.Tid,
		Kind:        task.Kind,
		Goal:        task.Goal,
		Checks:      task.Checks,
		ProjectPath: task.ProjectPath,
		ParentDev:   task.ParentDev,
		State:       "open",
	}
	if task.done {
		st.State = "done"
	}
	// Who holds it, and is that holder still writing?
	if held, ok := ix.Claim(task.Tid); ok {
		expires := held.Expires().UTC()
		st.Session, st.Round, st.LeaseExpires = held.Session, held.Round, &expires
		st.LeaseLive = held.Live(now)
		// An expired lease frees the task for a new session; it never marks work
		// complete, so a lapsed claim reads as open, not as done.
		if !task.done && st.LeaseLive {
			st.State = "claimed"
		}
	}

	events := eventsFor(ix, task.Tid, now)
	st.Agent = observed(events, "dev", "review")
	st.Runtime = observed(events, "runtime")
	// A review's evidence is every run it recorded, plus any verdict it wrote —
	// including a refusal, which carries no command but is the reason the task is
	// still open.
	for _, ev := range events {
		if ev.Role == "review" {
			st.Evidence = append(st.Evidence, ev)
		}
	}
	return st
}

// observed picks the newest report of one kind and says whether it still stands.
// Nothing observed, or observed too long ago, is unknown — never idle, never
// alive, never done.
func observed(events []Evidence, roles ...string) Observed {
	for _, ev := range events {
		want := false
		for _, role := range roles {
			if ev.Role == role {
				want = true
				break
			}
		}
		if !want {
			continue
		}
		fresh := ev.Age != nil && *ev.Age <= memory.DefaultTTL.Seconds()
		out := Observed{
			State: ev.State, Session: ev.Session, Role: ev.Role,
			Source: "fs.event", At: ev.At, Age: ev.Age, Fresh: fresh,
		}
		if !fresh {
			out.State = "unknown"
		}
		return out
	}
	return Observed{State: "unknown", Source: "none"}
}

// scopeOf returns the scope a session's binding row lives in.
func scopeOf(rows []memory.Row, sid string) string {
	for _, r := range rows {
		if r.Kind != memory.KindBind {
			continue
		}
		if got, _ := memory.ParseSession(r.OriginID); got == sid {
			return r.Scope
		}
	}
	return ""
}

// projectOf names the project a session is bound in, for a refusal's wording.
func projectOf(rows []memory.Row, sid string) string {
	project, ok := memory.Project(scopeOf(rows, sid))
	if !ok {
		return "no project"
	}
	return project
}

// sortedKeys orders scopes so two runs of the same query read the same way.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

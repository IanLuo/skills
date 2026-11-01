package coord_test

import (
	"strings"
	"testing"
	"time"

	"github.com/flagship-dev/flagship/internal/coord"
	"github.com/flagship-dev/flagship/internal/memory"
)

// TestStatusReportsStateHolderAndProvenance: a reader can tell what a task is, who
// holds it, and where each status came from and when.
func TestStatusReportsStateHolderAndProvenance(t *testing.T) {
	s, now := open(t)
	task := add(t, s, "p")
	claim(t, s, "p", "s-1", task.Tid)
	report(t, s, coord.ReportInput{Project: "p", Session: "s-1", Task: task.Tid, State: "progress", Note: "half way"})

	st, err := coord.Status(s, coord.StatusInput{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Projects) != 1 || st.Projects[0].Project != "p" || len(st.Projects[0].Tasks) != 1 {
		t.Fatalf("status = %+v, want one project with one task", st)
	}
	got := st.Projects[0].Tasks[0]
	if got.State != "claimed" || got.Session != "s-1" || got.Round != 1 || !got.LeaseLive {
		t.Fatalf("task = %+v, want claimed by s-1 at round 1", got)
	}
	if got.LeaseExpires == nil || !got.LeaseExpires.Equal(now.Add(memory.DefaultTTL)) {
		t.Fatalf("lease_expires = %v, want %v", got.LeaseExpires, now.Add(memory.DefaultTTL))
	}
	if got.Agent.State != "progress" || got.Agent.Source != "fs.event" || !got.Agent.Fresh {
		t.Fatalf("agent = %+v, want the live report from fs.event", got.Agent)
	}
	if got.Agent.At == nil || got.Agent.Age == nil {
		t.Fatalf("agent = %+v, want a timestamp and an age", got.Agent)
	}
	// Nothing observed runtime health, and absence reads as unknown — never alive.
	if got.Runtime.State != "unknown" || got.Runtime.Source != "none" || got.Runtime.Fresh {
		t.Fatalf("runtime = %+v, want unknown from no source", got.Runtime)
	}
}

// TestExpiredHeartbeatIsUnknown is AC4's sharp edge: an agent that stopped writing is
// not idle, not done and not alive. It is unknown, and expiry never advanced the task.
func TestExpiredHeartbeatIsUnknown(t *testing.T) {
	s, now := open(t)
	task := add(t, s, "p")
	claim(t, s, "p", "s-1", task.Tid)
	report(t, s, coord.ReportInput{Project: "p", Session: "s-1", Task: task.Tid, State: "pass", Note: "looks done"})

	*now = now.Add(memory.DefaultTTL + time.Second)
	st, err := coord.Status(s, coord.StatusInput{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	got := st.Projects[0].Tasks[0]
	if got.Agent.State != "unknown" {
		t.Fatalf("an expired heartbeat read as %q; it must be unknown", got.Agent.State)
	}
	if got.Agent.Fresh {
		t.Fatal("an expired heartbeat was reported fresh")
	}
	if got.State != "open" || got.LeaseLive {
		t.Fatalf("task = %+v, want open with a lapsed lease, not done", got)
	}
	if got.Agent.At == nil || got.Agent.Age == nil {
		t.Fatal("the expired observation lost its timestamp; staleness must stay visible")
	}
}

// TestExpiredRuntimeIsUnknown: a harness report that stopped is unknown too.
func TestExpiredRuntimeIsUnknown(t *testing.T) {
	s, now := open(t)
	task := add(t, s, "p")
	claim(t, s, "p", "s-1", task.Tid)
	report(t, s, coord.ReportInput{Project: "p", Session: "s-1", Task: task.Tid, State: "progress"})
	report(t, s, coord.ReportInput{Project: "p", Session: "s-1", Task: task.Tid, State: "progress", Role: "runtime"})

	st, err := coord.Status(s, coord.StatusInput{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Projects[0].Tasks[0].Runtime; got.State != "progress" || !got.Fresh {
		t.Fatalf("runtime = %+v, want the live harness report", got)
	}

	*now = now.Add(memory.DefaultTTL + time.Second)
	st, err = coord.Status(s, coord.StatusInput{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Projects[0].Tasks[0].Runtime; got.State != "unknown" || got.Fresh {
		t.Fatalf("runtime = %+v, want unknown once the harness stops reporting", got)
	}
}

// TestAggregateCrossesProjectsAndASessionDoesNot: the user asks about everything; an
// agent asks about what its binding authorizes, and nothing else.
func TestAggregateCrossesProjectsAndASessionDoesNot(t *testing.T) {
	s, _ := open(t)
	a := add(t, s, "alpha")
	add(t, s, "beta")
	claim(t, s, "alpha", "s-1", a.Tid)

	all, err := coord.Status(s, coord.StatusInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Projects) != 2 || all.Projects[0].Project != "alpha" || all.Projects[1].Project != "beta" {
		t.Fatalf("aggregate = %+v, want alpha and beta", all)
	}

	// An agent bound in alpha sees its own task only.
	own, err := coord.Status(s, coord.StatusInput{Session: "s-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(own.Projects) != 1 || own.Projects[0].Project != "alpha" || len(own.Projects[0].Tasks) != 1 {
		t.Fatalf("a bound session saw %+v, want only its own task in alpha", own)
	}
	if _, err := coord.Status(s, coord.StatusInput{Project: "beta", Session: "s-1"}); err == nil {
		t.Fatal("a session bound in alpha read beta")
	} else if !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("refusal %q does not say where the session is bound", err)
	}

	// An unbound session may look for work in a named project, but not aggregate.
	if _, err := coord.Status(s, coord.StatusInput{Session: "s-new"}); err == nil {
		t.Fatal("an unbound session read every project")
	}
	free, err := coord.Status(s, coord.StatusInput{Session: "s-new", Project: "beta"})
	if err != nil {
		t.Fatalf("unbound discovery of one project: %v", err)
	}
	if len(free.Projects) != 1 || len(free.Projects[0].Tasks) != 1 {
		t.Fatalf("discovery = %+v, want beta's one task", free)
	}
}

// TestStatusShowsDoneAndEvidence: a completed task reads done, and the checks that
// closed it stay visible with their exact commands and exits.
func TestStatusShowsDoneAndEvidence(t *testing.T) {
	s, _ := open(t)
	dev := add(t, s, "p")
	review := addReview(t, s, "p", dev.Tid)
	claim(t, s, "p", "s-dev", dev.Tid)
	claim(t, s, "p", "s-rev", review.Tid)
	runCheck(t, s, "p", "s-rev", review.Tid, dev.Checks[0], 0)
	if _, err := coord.Done(s, coord.DoneInput{Project: "p", Session: "s-rev", Task: review.Tid}); err != nil {
		t.Fatal(err)
	}

	st, err := coord.Status(s, coord.StatusInput{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]coord.TaskStatus{}
	for _, task := range st.Projects[0].Tasks {
		states[task.Task] = task
	}
	if states[dev.Tid].State != "done" || states[review.Tid].State != "done" {
		t.Fatalf("states = %v, want both done", states)
	}
	ev := states[review.Tid].Evidence
	if len(ev) != 1 || ev[0].Command != dev.Checks[0] || ev[0].Exit == nil || *ev[0].Exit != 0 || ev[0].Session != "s-rev" {
		t.Fatalf("evidence = %+v, want the reviewer's run with its command, exit and session", ev)
	}
}

// TestStatusIsStableAcrossRuns: two identical questions get byte-identical answers,
// so a reader can diff them.
func TestStatusIsStableAcrossRuns(t *testing.T) {
	s, _ := open(t)
	add(t, s, "zeta")
	add(t, s, "alpha")
	first, err := coord.Status(s, coord.StatusInput{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := coord.Status(s, coord.StatusInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Projects) != len(second.Projects) {
		t.Fatalf("two runs saw %d and %d projects", len(first.Projects), len(second.Projects))
	}
	for i := range first.Projects {
		if first.Projects[i].Project != second.Projects[i].Project {
			t.Fatalf("project order moved between runs: %q then %q", first.Projects[i].Project, second.Projects[i].Project)
		}
	}
}

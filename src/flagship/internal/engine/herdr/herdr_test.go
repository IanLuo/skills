package herdr

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/engine"
)

// The adapter is the seam's only implementation, so the compile-time check that
// it satisfies the interface is the one thing this file must never lose: a
// method that drifts out of the interface leaves the interface unsatisfiable and
// every caller of herdr.New() broken, which is what this catches at build time.
var _ engine.Engine = (*Adapter)(nil)

// recordingRunner stands in for the herdr binary: it records the argv the
// adapter built and answers with whatever the test says herdr would print.
// Building the argv and parsing the reply are exactly the two halves of the
// adapter that no dispatch-level test can see, so they are tested here.
type recordingRunner struct {
	calls []string
	run   func(args ...string) (string, error)
}

func (r *recordingRunner) Run(args ...string) (string, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	if r.run == nil {
		return "", nil
	}
	return r.run(args...)
}

// answers wires a runner that replies per subcommand, keyed `herdr <verb>
// <noun>` — the two words that select a canned answer, which is how the real
// CLI's own grammar separates its subcommands.
func answers(t *testing.T, replies map[string]string) *recordingRunner {
	t.Helper()
	return &recordingRunner{run: func(args ...string) (string, error) {
		key := strings.Join(args[:2], " ")
		out, ok := replies[key]
		if !ok {
			t.Fatalf("adapter ran `herdr %s`, which this test did not script", strings.Join(args, " "))
		}
		return out, nil
	}}
}

// herdrRefusal is the JSON envelope herdr writes when it refuses, so the
// adapter's error mapping is driven by the bytes herdr actually emits and not
// by a constructed Go value.
func herdrRefusal(code, message string) (string, error) {
	return "", &HerdrError{Code: code, Message: message}
}

func TestSplitPaneBuildsTheSplitAndReadsTheIDsBack(t *testing.T) {
	r := &recordingRunner{run: func(args ...string) (string, error) {
		return `{"result":{"pane":{"pane_id":"w1:p9","tab_id":"w1:t9"}}}`, nil
	}}

	pane, tab, err := NewAdapter(r).SplitPane("/tmp/project")
	if err != nil {
		t.Fatalf("SplitPane: %v", err)
	}
	if pane != "w1:p9" || tab != "w1:t9" {
		t.Errorf("SplitPane = (%q, %q), want the ids herdr reported", pane, tab)
	}
	want := "pane split --current --direction right --no-focus --cwd /tmp/project"
	if len(r.calls) != 1 || r.calls[0] != want {
		t.Errorf("argv = %v, want [%s]", r.calls, want)
	}
}

// A tab id is kept when herdr reports one and its absence must not fail a
// delivery: nothing is done with it beyond naming where the pane sits.
func TestSplitPaneTakesAPaneWithoutATab(t *testing.T) {
	r := answers(t, map[string]string{
		"pane split": `{"result":{"pane":{"pane_id":"w1:p9"}}}`,
	})

	pane, tab, err := NewAdapter(r).SplitPane("/tmp")
	if err != nil {
		t.Fatalf("SplitPane: %v", err)
	}
	if pane != "w1:p9" || tab != "" {
		t.Errorf("SplitPane = (%q, %q), want the pane and no tab", pane, tab)
	}
}

// A reply the adapter cannot read is refused rather than returning an empty pane
// id a delivery would then record as the address of its worker.
func TestSplitPaneRefusesAResponseThatNamesNoPane(t *testing.T) {
	for name, reply := range map[string]string{
		"no pane id": `{"result":{"pane":{"tab_id":"w1:t9"}}}`,
		"not json":   `herdr: something went wrong`,
	} {
		t.Run(name, func(t *testing.T) {
			r := answers(t, map[string]string{"pane split": reply})
			if _, _, err := NewAdapter(r).SplitPane("/tmp"); err == nil {
				t.Error("SplitPane = nil error, want a refusal")
			}
		})
	}
}

// The separator goes last, after every option, because herdr reads a word before
// `--pane` as an agent argument and leaves the pane unset; and it is emitted
// only when there are arguments, because a bare `--` is a different command from
// the same line without one. This is the seam's single emitter of the start
// command, so its grammar is asserted here and nowhere else.
func TestAgentStartArgsPutsTheSeparatorLastOnlyWhenThereAreArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "no arguments",
			want: []string{"agent", "start", "dispatch-dev-task", "--kind", "claude", "--pane", "w1:p9"},
		},
		{
			name: "arguments across the separator",
			args: []string{"--permission-mode", "auto"},
			want: []string{
				"agent", "start", "dispatch-dev-task", "--kind", "claude", "--pane", "w1:p9",
				"--", "--permission-mode", "auto",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := agentStartArgs("dispatch-dev-task", "claude", "w1:p9", tc.args)
			if !slices.Equal(got, tc.want) {
				t.Errorf("agentStartArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStartAgentNamesTheAgentKindAndPane(t *testing.T) {
	r := answers(t, map[string]string{"agent start": `{}`})

	err := NewAdapter(r).StartAgent("w1:p9", "dispatch-dev-task", "claude", []string{"--permission-mode", "auto"})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	want := "agent start dispatch-dev-task --kind claude --pane w1:p9 -- --permission-mode auto"
	if len(r.calls) != 1 || r.calls[0] != want {
		t.Errorf("argv = %v, want [%s]", r.calls, want)
	}
}

// A pane split a moment ago is not yet a shell, and herdr says so with its own
// code. The adapter is where that code stops being a string: dispatch's retry
// keys on the sentinel, so a second adapter never has to spell agent_pane_busy.
func TestStartAgentMapsAPaneThatIsNotYetAShellToErrPaneBusy(t *testing.T) {
	out, refusal := herdrRefusal("agent_pane_busy", "agent target pane w1:p9 is not an available shell")
	r := &recordingRunner{run: func(args ...string) (string, error) { return out, refusal }}

	err := NewAdapter(r).StartAgent("w1:p9", "dispatch-dev-task", "claude", nil)
	if !errors.Is(err, engine.ErrPaneBusy) {
		t.Errorf("err = %v, want engine.ErrPaneBusy", err)
	}
	if !strings.Contains(err.Error(), "not an available shell") {
		t.Errorf("err = %v, want herdr's own message carried through", err)
	}
}

// A name herdr will never accept is not a pane that is not ready yet, so it must
// not wear the sentinel the retry looks for.
func TestStartAgentLeavesARefusalThatIsNotTheRaceAlone(t *testing.T) {
	out, refusal := herdrRefusal("invalid_agent_name", "name must match [a-z][a-z0-9_-]{0,31}")
	r := &recordingRunner{run: func(args ...string) (string, error) { return out, refusal }}

	err := NewAdapter(r).StartAgent("w1:p9", "dispatch-dev-task-", "claude", nil)
	if err == nil || errors.Is(err, engine.ErrPaneBusy) {
		t.Errorf("err = %v, want herdr's own refusal and not ErrPaneBusy", err)
	}
}

func TestPromptSubmitsToThePane(t *testing.T) {
	r := answers(t, map[string]string{"agent prompt": `{}`})

	if err := NewAdapter(r).Prompt("w1:p9", "the brief"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	want := "agent prompt w1:p9 the brief"
	if len(r.calls) != 1 || r.calls[0] != want {
		t.Errorf("argv = %v, want [%s]", r.calls, want)
	}
}

// herdr reports every agent with the pane hosting it and the state it is in, and
// both the state map and the pane lookup are built from the one parse — so the
// two can never disagree about what herdr said.
const agentsReply = `{"result":{"agents":[
	{"name":"dispatch-dev-task","pane_id":"w1:p9","agent_status":"running"},
	{"name":"","pane_id":"w2:p1","agent_status":"idle"},
	{"name":"cap-skills","pane_id":"w1:p1","agent_status":"blocked"}
]}}`

func TestAgentsReportsNamedAgentsByState(t *testing.T) {
	r := answers(t, map[string]string{"agent list": agentsReply})

	states, err := NewAdapter(r).Agents()
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	want := map[string]string{"dispatch-dev-task": "running", "cap-skills": "blocked"}
	if len(states) != len(want) {
		t.Fatalf("Agents = %v, want %v: an unnamed agent cannot be addressed by a record", states, want)
	}
	for name, state := range want {
		if states[name] != state {
			t.Errorf("Agents[%q] = %q, want %q", name, states[name], state)
		}
	}
}

func TestAgentForPaneNamesTheAgentInThatPane(t *testing.T) {
	r := answers(t, map[string]string{"agent list": agentsReply})
	a := NewAdapter(r)

	if name, err := a.AgentForPane("w1:p9"); err != nil || name != "dispatch-dev-task" {
		t.Errorf("AgentForPane(w1:p9) = (%q, %v), want the agent herdr hosts there", name, err)
	}
	// A pane hosting an agent herdr did not name is a pane with no name to
	// return, not an error: the caller records "" rather than inventing one.
	if name, err := a.AgentForPane("w2:p1"); err != nil || name != "" {
		t.Errorf("AgentForPane(w2:p1) = (%q, %v), want no name and no error", name, err)
	}
	if name, err := a.AgentForPane("w9:p9"); err != nil || name != "" {
		t.Errorf("AgentForPane(w9:p9) = (%q, %v), want no name and no error", name, err)
	}
}

func TestCreateWorktreeReadsTheWorkspaceTheCheckoutAndTheBranch(t *testing.T) {
	r := answers(t, map[string]string{
		"worktree create": `{"result":{"workspace":{"workspace_id":"w7","worktree":{"checkout_path":"/tmp/wt"}},` +
			`"worktree":{"branch":"herdr/w7"}}}`,
	})

	wsID, checkout, branch, err := NewAdapter(r).CreateWorktree("/tmp/project")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if wsID != "w7" || checkout != "/tmp/wt" || branch != "herdr/w7" {
		t.Errorf("CreateWorktree = (%q, %q, %q), want herdr's own three answers", wsID, checkout, branch)
	}
	want := "worktree create --cwd /tmp/project --no-focus"
	if len(r.calls) != 1 || r.calls[0] != want {
		t.Errorf("argv = %v, want [%s]", r.calls, want)
	}
}

// A detached checkout reports no branch, and that is an empty string rather than
// a guess: the branch postcondition stands down on it.
func TestCreateWorktreeTakesADetachedCheckout(t *testing.T) {
	r := answers(t, map[string]string{
		"worktree create": `{"result":{"workspace":{"workspace_id":"w7","worktree":{"checkout_path":"/tmp/wt"}}}}`,
	})

	_, _, branch, err := NewAdapter(r).CreateWorktree("/tmp/project")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if branch != "" {
		t.Errorf("branch = %q, want none", branch)
	}
}

func TestCreateWorktreeRefusesAResponseThatNamesNoWorkspace(t *testing.T) {
	r := answers(t, map[string]string{"worktree create": `{"result":{"workspace":{}}}`})
	if _, _, _, err := NewAdapter(r).CreateWorktree("/tmp"); err == nil {
		t.Error("CreateWorktree = nil error, want a refusal")
	}
}

func TestWorktreeCheckoutReadsTheCheckoutPath(t *testing.T) {
	r := answers(t, map[string]string{
		"workspace get": `{"result":{"workspace":{"worktree":{"checkout_path":"/tmp/wt"}}}}`,
	})

	checkout, err := NewAdapter(r).WorktreeCheckout("w7")
	if err != nil {
		t.Fatalf("WorktreeCheckout: %v", err)
	}
	if checkout != "/tmp/wt" {
		t.Errorf("checkout = %q, want the workspace's own", checkout)
	}
}

// A workspace herdr does not know and one that is not a worktree are the same
// answer to every gate that asks: the tree cannot be resolved, so refuse rather
// than run checks against one the dispatch never touched.
func TestWorktreeCheckoutMapsEveryUnresolvableWorkspaceToErrNoWorktree(t *testing.T) {
	cases := map[string]func(args ...string) (string, error){
		"herdr does not know it": func(args ...string) (string, error) {
			return herdrRefusal("workspace_not_found", "no such workspace")
		},
		"it is not a worktree": func(args ...string) (string, error) {
			return `{"result":{"workspace":{}}}`, nil
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			r := &recordingRunner{run: run}
			if _, err := NewAdapter(r).WorktreeCheckout("w7"); !errors.Is(err, engine.ErrNoWorktree) {
				t.Errorf("err = %v, want engine.ErrNoWorktree", err)
			}
		})
	}
}

// herdr reports a pane's cwd as the process that owns it sees it and the
// checkout as the workspace sees it, and one directory has more than one
// spelling. A worktree dispatch must find its own pane anyway: a string compare
// refuses the worker the very worktree it was dispatched into, and the error
// blames the workspace for having no pane at the checkout it is sitting in.
func TestWorktreeRootPaneMatchesACheckoutReachedByAnotherSpelling(t *testing.T) {
	real := t.TempDir()
	linked := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(real, linked); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	r := answers(t, map[string]string{
		"workspace get": `{"result":{"workspace":{"worktree":{"checkout_path":"` + linked + `"}}}}`,
		"pane list":     `{"result":{"panes":[{"pane_id":"w9:p1","tab_id":"w9:t1","cwd":"` + real + `","foreground_cwd":""}]}}`,
	})

	pane, tab, checkout, err := NewAdapter(r).WorktreeRootPane("w9")
	if err != nil {
		t.Fatalf("WorktreeRootPane: %v", err)
	}
	if pane != "w9:p1" || tab != "w9:t1" {
		t.Errorf("pane=%q tab=%q, want the pane that sits at the checkout", pane, tab)
	}
	if checkout != linked {
		t.Errorf("checkout = %q, want the workspace's own %q", checkout, linked)
	}
}

// A workspace with no pane at its checkout is a refusal, not a fallback: a
// worker started anywhere else lands in the main checkout while the record
// claims isolation.
func TestWorktreeRootPaneRefusesAWorkspaceWithNoPaneAtTheCheckout(t *testing.T) {
	r := answers(t, map[string]string{
		"workspace get": `{"result":{"workspace":{"worktree":{"checkout_path":"/tmp/wt"}}}}`,
		"pane list":     `{"result":{"panes":[{"pane_id":"w9:p1","tab_id":"w9:t1","cwd":"/tmp/elsewhere","foreground_cwd":""}]}}`,
	})

	_, _, _, err := NewAdapter(r).WorktreeRootPane("w9")
	if !errors.Is(err, engine.ErrNoRootPane) {
		t.Errorf("err = %v, want engine.ErrNoRootPane", err)
	}
}

// sameFile is the question the worktree pane lookup asks, and these are its
// edges. Answering "same" for two paths it cannot both stat would start a worker
// at a checkout that is not there; answering "different" for one directory is
// the bug the lookup was fixed for. Both wrong answers are refusals of one kind
// or another, so the edges are worth stating exactly.
func TestSameFileAnswersOnlyForPathsItCanBothSee(t *testing.T) {
	dir := t.TempDir()

	if !sameFile(dir, dir) {
		t.Error("a directory is not reported as the same as itself")
	}
	if sameFile(dir, filepath.Join(dir, "absent")) {
		t.Error("a path that does not exist is reported as the same as one that does")
	}
	if sameFile("", dir) || sameFile(dir, "") {
		t.Error("an empty path is reported as the same as a directory")
	}
	if sameFile(dir, t.TempDir()) {
		t.Error("two different directories are reported as the same")
	}

	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sameFile(dir, file) {
		t.Error("a file in a directory is reported as the same as the directory")
	}
}

// Closing a pane that is already gone is a success for the caller — the
// dispatch is simply already torn down — so the adapter is where that code
// becomes the sentinel the teardown treats as done.
func TestClosePaneMapsPaneNotFoundToErrPaneGone(t *testing.T) {
	out, refusal := herdrRefusal("pane_not_found", "no such pane")
	r := &recordingRunner{run: func(args ...string) (string, error) { return out, refusal }}

	if err := NewAdapter(r).ClosePane("w1:p9"); !errors.Is(err, engine.ErrPaneGone) {
		t.Errorf("err = %v, want engine.ErrPaneGone", err)
	}
}

func TestClosePanePassesAnotherRefusalThrough(t *testing.T) {
	out, refusal := herdrRefusal("server_unreachable", "herdr is not running")
	r := &recordingRunner{run: func(args ...string) (string, error) { return out, refusal }}

	err := NewAdapter(r).ClosePane("w1:p9")
	if err == nil || errors.Is(err, engine.ErrPaneGone) {
		t.Errorf("err = %v, want herdr's own failure and not ErrPaneGone", err)
	}
}

// PaneExists is the read-back a close does, and pane_not_found is the one error
// that answers the question. Any other failure leaves it unanswered — which is
// not the same as the pane being absent — so it is reported as such.
func TestPaneExistsAnswersTheQuestionOrReportsItUnanswered(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		r := answers(t, map[string]string{"pane get": `{"result":{"pane":{"pane_id":"w1:p9"}}}`})
		exists, err := NewAdapter(r).PaneExists("w1:p9")
		if err != nil || !exists {
			t.Errorf("PaneExists = (%v, %v), want true and no error", exists, err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		out, refusal := herdrRefusal("pane_not_found", "no such pane")
		r := &recordingRunner{run: func(args ...string) (string, error) { return out, refusal }}
		exists, err := NewAdapter(r).PaneExists("w1:p9")
		if err != nil || exists {
			t.Errorf("PaneExists = (%v, %v), want false and no error", exists, err)
		}
	})

	t.Run("unanswered", func(t *testing.T) {
		out, refusal := herdrRefusal("server_unreachable", "herdr is not running")
		r := &recordingRunner{run: func(args ...string) (string, error) { return out, refusal }}
		if _, err := NewAdapter(r).PaneExists("w1:p9"); err == nil {
			t.Error("PaneExists = nil error, want the failure reported")
		}
	})
}

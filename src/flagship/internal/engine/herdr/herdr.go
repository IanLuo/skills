// Package herdr implements the engine.Engine interface via herdr CLI.
// It shells out to herdr commands: pane split, agent start, agent prompt,
// agent read, pane close. (SYSTEM-DESIGN R3, ARCHITECTURE decision 4)
//
// This package is the adapter layer: herdr's vocabulary stops here. The argv it
// builds, the JSON it parses, and the error codes it maps onto engine's
// sentinels (pane_not_found, workspace_not_found, agent_pane_busy) are all its
// own, so internal/dispatch names none of them.
package herdr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/flagship-dev/flagship/internal/engine"
)

// Runner abstracts command execution for testing.
type Runner interface {
	Run(args ...string) (string, error)
}

// CLIRunner executes herdr CLI commands.
type CLIRunner struct{}

// Run executes `herdr <args>` and returns stdout. herdr reports failures as
// JSON on stderr ({"error":{"code":...}}) and still exits non-zero, so the code
// is recovered from the output rather than from the exit status alone. Stdout
// is checked too: the envelope's destination is not contractual.
func (c *CLIRunner) Run(args ...string) (string, error) {
	cmd := exec.Command("herdr", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if e := parseHerdrError(stderr.Bytes()); e != nil {
			return "", e
		}
		if e := parseHerdrError(stdout.Bytes()); e != nil {
			return "", e
		}
		return "", fmt.Errorf("herdr %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// HerdrError is a failure the herdr CLI reported with a machine-readable code
// in its JSON error envelope.
type HerdrError struct {
	Code    string
	Message string
}

func (e *HerdrError) Error() string {
	if e.Message == "" {
		return "herdr: " + e.Code
	}
	return fmt.Sprintf("herdr %s: %s", e.Code, e.Message)
}

// herdrErrorCode returns the error code herdr reported, or "" for a failure
// that never reached herdr (a missing binary, a dead server).
func herdrErrorCode(err error) string {
	var e *HerdrError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func parseHerdrError(out []byte) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(out, &envelope) != nil || envelope.Error.Code == "" {
		return nil
	}
	return &HerdrError{Code: envelope.Error.Code, Message: envelope.Error.Message}
}

// Adapter implements engine.Engine via herdr CLI.
type Adapter struct {
	runner Runner
}

// NewAdapter creates a herdr adapter with the given command runner.
func NewAdapter(runner Runner) *Adapter {
	return &Adapter{runner: runner}
}

// New creates a herdr adapter using the real CLI runner.
func New() *Adapter {
	return &Adapter{runner: &CLIRunner{}}
}

func (a *Adapter) SplitPane(cwd string) (string, string, error) {
	out, err := a.runner.Run("pane", "split", "--current", "--direction", "right", "--no-focus", "--cwd", cwd)
	if err != nil {
		return "", "", err
	}

	// tab_id is kept when herdr reports it — it says which of the cap's tabs the
	// worker shares — but it is not required: nothing is done with it beyond
	// naming where the pane sits, so its absence must not fail a delivery.
	var resp struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
				TabID  string `json:"tab_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", "", fmt.Errorf("herdr pane split: parse response: %w", err)
	}
	if resp.Result.Pane.PaneID == "" {
		return "", "", errors.New("herdr pane split: response carried no pane id")
	}
	return resp.Result.Pane.PaneID, resp.Result.Pane.TabID, nil
}

func (a *Adapter) StartAgent(paneID, name, kind string, args []string) error {
	_, err := a.runner.Run(agentStartArgs(name, kind, paneID, args)...)
	if herdrErrorCode(err) == "agent_pane_busy" {
		return fmt.Errorf("%w: %v", engine.ErrPaneBusy, err)
	}
	return err
}

// agentStartArgs is the argv herdr is given to start one agent, and the only
// emitter of that command: a printed line cannot be held to it, because the
// agent is named after the node the delivery creates and no node exists before
// the delivery runs. nextCommand used to print one anyway; it was retired rather
// than kept in step, since a line started by hand names an agent no delivery
// record can reach.
//
// The `--` separator goes last, after every option, because herdr's grammar is
// `<NAME> --kind <KIND> --pane <ID> [OPTIONS] [-- [AGENT_ARG]...]` — an
// argument placed before `--pane` would be read as an agent argument and leave
// the pane unset. It is emitted only when there are arguments: a bare `--` is a
// different command from the same line without one.
func agentStartArgs(name, kind, paneID string, args []string) []string {
	argv := []string{"agent", "start", name, "--kind", kind, "--pane", paneID}
	if len(args) > 0 {
		argv = append(argv, "--")
		argv = append(argv, args...)
	}
	return argv
}

// WorktreeRootPane finds the pane a worktree dispatch starts its worker in.
//
// herdr exposes no "root pane" flag, so the root pane is identified by where it
// sits: herdr creates a worktree workspace with one pane already at the
// worktree's checkout path, and that pane — not one split off later, and not the
// root pane once someone has cd'd it elsewhere — is the workspace's own. If
// several panes sit at the checkout, the first herdr lists is taken. "Sits at" is
// sameFile and not string equality, because the two paths come from two places
// and one directory has several spellings (see sameFile). A workspace
// with no such pane is engine.ErrNoRootPane rather than a guess, because
// starting the worker anywhere else would be the very bug this avoids.
func (a *Adapter) WorktreeRootPane(wsID string) (string, string, string, error) {
	checkout, err := a.WorktreeCheckout(wsID)
	if err != nil {
		return "", "", "", err
	}

	out, err := a.runner.Run("pane", "list", "--workspace", wsID)
	if err != nil {
		return "", "", "", fmt.Errorf("worktree %s: %w", wsID, err)
	}
	var resp struct {
		Result struct {
			Panes []struct {
				PaneID        string `json:"pane_id"`
				TabID         string `json:"tab_id"`
				CWD           string `json:"cwd"`
				ForegroundCWD string `json:"foreground_cwd"`
			} `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", "", "", fmt.Errorf("herdr pane list --workspace %s: parse response: %w", wsID, err)
	}

	for _, pane := range resp.Result.Panes {
		if sameFile(pane.CWD, checkout) || sameFile(pane.ForegroundCWD, checkout) {
			return pane.PaneID, pane.TabID, checkout, nil
		}
	}
	return "", "", "", fmt.Errorf(
		"%w: workspace %s has %d pane(s) and none sits at the worktree checkout %s",
		engine.ErrNoRootPane, wsID, len(resp.Result.Panes), checkout)
}

// sameFile reports whether two paths name the same directory.
//
// It is os.Stat + os.SameFile — the device-and-inode question — and deliberately
// not a string compare, not filepath.EvalSymlinks, and not any canonical
// spelling. herdr reports a pane's cwd as the process that owns it sees it and
// the checkout as the workspace sees it, and one directory has more than one
// spelling: `/tmp` and `/private/tmp` on macOS, a path that went through `..`, a
// trailing slash, a case-insensitive filesystem, a bind mount. EvalSymlinks
// answers the first two of those and not the last three; SameFile answers all of
// them, because the filesystem has already decided what "the same directory"
// means and this asks it rather than re-deriving it. A canonical form is a list
// of the cases its author thought of, and the one that is missing is the one
// that refuses a worker its own worktree.
//
// Two paths that cannot both be stat'd are the same as nothing. That is the
// safe direction here: a pane at a checkout that is not there is not a pane the
// worker can start in, and the caller refuses — engine.ErrNoRootPane — rather
// than starting the worker somewhere it did not mean.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// CreateWorktree creates a new worktree workspace rooted at cwd and returns its
// workspace id, its checkout path, and the branch the checkout stands on. herdr
// worktree create reports the workspace as .result.workspace.workspace_id, the
// checkout as .result.workspace.worktree.checkout_path, and the branch as
// .result.worktree.branch — the created worktree's own record, beside the
// workspace's.
//
// The branch is read from the same response rather than asked of the checkout
// afterwards: it is the branch herdr just created for this worktree, and asking
// git afterwards would report what the checkout happens to stand on by then —
// the substitution the teardown's branch step was written to stop making.
func (a *Adapter) CreateWorktree(cwd string) (string, string, string, error) {
	out, err := a.runner.Run("worktree", "create", "--cwd", cwd, "--no-focus")
	if err != nil {
		return "", "", "", err
	}

	var resp struct {
		Result struct {
			Workspace struct {
				WorkspaceID string `json:"workspace_id"`
				Worktree    struct {
					CheckoutPath string `json:"checkout_path"`
				} `json:"worktree"`
			} `json:"workspace"`
			Worktree struct {
				Branch string `json:"branch"`
			} `json:"worktree"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", "", "", fmt.Errorf("herdr worktree create: parse response: %w", err)
	}
	if resp.Result.Workspace.WorkspaceID == "" {
		return "", "", "", errors.New("herdr worktree create: response carried no workspace id")
	}
	return resp.Result.Workspace.WorkspaceID, resp.Result.Workspace.Worktree.CheckoutPath, resp.Result.Worktree.Branch, nil
}

// WorktreeCheckout returns the checkout path of the worktree workspace wsID, or
// engine.ErrNoWorktree when herdr has no such workspace or it is not a worktree.
func (a *Adapter) WorktreeCheckout(wsID string) (string, error) {
	out, err := a.runner.Run("workspace", "get", wsID)
	if err != nil {
		if herdrErrorCode(err) == "workspace_not_found" {
			return "", fmt.Errorf("%w: herdr has no workspace %s", engine.ErrNoWorktree, wsID)
		}
		return "", fmt.Errorf("herdr workspace get %s: %w", wsID, err)
	}

	var resp struct {
		Result struct {
			Workspace struct {
				Worktree struct {
					CheckoutPath string `json:"checkout_path"`
				} `json:"worktree"`
			} `json:"workspace"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("herdr workspace get %s: parse response: %w", wsID, err)
	}
	checkout := resp.Result.Workspace.Worktree.CheckoutPath
	if checkout == "" {
		return "", fmt.Errorf("%w: workspace %s is not a worktree", engine.ErrNoWorktree, wsID)
	}
	return checkout, nil
}

func (a *Adapter) Prompt(paneID, text string) error {
	_, err := a.runner.Run("agent", "prompt", paneID, text)
	return err
}

func (a *Adapter) Agents() (map[string]string, error) {
	agents, err := a.listAgents()
	if err != nil {
		return nil, err
	}

	states := make(map[string]string, len(agents))
	for _, a := range agents {
		if a.Name == "" {
			continue // an unnamed agent cannot be addressed by a delivery record
		}
		states[a.Name] = a.State
	}
	return states, nil
}

func (a *Adapter) AgentForPane(paneID string) (string, error) {
	agents, err := a.listAgents()
	if err != nil {
		return "", err
	}
	for _, a := range agents {
		if a.PaneID == paneID && a.Name != "" {
			return a.Name, nil
		}
	}
	return "", nil
}

// herdrAgent is one agent herdr reports: the name it is addressed by, the pane
// hosting it, and the state it is in.
type herdrAgent struct {
	Name   string
	PaneID string
	State  string
}

// listAgents reads herdr's agents. It is the one parse both the state map and
// the pane lookup are built from, so the two can never disagree about what
// herdr said.
func (a *Adapter) listAgents() ([]herdrAgent, error) {
	out, err := a.runner.Run("agent", "list")
	if err != nil {
		return nil, err
	}

	var resp struct {
		Result struct {
			Agents []struct {
				Name   string `json:"name"`
				PaneID string `json:"pane_id"`
				State  string `json:"agent_status"`
			} `json:"agents"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil, fmt.Errorf("herdr agent list: parse response: %w", err)
	}

	agents := make([]herdrAgent, 0, len(resp.Result.Agents))
	for _, a := range resp.Result.Agents {
		agents = append(agents, herdrAgent{Name: a.Name, PaneID: a.PaneID, State: a.State})
	}
	return agents, nil
}

func (a *Adapter) ClosePane(paneID string) error {
	_, err := a.runner.Run("pane", "close", paneID)
	if herdrErrorCode(err) == "pane_not_found" {
		return engine.ErrPaneGone
	}
	return err
}

// PaneExists asks herdr for the pane and reads the answer, rather than trusting
// that a close succeeded. pane_not_found is the one error that answers the
// question — the pane is absent; any other failure leaves it unanswered and is
// reported as such.
func (a *Adapter) PaneExists(paneID string) (bool, error) {
	_, err := a.runner.Run("pane", "get", paneID)
	if err == nil {
		return true, nil
	}
	if herdrErrorCode(err) == "pane_not_found" {
		return false, nil
	}
	return false, fmt.Errorf("herdr pane get %s: %w", paneID, err)
}

// Delivery: handing a prepared brief to a worker and recording the binding.
//
// herdr is the dispatcher the plan resolves — the `engine:` the playbooks
// declare — so shelling out to its CLI is a deliberate coupling, not an
// accident. The HerdrCLI interface exists only so the gates can be exercised
// without a live terminal.
package dispatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/trace"
)

// ErrPaneGone reports that a pane no longer exists. Closing an already-closed
// pane is a success, not a failure: the dispatch is simply already torn down.
var ErrPaneGone = errors.New("pane is already gone")

// ErrNoRootPane reports that a worktree workspace has no pane a worker can be
// started in: none of its panes sits at the worktree's checkout. A worktree
// dispatch is refused rather than falling back to splitting the cap's pane,
// which would run the worker in the main checkout while the record claimed
// isolation.
var ErrNoRootPane = errors.New("worktree workspace has no root pane")

// ErrNoWorktree reports that a worktree workspace cannot be resolved to a
// checkout path: herdr does not know the workspace, or it is not a worktree.
// Every gate that needs the tree refuses on it rather than falling back to the
// project root — running a check against a tree the dispatch never touched, and
// reporting success, is worse than refusing.
var ErrNoWorktree = errors.New("worktree workspace cannot be resolved")

// HerdrCLI is the slice of the herdr CLI the dispatch lifecycle uses: split a
// pane for the worker, start an agent in it, send the brief, read what the
// agents are doing, and tear the pane down again.
//
// A dispatch in the project root gets a sibling pane of the cap's own, not a
// tab. A tab created by id carried no workspace, so a worker could land in
// another session's workspace and herdr has no command to move it back; a split
// is anchored to the cap's pane and so to the cap's workspace. And herdr no
// longer needs the unseen-work signal a separate tab bought:
// pane_agent_status_changed fires per pane regardless of visibility.
//
// A worktree dispatch gets no pane at all: the worktree workspace already has
// one, and splitting the cap's pane would put the worker in the main checkout.
type HerdrCLI interface {
	// SplitPane splits the pane the caller runs in — the cap's own — to the
	// right, without moving focus, and returns the new pane's id along with the
	// tab it landed in. cwd is where the worker starts, which is the project
	// root: the split would inherit the cap's directory otherwise.
	SplitPane(cwd string) (paneID, tabID string, err error)
	// WorktreeRootPane returns the pane a worktree dispatch starts its worker
	// in: the root pane herdr created with the workspace, whose cwd is the
	// worktree's own checkout. It returns that checkout too, because resolving
	// the pane means resolving the path — and the path is what the delivery
	// record must keep: herdr can forget the workspace before close, and then
	// the workspace id names nothing that can be removed. It returns
	// ErrNoWorktree when the workspace cannot be resolved and ErrNoRootPane when
	// it has no pane at the checkout — both naming wsID.
	WorktreeRootPane(wsID string) (paneID, tabID, checkout string, err error)
	// WorktreeCheckout returns the checkout path of the worktree workspace wsID.
	// It is how the cleanup gate finds the tree the dispatch worked in when the
	// record names no path of its own — a record written before the path was
	// recorded. ErrNoWorktree means the workspace could not be resolved, which a
	// gate refuses on rather than guessing.
	WorktreeCheckout(wsID string) (string, error)
	// CreateWorktree creates a new worktree workspace rooted at cwd and returns
	// its workspace id, its checkout path, and the branch herdr put that checkout
	// on. It is the mechanism behind --worktree-new: the workspace's root pane
	// sits at the checkout, which is what the delivery record must keep for the
	// teardown to remove it, and the branch is what it must keep for the close to
	// assert the removal happened. herdr names the branch when --branch is not
	// given, so the create response is the only place this delivery can learn
	// which branch it caused to exist; a detached checkout reports none, and that
	// is an empty string rather than a guess.
	CreateWorktree(cwd string) (workspaceID, checkout, branch string, err error)
	// StartAgent starts an interactive agent named name in an existing pane, of
	// the kind the plan's `agent:` field resolved to and with the arguments its
	// `agent_args:` field named — one argv element per entry, in order.
	StartAgent(paneID, name, kind string, args []string) error
	// Prompt submits text to the agent hosted by paneID.
	Prompt(paneID, text string) error
	// Agents reports every agent herdr knows, by name, with the state herdr
	// reports for it. fs pending reads it to tell a worker that is still going
	// from one that finished unseen or died; an error means herdr could not be
	// asked, which is not the same as no agent being there.
	Agents() (map[string]string, error)
	// AgentForPane returns the name herdr reports for the agent it hosts in
	// paneID, and "" when it names none there. It is how a claim records the
	// agent beside the pane: herdr routes to the pane, and the name is what a
	// person recognises. An error means herdr could not be asked, which is not
	// the same as there being no agent.
	AgentForPane(paneID string) (string, error)
	// ClosePane closes paneID, returning ErrPaneGone if it no longer exists.
	ClosePane(paneID string) error
	// PaneExists reports whether herdr still knows paneID. It is how close-out
	// confirms a pane it closed is really gone: a close that returns nil is a
	// claim, and the error-mapped "already gone" it treats as success is the same
	// mapping that once reported a worktree gone while its checkout stayed on
	// disk. An error means the question could not be answered, which is not the
	// same as the pane being absent.
	PaneExists(paneID string) (bool, error)
}

// Deliver hands the prepared brief to a worker: it finds the pane the worker
// starts in — a new sibling of the cap's own in the project root, or the
// worktree's existing root pane — starts the agent there, creates the worker's
// node in the target project, sends the brief naming that node, then records the
// binding as a delivery-recorded event and marks the cap node active.
//
// The worker's node is created before the brief is sent because the brief has to
// name it — the worker must not invent its own. That ordering is the only one
// that works, so a failure after the node exists rolls it back (see
// rollbackWorker): a failed delivery neither leaves outstanding work behind nor
// claims a delivery that did not happen.
func Deliver(h *command.Handler, hc HerdrCLI, lg *trace.Log, brief *Brief) command.Response {
	// The checkout --worktree-new made, known before the worker's pane is found:
	// a delivery that fails in between has to be able to name the tree it leaves
	// standing, and for --worktree-new this is the only place the path is known
	// before the delivery record exists.
	madePath := ""

	// The branch that checkout stands on, known at the same moment and for the
	// same reason. It is recorded only when this delivery created the worktree:
	// a worktree named with --worktree came from elsewhere, and an empty Branch
	// is what tells the two apart at close, where an exit step has to know
	// whether the branch was this delivery's to remove at all.
	madeBranch := ""

	// --worktree-new creates the worktree rather than naming an existing one:
	// the workspace and its checkout path are recorded on the delivery exactly
	// as --worktree records them, so the existing teardown removes them with no
	// new flags. The creation happens before the worker's pane is found because
	// the worker starts inside the tree.
	if brief.WorktreeNew {
		workspace, checkout, branch, err := hc.CreateWorktree(brief.RootPath)
		if err != nil {
			return deliverErr(h, brief, madePath, fmt.Errorf("create worktree: %w", err))
		}
		brief.Worktree = workspace
		madePath = checkout
		madeBranch = branch
	}

	paneID, tabID, worktreePath, err := workerPane(hc, brief)
	if err != nil {
		return deliverErr(h, brief, madePath, err)
	}
	if worktreePath == "" {
		worktreePath = madePath
	}

	// The worker's node comes first because the agent is named after it: two
	// workers of one task type dispatched at once would otherwise share the name
	// "dispatch-<type>", and herdr could not tell one's pane from the other's.
	workerID, err := createWorkerNode(h, brief)
	if err != nil {
		abandonWorker(hc, brief.Worktree, paneID)
		return deliverErr(h, brief, worktreePath, err)
	}

	agent, err := workerAgent(brief.TaskType, workerID)
	if err != nil {
		abandonWorker(hc, brief.Worktree, paneID)
		rollbackWorker(h, brief.Project, workerID, err.Error())
		return deliverErr(h, brief, worktreePath, err)
	}
	if err := startAgent(hc, paneID, agent, brief.Agent, brief.AgentArgs); err != nil {
		abandonWorker(hc, brief.Worktree, paneID)
		rollbackWorker(h, brief.Project, workerID, fmt.Sprintf("start agent %s: %v", agent, err))
		return deliverErr(h, brief, worktreePath, fmt.Errorf("start agent %s: %w", agent, err))
	}

	if err := hc.Prompt(paneID, renderBrief(brief)); err != nil {
		abandonWorker(hc, brief.Worktree, paneID)
		rollbackWorker(h, brief.Project, workerID, fmt.Sprintf("send brief: %v", err))
		return deliverErr(h, brief, worktreePath, fmt.Errorf("send brief: %w", err))
	}

	// The handover is now on the record, one line per work playbook. A line that
	// cannot be written rolls the delivery back: close refuses a plan whose work
	// playbook was never handed over, and a delivery that cannot be recorded is a
	// delivery that cannot be closed as if it had happened.
	if err := recordLoaded(lg, brief); err != nil {
		abandonWorker(hc, brief.Worktree, paneID)
		rollbackWorker(h, brief.Project, workerID, err.Error())
		return deliverErr(h, brief, worktreePath, err)
	}

	delivery := command.DeliveryRecord{
		PaneID:       paneID,
		TabID:        tabID,
		Agent:        agent,
		Engine:       brief.Engine,
		Project:      brief.Project,
		Node:         workerID,
		Type:         brief.TaskType,
		Worktree:     brief.Worktree,
		WorktreePath: worktreePath,
		Branch:       madeBranch,
	}
	if resp := h.RecordDelivery(capScope, brief.CapNodeID, delivery); !resp.OK {
		abandonWorker(hc, brief.Worktree, paneID)
		rollbackWorker(h, brief.Project, workerID, fmt.Sprintf("record delivery: %s", resp.Error))
		return deliverErr(h, brief, worktreePath, fmt.Errorf("record delivery: %s", resp.Error))
	}
	if err := recordDecision(h, brief.CapNodeID, fmt.Sprintf("delivered to pane %s, agent %s", paneID, agent)); err != nil {
		return deliverErr(h, brief, worktreePath, err)
	}
	if resp := h.TaskUpdate(capScope, brief.CapNodeID, "active", "", nil); !resp.OK {
		return deliverErr(h, brief, worktreePath, fmt.Errorf("mark node active: %s", resp.Error))
	}

	brief.Delivery = &delivery
	return command.Response{OK: true, Data: brief}
}

// recordLoaded appends the trace's `loaded` line for every work playbook in the
// plan. It is the whole of the claim the trace makes about guidance: the brief
// carried the playbook. There is deliberately no `followed` line — the trace
// cannot see inside the worker, and nothing here pretends otherwise.
func recordLoaded(lg *trace.Log, brief *Brief) error {
	for _, entry := range brief.Plan {
		if !slices.Contains(entry.Moments, knowledge.MomentWork) {
			continue
		}
		if err := lg.Append(brief.Project, trace.Line{
			ID:       trace.StepID(brief.CapNodeID, entry.Name, 0),
			Dispatch: brief.CapNodeID,
			Worker:   brief.WorkerNode,
			Playbook: entry.Name,
			Moment:   knowledge.MomentWork,
			Kind:     trace.KindLoaded,
			Status:   trace.StatusLoaded,
			Detail:   "handed to " + brief.WorkerNode,
		}); err != nil {
			return err
		}
	}
	return nil
}

// workerPane returns the pane the worker starts in — and, for a worktree
// dispatch, the checkout that pane sits at — for a dispatch that names a
// worktree, and a new sibling of the cap's own for one that does not.
//
// The worktree case must not split: the split inherits the cap's directory, so
// the worker would run in the main checkout while the record claimed isolation.
// And it must not fall back to a split when the worktree has no root pane — a
// silent fallback is the same bug, only quieter.
//
// The checkout comes back with the pane because the record has to keep it: the
// workspace id can be gone by close, and the path is the only thing left that
// names what a worktree teardown has to remove. A plain dispatch has no such
// path, and the record keeps none.
func workerPane(hc HerdrCLI, brief *Brief) (paneID, tabID, checkout string, err error) {
	if brief.Worktree != "" {
		return hc.WorktreeRootPane(brief.Worktree)
	}
	paneID, tabID, err = hc.SplitPane(brief.RootPath)
	if err != nil {
		return "", "", "", fmt.Errorf("split pane: %w", err)
	}
	return paneID, tabID, "", nil
}

// createWorkerNode creates the worker's node in the target project and points
// the brief at it, so the brief the worker receives names the node it owns
// rather than leaving it to invent one.
func createWorkerNode(h *command.Handler, brief *Brief) (string, error) {
	added := h.TaskAddKind(brief.Project, dispatchGoal(brief.TaskType, brief.Goal), query.KindDispatch, "", "", "", "", "", nil)
	if !added.OK {
		return "", fmt.Errorf("create worker node in %s: %s", brief.Project, added.Error)
	}
	data, ok := added.Data.(command.EventData)
	if !ok || data.NodeID == "" {
		return "", fmt.Errorf("create worker node in %s: task-created response carried no node id", brief.Project)
	}
	brief.WorkerNode = brief.Project + ":" + data.NodeID
	return data.NodeID, nil
}

// agentNameMax is the longest name herdr accepts. Its grammar for a live agent
// name is `[a-z][a-z0-9_-]{0,31}` (herdr's own skill text, and `herdr agent
// start` refuses what does not match), so a longer one is refused in herdr's
// vocabulary — invalid_agent_name — which names neither fs nor the limit that
// was hit.
const agentNameMax = 32

// agentNameSyntax is that grammar, spelled the way herdr spells it. It is the
// whole of the check: a name that matches is one herdr takes, and a name that
// does not is one it refuses whatever fs thought of it.
var agentNameSyntax = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// workerNodeWidth is how much of an agent name a worker's node costs. Every node
// id is "t-" plus four random bytes rendered as hex — internal/command's
// newNodeID — so ten characters, and it is what a task type has to leave room
// for.
const workerNodeWidth = len("t-") + 8

// maxTaskType is the longest task type an agent name can carry: "dispatch-", the
// "-" that joins the node and the node itself are spoken for before the type
// gets any room.
const maxTaskType = agentNameMax - len("dispatch-") - 1 - workerNodeWidth

// validTaskType refuses a --type the agent name cannot carry, naming the
// constraint it broke.
//
// It is asked at prepare rather than left to the builder at delivery because the
// type is the one input of the name a cap chose, and a refusal before a node
// exists or a pane is split is the difference between fs saying which limit was
// hit and herdr answering invalid_agent_name, an error that names neither fs nor
// the input. The builder checks the assembled name as well, because the node is
// the other input and its width is not this function's to bound.
func validTaskType(taskType string) error {
	if len(taskType) > maxTaskType {
		return fmt.Errorf(
			"task type %q is %d characters; a worker's agent is named \"dispatch-<type>-<node>\", herdr caps a live agent name at %d, and the node costs %d of those, so a type may be at most %d characters",
			taskType, len(taskType), agentNameMax, workerNodeWidth, maxTaskType)
	}
	if !agentNameSyntax.MatchString("dispatch-" + taskType) {
		return fmt.Errorf(
			"task type %q holds a character herdr's agent names do not: a live agent name matches [a-z][a-z0-9_-]{0,31}, and the type sits inside one, so it may hold only a-z, 0-9, '-' and '_'",
			taskType)
	}
	return nil
}

// workerAgent names the agent that carries a dispatch's brief: the task type,
// and the worker's node when there is one. Two dispatches of one type running
// at once are two names, so herdr's record of one cannot be read as the other's.
//
// It is the one place the name is built and the one place the built name is
// checked, and it refuses rather than trimming: a shortened type reads as a
// different type, and the node is what makes the name unique, so a bound that
// cut it would trade a refusal for a collision — the worse failure, and the
// quieter one. Nothing here slices, so no input reaches a panic; a name herdr
// would not take comes back as an error naming herdr's limit in fs's words.
func workerAgent(taskType, workerID string) (string, error) {
	name := "dispatch-" + taskType
	if workerID != "" {
		name += "-" + workerID
	}
	if len(name) > agentNameMax || !agentNameSyntax.MatchString(name) {
		return "", fmt.Errorf(
			"agent name %q cannot be carried: herdr caps a live agent name at %d characters matching [a-z][a-z0-9_-]{0,31}",
			name, agentNameMax)
	}
	return name, nil
}

// agentStartAttempts bounds how many times a start is tried while herdr says the
// pane is not yet a shell. herdr starts an agent only in a pane sitting at an
// interactive prompt, and a pane split a moment ago is not one yet: `agent start`
// answers agent_pane_busy and does so immediately — its own --timeout, which is
// herdr's readiness wait, does not cover this case (measured, see the dispatch's
// notes) — so the wait has to be fs's.
const agentStartAttempts = 20

// agentStartBackoff is how long to wait between those attempts. It is a variable
// only so a test can zero it; nothing in fs sets it.
var agentStartBackoff = 200 * time.Millisecond

// startAgent starts the agent in paneID, retrying for as long as herdr reports
// the pane is not yet an available shell.
//
// Detect-and-retry, not wait-for-readiness, because herdr publishes no readiness
// to wait on: neither `pane get` nor the api snapshot tells a shell at a prompt
// apart from a pane with no shell at all — both report agent_status "unknown" —
// so a readiness wait would have to be a sleep of a guessed length before every
// start. That is slower in the case that already worked and no more reliable in
// the case that did not. The retry costs nothing when the pane was ready, is
// bounded by an attempt count rather than by a guess, and gives up returning
// herdr's own error, so a start that never becomes possible reads exactly as it
// did before.
//
// Only agent_pane_busy is retried. An invalid name or a pane herdr does not know
// is not a race, and does not become one by being asked again.
func startAgent(hc HerdrCLI, paneID, name, kind string, args []string) error {
	var err error
	for attempt := 0; attempt < agentStartAttempts; attempt++ {
		err = hc.StartAgent(paneID, name, kind, args)
		if herdrErrorCode(err) != "agent_pane_busy" {
			return err
		}
		if attempt < agentStartAttempts-1 {
			time.Sleep(agentStartBackoff)
		}
	}
	return err
}

// rollbackWorker resolves a worker node whose delivery did not complete. The
// store is append-only, so the node cannot be deleted; marking it done with the
// reason is the rollback — nothing is left outstanding, and the log says why the
// node exists. Best-effort: the caller is already reporting a failure.
func rollbackWorker(h *command.Handler, project, workerID, reason string) {
	_ = h.TaskUpdate(project, workerID, "done", "dispatch never delivered: "+reason, nil)
}

// deliverErr reports a delivery that did not happen. It names the cap node it
// left unresolved, so the cap can find and close it instead of guessing, and —
// for a worktree dispatch — it names the tree the failure left standing.
//
// Leaving the tree is the point, not an oversight. A delivery that fails after
// the worktree exists has made something: a checkout that no delivery-recorded
// event mentions. Removing it would be fs destroying a tree on an inference —
// that it is ours because we are the ones who made it — and the inference is
// simply wrong for --worktree, where the tree was there before the dispatch was.
// A failed delivery that tidies up after itself reports a clean failure and
// takes a human's worktree with it. So nothing is removed: the refusal carries
// the workspace and the path, and the same line is recorded against the cap
// node, so a person reading either one knows what is on disk and can take it
// down deliberately.
func deliverErr(h *command.Handler, brief *Brief, worktreePath string, err error) command.Response {
	msg := fmt.Sprintf("deliver: %v", err)

	if brief.Worktree != "" {
		where := brief.Worktree
		if worktreePath != "" {
			where += " at " + worktreePath
		}
		left := fmt.Sprintf(
			"worktree %s was left standing — no delivery record names it, so inspect it and remove it by hand (git worktree remove, herdr workspace close %s)",
			where, brief.Worktree)
		msg += "; " + left
		_ = recordDecision(h, brief.CapNodeID, "delivery failed, "+left)
	}

	return errResp(msg + fmt.Sprintf(
		"; cap node %s is unresolved — fix it and re-dispatch, or close it with fs close --abandoned",
		brief.CapNodeID))
}

// abandonWorker best-effort tears down a worker whose dispatch is not going to
// be recorded, so a failed delivery does not leave a pane it opened sitting
// there. The split pane is all a plain dispatch owns: the tab it sits in is the
// cap's own.
//
// A worktree dispatch owns nothing it may close. Its pane is not a split this
// delivery made — it came with the workspace, and it is that workspace's only
// way in — and the worktree underneath it is not fs's to remove. Closing the
// pane would take down the one thing pointing at a checkout that the delivery
// record never got written to mention, leaving a tree on disk that nothing
// names. So for a worktree this does nothing, and deliverErr says where the tree
// is instead.
func abandonWorker(hc HerdrCLI, worktree, paneID string) {
	if worktree != "" {
		return
	}
	_ = tearDownPane(hc, paneID)
}

// herdrCLI shells out to the herdr binary on PATH.
type herdrCLI struct{}

// NewHerdrCLI returns the herdr CLI adapter used by the fs commands.
func NewHerdrCLI() HerdrCLI { return herdrCLI{} }

func (herdrCLI) SplitPane(cwd string) (string, string, error) {
	out, err := runHerdr("pane", "split", "--current", "--direction", "right", "--no-focus", "--cwd", cwd)
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

func (herdrCLI) StartAgent(paneID, name, kind string, args []string) error {
	_, err := runHerdr(agentStartArgs(name, kind, paneID, args)...)
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
// with no such pane is ErrNoRootPane rather than a guess, because starting the
// worker anywhere else would be the very bug this avoids.
func (h herdrCLI) WorktreeRootPane(wsID string) (string, string, string, error) {
	checkout, err := h.WorktreeCheckout(wsID)
	if err != nil {
		return "", "", "", err
	}

	out, err := runHerdr("pane", "list", "--workspace", wsID)
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
		ErrNoRootPane, wsID, len(resp.Result.Panes), checkout)
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
// worker can start in, and the caller refuses — ErrNoRootPane — rather than
// starting the worker somewhere it did not mean.
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
func (herdrCLI) CreateWorktree(cwd string) (string, string, string, error) {
	out, err := runHerdr("worktree", "create", "--cwd", cwd, "--no-focus")
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
// ErrNoWorktree when herdr has no such workspace or it is not a worktree.
func (herdrCLI) WorktreeCheckout(wsID string) (string, error) {
	out, err := runHerdr("workspace", "get", wsID)
	if err != nil {
		if herdrErrorCode(err) == "workspace_not_found" {
			return "", fmt.Errorf("%w: herdr has no workspace %s", ErrNoWorktree, wsID)
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
		return "", fmt.Errorf("%w: workspace %s is not a worktree", ErrNoWorktree, wsID)
	}
	return checkout, nil
}

func (herdrCLI) Prompt(paneID, text string) error {
	_, err := runHerdr("agent", "prompt", paneID, text)
	return err
}

func (herdrCLI) Agents() (map[string]string, error) {
	agents, err := listAgents()
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

func (herdrCLI) AgentForPane(paneID string) (string, error) {
	agents, err := listAgents()
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
func listAgents() ([]herdrAgent, error) {
	out, err := runHerdr("agent", "list")
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

func (herdrCLI) ClosePane(paneID string) error {
	_, err := runHerdr("pane", "close", paneID)
	if herdrErrorCode(err) == "pane_not_found" {
		return ErrPaneGone
	}
	return err
}

// PaneExists asks herdr for the pane and reads the answer, rather than trusting
// that a close succeeded. pane_not_found is the one error that answers the
// question — the pane is absent; any other failure leaves it unanswered and is
// reported as such.
func (herdrCLI) PaneExists(paneID string) (bool, error) {
	_, err := runHerdr("pane", "get", paneID)
	if err == nil {
		return true, nil
	}
	if herdrErrorCode(err) == "pane_not_found" {
		return false, nil
	}
	return false, fmt.Errorf("herdr pane get %s: %w", paneID, err)
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

// runHerdr runs the herdr CLI and returns its stdout. herdr reports failures as
// JSON on stderr ({"error":{"code":...}}) and still exits non-zero, so the code
// is recovered from the output rather than from the exit status alone. Stdout
// is checked too: the envelope's destination is not contractual.
func runHerdr(args ...string) (string, error) {
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

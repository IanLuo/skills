// Delivery: handing a prepared brief to a worker and recording the binding.
//
// The dispatcher is herdr, behind engine.Engine (launch.go): shelling out to a
// CLI is the adapter's business, and a delivery here is exercised by handing it
// an adapter that needs no live terminal.
package dispatch

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/engine"
	"github.com/flagship-dev/flagship/internal/query"
)

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
func Deliver(h *command.Handler, hc engine.Engine, brief *Brief) command.Response {
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
func workerPane(hc engine.Engine, brief *Brief) (paneID, tabID, checkout string, err error) {
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
// Only engine.ErrPaneBusy is retried, and the adapter is where an engine's own
// way of saying it becomes that sentinel. An invalid name or a pane the engine
// does not know is not a race, and does not become one by being asked again.
func startAgent(hc engine.Engine, paneID, name, kind string, args []string) error {
	var err error
	for attempt := 0; attempt < agentStartAttempts; attempt++ {
		err = hc.StartAgent(paneID, name, kind, args)
		if !errors.Is(err, engine.ErrPaneBusy) {
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
func abandonWorker(hc engine.Engine, worktree, paneID string) {
	if worktree != "" {
		return
	}
	_ = tearDownPane(hc, paneID)
}

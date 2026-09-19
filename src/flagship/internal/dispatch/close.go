// Close: the close-out gate for a dispatch.
//
// The gate exists because a cap that trusts its own in-context testing never
// reads the worker's node. It can check that the worker's node exists and is
// done, and that a verdict was recorded. It cannot check that the cap read that
// node. Do not pretend otherwise: the gate makes the omission visible and
// expensive, it does not make it impossible.
//
// Close-out also runs the exit gate its task type declares — a cleanup playbook
// — and tears down whatever the delivery opened: the pane, its tab, and the
// worktree the worker ran in. Prerequisites guard entry; without this, nothing
// guards exit, and a worktree leaks exactly the way panes once did.
package dispatch

import (
	"errors"
	"fmt"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/registry"
)

// CloseRequest is the cap's close-out of one dispatch.
type CloseRequest struct {
	NodeID    string // the cap's dispatch node
	Worker    string // optional "<project>:<node>"; must match the delivery record when given
	Decision  string // the cap's verdict, recorded on the cap node
	Confirm   bool   // the cap has answered the cleanup gate's ask steps
	Abandoned bool   // the dispatch was never delivered
	Reason    string // why it was never delivered; required with Abandoned
}

// CloseResult is the close-out's data payload. Worker names the node this
// close-out rested on, so the cap can see which one it just signed off. Cleanup
// says what the exit gate did — including that there was none, which must be
// visible rather than silent.
type CloseResult struct {
	NodeID   string `json:"node_id"`
	Decision string `json:"decision"`
	Worker   string `json:"worker,omitempty"`
	PaneID   string `json:"pane_id,omitempty"`
	TabID    string `json:"tab_id,omitempty"`
	Cleanup  string `json:"cleanup,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

// Close closes out a dispatch node. It refuses — naming what is missing — when
// the node is not a dispatch, when no delivery was recorded (or, with
// --abandoned, no reason was given), when the worker's node is missing or not
// done, or when no verdict was given. On success it closes the pane and tab from
// the delivery record and marks the cap node done with the verdict.
//
// The worker's node comes from the delivery record, not from the caller: --worker
// is optional and, when given, must name the recorded node. A record written
// before the link existed carries no node, and is the one case --worker is still
// the only way to name it.
//
// --abandoned skips the delivery and worker gates and records
// "abandoned, never delivered: <reason>" instead of a verdict.
//
// reg and kc are how the exit gate finds its footing: kc holds the cleanup
// playbook named by the delivery record's type, and reg says which root that
// playbook's checks run in. Both are needed only after the worker's node is
// known to be done, so a refusal never reads either.
func Close(h *command.Handler, hc HerdrCLI, reg *registry.Registry, kc *knowledge.Center, req CloseRequest) command.Response {
	if req.NodeID == "" {
		return errResp("--node is required: fs close --node <cap node> ...")
	}

	node, err := capNode(h, req.NodeID)
	if err != nil {
		return errResp(err.Error())
	}
	if !isDispatchGoal(node.Goal) {
		return errResp(fmt.Sprintf(
			"close: %s is not a dispatch node — its goal %q is not a dispatch goal (want the prefix \"dispatch TYPE: \")",
			req.NodeID, node.Goal))
	}

	decision := req.Decision
	var delivery command.DeliveryRecord
	workerRef := ""

	if req.Abandoned {
		if req.Reason == "" {
			return errResp("--abandoned requires --reason: say why the dispatch was never delivered")
		}
		decision = "abandoned, never delivered: " + req.Reason
	} else {
		d, ok, err := h.DeliveryFor(capScope, req.NodeID)
		if err != nil {
			return errResp(fmt.Sprintf("close: read delivery record: %v", err))
		}
		if !ok {
			return errResp(fmt.Sprintf(
				"no delivery recorded for %s — was this dispatched, or prepared and abandoned? close it with --abandoned --reason \"<why>\" if it was never delivered",
				req.NodeID))
		}
		delivery = d

		workerProject, workerID, err := workerFor(delivery, req.Worker)
		if err != nil {
			return errResp(err.Error())
		}
		workerRef = workerProject + ":" + workerID

		worker, found := scopeNode(h, workerProject, workerID)
		if !found {
			return errResp(fmt.Sprintf("worker %s not found in project %s", workerRef, workerProject))
		}
		if worker.Status != "done" {
			return errResp(fmt.Sprintf(
				"worker %s is %s — read its node and let it finish, or close with --abandoned",
				workerRef, worker.Status))
		}
		if req.Decision == "" {
			return errResp("--decision is required: record the verdict this close-out rests on")
		}
	}

	result := CloseResult{
		NodeID:   req.NodeID,
		Decision: decision,
		Worker:   workerRef,
		PaneID:   delivery.PaneID,
		TabID:    delivery.TabID,
	}
	if !req.Abandoned {
		// The exit gate runs before anything is torn down or marked done: a
		// dispatch that fails it is still open work, and its pane, tab, and
		// worktree must remain so it can be finished.
		gate, confirmations, err := runCleanupGate(reg, kc, delivery, req.Confirm)
		if err != nil {
			return errResp(err.Error())
		}
		result.Cleanup = gate
		for _, confirmation := range confirmations {
			if err := recordDecision(h, req.NodeID, confirmation); err != nil {
				return errResp(err.Error())
			}
		}

		var warnings []string
		if warning := tearDownWorker(hc, delivery.TabID, delivery.PaneID); warning != "" {
			warnings = append(warnings, warning)
		}
		if delivery.Worktree != "" {
			if warning := tearDownWorktree(hc, delivery.Worktree); warning != "" {
				warnings = append(warnings, warning)
			}
		}
		result.Warning = strings.Join(warnings, "; ")
	} else {
		result.Cleanup = "no cleanup gate: the dispatch was abandoned"
	}

	if resp := h.TaskUpdate(capScope, req.NodeID, "done", decision, nil); !resp.OK {
		return errResp("mark node done: " + resp.Error)
	}
	return command.Response{OK: true, Data: result}
}

// runCleanupGate runs the exit gate the delivery's task type declares: the
// <type>-cleanup playbook. It returns a note describing what happened and one
// confirmation per ask step, for the caller to record on the cap's node.
//
// A type with no shipped or local cleanup playbook has no gate, and says so —
// a missing exit gate must be visible, never silent. A playbook that exists but
// cannot be read is refused, because a gate that fails open is worse than none.
//
// Checks run in order with cwd set to the worker project's root, and the first
// failure refuses — the same fail-fast shape as fs dispatch, and for the same
// reason: the user fixes one thing at a time. ask steps are questions only the
// user can answer, so they are refused until --confirm says they have been.
func runCleanupGate(reg *registry.Registry, kc *knowledge.Center, delivery command.DeliveryRecord, confirm bool) (string, []string, error) {
	if delivery.Type == "" {
		return "no cleanup gate: the delivery record carries no task type", nil, nil
	}
	name := delivery.Type + "-cleanup"
	if !kc.Has(name) {
		return fmt.Sprintf("no cleanup gate: no cleanup playbook %s.yaml", name), nil, nil
	}

	pb, err := kc.Get(name)
	if err != nil {
		return "", nil, fmt.Errorf("close: read cleanup playbook %s: %v", name, err)
	}

	var asks []string
	for _, step := range pb.Steps {
		if step.Kind == knowledge.KindAsk {
			asks = append(asks, step.Body)
		}
	}
	if len(asks) > 0 && !confirm {
		quoted := make([]string, len(asks))
		for i, ask := range asks {
			quoted[i] = fmt.Sprintf("%q", ask)
		}
		return "", nil, fmt.Errorf(
			"close: cleanup gate %s has unconfirmed steps: %s; answer each, then re-run with --confirm",
			name, strings.Join(quoted, ", "))
	}

	proj, err := reg.Get(delivery.Project)
	if err != nil {
		return "", nil, fmt.Errorf("close: cleanup gate %s cannot run: %v", name, err)
	}

	checks := 0
	for _, step := range pb.Steps {
		if step.Kind != knowledge.KindCheck {
			continue
		}
		item := runCheck(step.Body, proj.RootPath)
		if item.Status != "pass" {
			return "", nil, fmt.Errorf(
				"close: cleanup check failed: %q; output: %s; fix it, then close again",
				item.Body, checkOutput(item))
		}
		checks++
	}

	confirmations := make([]string, 0, len(asks))
	for _, ask := range asks {
		confirmations = append(confirmations, "user confirmed cleanup step: "+ask)
	}

	note := fmt.Sprintf("cleanup gate %s: %d checks passed", name, checks)
	if len(asks) > 0 {
		note += fmt.Sprintf(", %d confirmed", len(asks))
	}
	return note, confirmations, nil
}

// tearDownWorker closes the pane a delivery opened, then its tab, and returns a
// warning for whatever herdr would not close.
//
// Closing a tab's last pane removes the tab, so the tab close that follows is
// normally a no-op — and it is not decoration: it is what guarantees an empty
// tab is not left behind, which is the state herdr cannot report on. A pane or
// tab that is already gone is the goal state, not a failure.
func tearDownWorker(hc HerdrCLI, tabID, paneID string) string {
	var warnings []string
	if err := hc.ClosePane(paneID); err != nil && !errors.Is(err, ErrPaneGone) {
		warnings = append(warnings, fmt.Sprintf("could not close pane %s: %v", paneID, err))
	}
	if tabID != "" {
		if err := hc.CloseTab(tabID); err != nil && !errors.Is(err, ErrTabGone) {
			warnings = append(warnings, fmt.Sprintf("could not close tab %s: %v", tabID, err))
		}
	}
	if len(warnings) == 0 {
		return ""
	}
	return strings.Join(warnings, "; ") + " — close it by hand"
}

// tearDownWorktree removes the worktree workspace the delivery recorded, and
// returns a warning for a worktree herdr would not remove.
//
// The point is that a worktree must not outlive its node. So a worktree herdr
// no longer knows is the goal state, not a failure, and herdr being unavailable
// is a warning rather than a refusal — the same rule the pane teardown follows,
// and the same reason: close-out must not depend on the dispatcher being up.
func tearDownWorktree(hc HerdrCLI, wsID string) string {
	if err := hc.RemoveWorktree(wsID); err != nil && !errors.Is(err, ErrWorktreeGone) {
		return fmt.Sprintf("could not remove worktree %s: %v — remove it by hand", wsID, err)
	}
	return ""
}

// workerFor resolves which node in the target project this close-out is about.
// The delivery record is the authority; --worker is optional, and when given it
// must name the recorded node, because a mismatch means the cap is closing
// something other than the work it dispatched.
//
// A record written before the link existed carries no node. There --worker is
// the only way to say which node this is, and the refusal says so rather than
// leaving the cap to guess.
func workerFor(d command.DeliveryRecord, requested string) (string, string, error) {
	if d.Project == "" || d.Node == "" {
		if requested == "" {
			return "", "", errors.New(
				"close: the delivery record carries no worker node (it was written before the link existed) — name it with --worker PROJECT:NODE; that is the only way to close a record this old")
		}
		project, nodeID, ok := strings.Cut(requested, ":")
		if !ok || project == "" || nodeID == "" {
			return "", "", fmt.Errorf("--worker %q must be PROJECT:NODE", requested)
		}
		return project, nodeID, nil
	}

	if recorded := d.Project + ":" + d.Node; requested != "" && requested != recorded {
		return "", "", fmt.Errorf(
			"close: --worker %s does not match the delivery record %s for this dispatch — the cap would be closing the wrong work",
			requested, recorded)
	}
	return d.Project, d.Node, nil
}

// capNode returns the cap-scope node, or an error when no such node exists.
func capNode(h *command.Handler, nodeID string) (command.TaskInfo, error) {
	node, ok := scopeNode(h, capScope, nodeID)
	if !ok {
		return command.TaskInfo{}, fmt.Errorf("close: no node %s in scope %s", nodeID, capScope)
	}
	return node, nil
}

// scopeNode finds one node in a scope via derived state, the same way fs status
// reports it. A scope with no events simply has no such node.
func scopeNode(h *command.Handler, scope, nodeID string) (command.TaskInfo, bool) {
	resp := h.Status(scope)
	if !resp.OK {
		return command.TaskInfo{}, false
	}
	result, ok := resp.Data.(command.StatusResult)
	if !ok {
		return command.TaskInfo{}, false
	}
	for _, task := range result.Tasks {
		if task.NodeID == nodeID {
			return task, true
		}
	}
	return command.TaskInfo{}, false
}

// Close: the close-out gate for a dispatch.
//
// The gate exists because a cap that trusts its own in-context testing never
// reads the worker's node. It can check that the worker's node exists and is
// done, and that a verdict was recorded. It cannot check that the cap read that
// node. Do not pretend otherwise: the gate makes the omission visible and
// expensive, it does not make it impossible.
//
// Close-out also runs the exit gate its task type declares — a cleanup playbook
// — and tears down whatever the delivery opened: the worker's pane, and the
// worktree the worker ran in. The pane is all it owns: the worker sits in a
// sibling pane of the cap's own tab, so no tab is ever closed. Prerequisites
// guard entry; without this, nothing guards exit, and a worktree leaks exactly
// the way panes once did.
package dispatch

import (
	"errors"
	"fmt"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/registry"
)

// CloseRequest is the cap's close-out of one dispatch.
type CloseRequest struct {
	NodeID    string // the cap's dispatch node
	Worker    string // optional "<project>:<node>"; must match the delivery record when given
	Decision  string // the cap's verdict, recorded on the cap node
	Confirm   bool   // the cap has answered the cleanup gate's ask steps
	Abandoned bool   // close without the delivery, worker, and cleanup gates
	Reason    string // why it is being abandoned; required with Abandoned
}

// CloseResult is the close-out's data payload. Worker names the node this
// close-out rested on, so the cap can see which one it just signed off. Cleanup
// says what the exit gate did — including that there was none, which must be
// visible rather than silent.
//
// TornDown names what close-out left gone: the pane, and the worktree when the
// record names one. An entry means that artifact is verified gone — removed now
// or already — never merely that a teardown was attempted; a teardown that could
// not finish says so in Warning instead. It exists so a reader can see the
// cleanup happened rather than infer it from a pane id that was there all along.
type CloseResult struct {
	NodeID   string   `json:"node_id"`
	Decision string   `json:"decision"`
	Worker   string   `json:"worker,omitempty"`
	PaneID   string   `json:"pane_id,omitempty"`
	Cleanup  string   `json:"cleanup,omitempty"`
	TornDown []string `json:"torn_down,omitempty"`
	Warning  string   `json:"warning,omitempty"`
}

// Close closes out a dispatch node. It refuses — naming what is missing — when
// the node is not a dispatch, when no delivery was recorded (or, with
// --abandoned, no reason was given), when the worker's node is missing or not
// done, or when no verdict was given. On success it closes the worker's pane
// and, when one was recorded, the worktree, and marks the cap node done with the
// verdict.
//
// The worker's node comes from the delivery record, not from the caller: --worker
// is optional and, when given, must name the recorded node. A record written
// before the link existed carries no node, and is the one case --worker is still
// the only way to name it.
//
// --abandoned skips the delivery and worker gates and records an abandonment —
// "abandoned, never delivered: <reason>" when no delivery was recorded, and
// "abandoned after delivery to <project>:<node>: <reason>" naming the pane and
// worktree the teardown removed when one was. It skips the cleanup gate too, but
// not the teardown: the delivery record, when there is one, is still read, and
// what it names is still removed. Abandoning is a decision about the work, not a
// licence to leak a pane — a delivered dispatch closed this way is still a
// successful close, and its pane and worktree would otherwise outlive it
// silently. The record is written after the teardown, so it can only claim what
// actually happened.
//
// reg, kc, hc, and gw are how the exit gate finds its footing: kc holds the
// cleanup playbook named by the delivery record's type, reg says which project
// root the checks would run in, and hc and gw resolve and verify the worktree
// when the record names one — the checks run there instead, because that is the
// tree the dispatch worked in, and gw is what proves the tree is gone afterwards
// rather than trusting the dispatcher to say so. All four are needed only after
// the worker's node is known to be done, so a refusal never reads any of them.
func Close(h *command.Handler, hc HerdrCLI, gw GitWorktrees, reg *registry.Registry, kc *knowledge.Center, req CloseRequest) command.Response {
	if req.NodeID == "" {
		return errResp("--node is required: fs close --node <cap node> ...")
	}

	node, err := capNode(h, req.NodeID)
	if err != nil {
		return errResp("close: " + err.Error())
	}
	if node.Kind != query.KindDispatch {
		return errResp(fmt.Sprintf(
			"close: %s is not a dispatch node — its kind is %q, want %q",
			req.NodeID, node.Kind, query.KindDispatch))
	}

	decision := req.Decision
	var delivery command.DeliveryRecord
	delivered := false
	workerRef := ""

	if req.Abandoned {
		if req.Reason == "" {
			return errResp("--abandoned requires --reason: say why the dispatch is being abandoned")
		}
		// The record is still read on this path, though none of its gates apply:
		// a dispatch can be delivered and then abandoned — a worker that never
		// finished, a request withdrawn — and the record is what names the pane
		// and the worktree to tear down. No record is the never-delivered case,
		// where nothing was opened and there is nothing to clean.
		d, ok, err := h.DeliveryFor(capScope, req.NodeID)
		if err != nil {
			return errResp(fmt.Sprintf("close: read delivery record: %v", err))
		}
		if ok {
			delivery = d
			delivered = true
		}
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
		NodeID: req.NodeID,
		Worker: workerRef,
		PaneID: delivery.PaneID,
	}
	if !req.Abandoned {
		// The exit gate runs before anything is torn down or marked done: a
		// dispatch that fails it is still open work, and its pane and worktree must
		// remain so it can be finished. A refusal above returned before this point,
		// and so does a refusal here — the teardown below is only ever reached by a
		// close that is going to succeed.
		gate, confirmations, err := runCleanupGate(hc, reg, kc, delivery, req.NodeID, node.Integrates, req.Confirm)
		if err != nil {
			return errResp(err.Error())
		}
		result.Cleanup = gate
		for _, confirmation := range confirmations {
			if err := recordDecision(h, req.NodeID, confirmation); err != nil {
				return errResp(err.Error())
			}
		}
	} else {
		result.Cleanup = "no cleanup gate: the dispatch was abandoned"
	}

	// Teardown runs on both paths: an abandoned close is a successful close, so
	// what the delivery opened must not outlive it. On a record that names
	// nothing there is nothing to tear down, which is the never-delivered case.
	var warnings []string
	if delivery.PaneID != "" {
		if warning := tearDownPane(hc, delivery.PaneID); warning != "" {
			warnings = append(warnings, warning)
		} else {
			result.TornDown = append(result.TornDown, "pane "+delivery.PaneID)
		}
	}
	if delivery.Worktree != "" {
		// The repository the worktree was made from is what git has to be asked
		// about it. A project whose registry row is gone leaves no repository
		// to ask, which the teardown reports rather than skipping the check.
		repo := ""
		if proj, err := reg.Get(delivery.Project); err == nil {
			repo = proj.RootPath
		}
		if warning := tearDownWorktree(hc, gw, repo, delivery); warning != "" {
			warnings = append(warnings, warning)
		} else {
			result.TornDown = append(result.TornDown, "worktree "+delivery.Worktree)
		}
	}
	result.Warning = strings.Join(warnings, "; ")
	if req.Abandoned {
		// Only now, after the teardown, can the record say what happened: which
		// dispatch was abandoned, and which resources were really removed. A
		// record written before the teardown would assert a clearance that had
		// not run yet.
		decision = abandonedDecision(delivery, delivered, req.Reason, result.TornDown, result.Warning)
	}
	result.Decision = decision

	if resp := h.TaskUpdate(capScope, req.NodeID, "done", decision, nil); !resp.OK {
		return errResp("mark node done: " + resp.Error)
	}
	return command.Response{OK: true, Data: result}
}

// abandonedDecision is the durable record of an abandoned close, written after
// the teardown so it states what actually happened rather than what was about
// to. A dispatch with no delivery record was never delivered. One with a record
// was delivered, and the record names it and whatever the teardown left gone; a
// teardown that could not finish says so instead of claiming a clean-up it did
// not do.
func abandonedDecision(delivery command.DeliveryRecord, delivered bool, reason string, tornDown []string, warning string) string {
	if !delivered {
		return "abandoned, never delivered: " + reason
	}

	deliveryRef := strings.TrimSuffix(delivery.Project+":"+delivery.Node, ":")
	decision := "abandoned after delivery: " + reason
	if deliveryRef != "" {
		decision = fmt.Sprintf("abandoned after delivery to %s: %s", deliveryRef, reason)
	}
	if len(tornDown) > 0 {
		decision += " (" + strings.Join(tornDown, " and ") + " torn down)"
	}
	if warning != "" {
		decision += " (teardown reported: " + warning + ")"
	}
	return decision
}

// runCleanupGate runs the exit gate the delivery's task type declares: the
// <type>-cleanup playbook. It returns a note describing what happened and one
// confirmation per ask step, for the caller to record on the cap's node.
//
// A type with no shipped or local cleanup playbook has no gate, and says so —
// a missing exit gate must be visible, never silent. A playbook that exists but
// cannot be read is refused, because a gate that fails open is worse than none.
//
// capNodeID is the dispatch node being closed and integrates the member it
// merges, when it merges one; both are passed on to the checks as FS_NODE and
// FS_INTEGRATES, so a gate can ask about the dispatch it is gating instead of a
// proxy for it.
//
// Checks run in order in the tree the dispatch worked in — the worktree the
// record names, or the worker project's root when it names none — and the first
// failure refuses: the same fail-fast shape as fs dispatch, and for the same
// reason, the user fixes one thing at a time. Checks come before the asks,
// because an ask is a question only the user can answer: making them confirm a
// gate and then revealing that a check fails wastes the answer and hides the
// failure. ask steps are refused until --confirm says they have been answered.
func runCleanupGate(hc HerdrCLI, reg *registry.Registry, kc *knowledge.Center, delivery command.DeliveryRecord, capNodeID, integrates string, confirm bool) (string, []string, error) {
	if delivery.Type == "" {
		return "no cleanup gate: the delivery record carries no task type", nil, nil
	}
	name := knowledge.CleanupName(delivery.Type)
	if !kc.Has(name) {
		return fmt.Sprintf("no cleanup gate: no cleanup playbook %s.yaml", name), nil, nil
	}

	pb, err := kc.Get(name)
	if err != nil {
		return "", nil, fmt.Errorf("close: read cleanup playbook %s: %v", name, err)
	}
	if err := triggerError("cleanup", name, pb, delivery.Type); err != nil {
		return "", nil, fmt.Errorf("close: %v", err)
	}

	var asks []string
	for _, step := range pb.Steps {
		if step.Kind == knowledge.KindAsk {
			asks = append(asks, step.Body)
		}
	}

	proj, err := reg.Get(delivery.Project)
	if err != nil {
		return "", nil, fmt.Errorf("close: cleanup gate %s cannot run: %v", name, err)
	}
	cwd, err := checkDir(hc, delivery, proj.RootPath)
	if err != nil {
		return "", nil, fmt.Errorf("close: cleanup gate %s cannot run: %v", name, err)
	}

	// Exit checks get the same FS_* treatment entry checks do, so a gate can ask
	// about the dispatch it is gating: which project and task type it was, which
	// cap node is closing, which worker's node it rested on, and which member it
	// integrates. FS_CARDS is exported empty — a close has no batch to offer — and
	// stays present rather than missing, so a check can tell "this dispatch named
	// no cards" from "this step has no env".
	env := cleanupEnv(delivery, capNodeID, integrates)
	checks := 0
	for _, step := range pb.Steps {
		if step.Kind != knowledge.KindCheck {
			continue
		}
		item := runCheck(step.Body, cwd, env)
		if item.Status != "pass" {
			return "", nil, fmt.Errorf(
				"close: cleanup check failed: %q; output: %s; fix it, then close again",
				item.Body, checkOutput(item))
		}
		checks++
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

	confirmations := make([]string, 0, len(asks))
	for _, ask := range asks {
		confirmations = append(confirmations, "user confirmed cleanup step: "+ask)
	}

	note := fmt.Sprintf("cleanup gate %s: %d checks passed", name, checks)
	if delivery.Worktree != "" {
		note += " in worktree " + delivery.Worktree
	}
	if len(asks) > 0 {
		note += fmt.Sprintf(", %d confirmed", len(asks))
	}
	return note, confirmations, nil
}

// cleanupEnv is the closing dispatch's own inputs, exposed to every exit check
// as FS_*. It is deliberately narrower than the entry gate's env: a close has no
// --goal and no --cards to offer, so it exports FS_CARDS empty rather than
// inventing a value nobody supplied. FS_INTEGRATES is the member this
// integration merges, empty for a dispatch that merges nothing.
func cleanupEnv(delivery command.DeliveryRecord, capNodeID, integrates string) []string {
	worker := ""
	if delivery.Project != "" && delivery.Node != "" {
		worker = delivery.Project + ":" + delivery.Node
	}
	return []string{
		"FS_PROJECT=" + delivery.Project,
		"FS_TYPE=" + delivery.Type,
		"FS_NODE=" + capNodeID,
		"FS_WORKER=" + worker,
		"FS_CARDS=",
		"FS_INTEGRATES=" + integrates,
	}
}

// checkDir returns the directory the cleanup checks run in: the worktree the
// dispatch worked in when the record names one, and the worker project's root
// otherwise.
//
// The worktree comes from the delivery record, not from herdr: the recorded path
// is the tree the dispatch itself worked in — resolved from herdr at delivery
// time — and it outlives the workspace, which is exactly the case that matters.
// A workspace that is gone by close would otherwise leave the gate with no
// footing, and the close would refuse and leave the checkout behind. A record
// written before the path was recorded falls back to resolving the workspace,
// and one that cannot be resolved is an error rather than a fall back to the
// project root: the shipped checks assert things about the worktree — no
// uncommitted files, branch merged — and the project root is routinely another
// agent's dirty tree, so checking it would fail or pass for reasons that have
// nothing to do with the dispatch while reporting that the gate ran.
func checkDir(hc HerdrCLI, delivery command.DeliveryRecord, root string) (string, error) {
	if delivery.Worktree == "" {
		return root, nil
	}
	if delivery.WorktreePath != "" {
		return delivery.WorktreePath, nil
	}
	checkout, err := hc.WorktreeCheckout(delivery.Worktree)
	if err != nil {
		return "", fmt.Errorf(
			"worktree %s: %w; its checks must run in the tree the dispatch worked in, so fix the workspace or close with --abandoned --reason \"<why>\"",
			delivery.Worktree, err)
	}
	return checkout, nil
}

// tearDownPane closes the pane a delivery split open, and returns a warning for
// a pane herdr would not close.
//
// The pane is all the delivery owns: the worker sits in a sibling pane of the
// cap's own tab, so the tab is the cap's and must never be closed with it. A
// pane that is already gone is the goal state, not a failure.
func tearDownPane(hc HerdrCLI, paneID string) string {
	if err := hc.ClosePane(paneID); err != nil && !errors.Is(err, ErrPaneGone) {
		return fmt.Sprintf("could not close pane %s: %v — close it by hand", paneID, err)
	}
	return ""
}

// tearDownWorktree makes the git worktree the dispatch worked in gone, and
// returns a warning for one that is still there.
//
// The point is that a worktree must not outlive its node, and the only witness
// that counts is git. herdr's removal is attempted first — it is what closes the
// workspace and its panes — but its answer is not this gate's: a herdr worktree
// and a git worktree are two artifacts that die separately, so a close that
// removed the workspace and reported success can leave the checkout on disk,
// which is how two worktrees leaked while the gate declared them clean. What
// decides is removeLeakedWorktree, which asks git and removes what git still
// lists.
//
// herdr being unavailable is a warning rather than a refusal — the same rule the
// pane teardown follows, and the same reason: close-out must not depend on the
// dispatcher being up. A worktree that cannot be verified, or cannot be removed,
// is a warning naming the path, never silence and never a claimed success.
func tearDownWorktree(hc HerdrCLI, gw GitWorktrees, repo string, d command.DeliveryRecord) string {
	var warnings []string
	if err := hc.RemoveWorktree(d.Worktree); err != nil && !errors.Is(err, ErrWorktreeGone) {
		warnings = append(warnings, fmt.Sprintf("herdr could not remove worktree workspace %s: %v", d.Worktree, err))
	}

	path, err := teardownPath(hc, d)
	if err != nil {
		warnings = append(warnings, "could not verify worktree: "+err.Error()+" — check it by hand")
		return strings.Join(warnings, "; ")
	}
	if repo == "" {
		warnings = append(warnings, fmt.Sprintf(
			"could not verify worktree %s: project %s is not registered, so no repository could be asked about it — check it by hand",
			path, d.Project))
		return strings.Join(warnings, "; ")
	}
	if warning := removeLeakedWorktree(gw, repo, path); warning != "" {
		warnings = append(warnings, warning)
	}
	return strings.Join(warnings, "; ")
}

// teardownPath is the checkout the teardown has to see gone: the path the record
// kept, or — for a record written before the path was recorded — the path herdr
// still resolves the workspace to, which is the best answer left. When neither
// exists the caller says so: a worktree nobody can name is a leak nobody has
// looked at.
func teardownPath(hc HerdrCLI, d command.DeliveryRecord) (string, error) {
	if d.WorktreePath != "" {
		return d.WorktreePath, nil
	}
	path, err := hc.WorktreeCheckout(d.Worktree)
	if err != nil {
		return "", fmt.Errorf(
			"worktree %s: the record names no path and herdr cannot resolve the workspace: %w",
			d.Worktree, err)
	}
	return path, nil
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
// The error carries no command's name: fs close and fs dispatch both resolve
// their nodes through here.
func capNode(h *command.Handler, nodeID string) (command.TaskInfo, error) {
	node, ok := scopeNode(h, capScope, nodeID)
	if !ok {
		return command.TaskInfo{}, fmt.Errorf("no node %s in scope %s", nodeID, capScope)
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

// Close: the close-out gate for a dispatch.
//
// The gate exists because a cap that trusts its own in-context testing never
// reads the worker's node. It can check that the worker's node exists and is
// done, and that a verdict was recorded. It cannot check that the cap read that
// node. Do not pretend otherwise: the gate makes the omission visible and
// expensive, it does not make it impossible.
//
// Close-out also runs the exit gate its task type declares — a cleanup playbook
// — and tears down what the delivery opened. The pane is machinery, because
// every dispatch has one. The worktree the worker ran in is not: removing it and
// deleting its branch are actions the playbook declares as do steps, so each
// task type owns its own teardown instead of sharing one hardcoded path.
// Prerequisites guard entry; without this, nothing guards exit, and a worktree
// leaks exactly the way panes once did.
package dispatch

import (
	"errors"
	"fmt"
	"os"
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
// TornDown names what close-out itself left gone. That is now the pane alone:
// removing a worktree is an action the cleanup playbook declares, and close-out
// runs it without owning it. It exists so a reader can see the cleanup happened
// rather than infer it from a pane id that was there all along.
//
// Actions names the declared do steps that ran, in order. It is a report of what
// the playbook was told to do, not a claim about what each command achieved —
// close-out cannot see inside a shell command, and pretending it could is how a
// teardown reported success over a checkout still on disk.
type CloseResult struct {
	NodeID   string   `json:"node_id"`
	Decision string   `json:"decision"`
	Worker   string   `json:"worker,omitempty"`
	PaneID   string   `json:"pane_id,omitempty"`
	Cleanup  string   `json:"cleanup,omitempty"`
	TornDown []string `json:"torn_down,omitempty"`
	Actions  []string `json:"actions,omitempty"`
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
// reg, kc, and hc are how the exit gate finds its footing: kc holds the cleanup
// playbook named by the delivery record's type, reg says which project root the
// gate runs in, and hc resolves the tree the checks run in when the record names
// a worktree but no path. All are needed only after the worker's node is known
// to be done, so a refusal never reads any of them.
func Close(h *command.Handler, hc HerdrCLI, reg *registry.Registry, kc *knowledge.Center, req CloseRequest) command.Response {
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
	// The exit gate runs before anything is torn down or marked done: a dispatch
	// that fails it is still open work, and its pane and worktree must remain so it
	// can be finished. A refusal above returned before this point, and so does a
	// refusal here — the teardown below is only ever reached by a close that is
	// going to succeed. runCleanup knows which path it is on: an abandoned close
	// skips the checks and asks but still runs the declared actions, because
	// abandoning is a decision about the work, not a licence to leak.
	run, err := runCleanup(hc, reg, kc, delivery, req.NodeID, node.Integrates, req.Confirm, req.Abandoned)
	if err != nil {
		return errResp(err.Error())
	}
	result.Cleanup = run.Note
	result.Actions = run.Actions
	for _, confirmation := range run.Confirmations {
		if err := recordDecision(h, req.NodeID, confirmation); err != nil {
			return errResp(err.Error())
		}
	}
	warnings := run.Warnings

	// The pane is machinery; the worktree is not. The split is the point:
	// universal invariants are machinery, per-type actions are declared.
	//
	// Every dispatch has a pane, and a playbook that forgot to close it would leak
	// a terminal with nothing left to catch it — so closing it is coded here, for
	// every type. A worktree is different: removing a tree and deleting its branch
	// are git actions, they differ by task type, and the cleanup playbook declares
	// them as do steps. close-out runs those steps and reports them; it does not
	// own them, and it cannot verify what a shell command did.
	if delivery.PaneID != "" {
		if warning := tearDownPane(hc, delivery.PaneID); warning != "" {
			warnings = append(warnings, warning)
		} else {
			result.TornDown = append(result.TornDown, "pane "+delivery.PaneID)
		}
	}
	result.Warning = strings.Join(warnings, "; ")
	if req.Abandoned {
		// Only now, after the teardown, can the record say what happened: which
		// dispatch was abandoned, and which resources were really removed. A
		// record written before the teardown would assert a clearance that had
		// not run yet.
		decision = abandonedDecision(delivery, delivered, req.Reason, result.TornDown, result.Actions, result.Warning)
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
// was delivered, and the record names it and what close-out itself left gone —
// the pane — plus how many declared cleanup actions ran. A teardown that could
// not finish says so instead of claiming a clean-up it did not do.
func abandonedDecision(delivery command.DeliveryRecord, delivered bool, reason string, tornDown, actions []string, warning string) string {
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
	if len(actions) > 0 {
		word := "actions"
		if len(actions) == 1 {
			word = "action"
		}
		decision += fmt.Sprintf(" (%d cleanup %s ran)", len(actions), word)
	}
	if warning != "" {
		decision += " (teardown reported: " + warning + ")"
	}
	return decision
}

// cleanupRun is what the exit gate did: the note the response reports, the
// decisions its ask steps earn, the declared do steps that ran in order, and —
// on the abandoned path only — the actions that could not finish.
type cleanupRun struct {
	Note          string
	Confirmations []string
	Actions       []string
	Warnings      []string
}

// runCleanup runs the exit gate the delivery's task type declares: the
// <type>-cleanup playbook. It returns a note describing what happened, one
// confirmation per ask step for the caller to record, and the declared actions
// that ran.
//
// A type with no shipped or local cleanup playbook has no gate, and says so —
// a missing exit gate must be visible, never silent. A playbook that exists but
// cannot be read is refused, because a gate that fails open is worse than none.
//
// capNodeID is the dispatch node being closed and integrates the member it
// merges, when it merges one; both are passed on to the steps as FS_NODE and
// FS_INTEGRATES, so a gate can ask about the dispatch it is gating instead of a
// proxy for it.
//
// The order is the contract, because later steps act on an earlier step's
// success: checks run in the tree the dispatch worked in, in order, and the
// first failure refuses; then the asks, because an ask is a question only the
// user can answer and making them confirm a gate that then fails wastes the
// answer; then the declared actions, in order, in the project root. A refusal
// from any of them leaves the node open and returns before anything is torn
// down.
//
// abandoned is the escape hatch, and it skips only the gates: checks and asks
// do not run, because a node being abandoned is not a node being gated. The
// declared actions still run — abandoning is a decision about the work, not a
// licence to leak the tree it ran in — and one that fails is a warning, never a
// refusal, for the same reason: a stuck teardown must not keep a node open.
func runCleanup(hc HerdrCLI, reg *registry.Registry, kc *knowledge.Center, delivery command.DeliveryRecord, capNodeID, integrates string, confirm, abandoned bool) (cleanupRun, error) {
	if delivery.Type == "" {
		return cleanupRun{Note: "no cleanup gate: the delivery record carries no task type"}, nil
	}
	name := knowledge.CleanupName(delivery.Type)
	if !kc.Has(name) {
		if abandoned {
			return cleanupRun{Note: "no cleanup gate: the dispatch was abandoned"}, nil
		}
		return cleanupRun{Note: fmt.Sprintf("no cleanup gate: no cleanup playbook %s.yaml", name)}, nil
	}

	pb, err := kc.Get(name)
	if err != nil {
		return cleanupRun{}, fmt.Errorf("close: read cleanup playbook %s: %v", name, err)
	}
	if err := triggerError("cleanup", name, pb, delivery.Type); err != nil {
		return cleanupRun{}, fmt.Errorf("close: %v", err)
	}

	proj, err := reg.Get(delivery.Project)
	if err != nil {
		// A project whose registry row is gone leaves no root for a declared
		// action to run in. On the gated path that is a refusal — the gate could
		// not run. On the abandoned path it is a warning: abandoning is a
		// decision, and a teardown that cannot run must not keep the node open.
		if abandoned {
			return cleanupRun{
				Note:     fmt.Sprintf("cleanup gate %s skipped (abandoned): project %s is not registered, so its declared actions cannot run", name, delivery.Project),
				Warnings: []string{fmt.Sprintf("cleanup gate %s cannot run: project %s is not registered (%v)", name, delivery.Project, err)},
			}, nil
		}
		return cleanupRun{}, fmt.Errorf("close: cleanup gate %s cannot run: %v", name, err)
	}

	// Exit steps get the same FS_* treatment entry checks do, so a gate can ask
	// about the dispatch it is gating: which project and task type it was, which
	// cap node is closing, which worker's node it rested on, which member it
	// integrates, and — for an action that has to remove a tree — the pane and
	// the worktree the delivery opened. FS_CARDS is exported empty — a close has
	// no batch to offer — and stays present rather than missing, so a step can
	// tell "this dispatch named no cards" from "this step has no env".
	env := cleanupEnv(delivery, capNodeID, integrates)

	var asks []string
	for _, step := range pb.Steps {
		if step.Kind == knowledge.KindAsk {
			asks = append(asks, step.Body)
		}
	}

	var run cleanupRun
	checks := 0
	if !abandoned {
		cwd, err := checkDir(hc, delivery, proj.RootPath)
		if err != nil {
			return cleanupRun{}, fmt.Errorf("close: cleanup gate %s cannot run: %v", name, err)
		}
		for _, step := range pb.Steps {
			if step.Kind != knowledge.KindCheck {
				continue
			}
			item := runCheck(step.Body, cwd, env)
			if item.Status != "pass" {
				return cleanupRun{}, fmt.Errorf(
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
			return cleanupRun{}, fmt.Errorf(
				"close: cleanup gate %s has unconfirmed steps: %s; answer each, then re-run with --confirm",
				name, strings.Join(quoted, ", "))
		}
		for _, ask := range asks {
			run.Confirmations = append(run.Confirmations, "user confirmed cleanup step: "+ask)
		}
	}

	// The actions are the per-type teardown, declared rather than hardcoded: each
	// is a shell command run in the project root, in the order the playbook
	// lists. Running them here — after the checks and the asks, before the node
	// is marked done — is what makes a failing action a refusal the cap has to
	// answer instead of a warning nobody reads.
	for _, step := range pb.Steps {
		if step.Kind != knowledge.KindDo {
			continue
		}
		item := runStep(knowledge.KindDo, step.Body, proj.RootPath, env)
		if item.Status != "pass" {
			if !abandoned {
				return cleanupRun{}, fmt.Errorf(
					"close: cleanup action failed: %q; output: %s; fix it, then close again",
					item.Body, checkOutput(item))
			}
			run.Warnings = append(run.Warnings,
				fmt.Sprintf("cleanup action %q failed: %s", item.Body, checkOutput(item)))
		}
		run.Actions = append(run.Actions, step.Body)
	}

	run.Note = cleanupNote(name, checks, len(run.Actions), len(asks), delivery.Worktree, abandoned)
	return run, nil
}

// cleanupNote is the one-line account of the exit gate the response carries:
// how many checks passed, how many declared actions ran, how many asks were
// confirmed, and the tree the checks ran in. A gate whose report hides which of
// those happened is how "the cleanup ran" stopped meaning anything.
func cleanupNote(name string, checks, actions, asks int, worktree string, abandoned bool) string {
	var parts []string
	if abandoned {
		if actions > 0 {
			parts = append(parts, fmt.Sprintf("%d actions run", actions))
		} else {
			parts = append(parts, "no steps ran")
		}
	} else {
		parts = append(parts, fmt.Sprintf("%d checks passed", checks))
		if actions > 0 {
			parts = append(parts, fmt.Sprintf("%d actions run", actions))
		}
		if asks > 0 {
			parts = append(parts, fmt.Sprintf("%d confirmed", asks))
		}
	}

	skipped := ""
	if abandoned {
		skipped = " skipped (abandoned)"
	}
	note := fmt.Sprintf("cleanup gate %s%s: %s", name, skipped, strings.Join(parts, ", "))
	if worktree != "" {
		note += " in worktree " + worktree
	}
	return note
}

// cleanupEnv is the closing dispatch's own inputs, exposed to every exit step as
// FS_*. It is deliberately narrower than the entry gate's env: a close has no
// --goal and no --cards to offer, so it exports FS_CARDS empty rather than
// inventing a value nobody supplied. FS_INTEGRATES is the member this
// integration merges, empty for a dispatch that merges nothing.
//
// FS_PANE, FS_WORKTREE, and FS_WORKTREE_PATH name what the delivery opened. They
// are what lets a declared action be about this dispatch's own tree — removing
// it, deleting its branch — without the mechanism knowing how.
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
		"FS_PANE=" + delivery.PaneID,
		"FS_WORKTREE=" + delivery.Worktree,
		"FS_WORKTREE_PATH=" + delivery.WorktreePath,
	}
}

// checkDir returns the directory the cleanup checks run in: the worktree the
// dispatch worked in when the record names one that is still there, and the
// worker project's root otherwise.
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
//
// A worktree that is already gone is the third case, and it is not a refusal:
// the tree the checks assert things about does not exist, so their footing falls
// back to the project root. Refusing instead would make a retry — or a close
// after a partial teardown — impossible, which is the failure this whole path is
// meant to end.
func checkDir(hc HerdrCLI, delivery command.DeliveryRecord, root string) (string, error) {
	if delivery.Worktree == "" {
		return root, nil
	}
	checkout := delivery.WorktreePath
	if checkout == "" {
		resolved, err := hc.WorktreeCheckout(delivery.Worktree)
		if err != nil {
			return "", fmt.Errorf(
				"worktree %s: %w; its checks must run in the tree the dispatch worked in, so fix the workspace or close with --abandoned --reason \"<why>\"",
				delivery.Worktree, err)
		}
		checkout = resolved
	}
	if info, err := os.Stat(checkout); err != nil || !info.IsDir() {
		return root, nil
	}
	return checkout, nil
}

// tearDownPane closes the pane a delivery split open, and returns a warning for
// a pane herdr would not close.
//
// The pane is all the delivery owns: the worker sits in a sibling pane of the
// cap's own tab, so the tab is the cap's and must never be closed with it. A
// pane that is already gone is the goal state, not a failure.
//
// This is machinery, not a declared step: every dispatch has a pane, and a
// playbook that forgot to close one would leak a terminal with nothing left to
// catch it. The worktree the dispatch ran in is the other case — its removal is
// a git action, so it is declared by the cleanup playbook and run by runCleanup
// above.
func tearDownPane(hc HerdrCLI, paneID string) string {
	if err := hc.ClosePane(paneID); err != nil && !errors.Is(err, ErrPaneGone) {
		return fmt.Sprintf("could not close pane %s: %v — close it by hand", paneID, err)
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

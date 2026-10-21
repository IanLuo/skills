// Close: the close-out of a dispatch.
//
// It is one evidence line and a teardown, and nothing else. Close refuses a
// worker node that is not done, refuses a dispatch whose tree has not moved past
// the revision it was prepared at, refuses a review-shaped close with no
// decision, closes the pane the delivery split open, and marks the cap's node
// done with the evidence on it.
//
// The deliverable check is the one the old exit script answered, reduced to the
// question git can answer: HEAD has moved past the revision the project stood at
// when the dispatch was prepared. It is deliberately not a clean-tree check — the
// tree is routinely dirty with another agent's work — and it does not read the
// paths a card declares, which was the trap the script's second half was. Whether
// the code works is the suite's answer, and the suite result is the cap's own
// line, recorded with the sha.
//
// The pane is machinery, because every dispatch has one. A worktree's removal is
// a git action the cap or the batch performs, not something a close declares.
package dispatch

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/engine"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/registry"
	"github.com/flagship-dev/flagship/internal/store"
)

// CloseRequest is the cap's close-out of one dispatch.
type CloseRequest struct {
	NodeID    string // the cap's dispatch node
	Worker    string // optional "<project>:<node>"; must match the delivery record when given
	Decision  string // the cap's verdict, recorded on the cap node
	Abandoned bool   // close without the delivery, worker and deliverable reads
	Reason    string // why it is being abandoned; required with Abandoned
}

// CloseResult is the close-out's data payload. Worker names the node this
// close-out rested on, so the cap can see which one it just signed off. Evidence
// is the line recorded on the node: where HEAD stands, and the cap's verdict.
//
// TornDown names what close-out itself left gone. That is the pane alone.
type CloseResult struct {
	NodeID   string   `json:"node_id"`
	Decision string   `json:"decision"`
	Evidence string   `json:"evidence,omitempty"`
	Worker   string   `json:"worker,omitempty"`
	PaneID   string   `json:"pane_id,omitempty"`
	TornDown []string `json:"torn_down,omitempty"`
	Warning  string   `json:"warning,omitempty"`
}

// Close closes out a dispatch node. It refuses — naming what is missing — when
// the node is not a dispatch, when no delivery was recorded (or, with
// --abandoned, no reason was given), when the worker's node is missing or not
// done, when the tree has not moved past the revision the dispatch was prepared
// at, or when no verdict was given. On success it records one evidence line on
// the node, closes the worker's pane, and marks the node done.
//
// The worker's node comes from the delivery record, not from the caller: --worker
// is optional and, when given, must name the recorded node. A record written
// before the link existed carries no node, and is the one case --worker is still
// the only way to name it.
//
// --abandoned skips the delivery, worker and deliverable reads and records an
// abandonment — "abandoned, never delivered: <reason>" when no delivery was
// recorded, and "abandoned after delivery to <project>:<node>: <reason>" when one
// was. It skips only the reads, never the teardown: abandoning is a decision
// about the work, not a licence to leak a pane. The record is written after the
// teardown, so it can only claim what actually happened.
func Close(h *command.Handler, hc engine.Engine, reg *registry.Registry, req CloseRequest) command.Response {
	if req.NodeID == "" {
		return errResp("--node is required: fs close --node <cap node> ...")
	}

	node, err := capNode(h, req.NodeID)
	if err != nil {
		return errResp("close: " + err.Error())
	}
	if node.Kind == query.KindBatch {
		return closeBatch(h, req)
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
		// The record is still read on this path, though none of its reads apply:
		// a dispatch can be delivered and then abandoned — a worker that never
		// finished, a request withdrawn — and the record is what names the pane to
		// tear down. No record is the never-delivered case, where nothing was
		// opened and there is nothing to clean.
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
		delivered = true

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
			refusal, err := workerNotDone(h, workerProject, workerID, workerRef, worker.Status)
			if err != nil {
				return errResp(fmt.Sprintf("close: read reports for %s: %v", workerRef, err))
			}
			return errResp(refusal)
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

	var warnings []string
	if delivered && !req.Abandoned {
		evidence, err := deliverable(h, hc, reg, delivery, req.NodeID)
		if err != nil {
			return errResp("close: " + err.Error())
		}
		result.Evidence = evidence
	}

	// The pane is machinery, because every dispatch has one and a dispatch that
	// forgot it would leak a terminal with nothing left to catch it. Closing is a
	// claim; the read-back is the fact — see tearDownPane.
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
		// dispatch was abandoned, and which resource was really removed. A record
		// written before the teardown would assert a clearance that had not run
		// yet.
		decision = abandonedDecision(delivery, delivered, req.Reason, result.TornDown, result.Warning)
	}
	result.Decision = decision

	// The evidence is its own line, recorded before the verdict: what was
	// measured, then what the cap concluded about it. Keeping them apart is what
	// lets a reader tell a close whose deliverable was checked from one whose was
	// not, without reading the verdict for a hint.
	if result.Evidence != "" {
		if err := recordDecision(h, req.NodeID, result.Evidence); err != nil {
			return errResp("close: " + err.Error())
		}
	}
	if resp := h.TaskUpdate(capScope, req.NodeID, "done", decision, nil); !resp.OK {
		return errResp("mark node done: " + resp.Error)
	}
	return command.Response{OK: true, Data: result}
}

// closeBatch closes out a batch node. A batch has no delivery and no worker of
// its own — it is the parent its members hang from — so the delivery, worker and
// deliverable reads do not apply. What does apply is the batch's own question:
// every member has a done integration naming it. It is the same read fs batch
// makes, so a close can never disagree with it.
func closeBatch(h *command.Handler, req CloseRequest) command.Response {
	if req.Decision == "" {
		return errResp("--decision is required: record the verdict this close-out rests on")
	}

	decision := req.Decision
	if req.Abandoned {
		if req.Reason == "" {
			return errResp("--abandoned requires --reason: say why the batch is being abandoned")
		}
		decision = "abandoned, never delivered: " + req.Reason
	} else if resp := Batch(h, req.NodeID); !resp.OK {
		return errResp("close: " + resp.Error)
	}

	if resp := h.TaskUpdate(capScope, req.NodeID, "done", decision, nil); !resp.OK {
		return errResp("mark node done: " + resp.Error)
	}
	return command.Response{OK: true, Data: CloseResult{NodeID: req.NodeID, Decision: decision}}
}

// deliverable is the close's one check, and the line it records: where the tree
// the dispatch worked in stands now. It is read by a stranger, so it leads with
// the sha and names the base it was measured against. The cap's verdict —
// which is where the suite result belongs, because the cap is what ran the suite
// — is recorded with it, as the node's own decision.
//
// It refuses when HEAD has not moved past the revision the dispatch was prepared
// at, because that is the one thing git can answer about whether the work reached
// the repository. It stands down — and says so — when there is no revision to
// measure: a project that was not a git repository when the dispatch was prepared,
// or an unborn HEAD, is a missing input, not a change that failed to land.
func deliverable(h *command.Handler, hc engine.Engine, reg *registry.Registry, delivery command.DeliveryRecord, nodeID string) (string, error) {
	base, err := baseRevision(h, nodeID)
	if err != nil {
		return "", err
	}
	proj, err := reg.Get(delivery.Project)
	if err != nil {
		return "", fmt.Errorf("the project %s is not registered, so there is no tree to measure the deliverable in: %v", delivery.Project, err)
	}
	tree, err := checkDir(hc, delivery, proj.RootPath)
	if err != nil {
		return "", err
	}
	head := command.DetectCommitSHAIn(tree)

	if base == "" || head == nil {
		// The two missing-input cases, named rather than smoothed over: nothing
		// measured, and the line says so instead of implying a check passed.
		where := "the dispatch recorded no base revision"
		if head == nil {
			where = tree + " is not a git repository with a commit"
		}
		return fmt.Sprintf("HEAD not measured (%s) — the deliverable was not checked", where), nil
	}
	if *head == base {
		return "", fmt.Errorf(
			"%s has not moved past %s, the revision the dispatch was prepared at: nothing reached the repository, so there is no deliverable to close. Commit the work (or close with --abandoned --reason \"<why>\" to record that it did not land)",
			tree, base)
	}
	return fmt.Sprintf("HEAD %s (base %s) — the deliverable reached the repository", *head, base), nil
}

// baseRevision reads the revision the dispatch's project stood at when the
// dispatch was prepared: the commit_sha the store recorded on the cap node's own
// task-created event.
//
// It is read rather than recorded on the node because the store already records
// it, immutably, for every dispatch that ever existed — every event carries the
// SHA of the project it is about (ARCHITECTURE R4) — so the revision a change
// began at needs no field of its own, and no dispatch is left without one by an
// upgrade. A dispatch prepared a year ago and one prepared a minute ago are
// measured against the same fact.
//
// Empty means the project was not a git repository when the dispatch was
// prepared, or its HEAD was unborn. Those are the two cases where there is no
// revision to be had, and the deliverable check stands down on them rather than
// refusing: a missing input is not a change that failed to reach the repository.
func baseRevision(h *command.Handler, nodeID string) (string, error) {
	created := store.TaskCreated
	resp := h.Log(capScope, &nodeID, &created)
	if !resp.OK {
		return "", fmt.Errorf("read the revision %s was prepared at: %s", nodeID, resp.Error)
	}
	result, ok := resp.Data.(command.LogResult)
	if !ok {
		return "", fmt.Errorf("read the revision %s was prepared at: the log carried no events", nodeID)
	}
	for _, event := range result.Events {
		if event.CommitSHA != nil && *event.CommitSHA != "" {
			return *event.CommitSHA, nil
		}
	}
	return "", nil
}

// abandonedDecision is the durable record of an abandoned close, written after
// the teardown so it states what actually happened rather than what was about
// to. A dispatch with no delivery record was never delivered. One with a record
// was delivered, and the record names it and what close-out itself left gone —
// the pane. A teardown that could not finish says so instead of claiming a
// clean-up it did not do.
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

// checkDir returns the directory the deliverable check runs in: the worktree the
// dispatch worked in when the record names one that is still there, and the
// worker project's root otherwise.
//
// The worktree comes from the delivery record, not from herdr: the recorded path
// is the tree the dispatch itself worked in — resolved from herdr at delivery
// time — and it outlives the workspace, which is exactly the case that matters. A
// record written before the path was recorded falls back to resolving the
// workspace, and one that cannot be resolved is an error rather than a fall back
// to the project root: a revision is about one particular tree, and the root is
// routinely another agent's dirty checkout.
//
// A worktree that is already gone is the third case, and it is not a refusal: the
// tree the check is about does not exist, so its footing falls back to the project
// root. Refusing instead would make a retry — or a close after a partial teardown
// — impossible.
func checkDir(hc engine.Engine, delivery command.DeliveryRecord, root string) (string, error) {
	if delivery.Worktree == "" {
		return root, nil
	}
	checkout := delivery.WorktreePath
	if checkout == "" {
		resolved, err := hc.WorktreeCheckout(delivery.Worktree)
		if err != nil {
			return "", fmt.Errorf(
				"worktree %s: %w; the deliverable check must read the tree the dispatch worked in, so fix the workspace or close with --abandoned --reason \"<why>\"",
				delivery.Worktree, err)
		}
		checkout = resolved
	}
	if info, err := os.Stat(checkout); err != nil || !info.IsDir() {
		return root, nil
	}
	return checkout, nil
}

// tearDownPane closes the pane a delivery split open, reads it back to confirm
// it is gone, and returns a warning when it is not.
//
// The pane is all the delivery owns: the worker sits in a sibling pane of the
// cap's own tab, so the tab is the cap's and must never be closed with it. A
// pane that is already gone is the goal state, not a failure.
//
// Closing is a claim; the read-back is the fact. Trusting a return code is the
// mistake this path keeps making — the same error-mapped "already gone" that made
// a worktree removal report success over a checkout still on disk — and the pane
// is machinery, so its verification belongs in machinery too. A pane herdr still
// reports after the close is a warning, never a claim that it is gone.
func tearDownPane(hc engine.Engine, paneID string) string {
	if err := hc.ClosePane(paneID); err != nil && !errors.Is(err, engine.ErrPaneGone) {
		return fmt.Sprintf("could not close pane %s: %v — close it by hand", paneID, err)
	}
	exists, err := hc.PaneExists(paneID)
	if err != nil {
		return fmt.Sprintf("could not verify pane %s is gone: %v — check it by hand", paneID, err)
	}
	if exists {
		return fmt.Sprintf("pane %s survived the close — close it by hand", paneID)
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

// workerNotDone is the refusal a close gets when its worker's node is not done.
//
// Two conditions wear this one message and they are not the same fact. A worker
// still going has not finished and the cap should let it work. A worker that
// reported done and went idle *has* finished — what is unmarked is its node — and
// a message that told the cap to wait for work that was already over cost four
// round trips in a day, each one a cap telling an idle worker to run
// `fs task update --status done`.
//
// The status is deliberately not moved for a report: the node is the record of
// the work and the cap closes the dispatch, so a worker saying "done" must not
// mark its own node done. What the refusal owes the reader instead is the real
// condition and the one command that ends it.
//
// Only a done report counts. A question or a note is a worker that has not
// finished, and reading either as a completion would be the same misdescription
// in the other direction.
func workerNotDone(h *command.Handler, project, nodeID, ref, status string) (string, error) {
	reports, err := h.ReportsFor(project, nodeID)
	if err != nil {
		return "", err
	}
	for _, r := range reports {
		if r.Kind == command.ReportDone {
			return fmt.Sprintf(
				"worker %s is %s but has reported done — a report is not the status, and this close reads the node: mark the node done (fs task update %s --status done), or close with --abandoned",
				ref, status, ref), nil
		}
	}
	return fmt.Sprintf(
		"worker %s is %s — read its node and let it finish, or close with --abandoned",
		ref, status), nil
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

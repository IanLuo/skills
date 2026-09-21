// Close: the close-out gate for a dispatch.
//
// The gate exists because a cap that trusts its own in-context testing never
// reads the worker's node. It can check that the worker's node exists and is
// done, and that a verdict was recorded. It cannot check that the cap read that
// node. Do not pretend otherwise: the gate makes the omission visible and
// expensive, it does not make it impossible.
//
// Close-out runs the exit phase of the plan the dispatch recorded, and gates on
// the dispatch's own trace. The gate runs in two passes, split by the teardown
// because the teardown is what it protects: before any exit step runs, every
// entry step and every work handover must have a line in the trace; after they
// run, every exit step must have one too. A step with no line is a step that did
// not happen, and the refusal names it — playbook, step, kind.
//
// What the trace proves is bounded, and every refusal says so: it records that a
// playbook was loaded and that a step ran. It can never record that the worker
// obeyed. There is no `followed` status, and there is no way to add one.
//
// The pane is machinery, because every dispatch has one. The worktree the worker
// ran in is not: removing it and deleting its branch are actions the exit
// playbook declares as do steps, so each protocol owns its own teardown instead
// of sharing one hardcoded path.
package dispatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/registry"
	"github.com/flagship-dev/flagship/internal/trace"
)

// CloseRequest is the cap's close-out of one dispatch.
type CloseRequest struct {
	NodeID    string // the cap's dispatch node
	Worker    string // optional "<project>:<node>"; must match the delivery record when given
	Decision  string // the cap's verdict, recorded on the cap node
	Confirm   bool   // the cap has answered the plan's ask steps
	Abandoned bool   // close without the delivery, worker, and trace gates
	Reason    string // why it is being abandoned; required with Abandoned
}

// CloseResult is the close-out's data payload. Worker names the node this
// close-out rested on, so the cap can see which one it just signed off. Cleanup
// says what the exit phase did — including that it was empty, which must be
// visible rather than silent. Trace names the file the gate read, so the record
// it gated on is one path away.
//
// TornDown names what close-out itself left gone. That is now the pane alone:
// removing a worktree is an action the exit playbook declares, and close-out
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
	Trace    string   `json:"trace,omitempty"`
	Cleanup  string   `json:"cleanup,omitempty"`
	TornDown []string `json:"torn_down,omitempty"`
	Actions  []string `json:"actions,omitempty"`
	Warning  string   `json:"warning,omitempty"`
}

// Close closes out a dispatch node. It refuses — naming what is missing — when
// the node is not a dispatch, when no delivery was recorded (or, with
// --abandoned, no reason was given), when the worker's node is missing or not
// done, when no verdict was given, or when the trace does not account for every
// step of the plan. On success it runs the plan's exit phase, closes the
// worker's pane, and marks the cap node done with the verdict.
//
// The worker's node comes from the delivery record, not from the caller: --worker
// is optional and, when given, must name the recorded node. A record written
// before the link existed carries no node, and is the one case --worker is still
// the only way to name it.
//
// --abandoned skips the delivery and worker gates and records an abandonment —
// "abandoned, never delivered: <reason>" when no delivery was recorded, and
// "abandoned after delivery to <project>:<node>: <reason>" naming the pane and
// worktree the teardown removed when one was. It skips the trace gate too, but
// not the teardown: the delivery record, when there is one, is still read, its
// plan's declared actions still run, and what it names is still removed.
// Abandoning is a decision about the work, not a licence to leak a pane — a
// delivered dispatch closed this way is still a successful close, and its pane
// and worktree would otherwise outlive it silently. The record is written after
// the teardown, so it can only claim what actually happened.
//
// reg, kc, lg, and hc are how the exit phase finds its footing: kc holds the
// playbooks the plan names, reg says which project root the declared actions run
// in, hc resolves the tree the checks run in when the record names a worktree
// but no path, and lg is the trace both the gate and the record read. All are
// needed only after the worker's node is known to be done, so a refusal never
// reads any of them.
func Close(h *command.Handler, hc HerdrCLI, reg *registry.Registry, kc *knowledge.Center, lg *trace.Log, req CloseRequest) command.Response {
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
	if delivered {
		result.Trace = lg.Path(delivery.Project, req.NodeID)
		// The plan resolved both scopes at dispatch time, so its exit playbooks
		// may live in the target project's KB. Close resolves the same way — the
		// dispatch's project, not the cap's terminal — or a project-scoped exit
		// gate would be invisible to the only command that runs it.
		kc = kc.InProject(delivery.Project)
	}

	// The plan the dispatch recorded is what this close-out is measured against:
	// which playbooks ran at entry, which were handed to the worker, and which
	// own the exit phase. It is read from the store, not from the trace, so a
	// trace that has been damaged is a trace the gate can refuse on rather than
	// an exit phase nobody can find.
	plan, err := recordedPlan(h, req.NodeID)
	if err != nil {
		if !req.Abandoned {
			return errResp("close: " + err.Error())
		}
		plan = nil
	}

	// The trace gate's first pass runs before any exit step, because the
	// teardown is what it protects: a dispatch whose own record says a step never
	// ran is not one to tear down, and a refusal here has to leave everything
	// standing so it can be finished. A refusal above returned before this point,
	// and so does a refusal here — the teardown below is only ever reached by a
	// close that is going to succeed.
	if !req.Abandoned {
		if err := gateEntry(h, kc, lg, delivery, req.NodeID, plan, req.Confirm); err != nil {
			return errResp(err.Error())
		}
	}

	run, err := runExit(hc, reg, kc, lg, delivery, req.NodeID, node.Integrates, plan, req.Confirm, req.Abandoned)
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

	// The second pass reads the trace back after the exit steps ran. It is what
	// makes the file trustworthy as an account of the close rather than a
	// by-product of it: a close that leaves the exit phase unrecorded would leave
	// a trace that reads as a dispatch which was never closed.
	if !req.Abandoned {
		if err := gateExit(kc, lg, delivery.Project, req.NodeID, plan); err != nil {
			return errResp(err.Error())
		}
	}

	// The pane is machinery; the worktree is not. The split is the point:
	// universal invariants are machinery, per-type actions are declared.
	//
	// Every dispatch has a pane, and a playbook that forgot to close it would leak
	// a terminal with nothing left to catch it — so closing it is coded here, for
	// every protocol. A worktree is different: removing a tree and deleting its
	// branch are git actions, they differ by task type, and the exit playbook
	// declares them as do steps. close-out runs those steps and reports them; it
	// does not own them, and it cannot verify what a shell command did.
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

// recordedPlan reads the plan a dispatch recorded at preparation time. A
// dispatch with no plan was never prepared by a binary that gated on one, and
// close says so instead of gating on an empty set: an exit phase nobody can find
// is not the same dispatch as one that declared no exit phase.
func recordedPlan(h *command.Handler, nodeID string) ([]trace.PlanEntry, error) {
	payload, ok, err := h.PlanFor(capScope, nodeID)
	if err != nil {
		return nil, fmt.Errorf("read the recorded plan: %v", err)
	}
	if !ok {
		return nil, fmt.Errorf(
			"no plan is recorded for %s — it was not prepared by a dispatch that records one, so there is nothing to gate the close against", nodeID)
	}
	var plan trace.Plan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return nil, fmt.Errorf("read the recorded plan: %v", err)
	}
	return plan.Plan, nil
}

// abandonedDecision is the durable record of an abandoned close, written after
// the teardown so it states what actually happened rather than what was about
// to. A dispatch with no delivery record was never delivered. One with a record
// was delivered, and the record names it and what close-out itself left gone —
// the pane — plus how many declared exit actions ran. A teardown that could not
// finish says so instead of claiming a clean-up it did not do.
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
		decision += fmt.Sprintf(" (%d exit %s ran)", len(actions), word)
	}
	if warning != "" {
		decision += " (teardown reported: " + warning + ")"
	}
	return decision
}

// planStep is one step of one playbook in a dispatch's plan, carrying the
// playbook it came from and its 1-based position there — the pair that makes up
// its trace id, and so the pair a refusal has to name.
type planStep struct {
	Playbook string
	Index    int
	Kind     string
	Body     string
}

// flatten resolves one phase of the plan to its steps, in plan order. A playbook
// the plan names but the knowledge centre cannot read is an error, never a skip:
// a gate that fails open is worse than no gate at all.
//
// The steps come from the playbook as it stands now, not from the plan line,
// which records a playbook's name and order but not its steps. That is
// deliberate: a playbook edited between dispatch and close is refused by the
// trace gate — its steps no longer line up with the lines already written —
// rather than silently running a different protocol than the one dispatched.
func flatten(kc *knowledge.Center, plan []trace.PlanEntry, phase string) ([]planStep, error) {
	var steps []planStep
	for _, entry := range plan {
		if entry.Phase != phase {
			continue
		}
		pb, err := kc.Get(entry.Name)
		if err != nil {
			return nil, fmt.Errorf(
				"the plan names %s playbook %s, which cannot be read: %v", phase, entry.Name, err)
		}
		for i, step := range pb.Steps {
			steps = append(steps, planStep{
				Playbook: entry.Name,
				Index:    i + 1,
				Kind:     step.Kind,
				Body:     step.Body,
			})
		}
	}
	return steps, nil
}

// lastLines indexes a trace by step id, keeping the last line written for each.
// A step can carry more than one — an ask recorded `skipped` at dispatch time and
// `confirmed` at close — and the last one is what the step stands as.
func lastLines(lines []trace.Line) map[string]trace.Line {
	last := map[string]trace.Line{}
	for _, ln := range lines {
		if ln.ID != "" {
			last[ln.ID] = ln
		}
	}
	return last
}

// Deviations is the whole comparison the trace makes against the plan: every
// step of every phase it does not account for, in plan order. It is one function
// because it is one question — `fs trace` reports it, and the close gate makes
// the same comparison in two passes, split by the teardown.
//
// A step is accounted for when it carries a line with an outcome the gate
// accepts: a check or do that passed (or that the cap confirmed), an ask the cap
// answered. A `say` is prose the brief or the report carried, and nothing about
// it is a gate. A work playbook is accounted for by its handover line — the
// `loaded` line Deliver writes — because that line is the whole of the record
// that the brief the worker received is the brief the plan named.
//
// Deviations reads; it never writes and never refuses. The caller decides what a
// gap means, which is why the same list can be a refusal at close and a report
// in `fs trace`.
func Deviations(kc *knowledge.Center, plan []trace.PlanEntry, lines []trace.Line, nodeID string) ([]string, error) {
	return phaseDeviations(kc, plan, lines, nodeID, knowledge.PhaseEntry, knowledge.PhaseExit)
}

// phaseDeviations is Deviations over a subset of the plan's phases, which is how
// the close gate checks the entry phase before the teardown and the exit phase
// after it.
func phaseDeviations(kc *knowledge.Center, plan []trace.PlanEntry, lines []trace.Line, nodeID string, phases ...string) ([]string, error) {
	last := lastLines(lines)
	var gaps []string

	for _, phase := range phases {
		steps, err := flatten(kc, plan, phase)
		if err != nil {
			return nil, err
		}
		for _, step := range steps {
			if step.Kind == knowledge.KindSay {
				continue
			}
			line, recorded := last[trace.StepID(nodeID, step.Playbook, step.Index)]
			if !recorded {
				gaps = append(gaps, gapText(step, ""))
				continue
			}
			if step.Kind == knowledge.KindAsk {
				if line.Status != trace.StatusConfirmed {
					gaps = append(gaps, gapText(step, line.Status))
				}
				continue
			}
			if line.Status != trace.StatusPass && line.Status != trace.StatusConfirmed {
				gaps = append(gaps, gapText(step, line.Status))
			}
		}
	}

	for _, entry := range plan {
		if entry.Phase != knowledge.PhaseWork {
			continue
		}
		line, recorded := last[trace.StepID(nodeID, entry.Name, 0)]
		if !recorded || line.Kind != trace.KindLoaded {
			gaps = append(gaps, fmt.Sprintf(
				"work playbook %s: no `loaded` line — the brief the worker received is not on the record", entry.Name))
		}
	}
	return gaps, nil
}

// gateEntry is the trace gate's first pass, and it runs before any exit step:
// the teardown must not start over a dispatch whose own record says a step never
// ran, and a refusal here has to leave everything standing so the work can be
// finished.
//
// An entry ask the cap left unanswered at dispatch time was recorded `skipped`,
// and --confirm is what answers it here — the same flag that answers the exit
// asks, at the same point in the close, so there is one place to answer a
// question and never two. A check is different: a failed one was the cap's to
// accept when it dispatched, and a close is not where it gets accepted.
func gateEntry(h *command.Handler, kc *knowledge.Center, lg *trace.Log, delivery command.DeliveryRecord, nodeID string, plan []trace.PlanEntry, confirm bool) error {
	lines, err := lg.Read(delivery.Project, nodeID)
	if err != nil {
		return fmt.Errorf("close: %v", err)
	}

	if confirm {
		lines, err = confirmAsks(h, kc, lg, delivery, nodeID, plan, knowledge.PhaseEntry, lines)
		if err != nil {
			return fmt.Errorf("close: %v", err)
		}
	}

	gaps, err := phaseDeviations(kc, plan, lines, nodeID, knowledge.PhaseEntry)
	if err != nil {
		return fmt.Errorf("close: %v", err)
	}
	if len(gaps) > 0 {
		return traceGapError(nodeID, gaps)
	}
	return nil
}

// confirmAsks answers every unanswered ask of one phase: the cap's --confirm is
// the answer, the trace records it, and the node carries the decision. It
// returns the lines with the new ones appended, so the caller's comparison sees
// the dispatch as it now stands rather than as it was when it was read.
//
// Only an ask is answered this way. A `skipped` check is not a question, and a
// close that quietly turned one into a pass would be the gate agreeing with
// itself.
func confirmAsks(h *command.Handler, kc *knowledge.Center, lg *trace.Log, delivery command.DeliveryRecord, nodeID string, plan []trace.PlanEntry, phase string, lines []trace.Line) ([]trace.Line, error) {
	steps, err := flatten(kc, plan, phase)
	if err != nil {
		return nil, err
	}
	last := lastLines(lines)

	for _, step := range steps {
		if step.Kind != knowledge.KindAsk {
			continue
		}
		if line, recorded := last[trace.StepID(nodeID, step.Playbook, step.Index)]; recorded && line.Status == trace.StatusConfirmed {
			continue
		}
		line := trace.Line{
			ID:       trace.StepID(nodeID, step.Playbook, step.Index),
			Dispatch: nodeID,
			Playbook: step.Playbook,
			Phase:    phase,
			Step:     step.Index,
			Kind:     step.Kind,
			Status:   trace.StatusConfirmed,
			Detail:   step.Body,
		}
		if err := lg.Append(delivery.Project, line); err != nil {
			return nil, err
		}
		if err := recordDecision(h, nodeID, "user confirmed "+phase+" step: "+step.Body); err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// gateExit is the trace gate's second pass. It reads the trace back after the
// exit phase ran and refuses if any of it is unrecorded, so a trace left behind
// by a close always reads as a dispatch that was closed — not as one that was
// prepared and then vanished.
//
// It re-checks the entry phase as well, which the first pass already cleared.
// That is not redundancy for its own sake: the two passes are one gate split by
// the teardown, and a gate that only ever verified the half it was looking at
// would let a close pass on a plan it never fully read.
func gateExit(kc *knowledge.Center, lg *trace.Log, project, nodeID string, plan []trace.PlanEntry) error {
	lines, err := lg.Read(project, nodeID)
	if err != nil {
		return fmt.Errorf("close: %v", err)
	}
	gaps, err := phaseDeviations(kc, plan, lines, nodeID, knowledge.PhaseEntry, knowledge.PhaseExit)
	if err != nil {
		return fmt.Errorf("close: %v", err)
	}
	if len(gaps) > 0 {
		return traceGapError(nodeID, gaps)
	}
	return nil
}

// gapText names one step the trace does not account for, in the terms the
// refusal has to use: which playbook, which step, which kind, and what the trace
// has there instead.
func gapText(s planStep, status string) string {
	switch status {
	case "":
		return fmt.Sprintf("%s step %d (%s): no line in the trace — a step with no line is a step that did not run",
			s.Playbook, s.Index, s.Kind)
	case trace.StatusSkipped:
		return fmt.Sprintf("%s step %d (%s): recorded skipped, not answered — answer it, then re-run with --confirm",
			s.Playbook, s.Index, s.Kind)
	case trace.StatusFail:
		return fmt.Sprintf("%s step %d (%s): recorded a failure — the dispatch should not have been delivered past it",
			s.Playbook, s.Index, s.Kind)
	default:
		return fmt.Sprintf("%s step %d (%s): recorded %q, which is not an outcome this gate accepts",
			s.Playbook, s.Index, s.Kind, status)
	}
}

// traceGapError is the refusal an incomplete trace earns. It says what the trace
// does and does not prove, because a cap that reads "the trace is incomplete" as
// "the worker disobeyed" has learned exactly the wrong lesson from it.
func traceGapError(nodeID string, gaps []string) error {
	return fmt.Errorf(
		"close: the trace for %s does not account for the plan — %s. The trace records what the dispatch loaded and ran, not what the worker obeyed, so a step with no line is a step that did not happen here. The dispatch cannot be closed as if it had: re-dispatch the work, or close with --abandoned --reason \"<why>\" to record that it did not",
		nodeID, strings.Join(gaps, "; "))
}

// exitRun is what the exit phase did: the note the response reports, the
// decisions its ask steps earn, the declared do steps that ran in order, and —
// on the abandoned path only — the actions that could not finish.
type exitRun struct {
	Note          string
	Confirmations []string
	Actions       []string
	Warnings      []string
}

// runExit runs the exit phase of the plan: the playbooks whose phase is exit, in
// plan order, appending one line to the dispatch's trace per step.
//
// The order is the DECLARED order — the plan's, which is the order the playbooks
// declared — and each step's kind says what it does. check runs a shell command
// in the tree the dispatch worked in and refuses the close, fail-fast, on
// non-zero, naming the step and its output. ask is a question only the user can
// answer: it requires --confirm and is recorded per step. do runs a shell command
// in the project root and refuses the same way. say is prose the close reports.
//
// A playbook's preconditions are enforced first because it declares them first,
// and it can place a postcondition after an action, which is the whole point:
// nothing verified what an action achieved before, which is how a teardown
// reported success over a checkout that was still on disk.
//
// A refusal leaves the node open and returns before anything is torn down. A
// check's tree is resolved before the first check runs; a check whose tree a
// preceding action removed falls back to the project root, because a
// postcondition about a removed resource has no footing inside it.
//
// abandoned is the escape hatch, and it skips only the gates: checks and asks do
// not run, because a node being abandoned is not a node being gated. They are
// still recorded as `skipped`, because a trace with holes in it would read as a
// protocol that ran when it did not. The declared actions still run — abandoning
// is a decision about the work, not a licence to leak the tree it ran in — and
// one that fails is a warning, never a refusal, for the same reason: a stuck
// teardown must not keep a node open.
func runExit(hc HerdrCLI, reg *registry.Registry, kc *knowledge.Center, lg *trace.Log, delivery command.DeliveryRecord, capNodeID, integrates string, plan []trace.PlanEntry, confirm, abandoned bool) (exitRun, error) {
	steps, err := flatten(kc, plan, knowledge.PhaseExit)
	if err != nil {
		return exitRun{}, fmt.Errorf("close: %v", err)
	}
	if len(steps) == 0 {
		if abandoned {
			return exitRun{Note: "no exit phase: the dispatch was abandoned"}, nil
		}
		return exitRun{Note: "no exit gate: the plan declares no exit playbook"}, nil
	}

	// The project root is only needed by a check or a do: an exit phase of asks
	// and says alone needs no tree to run in. An abandoned close runs the do
	// steps but skips the checks, so it needs the root only for an action.
	needRoot := false
	for _, step := range steps {
		if step.Kind == knowledge.KindDo || (step.Kind == knowledge.KindCheck && !abandoned) {
			needRoot = true
		}
	}
	var proj registry.Project
	if needRoot {
		proj, err = reg.Get(delivery.Project)
		if err != nil {
			// A project whose registry row is gone leaves no root for a declared
			// action to run in. On the gated path that is a refusal — the gate could
			// not run. On the abandoned path it is a warning: abandoning is a
			// decision, and a teardown that cannot run must not keep the node open.
			if abandoned {
				return exitRun{
					Note:     fmt.Sprintf("exit phase skipped (abandoned): project %s is not registered, so its declared actions cannot run", delivery.Project),
					Warnings: []string{fmt.Sprintf("exit actions cannot run: project %s is not registered (%v)", delivery.Project, err)},
				}, nil
			}
			return exitRun{}, fmt.Errorf("close: exit phase cannot run: %v", err)
		}
	}

	// Exit steps get the same FS_* treatment entry checks do, so a gate can ask
	// about the dispatch it is gating: which project and task type it was, which
	// cap node is closing, which worker's node it rested on, which member it
	// integrates, and — for an action that has to remove a tree — the pane and
	// the worktree the delivery opened. FS_CARDS is exported empty — a close has
	// no batch to offer — and stays present rather than missing, so a step can
	// tell "this dispatch named no cards" from "this step has no env".
	env := exitEnv(delivery, capNodeID, integrates)

	var run exitRun
	checks, asks, says := 0, 0, 0
	names := playbookNames(steps)
	tree := "" // the tree the checks run in; resolved before the first check

	for i, step := range steps {
		id := trace.StepID(capNodeID, step.Playbook, step.Index)
		line := trace.Line{
			ID:       id,
			Dispatch: capNodeID,
			Worker:   delivery.Project + ":" + delivery.Node,
			Playbook: step.Playbook,
			Phase:    knowledge.PhaseExit,
			Step:     step.Index,
			Kind:     step.Kind,
			Detail:   step.Body,
		}

		switch step.Kind {
		case knowledge.KindSay:
			line.Status = trace.StatusLoaded
			says++

		case knowledge.KindCheck:
			if abandoned {
				line.Status = trace.StatusSkipped
				line.Detail = "abandoned: " + step.Body
				break
			}
			if tree == "" {
				resolved, err := checkDir(hc, delivery, proj.RootPath)
				if err != nil {
					return exitRun{}, fmt.Errorf("close: exit check %s: %v", step.Playbook, err)
				}
				tree = resolved
			}
			item := runCheck(step.Body, checkTree(tree, proj.RootPath), env)
			line.Status = item.Status
			if err := lg.Append(delivery.Project, line); err != nil {
				return exitRun{}, fmt.Errorf("close: %v", err)
			}
			if item.Status != "pass" {
				return exitRun{}, fmt.Errorf(
					"close: exit check failed (playbook %s step %d): %q; output: %s; fix it, then close again",
					step.Playbook, step.Index, step.Body, checkOutput(item))
			}
			checks++
			continue

		case knowledge.KindAsk:
			if abandoned {
				line.Status = trace.StatusSkipped
				line.Detail = "abandoned: " + step.Body
				break
			}
			if !confirm {
				// The refusal is written down before it is returned. `refused` is
				// the one status that records a step the gate would not accept an
				// answer for, and it is what makes `fs trace` after a refused close
				// say which step stopped it rather than showing a protocol that
				// simply stops.
				line.Status = trace.StatusRefused
				if err := lg.Append(delivery.Project, line); err != nil {
					return exitRun{}, fmt.Errorf("close: %v", err)
				}
				// Every ask from here on is surfaced, not just this one: the cap
				// should see the whole set it is being asked to confirm, across
				// every exit playbook the plan names. Refusing here, at the ask's
				// declared position, is what lets a declared action before it have
				// already run.
				return exitRun{}, fmt.Errorf(
					"close: exit phase %s has unconfirmed steps: %s; answer each, then re-run with --confirm",
					strings.Join(names, ", "), strings.Join(pendingAsks(steps[i:]), ", "))
			}
			line.Status = trace.StatusConfirmed
			run.Confirmations = append(run.Confirmations, "user confirmed exit step: "+step.Body)
			asks++

		case knowledge.KindDo:
			item := runStep(knowledge.KindDo, step.Body, proj.RootPath, env)
			line.Status = item.Status
			if err := lg.Append(delivery.Project, line); err != nil {
				return exitRun{}, fmt.Errorf("close: %v", err)
			}
			if item.Status != "pass" {
				if !abandoned {
					return exitRun{}, fmt.Errorf(
						"close: exit action failed (playbook %s step %d): %q; output: %s; fix it, then close again",
						step.Playbook, step.Index, step.Body, checkOutput(item))
				}
				run.Warnings = append(run.Warnings,
					fmt.Sprintf("exit action %q failed: %s", step.Body, checkOutput(item)))
			}
			run.Actions = append(run.Actions, step.Body)
			continue
		}

		if err := lg.Append(delivery.Project, line); err != nil {
			return exitRun{}, fmt.Errorf("close: %v", err)
		}
	}

	run.Note = exitNote(names, checks, len(run.Actions), asks, says, delivery.Worktree, abandoned)
	return run, nil
}

// pendingAsks lists the asks from a point in the exit phase onward, each naming
// the playbook it belongs to. The cap answers them all in one pass, but the
// refusal still happens where the first one sits, so a declared action before it
// has already run.
func pendingAsks(rest []planStep) []string {
	var asks []string
	for _, step := range rest {
		if step.Kind == knowledge.KindAsk {
			asks = append(asks, fmt.Sprintf("%s step %d: %q", step.Playbook, step.Index, step.Body))
		}
	}
	return asks
}

// playbookNames is the distinct playbooks a phase's steps came from, in the order
// they appear. It is how a note or a refusal names the protocols it is about
// rather than a single hardcoded name.
func playbookNames(steps []planStep) []string {
	var names []string
	seen := map[string]bool{}
	for _, step := range steps {
		if !seen[step.Playbook] {
			seen[step.Playbook] = true
			names = append(names, step.Playbook)
		}
	}
	return names
}

// checkTree is the directory a check runs in: the tree the dispatch worked in
// while it exists, and the project root once a declared action has removed it.
// A postcondition about a removed resource has no footing inside the resource —
// `test ! -e "$FS_WORKTREE_PATH"` cannot run with $FS_WORKTREE_PATH as its cwd —
// and the root is the tree that outlives the teardown.
func checkTree(tree, root string) string {
	if info, err := os.Stat(tree); err == nil && info.IsDir() {
		return tree
	}
	return root
}

// exitNote is the one-line account of the exit phase the response carries: which
// playbooks ran, how many checks passed, how many declared actions ran, how many
// asks were confirmed, and the tree the checks ran in. A gate whose report hides
// which of those happened is how "the cleanup ran" stopped meaning anything.
func exitNote(names []string, checks, actions, asks, says int, worktree string, abandoned bool) string {
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
	if says > 0 {
		parts = append(parts, fmt.Sprintf("%d say", says))
	}

	skipped := ""
	if abandoned {
		skipped = " skipped (abandoned)"
	}
	note := fmt.Sprintf("exit phase %s%s: %s", strings.Join(names, ", "), skipped, strings.Join(parts, ", "))
	if worktree != "" {
		note += " in worktree " + worktree
	}
	return note
}

// exitEnv is the closing dispatch's own inputs, exposed to every exit step as
// FS_*. It is deliberately narrower than the entry gate's env: a close has no
// --goal and no --cards to offer, so it exports FS_CARDS empty rather than
// inventing a value nobody supplied. FS_INTEGRATES is the member this
// integration merges, empty for a dispatch that merges nothing.
//
// FS_PANE, FS_WORKTREE, and FS_WORKTREE_PATH name what the delivery opened. They
// are what lets a declared action be about this dispatch's own tree — removing
// it, deleting its branch — without the mechanism knowing how.
func exitEnv(delivery command.DeliveryRecord, capNodeID, integrates string) []string {
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

// checkDir returns the directory the exit checks run in: the worktree the
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

// tearDownPane closes the pane a delivery split open, reads it back to confirm
// it is gone, and returns a warning when it is not.
//
// The pane is all the delivery owns: the worker sits in a sibling pane of the
// cap's own tab, so the tab is the cap's and must never be closed with it. A
// pane that is already gone is the goal state, not a failure.
//
// Closing is a claim; the read-back is the fact. Trusting a return code is the
// mistake this path keeps making — the same error-mapped "already gone" that
// made a worktree removal report success over a checkout still on disk — and the
// pane is machinery, so its verification belongs in machinery too. A pane herdr
// still reports after the close is a warning, never a claim that it is gone.
//
// This is machinery, not a declared step: every dispatch has a pane, and a
// playbook that forgot to close one would leak a terminal with nothing left to
// catch it. The worktree the dispatch ran in is the other case — its removal is
// a git action, so it is declared by the exit playbook and run by runExit above.
func tearDownPane(hc HerdrCLI, paneID string) string {
	if err := hc.ClosePane(paneID); err != nil && !errors.Is(err, ErrPaneGone) {
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

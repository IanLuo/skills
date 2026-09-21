// Package dispatch owns the cap's dispatch lifecycle for a task type: prepare
// the brief, deliver it to a worker, and close it out.
//
// A dispatch is measured against a plan: the playbooks whose applies_when the
// dispatch's tags satisfy, ordered by the order they declare. Prepare builds
// that plan once, records it on the cap node, runs the plan's `entry` steps
// against the target project's root, and renders the brief from the plan's
// `work` steps. Every step that runs appends a line to the dispatch's trace
// under ~/.fs/projects/<project>/logs/, which is what close-out gates against.
//
// Every check step sees the dispatch's own inputs as FS_* (see checkEnv), so a
// gate can ask about this dispatch — which cards — and not only about the world.
// Deliver (fs dispatch --deliver) is the only step that spawns: it splits a
// sibling pane of the cap's own, starts the agent in it, sends the brief,
// appends a `loaded` line per work playbook, and records the pane binding
// structurally. Close (fs close) is the close-out gate: it refuses when the
// trace does not account for the plan, refuses a worker node that is not done,
// runs the plan's `exit` steps, and closes the pane from the recorded binding.
//
// It also gates preparation: a failing entry check or an earlier dispatch that
// was never delivered stops it. Each gate has its own override — --confirm for a
// failing check or an unanswered ask, --allow-unresolved for an unresolved
// dispatch — so saying yes to one can never wave the other through.
package dispatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/registry"
	"github.com/flagship-dev/flagship/internal/trace"
)

// capScope is the project_id of the cap's own event table. It is implicit: a
// scope is just a string, so it is never registered and never created.
const capScope = "cap"

// SessionTags is the tag set the cap's own session runs under: the engine it
// dispatches through. A `phase: cap` playbook tagged for that engine loads into
// the session's standing rules.
func SessionTags() map[string]any {
	return map[string]any{engineTag: herdrEngine}
}

// engineTag is the derived tag naming the dispatcher.
const engineTag = "engine"

// typeTag is the derived tag naming the dispatch's task type.
const typeTag = "type"

// Item is one gate step in the brief's checklist: a check the dispatcher ran,
// or an ask only the cap can confirm. Body is the shell command for a check.
type Item struct {
	Kind   string `json:"kind"`   // check | ask | do
	Body   string `json:"body"`   // the command to run, or the question
	Status string `json:"status"` // pass | fail for a check, ? for an ask
	Output string `json:"output,omitempty"`
}

// Brief is the data payload of fs dispatch.
type Brief struct {
	Goal      string            `json:"goal"`
	Project   string            `json:"project"`
	RootPath  string            `json:"root_path"`
	TaskType  string            `json:"task_type"`
	Plan      []trace.PlanEntry `json:"plan"`
	Checklist []Item            `json:"checklist"`

	// Obligations and Skills are the work-phase playbooks' `say` and `use`
	// steps, in plan order. They are the only place a worker's standing
	// obligations live: the brief renders them, and nothing here is hardcoded,
	// so changing what every worker must do is a playbook edit rather than a
	// rebuild.
	Obligations []string `json:"obligations,omitempty"`
	Skills      []string `json:"skills,omitempty"`

	// Context is the entry-phase playbooks' `say` steps: what the gate wanted
	// the worker to know before it starts.
	Context     []string `json:"context,omitempty"`
	LockedDocs  []string `json:"locked_docs"`
	Notes       []string `json:"notes"`
	CapNodeID   string   `json:"cap_node_id"`
	NextCommand string   `json:"next_command"`

	// WorkerNode is the node created for the worker in the target project, as
	// "<project>:<node>". Deliver sets it before the brief is sent, because the
	// brief has to name the node the worker owns. Without --deliver no such node
	// exists and it stays empty.
	WorkerNode string `json:"worker_node,omitempty"`

	// Worktree is the herdr worktree workspace this dispatch runs in, when it
	// runs in one. Deliver records it so close-out can remove it: a worktree
	// must not be able to outlive a closed node. Empty for a dispatch that runs
	// in the project root.
	Worktree string `json:"worktree,omitempty"`

	// Delivery is the pane binding, set only when fs dispatch --deliver handed
	// the brief to a worker. It is recorded as a delivery-recorded event; this
	// field is the same binding echoed back to the caller.
	Delivery *command.DeliveryRecord `json:"delivery,omitempty"`
}

// PrepareRequest is what the cap asked a dispatch to be.
type PrepareRequest struct {
	Project    string
	TaskType   string
	Goal       string
	Cards      []string
	Integrates string

	// Tags are declared situation terms (`k` or `k=v`), on top of the derived
	// `type=<taskType>` and `engine=herdr`.
	Tags []string
	// Also forces a playbook into the plan; Without drops one the tags selected.
	Also    []string
	Without []string

	Confirm         bool
	AllowUnresolved bool
}

// Prepare builds a dispatch's plan, runs its entry steps, and composes the
// brief. It returns the CLI response envelope, so failures are reported the same
// way as every other command's.
//
// Integrates is the member's cap node an integration merges, or empty for a
// dispatch that integrates nothing. A non-empty value must name a cap-scope
// dispatch node: the link is recorded on this dispatch's own task-created
// payload, so it exists from creation and never depends on parsing a goal.
//
// It refuses in three cases rather than preparing work in a bad state: an
// earlier dispatch is still unresolved, a matched playbook is not legal for its
// phase, or an entry check failed. The gates have one override each:
// allowUnresolved for the unresolved dispatch, confirm for the failing check.
// Neither overrides the other — a cap answering "go ahead with the other
// dispatch open" has not seen, and so cannot have accepted, a failing entry
// check the other flag would have short-circuited.
func Prepare(h *command.Handler, reg *registry.Registry, kc *knowledge.Center, lg *trace.Log, req PrepareRequest) command.Response {
	if req.Project == "" {
		return errResp("--project is required")
	}
	if req.TaskType == "" {
		return errResp("--type is required")
	}
	if req.Goal == "" {
		return errResp("--goal is required")
	}

	tags, err := tagSet(req.TaskType, req.Tags)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}

	member, err := integrateMember(h, req.Integrates)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}

	proj, err := reg.Get(req.Project)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}

	// The plan is built from both scopes, project first: a dispatch into a
	// project runs that project's own protocols where it has them and the shared
	// ones everywhere else. This is the only place the two scopes meet — the
	// plan records the resolution, so nothing downstream re-derives it.
	kc = kc.InProject(proj.Name)

	plan, err := buildPlan(kc, tags, req.Also, req.Without)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}

	// Gate: a dispatch the cap prepared but never delivered is still open work.
	// Refuse to pile another one on top of it unless the cap says to proceed.
	// --allow-unresolved is this gate's only override: --confirm is about a
	// failing check and must not pass this one.
	//
	// The member this integration names is expected to be unresolved: a member's
	// dispatch is closed only after the integration it waits on is done, so it is
	// not the other open work this gate protects — it is what this dispatch is
	// about. Every other open dispatch still refuses.
	unresolved, err := unresolvedDispatches(h)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}
	unresolved = withoutNode(unresolved, member)
	if len(unresolved) > 0 && !req.AllowUnresolved {
		return errResp(unresolvedError(unresolved))
	}

	// The node comes first, because the trace is named after it and AC3 wants an
	// entry step's outcome on the record. It is not left behind by a refusal: a
	// refused entry gate rolls it back below, marked done with the refusal as
	// its decision, so the attempt is legible and no open dispatch is invented.
	nodeID, err := recordCapNode(h, req.TaskType, req.Goal, req.Project, member)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}

	recorded := trace.Plan{Plan: planEntries(plan), Tags: tags}
	if err := recordPlan(h, lg, nodeID, req.Project, recorded); err != nil {
		return errResp("dispatch: " + refuseEntry(h, nodeID, err.Error()))
	}

	env := checkEnv(proj.Name, req.TaskType, req.Goal, req.Cards, req.Integrates)
	run, err := runEntry(plan, proj.RootPath, env, lg, req.Project, nodeID, req.Confirm)
	if err != nil {
		return errResp("dispatch: " + refuseEntry(h, nodeID, err.Error()))
	}

	brief := buildBrief(run, plan, proj, req, nodeID)
	brief.NextCommand = nextCommand(brief)

	// Each override is recorded by the flag that granted it, on the node it
	// granted it for.
	if req.Confirm {
		if err := recordCheckOverrides(h, nodeID, brief); err != nil {
			return errResp(fmt.Sprintf("dispatch: %v", err))
		}
	}
	if req.AllowUnresolved && len(unresolved) > 0 {
		if err := recordUnresolvedOverride(h, nodeID, unresolved); err != nil {
			return errResp(fmt.Sprintf("dispatch: %v", err))
		}
	}

	return command.Response{OK: true, Data: brief}
}

// refuseEntry marks a dispatch node done after preparation refused past the
// point where it had to exist, and returns the refusal unchanged. The node's
// evidence stays readable — its plan and the trace beside it — without leaving
// an open dispatch that blocks the next one.
func refuseEntry(h *command.Handler, nodeID, msg string) string {
	if err := recordStatus(h, nodeID, "done", "dispatch refused at entry: "+msg); err != nil {
		return msg + fmt.Sprintf(" (the rollback could not be recorded: %v)", err)
	}
	return msg
}

// tagSet builds the dispatch's tag set: `type=<taskType>`, the derived engine,
// and every declared term. A declared term may not restate a derived one — a
// tag that silently moves `type` is a tag that silently changes which protocol
// runs.
func tagSet(taskType string, declared []string) (map[string]any, error) {
	tags := map[string]any{typeTag: taskType, engineTag: herdrEngine}

	for _, term := range declared {
		key, value, hasValue := strings.Cut(term, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("--tag %q names no tag", term)
		}
		if key == typeTag || key == engineTag {
			return nil, fmt.Errorf(
				"--tag %s restates a derived tag; %s is set by the dispatch itself and cannot be declared",
				key, key)
		}
		if hasValue {
			tags[key] = strings.TrimSpace(value)
			continue
		}
		tags[key] = true
	}
	return tags, nil
}

// planned is one playbook in a dispatch's plan: where it sits, and the protocol
// it resolved to, includes and all.
type planned struct {
	Name  string
	Phase string
	Order int
	Steps []knowledge.Step
}

// planEntries is the plan as it is recorded: the ordered playbooks without
// their steps.
func planEntries(plan []planned) []trace.PlanEntry {
	entries := make([]trace.PlanEntry, 0, len(plan))
	for _, pb := range plan {
		entries = append(entries, trace.PlanEntry{Name: pb.Name, Phase: pb.Phase, Order: pb.Order})
	}
	return entries
}

// buildPlan selects the playbooks whose applies_when the tags satisfy, orders
// them by the order they declare and then by name, and validates each one: a
// playbook whose steps are not legal for its phase is refused here, naming it,
// before it can run half of them.
//
// Cap-phase playbooks are not part of a dispatch's plan: they are the session's
// standing rules, selected by the session's tags, and a dispatch runs the
// entry, work and exit phases.
//
// --also names a playbook the tags did not select — a deliberate widening, so a
// name that does not resolve is refused rather than ignored. --without drops one
// they did select, and it has the last word.
func buildPlan(kc *knowledge.Center, tags map[string]any, also, without []string) ([]planned, error) {
	names, err := kc.List()
	if err != nil {
		return nil, err
	}

	chosen := map[string]planned{}
	for _, name := range names {
		pb, err := kc.Get(name)
		if err != nil {
			return nil, err
		}
		if pb.Phase == knowledge.PhaseCap || !pb.Matches(tags) {
			continue
		}
		if err := knowledge.Validate(pb); err != nil {
			return nil, err
		}
		chosen[name] = plannedOf(pb)
	}

	for _, name := range also {
		if _, ok := chosen[name]; ok {
			continue
		}
		pb, err := kc.Get(name)
		if err != nil {
			return nil, fmt.Errorf("--also %s: %w", name, err)
		}
		if pb.Phase == knowledge.PhaseCap {
			return nil, fmt.Errorf(
				"--also %s: a `phase: cap` playbook loads into the session's standing rules, not into a dispatch's plan", name)
		}
		if err := knowledge.Validate(pb); err != nil {
			return nil, err
		}
		chosen[name] = plannedOf(pb)
	}

	for _, name := range without {
		delete(chosen, name)
	}

	plan := make([]planned, 0, len(chosen))
	for _, pb := range chosen {
		plan = append(plan, pb)
	}
	sort.Slice(plan, func(i, j int) bool {
		if plan[i].Order != plan[j].Order {
			return plan[i].Order < plan[j].Order
		}
		return plan[i].Name < plan[j].Name
	})
	return plan, nil
}

func plannedOf(pb *knowledge.Playbook) planned {
	return planned{Name: pb.Name, Phase: pb.Phase, Order: pb.Order, Steps: pb.Steps}
}

// integrateMember resolves a --integrates value to the bare id of the cap-scope
// dispatch node it names, or to "" when the flag was not given.
//
// It refuses a node that does not exist, and one that is not a dispatch node,
// naming it: the link is written into the integration's task-created payload, and
// events are immutable, so a wrong link could never be corrected. "Is this a
// dispatch node" is the same question the dispatch gates ask, so it is asked the
// same way — from the node's structural kind, never from its goal text.
func integrateMember(h *command.Handler, integrates string) (string, error) {
	if integrates == "" {
		return "", nil
	}

	member, err := bareNodeID(integrates)
	if err != nil {
		return "", err
	}
	node, err := capNode(h, member)
	if err != nil {
		return "", fmt.Errorf("--integrates %s: %v", integrates, err)
	}
	if node.Kind != query.KindDispatch {
		return "", fmt.Errorf(
			"--integrates %s names a %q node, not a %s node — an integration integrates a member's dispatch node",
			integrates, node.Kind, query.KindDispatch)
	}
	return member, nil
}

// bareNodeID strips an optional "<scope>:" qualifier from a node reference. A
// node id is "t-<hex>" — a colon never appears in one — so a qualified id is
// unambiguously qualified. Only capScope is accepted: an integration's member
// lives in the cap's own scope.
func bareNodeID(ref string) (string, error) {
	if ref == "" {
		return "", errors.New("a member cap node is required")
	}
	scope, node, qualified := strings.Cut(ref, ":")
	if qualified {
		if scope != capScope {
			return "", fmt.Errorf("%s names scope %s; a member lives in scope %s", ref, scope, capScope)
		}
		if node == "" {
			return "", fmt.Errorf("%s names no node", ref)
		}
		return node, nil
	}
	return ref, nil
}

// Integrated answers the member's question: is there a done integration
// dispatch in the cap scope that names this member? It returns that dispatch's
// node id.
//
// It is the member's exit gate. An outcome — "HEAD is an ancestor of main" — is
// satisfied by a hand merge, so it cannot tell a dispatched integration from a
// bypass; a done integration node naming this member can. A pending one does
// not count: the member is not integrated until the integration's own gate has
// passed and its node is done.
func Integrated(h *command.Handler, member string) command.Response {
	ref, err := bareNodeID(member)
	if err != nil {
		return errResp("integrated: " + err.Error())
	}

	// cap has no registered root, and nothing here needs one: the read is of the
	// cap's own scope.
	resp := h.StatusIn(capScope, "")
	if !resp.OK {
		return errResp("integrated: " + resp.Error)
	}
	result, ok := resp.Data.(command.StatusResult)
	if !ok {
		return errResp("integrated: read scope " + capScope + ": no status payload")
	}

	for _, task := range result.Tasks {
		if task.Integrates == ref && task.Kind == query.KindDispatch && task.Status == "done" {
			return command.Response{OK: true, Data: IntegratedResult{Member: ref, IntegratedBy: task.NodeID}}
		}
	}
	return errResp(fmt.Sprintf(
		"integrated: no done integration dispatch in scope %s names member %s; merge it with a dispatched integration (fs dispatch --type integrate --integrates %s), never by hand",
		capScope, ref, ref))
}

// IntegratedResult is the data payload of fs integrated: which member was asked
// about, and the done integration dispatch that names it.
type IntegratedResult struct {
	Member       string `json:"member"`
	IntegratedBy string `json:"integrated_by"`
}

// withoutNode drops one node id from a list. It is how the member an
// integration names is kept out of the unresolved-dispatch gate it would
// otherwise fail: that gate is about other open work.
func withoutNode(nodes []command.UnfinishedNode, nodeID string) []command.UnfinishedNode {
	if nodeID == "" {
		return nodes
	}
	kept := make([]command.UnfinishedNode, 0, len(nodes))
	for _, node := range nodes {
		if node.NodeID != nodeID {
			kept = append(kept, node)
		}
	}
	return kept
}

// dispatchGoal is the goal text fs dispatch writes — on the cap's node, and on
// the worker's node in the target project. Both ends of the link carry the same
// text, so the two nodes read as one piece of work. It is prose for a reader:
// what makes a node a dispatch is its kind, never this text.
func dispatchGoal(taskType, goal string) string {
	return fmt.Sprintf("dispatch %s: %s", taskType, goal)
}

// unresolvedDispatches returns the cap's dispatch nodes that are not done. A
// node is a dispatch because it was written as one, so a dispatch whose goal
// was later edited is still recognised — the failure the goal prefix had.
func unresolvedDispatches(h *command.Handler) ([]command.UnfinishedNode, error) {
	nodes, err := h.UnfinishedIn(capScope)
	if err != nil {
		return nil, err
	}

	var unresolved []command.UnfinishedNode
	for _, node := range nodes {
		if node.Kind == query.KindDispatch {
			unresolved = append(unresolved, node)
		}
	}
	return unresolved, nil
}

// unresolvedError names each unresolved dispatch and says how to clear it.
func unresolvedError(unresolved []command.UnfinishedNode) string {
	named := make([]string, len(unresolved))
	for i, node := range unresolved {
		named[i] = fmt.Sprintf("%s (%q)", node.NodeID, node.Goal)
	}
	return fmt.Sprintf(
		"dispatch: unresolved dispatches in scope %s: %s; resolve them (fs task update ID --status done --decision ...) or re-run with --allow-unresolved to proceed",
		capScope, strings.Join(named, ", "))
}

// entryRun is what running the plan's entry phase produced: the checklist the
// brief carries, and the context an entry `say` hands over.
type entryRun struct {
	Checklist []Item
	Context   []string
}

// runEntry runs every entry-phase playbook's steps in plan order, appending one
// line to the dispatch's trace per step. It is fail-fast: the first failing
// check stops the run unless the cap confirmed it, because the user fixes one
// thing at a time and a list of failures buries the decision.
//
// The line is written before anything is decided about the step, so a failure
// is on the record before the refusal that follows it, and a step that never ran
// is a step with no line — which is exactly what close refuses on.
//
// Each check runs with env, so a gate over the dispatch's own inputs — which
// cards, which type, which member — is a mechanical gate rather than a
// judgement call.
func runEntry(plan []planned, root string, env []string, lg *trace.Log, project, node string, confirm bool) (entryRun, error) {
	var run entryRun

	for _, pb := range plan {
		if pb.Phase != knowledge.PhaseEntry {
			continue
		}
		for i, step := range pb.Steps {
			start := time.Now()
			item, status := entryStep(step, root, env, confirm)

			if err := lg.Append(project, trace.Line{
				ID:         trace.StepID(node, pb.Name, i+1),
				Dispatch:   node,
				Playbook:   pb.Name,
				Phase:      pb.Phase,
				Step:       i + 1,
				Kind:       step.Kind,
				Status:     status,
				DurationMS: time.Since(start).Milliseconds(),
				Detail:     step.Body,
			}); err != nil {
				return run, err
			}

			switch step.Kind {
			case knowledge.KindCheck:
				run.Checklist = append(run.Checklist, item)
				if status == trace.StatusFail {
					return run, fmt.Errorf(
						"entry check %q (playbook %s step %d) failed: %s; tell the user and fix it, or re-run with --confirm to proceed",
						step.Body, pb.Name, i+1, checkOutput(item))
				}
			case knowledge.KindAsk:
				run.Checklist = append(run.Checklist, item)
			case knowledge.KindSay:
				run.Context = append(run.Context, step.Body)
			}
		}
	}
	return run, nil
}

// entryStep runs one entry step and returns the checklist item it produces, if
// it produces one, and the status its trace line carries.
//
// A check the cap overrode is recorded `confirmed`, not `fail`: the flag
// answered the failure, and close refuses a step whose last line is `fail`. The
// failure itself is not lost — Prepare records a decision naming the check and
// the output it failed with, exactly as before.
//
// An ask the cap did not answer is recorded `skipped`: the step was put to the
// cap and not answered, and close refuses a plan whose ask is not confirmed. It
// is confirmable at close, which is where the cap is asked for the verdict
// anyway.
func entryStep(step knowledge.Step, root string, env []string, confirm bool) (Item, string) {
	switch step.Kind {
	case knowledge.KindCheck:
		item := runCheck(step.Body, root, env)
		if item.Status == "fail" && confirm {
			return item, trace.StatusConfirmed
		}
		return item, item.Status
	case knowledge.KindAsk:
		item := Item{Kind: step.Kind, Body: step.Body, Status: "?"}
		if confirm {
			return item, trace.StatusConfirmed
		}
		return item, trace.StatusSkipped
	default: // say — worker context, not part of the gate
		return Item{}, trace.StatusLoaded
	}
}

// buildBrief composes the brief from the plan. The work-phase steps are the
// worker's: its `say` steps are obligations and its `use` steps are hints. The
// entry-phase steps are what the gate ran and what it asked.
func buildBrief(run entryRun, plan []planned, proj registry.Project, req PrepareRequest, nodeID string) *Brief {
	brief := &Brief{
		Goal:       req.Goal,
		Project:    proj.Name,
		RootPath:   proj.RootPath,
		TaskType:   req.TaskType,
		Plan:       planEntries(plan),
		Checklist:  run.Checklist,
		Context:    run.Context,
		LockedDocs: findLockedDocs(proj.RootPath),
		CapNodeID:  nodeID,
		Notes: []string{
			"the locked-doc grep matches the marker text anywhere, so it has a known false positive when a repo documents the marker syntax itself",
		},
	}

	for _, pb := range plan {
		if pb.Phase != knowledge.PhaseWork {
			continue
		}
		for _, step := range pb.Steps {
			switch step.Kind {
			case knowledge.KindSay:
				brief.Obligations = append(brief.Obligations, step.Body)
			case knowledge.KindUse:
				brief.Skills = append(brief.Skills, step.Body)
			}
		}
	}
	return brief
}

// recordPlan puts the plan on the record twice: as a plan-recorded event on the
// node, which the exit gate reads, and as the trace's first line, which
// `fs trace` prints before any step has an outcome. Either write failing is a
// refusal — a dispatch whose plan cannot be recorded cannot be gated.
func recordPlan(h *command.Handler, lg *trace.Log, nodeID, project string, plan trace.Plan) error {
	payload, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("record plan: %w", err)
	}
	if resp := h.RecordPlan(capScope, nodeID, payload); !resp.OK {
		return fmt.Errorf("record plan: %s", resp.Error)
	}
	return lg.Append(project, trace.Line{
		Kind:     trace.KindPlan,
		Dispatch: nodeID,
		Tags:     plan.Tags,
		Plan:     plan.Plan,
	})
}

// checkEnv is the dispatch's own inputs, exposed to every check step as FS_*.
//
// It exists because a prerequisite is otherwise a static shell string: it could
// ask about the world (is this a git repo?) but never about *this* dispatch
// (which cards? which member does it integrate?). A gate over the batch's cards
// cannot be written without it.
//
// FS_CARDS is the --cards value, comma-separated, and FS_INTEGRATES is the
// --integrates value. Both are exported empty rather than left absent when they
// were not given, so a check can tell "this dispatch named no cards/member" from
// "this step has no env" — and one that needs them must refuse on empty rather
// than pass vacuously.
func checkEnv(project, taskType, goal string, cards []string, integrates string) []string {
	return []string{
		"FS_PROJECT=" + project,
		"FS_TYPE=" + taskType,
		"FS_GOAL=" + goal,
		"FS_CARDS=" + strings.Join(cards, ","),
		"FS_INTEGRATES=" + integrates,
	}
}

// runCheck runs a check's body through the shared shell runner and reports
// pass/fail plus the command's combined output.
func runCheck(body, root string, env []string) Item {
	return runStep(knowledge.KindCheck, body, root, env)
}

// runStep runs one shell step and reports its kind, exit status, and combined
// output. A check and a do are the same mechanism with different meaning: a
// check gates, an action acts, and the caller decides which failure is a refusal
// and which is a warning.
func runStep(kind, body, cwd string, env []string) Item {
	out, err := command.RunShell(body, cwd, env)
	return Item{
		Kind:   kind,
		Body:   body,
		Status: passFail(err == nil),
		Output: strings.TrimRight(out, "\n"),
	}
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// checkOutput is the failing command's output, made explicit when it produced
// none so the refusal always says what happened.
func checkOutput(item Item) string {
	if strings.TrimSpace(item.Output) == "" {
		return "(no output)"
	}
	return item.Output
}

// findLockedDocs returns the *.md files under root carrying a specs:locked or
// design:locked marker, relative to root (as grep -rl would print them).
func findLockedDocs(root string) []string {
	var docs []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable entry does not abort the sweep
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if !bytes.Contains(data, []byte("<!-- specs:locked:")) &&
			!bytes.Contains(data, []byte("<!-- design:locked:")) {
			return nil
		}
		if rel, err := filepath.Rel(root, path); err == nil {
			path = rel
		}
		docs = append(docs, path)
		return nil
	})
	sort.Strings(docs)
	return docs
}

// recordCapNode writes the cap's two events for this dispatch: the node itself —
// carrying the member it integrates, when it integrates one — and the decision
// naming the project the work went to. It returns the node id so the cap can
// read its own dispatch back.
func recordCapNode(h *command.Handler, taskType, goal, project, integrates string) (string, error) {
	added := h.TaskAddKind(capScope, dispatchGoal(taskType, goal), query.KindDispatch, "", integrates, nil)
	if !added.OK {
		return "", fmt.Errorf("record cap node: %s", added.Error)
	}
	data, ok := added.Data.(command.EventData)
	if !ok || data.NodeID == "" {
		return "", fmt.Errorf("record cap node: task-created response carried no node id")
	}

	summary := fmt.Sprintf("dispatch %s: %s to project %s", taskType, goal, project)
	if err := recordDecision(h, data.NodeID, summary); err != nil {
		return "", err
	}
	return data.NodeID, nil
}

// recordCheckOverrides records the cap's --confirm override: one decision per
// failing check, naming the check and the output it failed with. Without it the
// refusal leaves no trace in the log, and a bare "user confirmed" would not say
// what was overridden or the value it was over.
func recordCheckOverrides(h *command.Handler, nodeID string, brief *Brief) error {
	for _, item := range brief.Checklist {
		if item.Status != "fail" {
			continue
		}
		summary := fmt.Sprintf(
			"--confirm overrode the failing check %q (output: %s)", item.Body, checkOutput(item))
		if err := recordDecision(h, nodeID, summary); err != nil {
			return err
		}
	}
	return nil
}

// recordUnresolvedOverride records the cap's --allow-unresolved override, naming
// the dispatches it proceeded past — the value the flag was over.
func recordUnresolvedOverride(h *command.Handler, nodeID string, unresolved []command.UnfinishedNode) error {
	named := make([]string, len(unresolved))
	for i, node := range unresolved {
		named[i] = node.NodeID
	}
	summary := "--allow-unresolved proceeded with unresolved dispatches: " + strings.Join(named, ", ")
	return recordDecision(h, nodeID, summary)
}

func recordDecision(h *command.Handler, nodeID, summary string) error {
	return recordStatus(h, nodeID, "", summary)
}

// recordStatus writes a decision and, when status is not empty, moves the node
// to it. It is one event either way: the decision is what a reader needs, and a
// separate status-changed event would say less about why.
func recordStatus(h *command.Handler, nodeID, status, summary string) error {
	resp := h.TaskUpdate(capScope, nodeID, status, summary, nil)
	if !resp.OK {
		return fmt.Errorf("record cap decision: %s", resp.Error)
	}
	return nil
}

// nextCommand is the herdr line the cap runs to deliver the brief to a worker.
// dispatch does not run it.
//
// It splits a sibling pane of the cap's own rather than creating a tab: the
// split is anchored to the cap's pane, and therefore to the cap's workspace, so
// there is no workspace to get wrong. It is the same line Deliver runs.
func nextCommand(b *Brief) string {
	name := "dispatch-" + b.TaskType
	return fmt.Sprintf(
		`P=$(herdr pane split --current --direction right --no-focus --cwd %s | jq -r '.result.pane.pane_id') && `+
			`herdr agent start %s --kind pi --pane "$P" && `+
			`herdr agent prompt %s %s --wait --timeout 120000`,
		shellQuote(b.RootPath), name, name, shellQuote(renderBrief(b)))
}

// renderBrief is the text the cap hands the worker: goal, project root, the
// node the worker owns, the plan, the work phase's obligations and hints, the
// gate's checklist with each check's output, and the locked docs.
//
// Every obligation in it comes from a playbook. What the worker must do is a
// file the user can read and edit, not a string compiled into the binary — so
// nothing here states a rule, it only lays out what the plan says.
func renderBrief(b *Brief) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Goal: %s\n", b.Goal)
	fmt.Fprintf(&sb, "Project: %s (root %s)\n", b.Project, b.RootPath)

	node := b.WorkerNode
	if node == "" {
		node = "(not created yet — it is created when the brief is delivered)"
	}
	fmt.Fprintf(&sb, "Your node: %s\n", node)

	sb.WriteString("Plan:\n")
	if len(b.Plan) == 0 {
		sb.WriteString("- plan: empty — no playbook's applies_when matched this dispatch's tags\n")
	} else {
		for _, entry := range b.Plan {
			fmt.Fprintf(&sb, "- %s (%s, order %d)\n", entry.Name, entry.Phase, entry.Order)
		}
	}

	if len(b.Obligations) > 0 {
		sb.WriteString("Obligations:\n")
		for _, obligation := range b.Obligations {
			fmt.Fprintf(&sb, "- %s\n", obligation)
		}
	}

	if len(b.Skills) > 0 {
		sb.WriteString("Skills — hints, not requirements: no skill is required to do this " +
			"work, and loading one is not checked. What is checked is that each playbook above " +
			"was handed over; whether a hint was followed is the review's judgement, not this " +
			"dispatch's claim.\n")
		for _, skill := range b.Skills {
			fmt.Fprintf(&sb, "- %s\n", skill)
		}
	}

	if len(b.Context) > 0 {
		sb.WriteString("Context:\n")
		for _, say := range b.Context {
			fmt.Fprintf(&sb, "- %s\n", say)
		}
	}

	sb.WriteString("Checklist:\n")
	for _, item := range b.Checklist {
		fmt.Fprintf(&sb, "- [%s] %s\n", item.Status, item.Body)
		if item.Output != "" {
			sb.WriteString("    output:\n")
			for _, line := range strings.Split(item.Output, "\n") {
				fmt.Fprintf(&sb, "      %s\n", line)
			}
		}
	}
	if len(b.LockedDocs) > 0 {
		fmt.Fprintf(&sb, "Locked docs: %s\n", strings.Join(b.LockedDocs, ", "))
	} else {
		sb.WriteString("Locked docs: none\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// shellQuote wraps s in single quotes for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func errResp(msg string) command.Response {
	return command.Response{OK: false, Error: msg}
}

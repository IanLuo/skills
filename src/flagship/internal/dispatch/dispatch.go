// Package dispatch owns the cap's dispatch lifecycle for a task type: prepare
// the brief, deliver it to a worker, and close it out.
//
// A dispatch is measured against a plan: the playbooks whose applies_when the
// dispatch's tags satisfy, ordered by the order they declare. Prepare builds
// that plan once, records it on the cap node, runs the plan's `entry` steps
// against the target project's root, and renders the brief from the plan's
// `work` steps. Every step that runs appends a line to the dispatch's trace
// under ~/.fs/traces/<project>.<node>.jsonl, which is what close-out gates against.
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
	"slices"
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

// IsCapScope reports whether a project_id names the cap's own scope. The CLI
// needs it to tell a write into the cap scope — which acts as cap, and is
// refused from a pane fs holds a delivery record for — from an ordinary write
// in a worker's own project, which is not.
func IsCapScope(scope string) bool { return scope == capScope }

// SessionTags is the tag set the cap's own session runs under. The engine tag
// is the standing declaration the playbooks resolve — not a constant — so a
// playbook with a `cap:` section tagged `engine=herdr` loads only because a
// standing playbook declares that engine. It is the same resolution a dispatch
// uses, so the prompt cannot drift from what a dispatch would run.
//
// The refusal carries the `dispatch: ` prefix because this is a second caller of
// the standing resolution: `fs kb prompt` prints its own name and the error, so
// a bare error reaches the cap naming no command at all. The prefix belongs to
// each caller rather than to the error, since `Prepare` reaches
// standingRealization directly and would otherwise carry it twice.
func SessionTags(kc *knowledge.Center) (map[string]any, error) {
	standing, err := standingRealization(kc)
	if err != nil {
		return nil, fmt.Errorf("dispatch: %w", err)
	}
	tags := map[string]any{}
	if standing.engine != "" {
		tags[engineTag] = standing.engine
	}
	return tags, nil
}

// engineTag is the derived tag naming the dispatcher.
const engineTag = "engine"

// typeTag is the derived tag naming the dispatch's task type.
const typeTag = "type"

// batchTag is the derived situation tag naming a batch: it is stamped from the
// plan's `creates: batch` declaration, and batch-exit matches on it.
const batchTag = "batch"

// memberTag is the derived situation tag naming a member of a batch: a
// dispatch that named --batch, so it works in its own worktree and defers its
// teardown to the batch. member and member-exit match on it.
const memberTag = "member"

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
	Context    []string `json:"context,omitempty"`
	LockedDocs []string `json:"locked_docs"`
	Notes      []string `json:"notes"`
	CapNodeID  string   `json:"cap_node_id"`

	// NextCommand is what the cap does next: the instruction that hands this
	// brief to a worker with --deliver. It is not a command fs runs and not a
	// command the cap can run as printed — see nextCommand.
	NextCommand string `json:"next_command"`

	// Engine and Agent are the realization the plan resolved: which dispatcher
	// runs the work and which kind of agent carries the brief. Both are playbook
	// declarations, never constants — Deliver starts the agent the plan named.
	Engine string `json:"engine,omitempty"`
	Agent  string `json:"agent,omitempty"`

	// AgentArgs are the arguments that agent is started with, one argv element
	// per entry. They resolve beside the kind and inherit independently of it,
	// and they reach the worker as Deliver's argv slice — one element each, for
	// the exec that starts the agent and for nothing else.
	AgentArgs []string `json:"agent_args,omitempty"`

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

	// WorktreeNew asks Deliver to create the worktree itself (--worktree-new)
	// rather than name an existing one. It is set by the CLI after Prepare, the
	// same way Worktree is; Deliver creates the workspace and records both
	// Worktree and WorktreePath exactly as --worktree does.
	WorktreeNew bool `json:"worktree_new,omitempty"`

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

	// Reviews names the cap-scope dispatch node whose change this dispatch
	// reads (--reviews). Empty for a dispatch that reviews nothing; it is a
	// different question from Integrates, which names what this dispatch merges.
	Reviews string

	// Tags are declared situation terms (`k` or `k=v`), on top of the derived
	// `type=<taskType>` and `engine=herdr`.
	Tags []string
	// Also forces a playbook into the plan; Without drops one the tags selected.
	Also    []string
	Without []string

	// Batch names the batch this dispatch is a member of (--batch). A member's
	// cap node is the batch's child, and the tag `member` is derived from it.
	Batch string

	// Fixes names the cap-scope dispatch nodes this one repairs (--fixes), and it
	// is repeatable because a fix round usually answers more than one: the change
	// it repairs and the review whose finding it fixes are both open while the
	// fix is made. This is the declaration that keeps the unresolved-dispatch
	// gate from reading them as open work piled on top of the fix.
	Fixes []string

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

	// The type is half of the agent name every worker of this dispatch will
	// carry, and it is the half a cap chose. Checked before the plan is built
	// and before any node exists, so an input herdr cannot encode is refused by
	// fs, naming the limit — rather than reaching herdr as invalid_agent_name,
	// which names neither fs nor the input.
	if err := validTaskType(req.TaskType); err != nil {
		return errResp("dispatch: " + err.Error())
	}

	tags, err := tagSet(req.TaskType, req.Tags, req.Batch != "")
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}

	// A dispatch that names --batch is a member of that batch: its cap node is
	// the batch's child and it works in its own worktree. The batch node must
	// already be a kind: batch node — the parent link is written into the
	// task-created payload, an immutable event, so a wrong link is refused here
	// before anything is recorded, naming what the node is.
	batch, err := batchMember(h, req.Batch)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}

	member, err := integrateMember(h, req.Integrates)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}

	reviewed, err := reviewedDispatch(h, req.Reviews)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}

	fixes, err := fixedDispatches(h, req.Fixes)
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

	// Pass 1 of the realization: the standing declaration — a playbook whose
	// applies_when is empty, knowable without selecting anything — stamps the
	// engine into the tag set, which is what the selection below can match on.
	standing, err := standingRealization(kc)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}
	if standing.engine != "" {
		tags[engineTag] = standing.engine
	}

	plan, err := buildPlan(kc, tags, req.Also, req.Without)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}

	// The node kind is declared by the plan, not mapped from the task type:
	// batch-entry carries `creates: batch`, and a dispatch whose plan declares
	// nothing leaves a dispatch node (the default). The batch situation tag is
	// stamped the same way — from the plan's declaration — so batch-exit, which
	// matches `batch`, is selected in a second pass. There is deliberately no
	// `if taskType == "batch"` anywhere.
	creates, err := resolveCreates(plan)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}
	if creates == query.KindBatch {
		tags[batchTag] = true
		plan, err = buildPlan(kc, tags, req.Also, req.Without)
		if err != nil {
			return errResp("dispatch: " + err.Error())
		}
	}

	// Pass 2: the situation-specific declarations in the selected plan override
	// the standing one. The three realization refusals — no declaration, two
	// situation-specific declarations that disagree, and an engine fs cannot
	// dispatch with — fire here, before any node is created, so a broken
	// configuration leaves nothing behind.
	real, err := resolveRealization(plan, standing)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}
	tags[engineTag] = real.engine

	// The missing-protocol refusal, and it comes after the realization for a
	// reason: a broken configuration is a more specific thing to be told than
	// "this type has no protocol", and a plan whose engine is not herdr would
	// otherwise be reported as the wrong defect.
	if err := missingProtocol(plan, req.TaskType); err != nil {
		return errResp("dispatch: " + err.Error())
	}

	// A review names the change it reads with --reviews, and it names the
	// revision it read it at here: the reviewed dispatch's tree, as it stands at
	// this moment. Recorded rather than derived, because the tree moves after
	// this — the whole point is to compare the two later — and recorded at
	// preparation, because that is when the claim is made.
	//
	// A review that cannot name a revision is refused rather than prepared with
	// an empty one: the close gate refuses a review that names none (see
	// Reviews), so preparing one would be handing the cap a review that can
	// never satisfy the gate it exists for.
	reads := ""
	if reviewed != "" {
		rev, _, err := revisionOf(h, reg, reviewed)
		if err != nil {
			return errResp(fmt.Sprintf("dispatch: --reviews %s: %v", req.Reviews, err))
		}
		reads = rev
	}

	// Gate: a dispatch the cap prepared but never delivered is still open work.
	// Refuse to pile another one on top of it unless the cap says to proceed.
	// --allow-unresolved is not this gate's only way past, though it is the one
	// the refusal below names. --confirm is about a failing check and must not
	// pass this one; --fixes is the other, and it is not an override but a
	// declaration — it subtracts the dispatches the round exists to repair (see
	// declaredSubjects), so the gate never reads them as work piled on top.
	// A round that repairs an open dispatch says so with --fixes rather than
	// waving through every open one.
	//
	// What the gate subtracts is the set of nodes this dispatch declares a
	// relation to — see declaredSubjects. Those are the dispatches it is about
	// or runs alongside, not open work piled on top of it, and every one of them
	// is expected to be unresolved when this dispatch is prepared. Every other
	// open dispatch still refuses.
	//
	// So this is one rule over the parsed relations, not a list of exemptions:
	// a relation is a pair of (flag, the nodes it names), and a fifth flag costs
	// a line here rather than another gate. An exemption wider than the
	// declaration would let any dispatch past the gate by pointing at the work
	// it is piling onto, which is exactly what the gate is for — which is why
	// the relations are read from the command line and never searched for in the
	// dispatch's own text. --allow-unresolved stays what it was for everything
	// else.
	subjects, err := declaredSubjects(h, member, reviewed, fixes, batch)
	if err != nil {
		return errResp("dispatch: " + err.Error())
	}
	unresolved, err := unresolvedDispatches(h)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}
	unresolved = withoutNodes(unresolved, subjects)
	if len(unresolved) > 0 && !req.AllowUnresolved {
		return errResp(unresolvedError(unresolved))
	}

	// The node comes first, because the trace is named after it and AC3 wants an
	// entry step's outcome on the record. It is not left behind by a refusal: a
	// refused entry gate rolls it back below, marked done with the refusal as
	// its decision, so the attempt is legible and no open dispatch is invented.
	nodeID, err := recordCapNode(h, req.TaskType, req.Goal, req.Project, member, reviewed, creates, batch)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}

	recorded := trace.Plan{
		Plan: planEntries(plan), Tags: tags,
		Engine: real.engine, Agent: real.agent, AgentArgs: real.args,
		Reads: reads, Cards: req.Cards,
	}
	if err := recordPlan(h, lg, nodeID, req.Project, recorded); err != nil {
		return errResp("dispatch: " + refuseEntry(h, nodeID, err.Error()))
	}

	env := checkEnv(proj.Name, req.TaskType, req.Goal, req.Cards, req.Integrates)
	run, err := runEntry(plan, proj.RootPath, env, lg, req.Project, nodeID, req.Confirm)
	if err != nil {
		return errResp("dispatch: " + refuseEntry(h, nodeID, err.Error()))
	}

	brief := buildBrief(run, plan, proj, req, nodeID, real)
	brief.NextCommand = nextCommand(brief)

	// A fix round records what it repairs, on its own node: it is dispatched
	// while that dispatch is still open, so without the record a reader sees an
	// open dispatch and a fix beside it and cannot tell which one answers which.
	// The link is a decision rather than a field on the task-created payload,
	// because the payload's shape is the store's and events are immutable —
	// this is a claim about why the dispatch exists, which is what decisions are.
	if len(fixes) > 0 {
		if err := recordDecision(h, nodeID, "fixes "+strings.Join(fixes, ", ")+": this dispatch repairs what they found, and is dispatched while they are open for that reason"); err != nil {
			return errResp(fmt.Sprintf("dispatch: %v", err))
		}
	}

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
func tagSet(taskType string, declared []string, member bool) (map[string]any, error) {
	tags := map[string]any{typeTag: taskType}
	if member {
		tags[memberTag] = true
	}

	for _, term := range declared {
		key, value, hasValue := strings.Cut(term, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("--tag %q names no tag", term)
		}
		if key == typeTag || key == engineTag || key == memberTag || key == batchTag {
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

// planned is one playbook in a dispatch's plan: where it sits, the protocol it
// resolved to, includes and all, and its realization declarations.
type planned struct {
	Name        string
	Moments     []string
	Order       int
	Indexed     []knowledge.IndexedStep
	AppliesWhen []string
	Engine      string
	Agent       string
	AgentArgs   []string
	Creates     string
}

// planEntries is the plan as it is recorded: the ordered playbooks without
// their steps.
func planEntries(plan []planned) []trace.PlanEntry {
	entries := make([]trace.PlanEntry, 0, len(plan))
	for _, pb := range plan {
		entries = append(entries, trace.PlanEntry{Name: pb.Name, Moments: pb.Moments, Order: pb.Order})
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
		if (len(pb.Moments()) == 1 && pb.Serves(knowledge.MomentCap)) || !pb.Matches(tags) {
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
		if len(pb.Moments()) == 1 && pb.Serves(knowledge.MomentCap) {
			return nil, fmt.Errorf(
				"--also %s: a `cap:`-only playbook loads into the session's standing rules, not into a dispatch's plan", name)
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
	return planned{
		Name:        pb.Name,
		Moments:     pb.Moments(),
		Order:       pb.Order,
		Indexed:     pb.IndexedSteps(),
		AppliesWhen: pb.AppliesWhen,
		Engine:      pb.Engine,
		Agent:       pb.Agent,
		AgentArgs:   pb.AgentArgs,
		Creates:     pb.Creates,
	}
}

// realization is the resolved triple: which engine dispatches the work, which
// kind of agent runs it, and the arguments that agent is started with. All three
// are playbook declarations, never Go constants.
type realization struct {
	engine string
	agent  string
	args   []string
}

// standingRealization is pass 1 of the resolution: the declarations on playbooks
// whose applies_when is empty, which match every dispatch and so are knowable
// without selecting anything. The engine it finds is what pass 2's selection
// needs as a tag.
//
// Two standing declarations that disagree are refused rather than averaged —
// there is one default, and two claims to it are a broken configuration. The
// agent's arguments are held to the same rule: they are a list, and two lists
// cannot be merged into the one the agent receives.
func standingRealization(kc *knowledge.Center) (realization, error) {
	names, err := kc.List()
	if err != nil {
		return realization{}, err
	}

	var real realization
	var engineFrom, agentFrom, argsFrom string
	for _, name := range names {
		pb, err := kc.Get(name)
		if err != nil {
			return realization{}, err
		}
		if len(pb.AppliesWhen) > 0 {
			continue // situation-specific; pass 2 reads it
		}
		if pb.Engine != "" {
			if real.engine != "" && real.engine != pb.Engine {
				return realization{}, fmt.Errorf(
					"playbooks %s and %s both declare the standing engine and disagree (%s and %s)",
					engineFrom, pb.Name, real.engine, pb.Engine)
			}
			real.engine, engineFrom = pb.Engine, pb.Name
		}
		if pb.Agent != "" {
			if real.agent != "" && real.agent != pb.Agent {
				return realization{}, fmt.Errorf(
					"playbooks %s and %s both declare the standing agent and disagree (%s and %s)",
					agentFrom, pb.Name, real.agent, pb.Agent)
			}
			real.agent, agentFrom = pb.Agent, pb.Name
		}
		if len(pb.AgentArgs) > 0 {
			if len(real.args) > 0 && !slices.Equal(real.args, pb.AgentArgs) {
				return realization{}, fmt.Errorf(
					"playbooks %s and %s both declare the standing agent_args and disagree (%s and %s)",
					argsFrom, pb.Name, strings.Join(real.args, ", "), strings.Join(pb.AgentArgs, ", "))
			}
			real.args, argsFrom = pb.AgentArgs, pb.Name
		}
	}
	return real, nil
}

// resolveRealization is pass 2: the situation-specific declarations in the
// selected plan override the standing ones, and two of them that disagree
// refuse, naming both playbooks and both values. A plan declaring neither — no
// standing and no situation-specific declaration — refuses naming worker.yaml
// as the place to declare them, and a resolved engine that is not the one fs
// dispatches with refuses rather than routing into the void.
//
// The agent's arguments resolve by the same rule and inherit independently of
// the kind: a situation-specific playbook that declares `agent:` and no
// `agent_args:` keeps the standing arguments, because coupling the two would
// require fs to know that arguments are kind-specific — the knowledge the
// realization fields exist to remove. There is deliberately no way to declare
// "no arguments": a playbook overriding the kind decides what its args are, and
// one that says nothing is not a claim that there should be none.
func resolveRealization(plan []planned, standing realization) (realization, error) {
	real := standing

	var engine, engineFrom, agent, agentFrom, argsFrom string
	var args []string
	for _, pb := range plan {
		if len(pb.AppliesWhen) == 0 {
			continue // standing; already resolved in pass 1
		}
		if pb.Engine != "" {
			if engine != "" && engine != pb.Engine {
				return realization{}, fmt.Errorf(
					"playbooks %s and %s declare conflicting engines (%s and %s); a dispatch cannot run two harnesses",
					engineFrom, pb.Name, engine, pb.Engine)
			}
			engine, engineFrom = pb.Engine, pb.Name
		}
		if pb.Agent != "" {
			if agent != "" && agent != pb.Agent {
				return realization{}, fmt.Errorf(
					"playbooks %s and %s declare conflicting agents (%s and %s); a dispatch cannot run two kinds of agent",
					agentFrom, pb.Name, agent, pb.Agent)
			}
			agent, agentFrom = pb.Agent, pb.Name
		}
		if len(pb.AgentArgs) > 0 {
			// Refused rather than concatenated: fs cannot read the arguments,
			// so merging would hand the agent both and let its own last-wins
			// rule decide, with the winner set by two `order:` integers in two
			// files. That is invisible arbitration.
			if args != nil && !slices.Equal(args, pb.AgentArgs) {
				return realization{}, fmt.Errorf(
					"playbooks %s and %s declare conflicting agent_args (%s and %s); fs cannot know which the agent should receive",
					argsFrom, pb.Name, strings.Join(args, ", "), strings.Join(pb.AgentArgs, ", "))
			}
			args, argsFrom = pb.AgentArgs, pb.Name
		}
	}
	if engine != "" {
		real.engine = engine
	}
	if agent != "" {
		real.agent = agent
	}
	if args != nil {
		real.args = args
	}

	if real.engine == "" || real.agent == "" {
		var missing []string
		if real.engine == "" {
			missing = append(missing, "engine")
		}
		if real.agent == "" {
			missing = append(missing, "agent")
		}
		return realization{}, fmt.Errorf(
			"no playbook declares the realization's %s — declare %s in worker.yaml (the standing playbook with no applies_when)",
			strings.Join(missing, " and "), strings.Join(missing, " and "))
	}

	if real.engine != "herdr" {
		return realization{}, fmt.Errorf(
			"the plan resolves to engine %q, but fs dispatches through herdr; declare `engine: herdr` (fs has no other adapter to route to)",
			real.engine)
	}

	return real, nil
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

// reviewedDispatch resolves a --reviews value to the bare id of the cap-scope
// dispatch node it names, or to "" when the flag was not given.
//
// It refuses a node that does not exist, and one that is not a dispatch node,
// naming it — the same two questions integrateMember asks, asked the same way,
// and for the same reason: the link is written into the review's task-created
// payload and events are immutable, so a wrong link could never be corrected.
//
// The two flags are not each other's synonyms. --integrates names what this
// dispatch merges (an integration looks forward, at the member it lands);
// --reviews names whose change this dispatch reads (a review looks backward, at
// the dispatch it follows). Either, both, or neither — they are separate fields.
func reviewedDispatch(h *command.Handler, reviews string) (string, error) {
	if reviews == "" {
		return "", nil
	}

	reviewed, err := bareNodeID(reviews)
	if err != nil {
		return "", err
	}
	node, err := capNode(h, reviewed)
	if err != nil {
		return "", fmt.Errorf("--reviews %s: %v", reviews, err)
	}
	if node.Kind != query.KindDispatch {
		return "", fmt.Errorf(
			"--reviews %s names a %q node, not a %s node — a review reads a dispatch node's change",
			reviews, node.Kind, query.KindDispatch)
	}
	return reviewed, nil
}

// fixedDispatches resolves every --fixes value to the bare id of the cap-scope
// dispatch node it names. The flag is repeatable, because a fix round answers
// more than one dispatch in the ordinary case: the change it repairs and the
// review whose finding it fixes are both open while the fix is made, and a rule
// that let the round name only one of them would still send the cap to
// --allow-unresolved for the other.
//
// It answers the ordering the review protocol cannot: the dispatch a fix round
// repairs is open by construction — a blocking review finding is fixed while
// the change it was found in is still open — so the fix round has to be able to
// name them as the subjects they are, rather than carry --allow-unresolved and
// be indistinguishable from a dispatch piling onto open work. The flag is what
// makes that distinction; the gate reads the relations and subtracts them (see
// declaredSubjects).
//
// It refuses the same two things --integrates and --reviews refuse, asked the
// same way and for the same reason: the relation is written into an immutable
// event, so a link that names nothing, or names the wrong kind of node, could
// never be corrected.
func fixedDispatches(h *command.Handler, fixes []string) ([]string, error) {
	fixed := make([]string, 0, len(fixes))
	for _, ref := range fixes {
		id, err := bareNodeID(ref)
		if err != nil {
			return nil, err
		}
		node, err := capNode(h, id)
		if err != nil {
			return nil, fmt.Errorf("--fixes %s: %v", ref, err)
		}
		if node.Kind != query.KindDispatch {
			return nil, fmt.Errorf(
				"--fixes %s names a %q node, not a %s node — a fix round repairs a dispatch node's change",
				ref, node.Kind, query.KindDispatch)
		}
		fixed = append(fixed, id)
	}
	return fixed, nil
}

// bareNodeID resolves a node reference that must live in the cap's own scope —
// a member, the dispatch a review reads, a batch, one of a fix round's subjects
// — to its bare id.
//
// The splitting is not this function's: command.SplitAddress is the one resolver
// for the address form, and this is the cap-scope judgement over it. What the
// judgement is for: these are links between the cap's own nodes, written into
// immutable events, so a reference to another scope would record a relation to a
// node this scope cannot read. It is refused naming both.
func bareNodeID(ref string) (string, error) {
	if ref == "" {
		return "", errors.New("a member cap node is required")
	}
	scope, node, err := command.SplitAddress(capScope, ref)
	if err != nil {
		return "", err
	}
	if scope != capScope {
		return "", fmt.Errorf("%s names scope %s; a member lives in scope %s", ref, scope, capScope)
	}
	return node, nil
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

// Reviews answers a close's question: was this dispatch's change reviewed? It
// returns the done review dispatch that names this node, or refuses.
//
// It is the exit gate's half of the sequencing the review's own protocol cannot
// enforce. `independent-review` already decides *whether every finding is
// dispositioned*; what nothing noticed was whether a review happened at all, or
// whether it happened before the close it was about. A done review dispatch
// carrying `reviews: <this node>` is the record that it did — done, and not
// merely dispatched, because a review is not finished until its own exit gate
// has refused or accepted every finding.
//
// It deliberately does not read the findings. A fixed one leaves no record of
// itself and an accepted one is a sentence, so no check could decide that from
// the store; asking a second question here would be a gate reporting compliance
// it never tested. This asks the one thing the record can answer.
//
// It asks that one thing about a revision, because "was it reviewed" without
// "at which revision" is not a question a change can be measured against: a
// review that closed before the last three commits satisfies the bare version
// of it for code that review never read, which is the failure this gate was
// written after. So a review records the revision it read (trace.Plan.Reads,
// stamped by Prepare from the reviewed dispatch's own tree) and this compares
// it against the revision that tree stands at now.
//
// The rule for a review that names no revision — every review closed before
// this field existed — is that it does NOT satisfy the gate. Accepting it would
// be reinterpreting it as a review of the current revision, which is the claim
// it never made and the one thing the card says not to do silently. The
// sanctioned way past is --without-review "<why>", which waives the gate and
// records the reason on the node: a skip the cap owns, rather than a pass the
// store cannot justify.
func Reviews(h *command.Handler, reg *registry.Registry, node string) command.Response {
	ref, err := bareNodeID(node)
	if err != nil {
		return errResp("reviews: " + err.Error())
	}

	// The ref has to resolve to something before "is it reviewed" is a question
	// with an answer, and which thing it resolved to is half the answer. An id
	// that is not there is a typo, one that is there and is not a dispatch is
	// the wrong kind of thing to review, and only a dispatch that exists can be
	// unreviewed. All three used to come back as "no dispatch names it", which
	// is true of one of them.
	subject, err := capNode(h, ref)
	if err != nil {
		return errResp("reviews: " + err.Error())
	}
	if subject.Kind != query.KindDispatch {
		return errResp(fmt.Sprintf(
			"reviews: %s names a %q node, not a %s node — a review reads a dispatch node's change",
			ref, subject.Kind, query.KindDispatch))
	}

	// cap has no registered root, and nothing here needs one: the read is of the
	// cap's own scope.
	resp := h.StatusIn(capScope, "")
	if !resp.OK {
		return errResp("reviews: " + resp.Error)
	}
	result, ok := resp.Data.(command.StatusResult)
	if !ok {
		return errResp("reviews: read scope " + capScope + ": no status payload")
	}

	// A review that exists but has not finished is named as such: "not reviewed"
	// and "reviewed, still open" are different facts, and a close that read the
	// second as the first would send the cap to dispatch a review it already has.
	//
	// A done review is not accepted here and there: the subject's revision is
	// read once, and only if some review is done. An unreviewed dispatch must be
	// told it is unreviewed — not that its tree could not be read, which is a
	// question this gate only has because a review answered the first one.
	open := ""
	stale, nameless := "", ""
	now, tree := "", ""
	for _, task := range result.Tasks {
		if task.Reviews != ref || task.Kind != query.KindDispatch {
			continue
		}
		if task.Status != "done" {
			open = task.NodeID
			continue
		}
		reads, err := reviewReads(h, task.NodeID)
		if err != nil {
			return errResp("reviews: " + err.Error())
		}
		if reads == "" {
			nameless = task.NodeID
			continue
		}
		if now == "" {
			rev, footing, err := revisionOf(h, reg, ref)
			if err != nil {
				return errResp("reviews: " + err.Error())
			}
			now, tree = rev, footing
		}
		if reads == now {
			return command.Response{OK: true, Data: ReviewsResult{
				Node: ref, ReviewedBy: task.NodeID, Reads: reads,
			}}
		}
		stale = fmt.Sprintf("%s read %s, and %s now stands at %s", task.NodeID, reads, tree, now)
	}
	if stale != "" {
		return errResp(fmt.Sprintf(
			"reviews: %s is not reviewed at the revision it stands at — the done review %s, so the work has moved past what that review read; review the change again with `fs dispatch --project <project> --type review-task --goal \"...\" --reviews %s`, and close only once that dispatch is done",
			ref, stale, ref))
	}
	if nameless != "" {
		return errResp(fmt.Sprintf(
			"reviews: %s is not reviewed at the revision it stands at — the done review %s names no revision, so fs cannot tell a review of this change from a review of an earlier state of it, and it is not accepted as either; review the change again with `fs dispatch --project <project> --type review-task --goal \"...\" --reviews %s`, or close with --without-review \"<why>\" to waive this gate and record the reason",
			ref, nameless, ref))
	}
	if open != "" {
		return errResp(fmt.Sprintf(
			"reviews: %s is not reviewed — the review dispatch %s names it but is not done; finish it (its own exit gate refuses until every finding is dispositioned), then close again",
			ref, open))
	}
	return errResp(fmt.Sprintf(
		"reviews: %s is not reviewed — no dispatch names it; review it with `fs dispatch --project <project> --type review-task --goal \"...\" --reviews %s`, and close only once that dispatch is done",
		ref, ref))
}

// reviewReads reads the revision a review dispatch recorded that it read, or ""
// when it recorded none — a review prepared before the field existed, or one
// whose plan is not on the record at all.
//
// The plan is where the claim lives, because the review is what made it: the
// link `reviews: <node>` says which change, and this says which state of it.
func reviewReads(h *command.Handler, reviewNode string) (string, error) {
	payload, ok, err := h.PlanFor(capScope, reviewNode)
	if err != nil {
		return "", fmt.Errorf("read the plan of review %s: %v", reviewNode, err)
	}
	if !ok {
		return "", nil
	}
	var plan trace.Plan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return "", fmt.Errorf("read the plan of review %s: %v", reviewNode, err)
	}
	return plan.Reads, nil
}

// revisionOf reads the revision a cap-scope dispatch's tree stands at now, and
// the tree it read it from, which every refusal names: a comparison is only as
// good as the footing it was made on.
func revisionOf(h *command.Handler, reg *registry.Registry, node string) (string, string, error) {
	tree, err := dispatchTree(h, reg, node)
	if err != nil {
		return "", "", err
	}
	sha := command.DetectCommitSHAIn(tree)
	if sha == nil {
		return "", tree, fmt.Errorf(
			"fs cannot read a revision from %s, the tree %s works in: it is not a git repository with a commit, so there is no revision for a review to name or to be measured against",
			tree, node)
	}
	return *sha, tree, nil
}

// dispatchTree resolves the tree a cap-scope dispatch's change lives in: the
// worktree its delivery record names, or the root of the project it was made
// into when the record names no worktree — a dispatch that worked in the project
// itself.
//
// A worktree the record names and that is no longer there is refused, not
// resolved around. The record is immutable, so a missing checkout means the tree
// is gone, and comparing some other tree's revision instead would answer a
// question nobody asked and report it as the review's footing. This is the one
// place it deliberately differs from checkDir, whose checks are about the state
// of a tree and any tree will do; a revision is about one particular change.
func dispatchTree(h *command.Handler, reg *registry.Registry, node string) (string, error) {
	d, ok, err := h.DeliveryFor(capScope, node)
	if err != nil {
		return "", fmt.Errorf("read the delivery record for %s: %v", node, err)
	}
	if !ok {
		return "", fmt.Errorf(
			"%s has no delivery record, so there is no tree to read a revision from — a dispatch that was never delivered has no change to review",
			node)
	}
	if d.WorktreePath != "" {
		if fi, err := os.Stat(d.WorktreePath); err != nil || !fi.IsDir() {
			return "", fmt.Errorf(
				"the worktree %s recorded for %s is gone, so the tree this reads is gone with it — a review cannot name a revision of a checkout that is no longer there",
				d.WorktreePath, node)
		}
		return d.WorktreePath, nil
	}
	proj, err := reg.Get(d.Project)
	if err != nil {
		return "", fmt.Errorf("resolve the project %s, which %s was dispatched into: %v", d.Project, node, err)
	}
	if proj.RootPath == "" {
		return "", fmt.Errorf("project %s has no registered root, so there is no tree to read a revision from", proj.Name)
	}
	return proj.RootPath, nil
}

// ReviewsResult is the data payload of fs reviews: which dispatch was asked
// about, the done review dispatch that reads it, and the revision that review
// read — the acceptance is worth as much as the revision it was made at.
type ReviewsResult struct {
	Node       string `json:"node"`
	ReviewedBy string `json:"reviewed_by"`
	Reads      string `json:"reads,omitempty"`
}

// missingProtocol is the missing-protocol signal, and it is the rule that makes
// a type with no protocol refuse rather than run a degraded plan.
//
// The distinction it rests on is the mechanism's own: a playbook whose
// applies_when is empty matches every dispatch and is therefore the standing
// declaration — the default — while a playbook with terms is situation-specific
// and is in the plan because this dispatch's tags satisfied them. So a plan
// whose playbooks are ALL standing is a plan nothing selected: the tags matched
// the default and nothing else, and what the dispatch would run is the standing
// obligations with no protocol for its own type.
//
// It is about the PLAN and never about the type string, and the difference
// matters: `--tag code` is how this repo dispatches ordinary code work, and no
// playbook anywhere declares `applies_when: type=dev-task`. A rule that demanded
// the type appear in some applies_when would refuse the repo's normal dispatch.
// A rule about the plan accepts any selection the cap actually made, including
// the deliberate widening --also names.
//
// This is the signal the spec already describes and nothing produced:
// playbook-system.md says of the types no protocol is written for that
// "dispatching those types still refuses — now visibly, as the missing-protocol
// signal", and names the state as its off-switch — "its plan holds nothing but
// the standing `worker` playbook while its tags should have matched more". The
// mechanism could not report it while worker.yaml had no applies_when and so
// matched every dispatch: no type could match no playbook, and the plan was
// never empty. The user's decision (cap:t-4c9a3169, 2026-09-26) is that this
// refuses rather than reports.
func missingProtocol(plan []planned, taskType string) error {
	var standing []string
	for _, pb := range plan {
		if len(pb.AppliesWhen) > 0 {
			return nil
		}
		standing = append(standing, pb.Name)
	}

	// A plan with no playbooks at all is the same defect with nothing to name,
	// and naming no playbook must not read as a plan that was fine.
	resolved := "nothing"
	if len(standing) > 0 {
		resolved = strings.Join(standing, ", ")
	}
	return fmt.Errorf(
		"no playbook declares a protocol for type %q: nothing matched this dispatch's tags, so the plan is the standing declaration alone (%s), which carries no entry, work or exit steps for this type. A dispatch with no protocol is refused rather than run against a degraded plan — write one (`applies_when: type=%s`), select a playbook that matches a tag you can declare with --tag <name>, or force one into the plan with --also <playbook>",
		taskType, resolved, taskType)
}

// resolveCreates reads the node kind a dispatch leaves behind from its plan:
// the `creates:` declaration on a situation-specific playbook, defaulting to
// dispatch. It is how a batch node is a batch — batch-entry declares
// `creates: batch` — without a Go map from task type to kind. Two declarations
// that disagree refuse, naming both, rather than averaging.
func resolveCreates(plan []planned) (query.Kind, error) {
	var kind query.Kind
	from := ""
	for _, pb := range plan {
		if pb.Creates == "" {
			continue
		}
		k := query.Kind(pb.Creates)
		if kind != "" && k != kind {
			return "", fmt.Errorf(
				"playbooks %s and %s declare conflicting node kinds (%s and %s); a dispatch creates one kind of node",
				from, pb.Name, kind, k)
		}
		kind, from = k, pb.Name
	}
	if kind == "" {
		return query.KindDispatch, nil
	}
	return kind, nil
}

// batchMember resolves a --batch value to the bare id of the batch node it
// names, or "" when the flag was not given. A member's parent link is written
// into its task-created payload — an immutable event — so naming a node that is
// not a batch is refused here, before the write, naming what the node is.
func batchMember(h *command.Handler, batch string) (string, error) {
	if batch == "" {
		return "", nil
	}
	ref, err := bareNodeID(batch)
	if err != nil {
		return "", err
	}
	node, err := capNode(h, ref)
	if err != nil {
		return "", fmt.Errorf("--batch %s: %v", batch, err)
	}
	if node.Kind != query.KindBatch {
		return "", fmt.Errorf(
			"--batch %s names a %q node, not a %s node — a member's parent is a kind: batch node, and the link cannot be corrected after it is written",
			batch, node.Kind, query.KindBatch)
	}
	return ref, nil
}

// BatchMember is one member of a batch, as fs batch reports it: the node, the
// project its worker lived in, and the resources its delivery opened — the
// worktree, checkout path, branch and pane the batch's declared teardown
// removes. Branch is empty for a member whose worktree the delivery did not
// create, which is how a per-member postcondition tells a branch that was never
// the batch's to remove from one it failed to remove.
type BatchMember struct {
	Node         string `json:"node"`
	Project      string `json:"project,omitempty"`
	Worktree     string `json:"worktree,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	Branch       string `json:"branch,omitempty"`
	Pane         string `json:"pane,omitempty"`
	IntegratedBy string `json:"integrated_by,omitempty"`
}

// BatchResult is the data payload of fs batch: the batch node, its members, and
// the answer the read just gave. Complete is never vacuously true.
type BatchResult struct {
	Batch    string        `json:"batch"`
	Complete bool          `json:"complete"`
	Members  []BatchMember `json:"members"`
}

// Batch answers the batch's question: is every member integrated? It is a read
// with an exit code — 0 iff every member has a done integration dispatch naming
// it, 1 naming the unintegrated members otherwise. A batch with no members is
// refused rather than read as complete: vacuously true is not complete.
//
// Members are the batch's children: cap-scope dispatch nodes whose
// parent_node_id names the batch. Each member's integration is the same read fs
// integrated makes, so the batch gate can never disagree with a member's own
// gate. The member list carries each delivery's resources, which is what the
// batch's declared teardown loops over when every member is integrated.
func Batch(h *command.Handler, batch string) command.Response {
	ref, err := bareNodeID(batch)
	if err != nil {
		return errResp("batch: " + err.Error())
	}
	node, err := capNode(h, ref)
	if err != nil {
		return errResp("batch: " + err.Error())
	}
	if node.Kind != query.KindBatch {
		return errResp(fmt.Sprintf(
			"batch: %s is a %q node, not a %s node — fs batch reads a kind: batch node",
			ref, node.Kind, query.KindBatch))
	}

	resp := h.StatusIn(capScope, "")
	if !resp.OK {
		return errResp("batch: " + resp.Error)
	}
	result, ok := resp.Data.(command.StatusResult)
	if !ok {
		return errResp("batch: read scope " + capScope + ": no status payload")
	}

	members := make([]BatchMember, 0)
	var unintegrated []string
	for _, task := range result.Tasks {
		if task.ParentNodeID != ref || task.Kind != query.KindDispatch {
			continue
		}
		member := BatchMember{Node: task.NodeID}
		if d, recorded, err := h.DeliveryFor(capScope, task.NodeID); err == nil && recorded {
			member.Project = d.Project
			member.Worktree = d.Worktree
			member.WorktreePath = d.WorktreePath
			member.Branch = d.Branch
			member.Pane = d.PaneID
		}
		if integrated := Integrated(h, task.NodeID); integrated.OK {
			if ir, ok := integrated.Data.(IntegratedResult); ok {
				member.IntegratedBy = ir.IntegratedBy
			}
		} else {
			unintegrated = append(unintegrated, task.NodeID)
		}
		members = append(members, member)
	}

	if len(members) == 0 {
		return errResp(fmt.Sprintf(
			"batch: %s has no members — a batch with no members is not complete; dispatch its members with fs dispatch --batch %s",
			ref, ref))
	}
	if len(unintegrated) > 0 {
		return errResp(fmt.Sprintf(
			"batch: %s is not complete — %d member(s) have no done integration naming them: %s; merge each with a dispatched integration (fs dispatch --type integrate --integrates <member>)",
			ref, len(unintegrated), strings.Join(unintegrated, ", ")))
	}

	return command.Response{OK: true, Data: BatchResult{
		Batch: ref, Complete: true, Members: members,
	}}
}

// declaredSubjects is the exemption the unresolved-dispatch gate subtracts: the
// cap-scope node ids this dispatch declares a relation to, read from the flags
// fs already parses. Each relation says the named nodes are what this dispatch
// is about rather than work it piles on top of — an integration's member, a
// review's change, the dispatch a fix round repairs, a member's siblings in a
// batch, which are by construction running at the same time as it is.
//
// The flags are the declaration. A node id that merely appears in the dispatch's
// goal text grants nothing, which is what keeps this an exemption the dispatch
// states rather than one it can stumble into: every node named here was also
// resolved and refused-if-absent before this ran (integrateMember,
// reviewedDispatch, fixedDispatch, batchMember), so no dangling reference
// reaches the gate through it.
//
// A relation fs does not parse is not an exemption, and a dispatch with no
// relation declares nothing — such a dispatch still carries --allow-unresolved.
func declaredSubjects(h *command.Handler, member, reviewed string, fixes []string, batch string) (map[string]bool, error) {
	subjects := map[string]bool{}
	if member != "" {
		subjects[member] = true
	}
	if reviewed != "" {
		subjects[reviewed] = true
	}
	for _, fixed := range fixes {
		subjects[fixed] = true
	}
	if batch == "" {
		return subjects, nil
	}

	// --batch names the batch, and the batch's other members are the subjects:
	// the fan-out's purpose is that they run concurrently, so a sibling is work
	// this dispatch runs alongside, never work piled on top of it. Membership is
	// the parent link the member's own dispatch wrote, so this is the same fact
	// fs status renders as a tree, read without the tree.
	unfinished, err := h.UnfinishedIn(capScope)
	if err != nil {
		return nil, err
	}
	for _, node := range unfinished {
		if node.ParentNodeID == batch {
			subjects[node.NodeID] = true
		}
	}
	return subjects, nil
}

// withoutNodes drops every named node id from a list. It is how the nodes the
// dispatch declares a relation to are kept out of the unresolved-dispatch gate
// they would otherwise fail: that gate is about other open work.
func withoutNodes(nodes []command.UnfinishedNode, drop map[string]bool) []command.UnfinishedNode {
	if len(drop) == 0 {
		return nodes
	}
	kept := make([]command.UnfinishedNode, 0, len(nodes))
	for _, node := range nodes {
		if !drop[node.NodeID] {
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
		for _, is := range pb.Indexed {
			if is.Moment != knowledge.MomentEntry {
				continue
			}
			step := is.Step
			start := time.Now()
			item, status := entryStep(step, root, env, confirm)

			if err := lg.Append(project, trace.Line{
				ID:         trace.StepID(node, pb.Name, is.Index),
				Dispatch:   node,
				Playbook:   pb.Name,
				Moment:     is.Moment,
				Step:       is.Index,
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
						step.Body, pb.Name, is.Index, checkOutput(item))
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
func buildBrief(run entryRun, plan []planned, proj registry.Project, req PrepareRequest, nodeID string, real realization) *Brief {
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
		Engine:     real.engine,
		Agent:      real.agent,
		AgentArgs:  real.args,
		Notes: []string{
			"the locked-doc grep matches the marker text anywhere, so it has a known false positive when a repo documents the marker syntax itself",
		},
	}

	for _, pb := range plan {
		for _, is := range pb.Indexed {
			if is.Moment != knowledge.MomentWork {
				continue
			}
			step := is.Step
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
		Kind:      trace.KindPlan,
		Dispatch:  nodeID,
		Tags:      plan.Tags,
		Plan:      plan.Plan,
		Engine:    plan.Engine,
		Agent:     plan.Agent,
		AgentArgs: plan.AgentArgs,
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
//
// FS_BIN is the running binary's own path — the same rule applied to fs itself.
// A step that wants to run fs cannot say `fs`: the binary is installed into
// ~/.fs, not into PATH, so a bare `fs` is a name that resolves nowhere and the
// gate exits 127 in every project. "$FS_BIN" is the way to name it, and it is
// the path of the binary running the step rather than whatever `fs` PATH might
// one day offer. Empty when the path cannot be read, which a step must treat as
// missing rather than as a path to run.
//
// It is also how a step names a script the binary ships: "$FS_BIN" script
// <name> [args...]. The scripts are read out of the binary and written nowhere,
// so there is no directory to export and nothing to go stale — the step runs
// the same body from the same binary it dispatched under.
func checkEnv(project, taskType, goal string, cards []string, integrates string) []string {
	return []string{
		"FS_PROJECT=" + project,
		"FS_TYPE=" + taskType,
		"FS_GOAL=" + goal,
		"FS_CARDS=" + strings.Join(cards, ","),
		"FS_INTEGRATES=" + integrates,
		"FS_BIN=" + command.BinaryPath(),
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
// carrying the member it integrates (when it integrates one), the dispatch it
// reads (when it reviews one), the node kind the plan declared (creates), and
// the batch it is a member of (as its parent) — and the decision naming the
// project the work went to. It returns the node id so the cap can read its own
// dispatch back.
func recordCapNode(h *command.Handler, taskType, goal, project, integrates, reviews string, kind query.Kind, parent string) (string, error) {
	var parentPtr *string
	if parent != "" {
		parentPtr = &parent
	}
	added := h.TaskAddKind(capScope, dispatchGoal(taskType, goal), kind, "", integrates, reviews, "", "", parentPtr)
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

// nextCommand is what the brief carries in place of a command: the instruction
// that hands this prepared brief to a worker.
//
// There is no herdr line for that, and printing one was the defect. A worker's
// agent is named after the node the delivery creates —
// "dispatch-<type>-<node>" — and that node does not exist until --deliver runs,
// so any line printed here names an agent no delivery record can reach: running
// it starts a worker whose node is not on the record, and fs pending never
// learns it exists. The line and the delivery were never one command, and the
// brief said they were.
func nextCommand(b *Brief) string {
	return "prepared, not delivered — re-run this dispatch with --deliver to hand it to a worker " +
		"(the node prepared here stays open until it is delivered or closed, which is what the next " +
		"dispatch refuses on; --allow-unresolved proceeds past that)"
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
			fmt.Fprintf(&sb, "- %s (%s, order %d)\n", entry.Name, strings.Join(entry.Moments, "+"), entry.Order)
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

func errResp(msg string) command.Response {
	return command.Response{OK: false, Error: msg}
}

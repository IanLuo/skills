// Package dispatch owns the cap's dispatch lifecycle: prepare the brief, deliver
// it to a worker, and close it out.
//
// A dispatch is a worker node, a rendered brief, and a herdr delivery. Nothing
// else: Prepare records the cap's node and composes the brief, Deliver splits a
// pane, starts the agent on the launch config (launch.go), hands the brief over
// and records the binding, and Close records one evidence line and tears the
// pane down. There is no plan to build, no step to run and no trace to gate on —
// the ledger is the record, and a review is a node kind with a link rather than
// a gate.
package dispatch

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/registry"
)

// capScope is the project_id of the cap's own event table. It is implicit: a
// scope is just a string, so it is never registered and never created.
const capScope = "cap"

// IsCapScope reports whether a project_id names the cap's own scope. The CLI
// needs it to tell a write into the cap scope — which acts as cap, and is
// refused from a pane fs holds a delivery record for — from an ordinary write
// in a worker's own project, which is not.
func IsCapScope(scope string) bool { return scope == capScope }

// workerObligations are every worker's standing rules, rendered into every
// brief.
//
// They were a playbook's `say:` steps until the playbook layer went. They are
// here rather than deleted with it because they are not protocol: they are what
// a worker is told about its own node, and a brief that said nothing but the
// goal would leave a worker with no instruction to write one. The words are the
// ones worker.yaml carried.
var workerObligations = []string{
	"note your work on your node as you go — goal, status, the exact commands you ran, the commit — and mark it done with your evidence when you finish",
	"your node is named in your brief. Use it; never invent one, and never write to another node",
	"when you find work that is not yours — a gap, a broken gate, a stale fact — report it to the cap with `fs report <your project>:<your node> --question --line \"<what is wrong, and where>\"` and let the cap file it, rather than fixing it outside your card; the report is the obligation and the filing is not, because a gap node names the scope it belongs to, choosing that scope is a coordinating act, and a write into the cap's own scope is refused from your pane, which holds a delivery record",
	"a claim without the command that produced it is not evidence; paste the command and its output, or say you did not run it",
	"when a card is wrong or under-specified, say so on your node and stop — do not guess at intent, and do not widen the card to cover the guess",
	"report with `fs report <your project>:<your node> --line \"<one line>\"` when you are blocked, when you need a decision the cap has not made, or when a unit of work is finished; `--blocked`, `--question` and `--done` notify the live cap, and a report with none of them records silently for whoever reads",
	"report whether or not a cap is live — the event is written first and always, so \"recorded, not notified\" is not a failed report — and keep it a pointer, never the content: under 200 bytes, a status word and one line, because the node remains the record and the cap reads the node",
	"your final message is not read and your node is. Lead the node with the result, then the exact commands and what they printed, then one line naming what you did NOT verify — in that order, nothing repeated between them. Write it for a stranger who has the node and a job to do and cannot ask you anything",
}

// Brief is the data payload of fs dispatch.
type Brief struct {
	Goal        string   `json:"goal"`
	Project     string   `json:"project"`
	RootPath    string   `json:"root_path"`
	TaskType    string   `json:"task_type"`
	Obligations []string `json:"obligations,omitempty"`
	LockedDocs  []string `json:"locked_docs"`
	Notes       []string `json:"notes"`
	CapNodeID   string   `json:"cap_node_id"`

	// NextCommand is what the cap does next: the instruction that hands this
	// brief to a worker with --deliver. It is not a command fs runs and not a
	// command the cap can run as printed — see nextCommand.
	NextCommand string `json:"next_command"`

	// Engine, Agent and AgentArgs are the launch config the worker is started
	// on. They are frozen in code (launch.go, contract R8) and carried on the
	// brief so a reader can see what a dispatch ran with, and so Deliver starts
	// from the brief it was handed rather than from a file that may be gone.
	Engine    string   `json:"engine,omitempty"`
	Agent     string   `json:"agent,omitempty"`
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
	Project  string
	TaskType string
	Goal     string

	// Integrates names the member's cap node an integration merges, or empty for
	// a dispatch that integrates nothing.
	Integrates string

	// Reviews names the cap-scope dispatch node whose change this dispatch
	// reads (--reviews). Empty for a dispatch that reviews nothing; it is a
	// different question from Integrates, which names what this dispatch merges.
	Reviews string

	// Fixes names the cap-scope dispatch nodes this one repairs (--fixes), and it
	// is repeatable because a fix round usually answers more than one: the change
	// it repairs and the review whose finding it fixes are both open while the
	// fix is made. This is the declaration that keeps the unresolved-dispatch
	// read from treating them as open work piled on top of the fix.
	Fixes []string

	AllowUnresolved bool
}

// Prepare records the dispatch's node and composes the brief.
//
// Integrates is the member's cap node an integration merges, or empty for a
// dispatch that integrates nothing. A non-empty value must name a cap-scope
// dispatch node: the link is recorded on this dispatch's own task-created
// payload, so it exists from creation and never depends on parsing a goal.
//
// It refuses in one case rather than preparing work in a bad state: an earlier
// dispatch is still unresolved. --allow-unresolved is that read's one override,
// and --fixes is not an override but a declaration — it subtracts the
// dispatches the round exists to repair (see declaredSubjects), so they are
// never read as work piled on top.
func Prepare(h *command.Handler, reg *registry.Registry, req PrepareRequest) command.Response {
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
	// carry, and it is the half a cap chose. Checked before any node exists, so
	// an input herdr cannot encode is refused by fs, naming the limit — rather
	// than reaching herdr as invalid_agent_name, which names neither fs nor the
	// input.
	if err := validTaskType(req.TaskType); err != nil {
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

	// A dispatch the cap prepared but never delivered is still open work.
	// Refuse to pile another one on top of it unless the cap says to proceed.
	// --allow-unresolved is this read's only way past, and it is not an
	// override of anything else: it names the open dispatches it proceeded
	// past, and the node carries that as a decision.
	//
	// What the read subtracts is the set of nodes this dispatch declares a
	// relation to — see declaredSubjects. Those are the dispatches it is about
	// or runs alongside, not open work piled on top of it.
	subjects := declaredSubjects(member, reviewed, fixes)
	unresolved, err := unresolvedDispatches(h)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}
	unresolved = withoutNodes(unresolved, subjects)
	if len(unresolved) > 0 && !req.AllowUnresolved {
		return errResp(unresolvedError(unresolved))
	}

	nodeID, err := recordCapNode(h, req.TaskType, req.Goal, req.Project, member, reviewed)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}

	brief := buildBrief(proj, req, nodeID)
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

	// The override is recorded by the flag that granted it, on the node it
	// granted it for.
	if req.AllowUnresolved && len(unresolved) > 0 {
		if err := recordUnresolvedOverride(h, nodeID, unresolved); err != nil {
			return errResp(fmt.Sprintf("dispatch: %v", err))
		}
	}

	return command.Response{OK: true, Data: brief}
}

// buildBrief composes the brief: the goal, the tree the work happens in, the
// node the worker owns, every worker's standing obligations, the locked docs the
// tree carries, and the launch config.
func buildBrief(proj registry.Project, req PrepareRequest, nodeID string) *Brief {
	return &Brief{
		Goal:        req.Goal,
		Project:     proj.Name,
		RootPath:    proj.RootPath,
		TaskType:    req.TaskType,
		Obligations: workerObligations,
		LockedDocs:  findLockedDocs(proj.RootPath),
		CapNodeID:   nodeID,
		Engine:      LaunchEngine,
		Agent:       LaunchAgent,
		AgentArgs:   launchArgs(),
		Notes: []string{
			"the locked-doc grep matches the marker text anywhere, so it has a known false positive when a repo documents the marker syntax itself",
		},
	}
}

// integrateMember resolves a --integrates value to the bare id of the cap-scope
// dispatch node it names, or to "" when the flag was not given.
//
// It refuses a node that does not exist, and one that is not a dispatch node,
// naming it: the link is written into the integration's task-created payload, and
// events are immutable, so a wrong link could never be corrected. "Is this a
// dispatch node" is the same question the dispatch reads ask, so it is asked the
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
// review whose finding it fixes are both open while the fix is made.
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
// a member, the dispatch a review reads, one of a fix round's subjects — to its
// bare id.
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

// Reviews answers a close's question: was this dispatch's change reviewed? It
// returns the done review dispatch that names this node, or refuses.
//
// A done review dispatch carrying `reviews: <this node>` is the record that a
// review happened — done, and not merely dispatched, because a review is not
// finished until its own work is.
//
// It deliberately does not read the findings: a fixed one leaves no record of
// itself and an accepted one is a sentence, so no check could decide that from
// the store; asking a second question here would be a gate reporting compliance
// it never tested. This asks the one thing the record can answer: whether a done
// review names this node.
func Reviews(h *command.Handler, node string) command.Response {
	ref, err := bareNodeID(node)
	if err != nil {
		return errResp("reviews: " + err.Error())
	}

	// The ref has to resolve to something before "is it reviewed" is a question
	// with an answer, and which thing it resolved to is half the answer. An id
	// that is not there is a typo, one that is there and is not a dispatch is
	// the wrong kind of thing to review, and only a dispatch that exists can be
	// unreviewed.
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
	open := ""
	for _, task := range result.Tasks {
		if task.Reviews != ref || task.Kind != query.KindDispatch {
			continue
		}
		if task.Status != "done" {
			open = task.NodeID
			continue
		}
		return command.Response{OK: true, Data: ReviewsResult{
			Node: ref, ReviewedBy: task.NodeID,
		}}
	}
	if open != "" {
		return errResp(fmt.Sprintf(
			"reviews: %s is not reviewed — the review dispatch %s names it but is not done; finish it, then close again",
			ref, open))
	}
	return errResp(fmt.Sprintf(
		"reviews: %s is not reviewed — no dispatch names it; review it with `fs dispatch --project <project> --type review-task --goal \"...\" --reviews %s`, and close only once that dispatch is done",
		ref, ref))
}

// ReviewsResult is the data payload of fs reviews: which dispatch was asked
// about, and the done review dispatch that reads it.
type ReviewsResult struct {
	Node       string `json:"node"`
	ReviewedBy string `json:"reviewed_by"`
}

// declaredSubjects is what the unresolved-dispatch read subtracts: the cap-scope
// node ids this dispatch declares a relation to, read from the flags fs already
// parses. Each relation says the named nodes are what this dispatch is about
// rather than work it piles on top of — an integration's member, a review's
// change, the dispatch a fix round repairs.
//
// The flags are the declaration. A node id that merely appears in the dispatch's
// goal text grants nothing, which is what keeps this an exemption the dispatch
// states rather than one it can stumble into: every node named here was also
// resolved and refused-if-absent before this ran (integrateMember,
// reviewedDispatch, fixedDispatches), so no dangling reference reaches the read
// through it.
//
// A relation fs does not parse is not an exemption, and a dispatch with no
// relation declares nothing — such a dispatch still carries --allow-unresolved.
func declaredSubjects(member, reviewed string, fixes []string) map[string]bool {
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
	return subjects
}

// withoutNodes drops every named node id from a list. It is how the nodes the
// dispatch declares a relation to are kept out of the unresolved-dispatch read
// they would otherwise fail: that read is about other open work.
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

// unresolvedDispatches returns every dispatch node that is not done and not
// already covered by a delivery, in every scope. A node is a dispatch because it
// was written as one, so a dispatch whose goal was later edited is still
// recognised — the failure the goal prefix had.
//
// The scope is not a filter. A dispatch written in a project's own scope is in
// flight exactly like one the cap prepared, and reading the cap's scope alone let
// the next dispatch proceed while a dispatch stood — the same blindness fs
// pending had, in the same place: one scope's events are not the record.
//
// Both ends of a link are kind=dispatch, so the same rule fs pending applies is
// applied here: the worker node of a dispatch that is still open is that
// dispatch, not a second one. Without it every open dispatch would be named
// twice, and the exemption that subtracts a declared dispatch would subtract the
// cap's node while the worker's stayed behind to refuse the dispatch anyway.
func unresolvedDispatches(h *command.Handler) ([]command.UnfinishedNode, error) {
	scopes, err := h.Scopes()
	if err != nil {
		return nil, err
	}

	var nodes []command.UnfinishedNode
	covered := map[string]bool{}
	for _, scope := range scopes {
		unfinished, err := h.UnfinishedIn(scope)
		if err != nil {
			return nil, err
		}
		for _, node := range unfinished {
			if node.Kind != query.KindDispatch {
				continue
			}
			nodes = append(nodes, node)

			delivery, recorded, err := h.DeliveryFor(scope, node.NodeID)
			if err != nil {
				return nil, err
			}
			if recorded && delivery.Project != "" && delivery.Node != "" {
				covered[workerRef(delivery.Project, delivery.Node)] = true
			}
		}
	}

	var unresolved []command.UnfinishedNode
	for _, node := range nodes {
		if covered[workerRef(node.ProjectID, node.NodeID)] {
			continue
		}
		unresolved = append(unresolved, node)
	}
	return unresolved, nil
}

// nodeRef names a node the way every command resolves one: the bare id for the
// cap's own scope, and the address form for a node in a project. A bare id
// resolves in the cap's scope, so an id without its scope is a node a reader can
// be told about and cannot reach.
func nodeRef(node command.UnfinishedNode) string {
	if node.ProjectID == "" || node.ProjectID == capScope {
		return node.NodeID
	}
	return node.ProjectID + ":" + node.NodeID
}

// unresolvedError names each unresolved dispatch and says how to clear it.
func unresolvedError(unresolved []command.UnfinishedNode) string {
	named := make([]string, len(unresolved))
	for i, node := range unresolved {
		named[i] = fmt.Sprintf("%s (%q)", nodeRef(node), node.Goal)
	}
	return fmt.Sprintf(
		"dispatch: unresolved dispatches: %s; resolve them (fs task update ID --status done --decision ...) or re-run with --allow-unresolved to proceed",
		strings.Join(named, ", "))
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
// carrying the member it integrates (when it integrates one) and the dispatch it
// reads (when it reviews one) — and the decision naming the project the work went
// to. It returns the node id so the cap can read its own dispatch back.
func recordCapNode(h *command.Handler, taskType, goal, project, integrates, reviews string) (string, error) {
	added := h.TaskAddKind(capScope, dispatchGoal(taskType, goal), query.KindDispatch, "", integrates, reviews, "", "", nil)
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

// recordUnresolvedOverride records the cap's --allow-unresolved override, naming
// the dispatches it proceeded past — the value the flag was over.
func recordUnresolvedOverride(h *command.Handler, nodeID string, unresolved []command.UnfinishedNode) error {
	named := make([]string, len(unresolved))
	for i, node := range unresolved {
		named[i] = nodeRef(node)
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

// renderBrief is the text the cap hands the worker: goal, project root, the node
// the worker owns, the standing obligations, the launch config it was started
// on, the locked docs the tree carries, and the notes about this brief.
func renderBrief(b *Brief) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Goal: %s\n", b.Goal)
	fmt.Fprintf(&sb, "Project: %s (root %s)\n", b.Project, b.RootPath)

	node := b.WorkerNode
	if node == "" {
		node = "(not created yet — it is created when the brief is delivered)"
	}
	fmt.Fprintf(&sb, "Your node: %s\n", node)

	if len(b.Obligations) > 0 {
		sb.WriteString("Obligations:\n")
		for _, obligation := range b.Obligations {
			fmt.Fprintf(&sb, "- %s\n", obligation)
		}
	}

	fmt.Fprintf(&sb, "Launch config: engine=%s agent=%s args=%s\n",
		b.Engine, b.Agent, strings.Join(b.AgentArgs, " "))

	if len(b.LockedDocs) > 0 {
		fmt.Fprintf(&sb, "Locked docs: %s\n", strings.Join(b.LockedDocs, ", "))
	} else {
		sb.WriteString("Locked docs: none\n")
	}

	if len(b.Notes) > 0 {
		sb.WriteString("Notes:\n")
		for _, note := range b.Notes {
			fmt.Fprintf(&sb, "- %s\n", note)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func errResp(msg string) command.Response {
	return command.Response{OK: false, Error: msg}
}

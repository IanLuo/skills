// Package command validates input, produces typed events, and manages the
// response envelope. It is the gatekeeper between CLI and Event Store.
// (SYSTEM-DESIGN R1, ARCHITECTURE R4)
package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/IanLuo/memstore"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

// Valid task statuses (SYSTEM-DESIGN R2).
var validStatuses = map[string]bool{
	"pending": true,
	"active":  true,
	"done":    true,
	"blocked": true,
}

// Response is the structured JSON envelope returned by every command.
// {ok, error?, data?} + exit code (ARCHITECTURE R4).
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

// EventData is the data payload for single-event responses.
type EventData struct {
	EventID   string          `json:"event_id"`
	EventType store.EventType `json:"event_type"`
	NodeID    string          `json:"node_id,omitempty"`
	CommitSHA *string         `json:"commit_sha,omitempty"`
}

// UpdateResult is the data payload when a command produces multiple events.
type UpdateResult struct {
	Events []EventData `json:"events"`
}

// MergeResult is the data payload for fs task merge: the node that was absorbed
// and the node that took it in. Each is a full "<project>:<node>" address,
// because a fold's two ends may be in two scopes and a bare id would not say
// which — the same reason the tool prints addresses in its briefs and reports.
type MergeResult struct {
	Absorbed string      `json:"absorbed"`
	Into     string      `json:"into"`
	Events   []EventData `json:"events"`
}

// DeliveryRecord is the payload of a delivery-recorded event: the structural
// binding of a cap dispatch node to the pane, the tab that pane sits in, and the
// agent carrying its brief, and to the node created for the worker in the target
// project.
//
// Project and Node are what let close-out name the worker without the cap
// guessing. They are absent only on records written before the link existed;
// TabID likewise, on records written before it was recorded at all — it names
// the cap's own tab, which the worker shares and which is never closed;
// Type, Worktree, and WorktreePath, on records written before the cleanup gate
// and worktree teardown existed. Type is what close-out reads to find the exit
// gate, and it is structural — never recovered from the goal string, which is
// prose.
//
// Worktree names the herdr workspace; WorktreePath names the checkout it was
// made from. Both are recorded because they can die separately: herdr can
// forget a workspace — its agent having ended — while the checkout survives on
// disk, and then the workspace id alone names nothing that can be removed.
//
// Branch names the branch that checkout stands on, and it is recorded only for
// a worktree this delivery created — `--worktree-new`. A worktree named with
// `--worktree` came from elsewhere, and its branch is not this delivery's to
// remove; an empty Branch is how an exit step tells those two apart without
// asking a question it cannot answer. Empty is also what a created worktree
// carries when herdr reports no branch at all — a detached checkout — so the
// field is a claim about what was created, never a guess at what to delete.
type DeliveryRecord struct {
	PaneID       string `json:"pane_id"`
	TabID        string `json:"tab_id,omitempty"`
	Agent        string `json:"agent"`
	Engine       string `json:"engine"`
	Project      string `json:"project"`
	Node         string `json:"node"`
	Type         string `json:"type,omitempty"`
	Worktree     string `json:"worktree,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	Branch       string `json:"branch,omitempty"`
}

// TaskInfo represents derived task state for status output.
type TaskInfo struct {
	NodeID       string     `json:"node_id"`
	Goal         string     `json:"goal"`
	Kind         query.Kind `json:"kind"`
	FoundBy      string     `json:"found_by,omitempty"`
	Status       string     `json:"status"`
	ParentNodeID string     `json:"parent_node_id,omitempty"`
	Orphan       bool       `json:"orphan,omitempty"`
	Decisions    []string   `json:"decisions,omitempty"`
	Knowledge    []string   `json:"knowledge,omitempty"`
	// BlockCheck is the check the node's last block recorded, when one did.
	// Every reader that renders a status re-runs it, so a block whose condition
	// is over is reported as such in all of them.
	BlockCheck string `json:"block_check,omitempty"`
	// Check is the check the node recorded when it was created, when one was: a
	// gap's claim, which every reader that renders a status re-runs the same way.
	Check string `json:"check,omitempty"`
	// Integrates names the cap-scope dispatch node an integration dispatch
	// merges. It is the structural link, read from the task-created payload.
	Integrates string `json:"integrates,omitempty"`
	// Reviews names the cap-scope dispatch node a review dispatch reads. Same
	// kind of link, opposite direction, and never the same value: a dispatch may
	// carry either, both, or neither.
	Reviews string `json:"reviews,omitempty"`
	// Severity is a gap's weight, empty when nobody has weighed it. This is the
	// stored level and not fs gaps's label: the label and the counts belong to
	// the backlog list, and this is one node's derived state.
	Severity string `json:"severity,omitempty"`
	// Absorbed names the nodes this one has taken in by a merge, in the order
	// they were folded in. It is what lets a reader of the survivor alone see
	// that two nodes became one.
	Absorbed []string `json:"absorbed,omitempty"`
}

// StatusResult is the data payload for fs status.
type StatusResult struct {
	ProjectID string     `json:"project_id"`
	Tasks     []TaskInfo `json:"tasks"`
}

// UnfinishedNode is one task that is not done, tagged with the scope it lives
// in and its structural kind, so fs unfinished can group every scope's work.
type UnfinishedNode struct {
	ProjectID string     `json:"project_id"`
	NodeID    string     `json:"node_id"`
	Status    string     `json:"status"`
	Goal      string     `json:"goal"`
	Kind      query.Kind `json:"kind"`
	FoundBy   string     `json:"found_by,omitempty"`
	// ParentNodeID is the node this one was created under, for a reader that has
	// to ask what a node is part of rather than what it is — the batch a member
	// belongs to. It is derivable from fs status, which walks the tree; it is
	// carried here so a reader can ask the question without the tree.
	ParentNodeID string `json:"parent_node_id,omitempty"`
	// BlockCheck is the command the node's last block recorded, when one did:
	// what every reader re-runs to tell a live block from one whose condition is
	// over. Empty for a block with no check.
	BlockCheck string `json:"block_check,omitempty"`
	// Check is the command the node recorded when it was created, when one was:
	// what the same readers re-run to tell a claim that still stands from one
	// that is over. Empty for a node created without one.
	Check string `json:"check,omitempty"`
}

// GapNode is one kind=gap node: something noticed and not dispatched, with
// found_by naming the node that noticed it when one was recorded, and severity
// its weight. Severity is rendered and never omitted, so a gap nobody has
// weighed reads as the word "unset" rather than as an empty string a reader
// would have to guess at, or as the lightest level it is not.
type GapNode struct {
	ProjectID string `json:"project_id"`
	NodeID    string `json:"node_id"`
	Status    string `json:"status"`
	Goal      string `json:"goal"`
	FoundBy   string `json:"found_by,omitempty"`
	Severity  string `json:"severity"`

	// level is the same weight unrendered, for the sort — a declared level has
	// a rank and the word "unset" does not, so the sort reads this rather than
	// converting the label back into a level.
	level query.Severity
}

// GapCounts is the header fs gaps prints before the list: what the reader is
// looking at, so nobody has to check 181 nodes by hand to find out whether any
// of them is still open.
type GapCounts struct {
	Total int `json:"total"`
	Open  int `json:"open"`
	Done  int `json:"done"`
	// OpenBySeverity is the half a cap acts on: the weight of the backlog. All
	// four levels are named whatever their counts are, so a zero reads as a
	// zero rather than as a level that has gone missing.
	OpenBySeverity SeverityCounts `json:"open_by_severity"`
}

// SeverityCounts is a count per level, in the order a cap triages in, with the
// unweighed backlog last.
type SeverityCounts struct {
	Severe int `json:"severe"`
	Defect int `json:"defect"`
	Prose  int `json:"prose"`
	Unset  int `json:"unset"`
}

// add counts one gap's weight. Anything that is not a declared level is the
// unweighed backlog, which is the same reading parseSeverity gives it.
func (c *SeverityCounts) add(s query.Severity) {
	switch s {
	case query.SeveritySevere:
		c.Severe++
	case query.SeverityDefect:
		c.Defect++
	case query.SeverityProse:
		c.Prose++
	default:
		c.Unset++
	}
}

// Handler executes commands against a store.
type Handler struct {
	store     *store.Store
	commitSHA *string
	logger    *slog.Logger
}

// NewHandler opens a store at path and returns a handler.
func NewHandler(dbPath string) (*Handler, error) {
	s, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &Handler{store: s, logger: slog.Default()}, nil
}

// SetLogger sets the structured logger for this handler.
func (h *Handler) SetLogger(l *slog.Logger) {
	h.logger = l
}

// Logger returns the handler's logger, so a read that composes its own answer —
// fs pending and its parity gate — records on the same running log as the
// commands.
func (h *Handler) Logger() *slog.Logger {
	return h.logger
}

// AttachProjection opens the memstore file at path and folds every later append
// into it. The CLI attaches ~/.memstore/memory.db once per process; a test
// attaches a scratch file. A handler with no projection attached folds nothing,
// which is what keeps every pre-slice-2 unit test hermetic.
func (h *Handler) AttachProjection(path string) error {
	ms, err := store.OpenProjection(path)
	if err != nil {
		return err
	}
	h.store.AttachProjection(ms)
	return nil
}

// Projection returns the attached projection, or nil.
func (h *Handler) Projection() *memstore.Store {
	return h.store.Projection()
}

// Scopes lists every scope the event store holds. A state read needs the set to
// know which projects to ask the projection about; it is a DISTINCT query over
// the event store, not a replay.
func (h *Handler) Scopes() ([]string, error) {
	return h.store.Scopes()
}

// StateVerify re-folds every scope into a scratch projection and compares it to
// the live one, naming the first difference per scope and the two files it
// compared. It writes nothing.
func (h *Handler) StateVerify(projPath string) ([]store.StateIssue, store.VerifyReport, error) {
	return h.store.StateVerify(projPath)
}

// StateRebuild re-folds every scope into the live projection file.
func (h *Handler) StateRebuild(projPath string) (int, error) {
	return h.store.StateRebuild(projPath)
}

// Close releases the underlying store.
func (h *Handler) Close() error {
	return h.store.Close()
}

// SetCommitSHA sets the git HEAD sha to embed in events (ARCHITECTURE R4).
func (h *Handler) SetCommitSHA(sha *string) {
	h.commitSHA = sha
}

// storeWriteOk clears a projection failure and leaves every other error for the
// caller's error path.
//
// The event is appended and committed before its fold runs (NFR Ordering), so a
// fold failure is not a failed write: the action is recorded and only the
// projection is behind. Reporting it as a failed write would invite a retry,
// which appends a second event and doubles the state — the failure says so and
// names the heal instead (AC10).
func (h *Handler) storeWriteOk(op, projectID string, err error) error {
	if err == nil {
		return nil
	}
	var pf *store.ProjectionFailure
	if errors.As(err, &pf) {
		h.logger.Error(pf.Error(), "command", op, "project", projectID, "event_committed", true)
		return nil
	}
	return err
}

// ProjectCreate appends a project-created event. AC1.
func (h *Handler) ProjectCreate(name, rootPath string) Response {
	if name == "" {
		return errResp("project name is required")
	}
	if rootPath == "" {
		return errResp("project root path is required")
	}

	payload, _ := json.Marshal(map[string]string{
		"name":      name,
		"root_path": rootPath,
	})

	id, err := h.store.Append(store.Event{
		Type:      store.ProjectCreated,
		ProjectID: name,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	})
	err = h.storeWriteOk("project-create", name, err)
	if err != nil {
		h.logger.Error("store append failed", "command", "project-create", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "project-create", "project", name, "event_id", id)
	return okResp(EventData{
		EventID:   id,
		EventType: store.ProjectCreated,
		CommitSHA: h.commitSHA,
	})
}

// TaskAdd appends a task-created event of the default kind, work. AC2.
func (h *Handler) TaskAdd(projectID, goal string, parentNodeID *string) Response {
	return h.TaskAddKind(projectID, goal, query.KindWork, "", "", "", "", "", parentNodeID)
}

// TaskAddKind appends a task-created event carrying its structural kind — and,
// for a gap, the node that noticed it and its weight; for an integration, the
// member it integrates; for a review, the dispatch whose change it reads; and,
// when one was given, the check that would show its claim is over. It is the
// write side of the kind the dispatch gates and fs gaps read, and of the two
// links the member's exit gate and a dispatch's close each read.
func (h *Handler) TaskAddKind(projectID, goal string, kind query.Kind, foundBy, integrates, reviews, check string, severity query.Severity, parentNodeID *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if goal == "" {
		return errResp("goal is required")
	}
	if !query.ValidKind(kind) {
		return errResp(fmt.Sprintf("invalid kind: %q (valid: work, dispatch, gap)", kind))
	}
	// A severity is a backlog's weight. A node that is dispatched rather than
	// triaged has no backlog to be weighed out of, so it is refused here rather
	// than stored to be counted by nobody.
	if severity != "" {
		if kind != query.KindGap {
			return errResp(fmt.Sprintf("severity is a gap's weight and this node is a %s node", kind))
		}
		if !query.ValidSeverity(severity) {
			return errResp(fmt.Sprintf("invalid severity: %q (valid: severe, defect, prose)", severity))
		}
	}
	if foundBy != "" {
		if err := validNodeRef(foundBy); err != nil {
			return errResp(err.Error())
		}
		// A provenance field that names nothing is a claim with no evidence, which
		// is what this store refuses everywhere else: the gap would point at
		// nothing forever, and events are immutable, so the mistake could never be
		// corrected. Existence is a task-created event, not tree membership — the
		// same test the parent check below makes.
		missing, err := h.foundByMissing(foundBy)
		if err != nil {
			h.logger.Error("store replay failed", "command", "task-add", "error", err)
			return errResp(err.Error())
		}
		if missing != "" {
			return errResp(missing)
		}
	}
	if integrates != "" {
		if err := validIntegrates(integrates); err != nil {
			return errResp(err.Error())
		}
	}
	if reviews != "" {
		if err := validReviews(reviews); err != nil {
			return errResp(err.Error())
		}
	}

	// A parent that is not in the project would leave the node unreachable in
	// the tree: not a root, never a child, invisible to fs status — and events
	// are immutable, so the mistake could never be corrected. Refuse it here.
	// Existence is a task-created event, not tree membership: derived state
	// materializes a node from any event written to it, so a conjured node (see
	// NodeExists) would otherwise be an acceptable parent.
	//
	// --parent takes the address form, and the tree has no cross-scope edges: a
	// parent is a node in the child's own scope, so a qualifier naming another is
	// refused naming both rather than followed.
	if parentNodeID != nil && *parentNodeID != "" {
		scope, parentID, err := SplitAddress(projectID, *parentNodeID)
		if err != nil {
			return errResp(err.Error())
		}
		if scope != projectID {
			return errResp(fmt.Sprintf(
				"parent %s names project %s but the node is in project %s — a parent is a node in the child's own scope",
				*parentNodeID, scope, projectID))
		}
		exists, err := h.NodeExists(projectID, parentID)
		if err != nil {
			h.logger.Error("store replay failed", "command", "task-add", "error", err)
			return errResp(err.Error())
		}
		if !exists {
			return errResp(fmt.Sprintf("parent %s does not exist in project %s", parentID, projectID))
		}
		// The qualifier is an address, not part of the id: what is stored is the
		// node, so a reader of the tree finds it.
		parentNodeID = &parentID
	}

	nodeID := newNodeID()
	payload := map[string]string{"goal": goal, "kind": string(kind)}
	if foundBy != "" {
		payload["found_by"] = foundBy
	}
	if integrates != "" {
		payload["integrates"] = integrates
	}
	if reviews != "" {
		payload["reviews"] = reviews
	}
	if check != "" {
		payload["check"] = check
	}
	if severity != "" {
		payload["severity"] = string(severity)
	}
	data, _ := json.Marshal(payload)

	id, err := h.store.Append(store.Event{
		Type:         store.TaskCreated,
		ProjectID:    projectID,
		NodeID:       &nodeID,
		ParentNodeID: parentNodeID,
		CommitSHA:    h.commitSHA,
		Payload:      data,
	})
	err = h.storeWriteOk("task-add", projectID, err)
	if err != nil {
		h.logger.Error("store append failed", "command", "task-add", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "task-add", "project", projectID, "node_id", nodeID, "kind", kind, "event_id", id)
	return okResp(EventData{
		EventID:   id,
		EventType: store.TaskCreated,
		NodeID:    nodeID,
		CommitSHA: h.commitSHA,
	})
}

// validNodeRef refuses a found_by that is not "<project>:<node>". It is the only
// thing naming what a gap was found by, so a typo must fail at write time.
func validNodeRef(ref string) error {
	project, node, ok := strings.Cut(ref, ":")
	if !ok || project == "" || node == "" || strings.Contains(node, ":") {
		return fmt.Errorf("found_by must be \"<project>:<node>\", got %q", ref)
	}
	return nil
}

// validIntegrates refuses an integrates link that is not a bare node id. The
// link is the one structural statement of which member an integration merges,
// and it names a node in the integration's own scope — so a qualified or
// malformed value must fail at write time, where it can still be corrected.
func validIntegrates(ref string) error {
	if strings.ContainsAny(ref, ": \t") {
		return fmt.Errorf("integrates must be a bare node id (e.g. t-1a2b3c4d), got %q", ref)
	}
	return nil
}

// validReviews refuses a reviews link that is not a bare node id, for the same
// reason and with the same shape as validIntegrates: it names a node in the
// review's own scope, so a qualified or malformed value must fail at write time,
// where it can still be corrected.
func validReviews(ref string) error {
	if strings.ContainsAny(ref, ": \t") {
		return fmt.Errorf("reviews must be a bare node id (e.g. t-1a2b3c4d), got %q", ref)
	}
	return nil
}

// NodeExists reports whether a node was created in the project. The test is a
// task-created event, not tree membership: derived state materializes a node
// from any event that names it, so a node that only ever had a status or
// decision written to it — the damage the old path left behind — is visible in
// fs status while never having been created. A node whose task-created parent is
// missing is an orphan, which is a node that exists.
func (h *Handler) NodeExists(projectID, nodeID string) (bool, error) {
	filter := &store.ReplayFilter{
		Types:  []store.EventType{store.TaskCreated},
		NodeID: &nodeID,
	}
	events, err := h.store.Replay(projectID, filter)
	if err != nil {
		return false, err
	}
	return len(events) > 0, nil
}

// foundByMissing reports why a --found-by reference names no node, or "" when it
// names one.
//
// A reference carries its own scope prefix, so only that project is the one the
// reference means — but the way this has actually gone wrong is a prefix that is
// the wrong part, taken from some other command's output. So the other scopes are
// searched too, and the refusal can say where the node really is: one query on a
// write-time path, in exchange for a refusal the reader can act on instead of a
// guess. The message names every project searched, so an empty answer is
// distinguishable from a check that did not run.
func (h *Handler) foundByMissing(ref string) (string, error) {
	project, node, _ := strings.Cut(ref, ":")
	exists, err := h.NodeExists(project, node)
	if err != nil {
		return "", err
	}
	if exists {
		return "", nil
	}

	scopes, err := h.store.Scopes()
	if err != nil {
		return "", err
	}
	searched := []string{project}
	for _, scope := range scopes {
		if scope == project {
			continue
		}
		exists, err := h.NodeExists(scope, node)
		if err != nil {
			return "", err
		}
		if exists {
			return fmt.Sprintf(
				"found_by %s: project %s has no such node, but %s does — the reference carries its own scope prefix, so use %s:%s",
				ref, project, scope, scope, node), nil
		}
		searched = append(searched, scope)
	}
	return fmt.Sprintf("found_by %s does not exist — no such node in %s",
		ref, strings.Join(searched, ", ")), nil
}

// SplitAddress resolves a node reference into the scope it names and the bare
// node id. It is the one resolver for the address form the tool prints
// everywhere — every brief, every report line, every card — so that the form is
// accepted wherever a node is named, rather than each path parsing it for
// itself and some of them not at all.
//
// A node id is "t-<hex>", so a colon never appears in one: a reference carrying
// a colon names its own scope, unambiguously, and a reference without one takes
// defaultScope — the scope the caller would have used anyway.
//
// It locates, it does not judge. Whether the scope it returns may be used here
// is the caller's question, and its answer is not the same everywhere: a write
// must land in the scope the command resolved, a dispatch link must live in the
// cap's scope, and task merge's two operands are the two ends of a fold and may
// name two scopes by construction. Each path states its own judgement against
// the scope this returns.
func SplitAddress(defaultScope, ref string) (scope, id string, err error) {
	if ref == "" {
		return "", "", errors.New("a node is required")
	}
	scope, id, qualified := strings.Cut(ref, ":")
	if !qualified {
		return defaultScope, ref, nil
	}
	if id == "" {
		return "", "", fmt.Errorf("%s names no node", ref)
	}
	return scope, id, nil
}

// resolveWriteNode resolves the node id a write names and confirms it exists in
// projectID. A writer that appends an event for a node that was never created
// conjures a work item with no task-created event: a status and decisions for
// something that does not exist. Refusing here, before any append, is what makes
// "a write lands on the node it names, or fails" true.
//
// The write targets one scope, so a qualifier naming a different one is refused
// naming both: the caller named two scopes for one write, and which they meant
// is not this layer's to guess.
func (h *Handler) resolveWriteNode(projectID, nodeID string) (string, error) {
	scope, id, err := SplitAddress(projectID, nodeID)
	if err != nil {
		return "", err
	}
	if scope != projectID {
		return "", fmt.Errorf("id %q names project %s but the write targets project %s", nodeID, scope, projectID)
	}

	exists, err := h.NodeExists(projectID, id)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("node %s does not exist in project %s", id, projectID)
	}
	return id, nil
}

// TaskUpdate appends status-changed and optionally decision-recorded events. AC4.
func (h *Handler) TaskUpdate(projectID, nodeID, status, decision string, commitSHA *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	sha := h.commitSHA
	if commitSHA != nil {
		sha = commitSHA
	}

	var events []EventData

	if status != "" {
		if !validStatuses[status] {
			return errResp(fmt.Sprintf("invalid status: %s (valid: pending, active, done, blocked)", status))
		}

		// Derive current status for the from field.
		from := h.deriveStatus(projectID, nodeID)

		payload, _ := json.Marshal(map[string]string{"from": from, "to": status})
		id, err := h.store.Append(store.Event{
			Type:      store.StatusChanged,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: sha,
			Payload:   payload,
		})
		err = h.storeWriteOk("task-update", projectID, err)
		if err != nil {
			return errResp(err.Error())
		}
		events = append(events, EventData{
			EventID:   id,
			EventType: store.StatusChanged,
			NodeID:    nodeID,
			CommitSHA: sha,
		})
	}

	if decision != "" {
		payload, _ := json.Marshal(map[string]string{"summary": decision})
		id, err := h.store.Append(store.Event{
			Type:      store.DecisionRecorded,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: sha,
			Payload:   payload,
		})
		err = h.storeWriteOk("task-update", projectID, err)
		if err != nil {
			return errResp(err.Error())
		}
		events = append(events, EventData{
			EventID:   id,
			EventType: store.DecisionRecorded,
			NodeID:    nodeID,
			CommitSHA: sha,
		})
	}

	if len(events) == 0 {
		return errResp("nothing to update: provide --status and/or --decision")
	}

	h.logger.Info("command executed", "command", "task-update", "project", projectID, "node_id", nodeID, "events", len(events))
	return okResp(UpdateResult{Events: events})
}

// RecordDelivery appends a delivery-recorded event on a cap dispatch node,
// binding it to the pane and agent that carry its brief and to the node created
// for the worker in the target project. Recorded by the dispatcher at delivery
// time, so close-out reads the binding rather than whatever a decision happened
// to say.
func (h *Handler) RecordDelivery(projectID, nodeID string, d DeliveryRecord) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if d.PaneID == "" {
		return errResp("delivery pane_id is required")
	}
	if d.Project == "" {
		return errResp("delivery project is required: it names the worker node's scope")
	}
	if d.Node == "" {
		return errResp("delivery node is required: it names the node the worker was given")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	payload, _ := json.Marshal(d)
	id, err := h.store.Append(store.Event{
		Type:      store.DeliveryRecorded,
		ProjectID: projectID,
		NodeID:    &nodeID,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	})
	err = h.storeWriteOk("record-delivery", projectID, err)
	if err != nil {
		h.logger.Error("store append failed", "command", "record-delivery", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "record-delivery", "project", projectID, "node_id", nodeID, "pane_id", d.PaneID, "event_id", id)
	return okResp(EventData{
		EventID:   id,
		EventType: store.DeliveryRecorded,
		NodeID:    nodeID,
		CommitSHA: h.commitSHA,
	})
}

// DeliveryFor returns the pane binding recorded for a node, and ok=false when
// the node has no delivery-recorded event. It reads the event payload, never
// the prose decision recorded beside it.
func (h *Handler) DeliveryFor(projectID, nodeID string) (DeliveryRecord, bool, error) {
	filter := &store.ReplayFilter{
		Types:  []store.EventType{store.DeliveryRecorded},
		NodeID: &nodeID,
	}
	events, err := h.store.Replay(projectID, filter)
	if err != nil {
		return DeliveryRecord{}, false, err
	}
	if len(events) == 0 {
		return DeliveryRecord{}, false, nil
	}

	var d DeliveryRecord
	if err := json.Unmarshal(events[len(events)-1].Payload, &d); err != nil {
		return DeliveryRecord{}, false, fmt.Errorf("delivery-recorded payload: %w", err)
	}
	return d, true, nil
}

// RecordPlan appends a plan-recorded event on a cap dispatch node: the ordered
// playbooks the dispatch is measured against, and the tags that selected them.
//
// It is recorded once, at dispatch time, so the entry gate, the brief and the
// exit gate all work from the plan that was used. Re-deriving it at close would
// read today's playbook files against flags close no longer has, and a plan that
// changes underneath a running dispatch is not a plan.
func (h *Handler) RecordPlan(projectID, nodeID string, payload []byte) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if len(payload) == 0 {
		return errResp("plan payload is required")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	id, err := h.store.Append(store.Event{
		Type:      store.PlanRecorded,
		ProjectID: projectID,
		NodeID:    &nodeID,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	})
	err = h.storeWriteOk("record-plan", projectID, err)
	if err != nil {
		h.logger.Error("store append failed", "command", "record-plan", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "record-plan", "project", projectID, "node_id", nodeID, "event_id", id)
	return okResp(EventData{
		EventID:   id,
		EventType: store.PlanRecorded,
		NodeID:    nodeID,
		CommitSHA: h.commitSHA,
	})
}

// PlanFor returns the plan recorded for a node, and ok=false when the node has
// no plan-recorded event. The bytes are the event's payload, so the caller
// decodes the shape it wrote.
func (h *Handler) PlanFor(projectID, nodeID string) ([]byte, bool, error) {
	filter := &store.ReplayFilter{
		Types:  []store.EventType{store.PlanRecorded},
		NodeID: &nodeID,
	}
	events, err := h.store.Replay(projectID, filter)
	if err != nil {
		return nil, false, err
	}
	if len(events) == 0 {
		return nil, false, nil
	}
	return events[len(events)-1].Payload, true, nil
}

// The report kinds. A kind is a status word the cap must not miss; the empty
// kind is a progress note, which nobody is interrupted for.
const (
	ReportBlocked  = "blocked"
	ReportQuestion = "question"
	ReportDone     = "done"
)

var reportKinds = map[string]bool{
	"":             true,
	ReportBlocked:  true,
	ReportQuestion: true,
	ReportDone:     true,
}

// ReportNotifies says whether a report of this kind interrupts the cap. The
// kind is the only switch, deliberately: a worker that can force a ping for a
// kind that does not notify can spend the cap's context back up, which is the
// resource the kind distinction exists to protect.
func ReportNotifies(kind string) bool { return kind != "" }

// CapClaim is the payload of a cap-claimed event: the pane that holds the cap
// scope, the tab it sits in, the agent herdr reports there, and when the claim
// was made. It is the cap's address — what routing and the guard read, and all
// they read. It carries no capability and no session state.
//
// The take-over fields are set only by a claim over an address herdr no longer
// has: TakenOverFrom and TakenOverAt name the address it replaced and when it
// was last heard, Reconstructed is what the claimant read before taking the
// scope. They ride on the claim so a take-over stays what a claim is — one
// event — rather than a claim plus an event nothing could read.
type CapClaim struct {
	PaneID string `json:"pane_id"`
	TabID  string `json:"tab_id,omitempty"`
	Agent  string `json:"agent,omitempty"`
	// ClaimedAt is when the claim was recorded, RFC3339 in UTC.
	ClaimedAt     string `json:"claimed_at"`
	TakenOverFrom string `json:"taken_over_from,omitempty"`
	TakenOverAt   string `json:"taken_over_at,omitempty"`
	Reconstructed string `json:"reconstructed,omitempty"`
}

// Report is one report a worker recorded on a node: a kind, one line, and when
// it was said. It is not a decision — what the cap answered lands on the node
// as a decision — and it carries no content of the work, which the node holds.
type Report struct {
	Kind string    `json:"kind,omitempty"`
	Line string    `json:"line"`
	At   time.Time `json:"at"`
}

// ClaimCap appends the event that makes a pane the cap scope's address. It
// writes and nothing else: whether a claim may proceed at all — a live address
// refuses it, a dead one makes it a take-over — is decided against herdr by the
// caller, which is the only layer that can ask.
//
// The event carries no node_id. A claim addresses a scope, and a node_id would
// materialise a card in the cap scope that nobody created, where every reader
// would see it.
func (h *Handler) ClaimCap(scope string, c CapClaim) Response {
	if scope == "" {
		return errResp("scope is required")
	}
	if c.PaneID == "" {
		return errResp("a claim must name a pane: HERDR_PANE_ID is how a herdr-hosted session has one")
	}
	c.ClaimedAt = time.Now().UTC().Format(time.RFC3339)

	payload, _ := json.Marshal(c)
	id, err := h.store.Append(store.Event{
		Type:      store.CapClaimed,
		ProjectID: scope,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	})
	err = h.storeWriteOk("cap-claim", scope, err)
	if err != nil {
		h.logger.Error("store append failed", "command", "cap-claim", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "cap-claim", "project", scope, "pane_id", c.PaneID, "event_id", id)
	return okResp(EventData{EventID: id, EventType: store.CapClaimed, CommitSHA: h.commitSHA})
}

// CapFor returns the scope's current address: the last claim recorded, and
// ok=false when nothing has claimed. The two are not alike — no claim is an
// empty record, a claim whose pane is gone is a dead cap — so the caller is
// told which it has rather than handed a zero claim.
func (h *Handler) CapFor(scope string) (CapClaim, bool, error) {
	filter := &store.ReplayFilter{Types: []store.EventType{store.CapClaimed}}
	events, err := h.store.Replay(scope, filter)
	if err != nil {
		return CapClaim{}, false, err
	}
	if len(events) == 0 {
		return CapClaim{}, false, nil
	}

	var c CapClaim
	if err := json.Unmarshal(events[len(events)-1].Payload, &c); err != nil {
		return CapClaim{}, false, fmt.Errorf("cap-claimed payload: %w", err)
	}
	return c, true, nil
}

// ReportAdd appends a worker's report on a node and, when the report is a
// block, the block beside it — one atomic batch, so a blocked report's two
// facts can never land apart.
//
// It writes and nothing else, always: the report is durable whether or not a
// cap exists, and the caller notifies afterwards. Nothing here can notify about
// something unwritten, because nothing here notifies at all.
func (h *Handler) ReportAdd(projectID, nodeID, kind, line string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if line == "" {
		return errResp("--line is required: a report is a status word and one line")
	}
	if !reportKinds[kind] {
		return errResp(fmt.Sprintf(
			"unknown report kind %q: use --blocked, --question or --done, or none of them for a note", kind))
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	payload, _ := json.Marshal(struct {
		Kind string `json:"kind,omitempty"`
		Line string `json:"line"`
	}{Kind: kind, Line: line})

	batch := []store.Event{{
		Type:      store.ReportRecorded,
		ProjectID: projectID,
		NodeID:    &nodeID,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	}}

	// A block is a block: the status every reader already handles, and the
	// recorded check beside it, unchanged. The report joins them rather than
	// replacing them, and its line is the reason.
	if kind == ReportBlocked {
		from := h.deriveStatus(projectID, nodeID)
		blockedPayload, _ := json.Marshal(map[string]string{"reason": line, "check": ""})
		statusPayload, _ := json.Marshal(map[string]string{"from": from, "to": "blocked"})
		batch = append(batch,
			store.Event{Type: store.TaskBlocked, ProjectID: projectID, NodeID: &nodeID, CommitSHA: h.commitSHA, Payload: blockedPayload},
			store.Event{Type: store.StatusChanged, ProjectID: projectID, NodeID: &nodeID, CommitSHA: h.commitSHA, Payload: statusPayload},
		)
	}

	ids, err := h.store.AppendBatch(batch)
	err = h.storeWriteOk("report", projectID, err)
	if err != nil {
		h.logger.Error("store append failed", "command", "report", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "report", "project", projectID, "node_id", nodeID, "kind", kind, "event_id", ids[0])
	events := make([]EventData, len(batch))
	for i, evt := range batch {
		events[i] = EventData{EventID: ids[i], EventType: evt.Type, NodeID: nodeID, CommitSHA: h.commitSHA}
	}
	return okResp(UpdateResult{Events: events})
}

// ReportsFor returns the reports recorded on a node, oldest first. It reads the
// report events alone: a decision recorded beside them is the cap's answer, not
// another report.
func (h *Handler) ReportsFor(projectID, nodeID string) ([]Report, error) {
	filter := &store.ReplayFilter{
		Types:  []store.EventType{store.ReportRecorded},
		NodeID: &nodeID,
	}
	events, err := h.store.Replay(projectID, filter)
	if err != nil {
		return nil, err
	}

	reports := make([]Report, 0, len(events))
	for _, evt := range events {
		var p struct {
			Kind string `json:"kind"`
			Line string `json:"line"`
		}
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return nil, fmt.Errorf("report-recorded payload: %w", err)
		}
		reports = append(reports, Report{Kind: p.Kind, Line: p.Line, At: evt.Timestamp})
	}
	return reports, nil
}

// DeliveryForPane returns the delivery record a scope holds for a pane, with the
// dispatch node it was recorded on: the evidence that the pane carries a
// dispatched worker. ok=false means fs holds no such record, which is not the
// same as proving the pane is a cap — it is the guard degrading to unclaimed,
// and it says so.
//
// The newest record wins, so a pane reused for a later dispatch answers for the
// dispatch it holds now.
func (h *Handler) DeliveryForPane(scope, paneID string) (DeliveryRecord, string, bool, error) {
	if paneID == "" {
		return DeliveryRecord{}, "", false, nil
	}

	filter := &store.ReplayFilter{Types: []store.EventType{store.DeliveryRecorded}}
	events, err := h.store.Replay(scope, filter)
	if err != nil {
		return DeliveryRecord{}, "", false, err
	}

	for i := len(events) - 1; i >= 0; i-- {
		var d DeliveryRecord
		if err := json.Unmarshal(events[i].Payload, &d); err != nil {
			return DeliveryRecord{}, "", false, fmt.Errorf("delivery-recorded payload: %w", err)
		}
		if d.PaneID != paneID {
			continue
		}
		capNode := ""
		if events[i].NodeID != nil {
			capNode = *events[i].NodeID
		}
		return d, capNode, true, nil
	}
	return DeliveryRecord{}, "", false, nil
}

// DispatchFor returns the dispatch node in scope whose delivery record names
// the worker in workerProject/workerNode, with that record — the reverse of
// DeliveryFor, and how a reply is resolved to the pane it is sent through.
// ok=false when no record names that worker, which includes a record written
// before records carried the worker's node.
//
// The records live in the cap's scope and the worker's node lives in its own,
// so both are named: scope is where to look, and the pair is what to match.
// The newest record wins, so a worker dispatched twice is answered on the
// dispatch it holds now.
func (h *Handler) DispatchFor(scope, workerProject, workerNode string) (DeliveryRecord, string, bool, error) {
	if workerNode == "" {
		return DeliveryRecord{}, "", false, nil
	}

	filter := &store.ReplayFilter{Types: []store.EventType{store.DeliveryRecorded}}
	events, err := h.store.Replay(scope, filter)
	if err != nil {
		return DeliveryRecord{}, "", false, err
	}

	for i := len(events) - 1; i >= 0; i-- {
		var d DeliveryRecord
		if err := json.Unmarshal(events[i].Payload, &d); err != nil {
			return DeliveryRecord{}, "", false, fmt.Errorf("delivery-recorded payload: %w", err)
		}
		if d.Project != workerProject || d.Node != workerNode {
			continue
		}
		capNode := ""
		if events[i].NodeID != nil {
			capNode = *events[i].NodeID
		}
		return d, capNode, true, nil
	}
	return DeliveryRecord{}, "", false, nil
}

// TaskEdit appends a metadata-changed event for each field it is given: the
// goal, the kind, the severity, or any combination. AC11.
func (h *Handler) TaskEdit(projectID, nodeID, newGoal string, newKind query.Kind, newSeverity query.Severity, commitSHA *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	sha := h.commitSHA
	if commitSHA != nil {
		sha = commitSHA
	}

	var events []EventData

	if newGoal != "" {
		data, err := h.appendMetadata(projectID, nodeID, "goal", h.deriveGoal(projectID, nodeID), newGoal, sha)
		if err != nil {
			h.logger.Error("store append failed", "command", "task-edit", "error", err)
			return errResp(err.Error())
		}
		events = append(events, data)
	}

	// An empty kind means "leave it alone": the CLI cannot distinguish an
	// absent --kind from an empty one, and neither should be stored.
	if newKind != "" {
		if !query.ValidKind(newKind) {
			return errResp(fmt.Sprintf("invalid kind: %q (valid: work, dispatch, gap)", newKind))
		}
		data, err := h.appendMetadata(projectID, nodeID, "kind", string(h.deriveKind(projectID, nodeID)), string(newKind), sha)
		if err != nil {
			h.logger.Error("store append failed", "command", "task-edit", "error", err)
			return errResp(err.Error())
		}
		events = append(events, data)

		// A weight is a gap's and only a gap's, on this path as on every other:
		// a kind that leaves gap takes the weight with it, so the node never
		// carries one it is not entitled to and could not be told to drop.
		// Refusing the edit instead would strand the node — an empty --severity
		// means "leave it alone", so a node nothing can re-kind is a state no
		// command undoes — while the clear is an event like any other edit: the
		// weight it dropped is on the record, and setting it again is two
		// commands.
		if newKind != query.KindGap {
			if prior := h.deriveSeverity(projectID, nodeID); prior != "" {
				data, err := h.appendMetadata(projectID, nodeID, "severity", string(prior), "", sha)
				if err != nil {
					h.logger.Error("store append failed", "command", "task-edit", "error", err)
					return errResp(err.Error())
				}
				events = append(events, data)
			}
		}
	}

	// An empty severity means "leave it alone", like the goal and the kind. A
	// node that is not a gap has no weight to set, for the same reason fs task
	// add refuses one.
	if newSeverity != "" {
		if !query.ValidSeverity(newSeverity) {
			return errResp(fmt.Sprintf("invalid severity: %q (valid: severe, defect, prose)", newSeverity))
		}
		if h.deriveKind(projectID, nodeID) != query.KindGap {
			return errResp(fmt.Sprintf("severity is a gap's weight and node %s is not a gap", nodeID))
		}
		data, err := h.appendMetadata(projectID, nodeID, "severity",
			string(h.deriveSeverity(projectID, nodeID)), string(newSeverity), sha)
		if err != nil {
			h.logger.Error("store append failed", "command", "task-edit", "error", err)
			return errResp(err.Error())
		}
		events = append(events, data)
	}

	if len(events) == 0 {
		return errResp("nothing to edit: provide --goal, --kind and/or --severity")
	}

	h.logger.Info("command executed", "command", "task-edit", "project", projectID, "node_id", nodeID, "events", len(events))
	return okResp(UpdateResult{Events: events})
}

// TaskMerge folds one node into another, and it is compensating events rather
// than a deletion: events are immutable, so a merge is written as a statement
// about both nodes. The absorbed node is marked done with a decision naming
// what took it in, and the survivor's own record gains the absorbed node — so a
// reader of the survivor alone can see that two nodes became one.
//
// The two ends of a fold may be in two scopes, and that is the fold triage needs
// most: every backlog family pairs a finding in the cap's scope with the same
// finding noticed in the worker's, and a merge that could only name one scope
// could not say the two are one defect. So each operand resolves by the address
// it carries (SplitAddress), and projectID — the scope the command resolved — is
// the default for an operand that carries none. Each half is then written into
// its own scope: a statement about the absorbed node belongs where that node is
// read, or the node is never marked done.
//
// The disagreement rule every other path applies is deliberately not applied
// here. There, one scope is in hand and an address naming another is a
// contradiction the caller stated twice; here there is no single scope for an
// operand to disagree with, and refusing would refuse the case this path exists
// for. What that costs is the one thing the rule bought: for these two operands
// an explicit --project is a default, not an assertion.
//
// Two rules keep a fold a fold and not a chain (A into B, B into C), and both
// are about the survivor staying the whole fold: neither node may be done,
// which stops a gap being folded in twice and stops a closed node being
// reopened by a merge; and a node that has itself absorbed others may not be
// absorbed. The second is the one that stops a chain, because the survivor of a
// fold stays open and nothing else would refuse it — and what it buys is that
// the survivor's absorbed list is every node that became one, so a reader of the
// final survivor sees the whole fold instead of a link in it.
func (h *Handler) TaskMerge(projectID, nodeID, into, reason string, commitSHA *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if into == "" {
		return errResp("--into is required: fs task merge NODE_ID --into SURVIVOR")
	}

	absorbedScope, absorbed, err := SplitAddress(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}
	survivorScope, survivor, err := SplitAddress(projectID, into)
	if err != nil {
		return errResp(err.Error())
	}
	// Two nodes with the same id in different scopes are two nodes, so the
	// self-absorption check is about one node, not one id.
	if absorbedScope == survivorScope && absorbed == survivor {
		return errResp(fmt.Sprintf("node %s:%s cannot absorb itself", absorbedScope, absorbed))
	}

	for _, node := range []struct{ scope, id, role string }{
		{absorbedScope, absorbed, "absorbed"},
		{survivorScope, survivor, "survivor"},
	} {
		exists, err := h.NodeExists(node.scope, node.id)
		if err != nil {
			h.logger.Error("store replay failed", "command", "task-merge", "error", err)
			return errResp(err.Error())
		}
		if !exists {
			return errResp(fmt.Sprintf("node %s:%s does not exist", node.scope, node.id))
		}
		if h.deriveStatus(node.scope, node.id) == "done" {
			return errResp(fmt.Sprintf(
				"the %s node %s:%s is done; a merge folds open nodes, and a done one was closed or already absorbed",
				node.role, node.scope, node.id))
		}
	}

	// The survivor of a fold stays open, so the done check above does not stop a
	// chain: it is exactly the node a second merge would absorb. Refuse that
	// here, so the final survivor's absorbed list is the whole fold and nothing
	// can be reached only by reading a node that is itself already folded away.
	if prior := h.deriveAbsorbed(absorbedScope, absorbed); len(prior) > 0 {
		return errResp(fmt.Sprintf(
			"node %s:%s survived an earlier fold; absorbing it would chain one fold into another and the survivor would name only part of it — merge %s:%s --into %s:%s instead",
			absorbedScope, absorbed, survivorScope, survivor, absorbedScope, absorbed))
	}

	sha := h.commitSHA
	if commitSHA != nil {
		sha = commitSHA
	}

	summary := fmt.Sprintf("merged into %s:%s", survivorScope, survivor)
	if reason != "" {
		summary += " — " + reason
	}

	var events []EventData

	// The absorbed node first, in its own scope: which node took it in, then that
	// it is over.
	payload, _ := json.Marshal(map[string]string{"summary": summary})
	id, err := h.store.Append(store.Event{
		Type:      store.DecisionRecorded,
		ProjectID: absorbedScope,
		NodeID:    &absorbed,
		CommitSHA: sha,
		Payload:   payload,
	})
	err = h.storeWriteOk("task-merge", absorbedScope, err)
	if err != nil {
		h.logger.Error("store append failed", "command", "task-merge", "error", err)
		return errResp(err.Error())
	}
	events = append(events, EventData{
		EventID: id, EventType: store.DecisionRecorded, NodeID: absorbed, CommitSHA: sha,
	})

	from := h.deriveStatus(absorbedScope, absorbed)
	payload, _ = json.Marshal(map[string]string{"from": from, "to": "done"})
	id, err = h.store.Append(store.Event{
		Type:      store.StatusChanged,
		ProjectID: absorbedScope,
		NodeID:    &absorbed,
		CommitSHA: sha,
		Payload:   payload,
	})
	err = h.storeWriteOk("task-merge", absorbedScope, err)
	if err != nil {
		h.logger.Error("store append failed", "command", "task-merge", "error", err)
		return errResp(err.Error())
	}
	events = append(events, EventData{
		EventID: id, EventType: store.StatusChanged, NodeID: absorbed, CommitSHA: sha,
	})

	// Then the survivor's half: the absorbed list as it now stands, stated in
	// full, so replay needs no accumulation.
	prior := h.deriveAbsorbed(survivorScope, survivor)
	absorbedList := make([]string, 0, len(prior)+1)
	absorbedList = append(absorbedList, prior...)
	absorbedList = append(absorbedList, absorbedRef(absorbedScope, survivorScope, absorbed))
	data, err := h.appendMetadata(survivorScope, survivor, "absorbed",
		strings.Join(prior, ","), strings.Join(absorbedList, ","), sha)
	if err != nil {
		h.logger.Error("store append failed", "command", "task-merge", "error", err)
		return errResp(err.Error())
	}
	events = append(events, data)

	h.logger.Info("command executed", "command", "task-merge",
		"project", absorbedScope, "node_id", absorbed, "into", survivorScope+":"+survivor, "events", len(events))
	return okResp(MergeResult{
		Absorbed: absorbedScope + ":" + absorbed,
		Into:     survivorScope + ":" + survivor,
		Events:   events,
	})
}

// absorbedRef is how the survivor's own record names an absorbed node: the bare
// id when it lives in the survivor's scope, and the full address when it does
// not. A bare id from another scope names nothing where it is read — the defect
// this whole path exists to remove — while a bare id from the same scope is what
// every fold before this one recorded, and it still resolves.
//
// It is the metadata field's rule only. MergeResult, which is reported and never
// stored, carries addresses on both sides: nothing reads it back, so there is
// nothing to stay compatible with, and a report of a cross-scope fold that named
// two bare ids would be the ambiguity the fold just removed.
func absorbedRef(absorbedScope, survivorScope, id string) string {
	if absorbedScope == survivorScope {
		return id
	}
	return absorbedScope + ":" + id
}

// appendMetadata appends one metadata-changed event and returns its envelope
// entry. A kind change is a new event, never a rewrite.
func (h *Handler) appendMetadata(projectID, nodeID, field, oldValue, newValue string, sha *string) (EventData, error) {
	payload, _ := json.Marshal(map[string]string{
		"field":     field,
		"old_value": oldValue,
		"new_value": newValue,
	})
	id, err := h.store.Append(store.Event{
		Type:      store.MetadataChanged,
		ProjectID: projectID,
		NodeID:    &nodeID,
		CommitSHA: sha,
		Payload:   payload,
	})
	err = h.storeWriteOk("metadata-changed", projectID, err)
	if err != nil {
		return EventData{}, err
	}
	return EventData{EventID: id, EventType: store.MetadataChanged, NodeID: nodeID, CommitSHA: sha}, nil
}

// Status derives current state for all tasks in a project. AC3.
// Delegates tree-building to the query engine. It runs a node's recorded check
// in this process's own directory; StatusIn names the project root the check
// belongs in.
func (h *Handler) Status(projectID string) Response {
	return h.StatusIn(projectID, "")
}

// StatusIn is Status with the directory a node's recorded check runs in: the
// project root the node belongs to. The check is about that project, not about
// wherever the terminal happens to be, and re-verifying it here is what makes
// fs status agree with fs pending, fs unfinished and fs gaps — a claim that is
// over must not read as current truth in one of the readers only. Nothing is
// unblocked: only the reported status changes.
func (h *Handler) StatusIn(projectID, blockDir string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}

	events, err := h.store.Replay(projectID, nil)
	if err != nil {
		h.logger.Error("store replay failed", "command", "status", "error", err)
		return errResp(err.Error())
	}

	tree := query.BuildTree(projectID, events)

	result := StatusResult{ProjectID: projectID}
	for _, root := range tree.Roots {
		collectTasks(root, &result.Tasks)
	}
	for i := range result.Tasks {
		task := &result.Tasks[i]
		task.Status = reportedStatus(task.Status, task.BlockCheck, task.Check, blockDir)
	}

	return okResp(result)
}

// collectTasks flattens the tree into the TaskInfo slice (pre-order).
func collectTasks(node *query.Node, out *[]TaskInfo) {
	*out = append(*out, taskInfoOf(node))
	for _, child := range node.Children {
		collectTasks(child, out)
	}
}

// taskInfoOf renders one derived node as the shape the reads report.
func taskInfoOf(node *query.Node) TaskInfo {
	return TaskInfo{
		NodeID:       node.NodeID,
		Goal:         node.Goal,
		Kind:         node.Kind,
		FoundBy:      node.FoundBy,
		Status:       node.Status,
		ParentNodeID: node.ParentID,
		Orphan:       node.Orphan,
		Decisions:    node.Decisions,
		Knowledge:    node.Knowledge,
		BlockCheck:   node.BlockCheck,
		Check:        node.Check,
		Integrates:   node.Integrates,
		Reviews:      node.Reviews,
		Severity:     string(node.Severity),
		Absorbed:     node.Absorbed,
	}
}

// Unfinished lists every node that is not done, across every scope in the
// store, grouped by structural kind so the backlog and the dispatch status are
// visible without reading goals. Scopes come from the events themselves, not
// the registry: a node in a scope that was never registered, or whose registry
// row was lost, still reports here.
//
// rootOf resolves a scope to the directory a recorded block check runs in — the
// project root — so a block whose condition is over reads as such instead of as
// current truth. A nil rootOf, or a scope it cannot resolve, runs the check in
// this process's own directory. Nothing is unblocked: the node's record still
// says blocked, and only the report notes that the condition no longer holds.
func (h *Handler) Unfinished(rootOf func(scope string) string) Response {
	scopes, err := h.store.Scopes()
	if err != nil {
		h.logger.Error("store scopes failed", "command", "unfinished", "error", err)
		return errResp(err.Error())
	}

	// Every kind is present even when empty, so a reader sees the groups it
	// should have looked in rather than inferring them from what happens to be
	// there.
	grouped := map[string][]UnfinishedNode{
		string(query.KindWork):     {},
		string(query.KindDispatch): {},
		string(query.KindGap):      {},
	}
	total := 0
	for _, scope := range scopes {
		nodes, err := h.UnfinishedIn(scope)
		if err != nil {
			h.logger.Error("store replay failed", "command", "unfinished", "scope", scope, "error", err)
			return errResp(err.Error())
		}
		refreshChecks(nodes, rootOf)
		for _, node := range nodes {
			grouped[string(node.Kind)] = append(grouped[string(node.Kind)], node)
			total++
		}
	}

	h.logger.Info("command executed", "command", "unfinished", "source", StateSource("unfinished"),
		"scopes", len(scopes), "unfinished", total)
	return okResp(map[string]any{"unfinished": grouped})
}

// Gaps lists every kind=gap node across every scope, open ones first and the
// heaviest of those first, so a cap reads its backlog in one place instead of
// remembering it. A gap that has been dispatched or resolved is done and sorts
// after the open ones; it is not dropped, so the record of what was noticed
// survives — which is exactly why the payload opens with a header counting what
// is being shown, rather than leaving a reader to grep the list to find out
// whether any of it is still open.
//
// rootOf resolves a scope to the directory a recorded check runs in — the
// project root — the same way every reader that renders a status resolves it,
// so a gap whose claim the check has just denied reads as expired here too.
// This is the reader the cap reads its backlog with, so it is the one that
// matters most: a gap read as open in this one list while every other reader
// says the claim is over is the defect this re-run exists to close. A nil
// rootOf, or a scope it cannot resolve, runs the check in this process's own
// directory.
func (h *Handler) Gaps(rootOf func(scope string) string) Response {
	scopes, err := h.store.Scopes()
	if err != nil {
		h.logger.Error("store scopes failed", "command", "gaps", "error", err)
		return errResp(err.Error())
	}

	gaps := []GapNode{}
	counts := GapCounts{}
	for _, scope := range scopes {
		nodes, err := h.nodesIn(scope)
		if err != nil {
			h.logger.Error("store replay failed", "command", "gaps", "scope", scope, "error", err)
			return errResp(err.Error())
		}
		dir := ""
		if rootOf != nil {
			dir = rootOf(scope)
		}
		for _, node := range nodes {
			if node.Kind != query.KindGap {
				continue
			}
			status := reportedStatus(node.Status, node.BlockCheck, node.Check, dir)
			gaps = append(gaps, GapNode{
				ProjectID: scope,
				NodeID:    node.NodeID,
				Status:    status,
				Goal:      node.Goal,
				FoundBy:   node.FoundBy,
				Severity:  node.Severity.Label(),
				level:     node.Severity,
			})
			counts.Total++
			if status == "done" {
				counts.Done++
				continue
			}
			counts.Open++
			counts.OpenBySeverity.add(node.Severity)
		}
	}

	// Open first, then heaviest first, then scope, then node id: deterministic,
	// and what a cap still has to act on is at the top in the order it would
	// act on it.
	sort.SliceStable(gaps, func(i, j int) bool {
		iOpen, jOpen := gaps[i].Status != "done", gaps[j].Status != "done"
		if iOpen != jOpen {
			return iOpen
		}
		iRank, jRank := query.SeverityRank(gaps[i].level), query.SeverityRank(gaps[j].level)
		if iRank != jRank {
			return iRank > jRank
		}
		if gaps[i].ProjectID != gaps[j].ProjectID {
			return gaps[i].ProjectID < gaps[j].ProjectID
		}
		return gaps[i].NodeID < gaps[j].NodeID
	})

	// The source is a property of the read (D2), so the read's own line carries
	// it: a second line beside this one would leave AC1's `tail -1` proof reading
	// whichever line came last.
	h.logger.Info("command executed", "command", "gaps", "source", StateSource("gaps"),
		"scopes", len(scopes), "gaps", len(gaps), "open", counts.Open, "done", counts.Done)
	// counts before gaps is the header position: encoding/json emits map keys in
	// sorted order, and the counts are what a reader should meet first.
	return okResp(map[string]any{"counts": counts, "gaps": gaps})
}

// OpenGaps counts the kind=gap nodes that are not done, across every scope.
// A gap is open until it is dispatched or resolved — done is the only way it
// stops counting — so this is the backlog nobody has picked up. fs pending
// reports it, so the backlog arrives with the turn's first read.
func (h *Handler) OpenGaps() (int, error) {
	scopes, err := h.store.Scopes()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, scope := range scopes {
		nodes, err := h.UnfinishedIn(scope)
		if err != nil {
			return 0, err
		}
		for _, node := range nodes {
			if node.Kind == query.KindGap {
				count++
			}
		}
	}
	return count, nil
}

// UnfinishedIn lists the nodes in one scope that are not done, ordered by node
// id. Status is derived by replaying the scope's events, the same way fs status
// derives it.
func (h *Handler) UnfinishedIn(scope string) ([]UnfinishedNode, error) {
	nodes, err := h.nodesIn(scope)
	if err != nil {
		return nil, err
	}

	unfinished := make([]UnfinishedNode, 0, len(nodes))
	for _, node := range nodes {
		if node.Status == "done" {
			continue
		}
		unfinished = append(unfinished, UnfinishedNode{
			ProjectID:    scope,
			NodeID:       node.NodeID,
			Status:       node.Status,
			Goal:         node.Goal,
			Kind:         node.Kind,
			FoundBy:      node.FoundBy,
			ParentNodeID: node.ParentID,
			BlockCheck:   node.BlockCheck,
			Check:        node.Check,
		})
	}
	return unfinished, nil
}

// NodeIn returns one node's derived state in a scope, the way fs status reports
// it, and ok=false when the scope has no such node. It is the read fs pending
// makes of a worker's node, and it derives nothing else — a caller that wants
// one node does not pay for the whole tree walk.
func (h *Handler) NodeIn(projectID, nodeID string) (TaskInfo, bool, error) {
	nodes, err := h.nodesIn(projectID)
	if err != nil {
		return TaskInfo{}, false, err
	}
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return taskInfoOf(node), true, nil
		}
	}
	return TaskInfo{}, false, nil
}

// nodesIn derives every node in one scope, ordered by node id. It is the walk
// fs status, fs unfinished, and fs gaps share.
func (h *Handler) nodesIn(scope string) ([]*query.Node, error) {
	events, err := h.store.Replay(scope, nil)
	if err != nil {
		return nil, err
	}

	tree := query.BuildTree(scope, events)
	nodes := make([]*query.Node, 0, len(tree.Nodes))
	for _, node := range tree.Nodes {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	return nodes, nil
}

// deriveStatus replays events to find the current status of a node.
func (h *Handler) deriveStatus(projectID, nodeID string) string {
	filter := &store.ReplayFilter{
		Types:  []store.EventType{store.StatusChanged},
		NodeID: &nodeID,
	}
	events, err := h.store.Replay(projectID, filter)
	if err != nil || len(events) == 0 {
		return "pending"
	}
	var p struct{ To string }
	json.Unmarshal(events[len(events)-1].Payload, &p)
	if p.To == "" {
		return "pending"
	}
	return p.To
}

// deriveKind replays events to find the current kind of a node, using the same
// derivation fs status reports. A node that does not exist reads as the default.
func (h *Handler) deriveKind(projectID, nodeID string) query.Kind {
	events, err := h.store.Replay(projectID, nil)
	if err != nil {
		return query.KindWork
	}
	if node, ok := query.BuildTree(projectID, events).Nodes[nodeID]; ok {
		return node.Kind
	}
	return query.KindWork
}

// deriveSeverity replays events to find the current severity of a node, unset
// when it has none — the same derivation fs gaps renders and sorts on.
func (h *Handler) deriveSeverity(projectID, nodeID string) query.Severity {
	events, err := h.store.Replay(projectID, nil)
	if err != nil {
		return query.SeverityUnset
	}
	if node, ok := query.BuildTree(projectID, events).Nodes[nodeID]; ok {
		return node.Severity
	}
	return query.SeverityUnset
}

// deriveAbsorbed replays events to find the nodes a node has taken in by a
// merge, in the order they were folded in.
func (h *Handler) deriveAbsorbed(projectID, nodeID string) []string {
	events, err := h.store.Replay(projectID, nil)
	if err != nil {
		return nil
	}
	if node, ok := query.BuildTree(projectID, events).Nodes[nodeID]; ok {
		return node.Absorbed
	}
	return nil
}

// deriveGoal replays events to find the current goal of a node.
func (h *Handler) deriveGoal(projectID, nodeID string) string {
	// First check for metadata-changed events.
	filter := &store.ReplayFilter{
		Types:  []store.EventType{store.MetadataChanged},
		NodeID: &nodeID,
	}
	events, _ := h.store.Replay(projectID, filter)
	for i := len(events) - 1; i >= 0; i-- {
		var p struct {
			Field    string `json:"field"`
			NewValue string `json:"new_value"`
		}
		json.Unmarshal(events[i].Payload, &p)
		if p.Field == "goal" {
			return p.NewValue
		}
	}
	// Fall back to task-created.
	createFilter := &store.ReplayFilter{
		Types:  []store.EventType{store.TaskCreated},
		NodeID: &nodeID,
	}
	created, _ := h.store.Replay(projectID, createFilter)
	if len(created) > 0 {
		var p struct{ Goal string }
		json.Unmarshal(created[0].Payload, &p)
		return p.Goal
	}
	return ""
}

// TaskBlock appends a task-blocked event and a status-changed event atomically.
// It records no check: the reason stands as prose. TaskBlockWithCheck is the
// same block with a condition a reader can re-run.
func (h *Handler) TaskBlock(projectID, nodeID, reason string) Response {
	return h.TaskBlockWithCheck(projectID, nodeID, reason, "")
}

// TaskBlockWithCheck appends a task-blocked event and a status-changed event
// atomically, recording the optional check: the shell command that would show
// the blocking condition is over. Recording it is what lets fs pending and fs
// unfinished re-verify the block instead of repeating stale prose.
func (h *Handler) TaskBlockWithCheck(projectID, nodeID, reason, check string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if reason == "" {
		return errResp("--reason is required")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	from := h.deriveStatus(projectID, nodeID)

	blockedPayload, _ := json.Marshal(map[string]string{"reason": reason, "check": check})
	statusPayload, _ := json.Marshal(map[string]string{"from": from, "to": "blocked"})

	batch := []store.Event{
		{
			Type:      store.TaskBlocked,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: h.commitSHA,
			Payload:   blockedPayload,
		},
		{
			Type:      store.StatusChanged,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: h.commitSHA,
			Payload:   statusPayload,
		},
	}

	ids, err := h.store.AppendBatch(batch)
	err = h.storeWriteOk("task-block", projectID, err)
	if err != nil {
		h.logger.Error("store append failed", "command", "task-block", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "task-block", "project", projectID, "node_id", nodeID)
	events := []EventData{
		{EventID: ids[0], EventType: store.TaskBlocked, NodeID: nodeID, CommitSHA: h.commitSHA},
		{EventID: ids[1], EventType: store.StatusChanged, NodeID: nodeID, CommitSHA: h.commitSHA},
	}

	return okResp(UpdateResult{Events: events})
}

// TaskUnblock appends a task-unblocked event and a status-changed event atomically.
func (h *Handler) TaskUnblock(projectID, nodeID string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	unblockedPayload, _ := json.Marshal(map[string]string{})
	statusPayload, _ := json.Marshal(map[string]string{"from": "blocked", "to": "pending"})

	batch := []store.Event{
		{
			Type:      store.TaskUnblocked,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: h.commitSHA,
			Payload:   unblockedPayload,
		},
		{
			Type:      store.StatusChanged,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: h.commitSHA,
			Payload:   statusPayload,
		},
	}

	ids, err := h.store.AppendBatch(batch)
	err = h.storeWriteOk("task-unblock", projectID, err)
	if err != nil {
		h.logger.Error("store append failed", "command", "task-unblock", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "task-unblock", "project", projectID, "node_id", nodeID)
	events := []EventData{
		{EventID: ids[0], EventType: store.TaskUnblocked, NodeID: nodeID, CommitSHA: h.commitSHA},
		{EventID: ids[1], EventType: store.StatusChanged, NodeID: nodeID, CommitSHA: h.commitSHA},
	}

	return okResp(UpdateResult{Events: events})
}

// KnowledgeAdd appends a knowledge-added event.
func (h *Handler) KnowledgeAdd(projectID, nodeID, summary string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if summary == "" {
		return errResp("--summary is required")
	}

	nodeID, err := h.resolveWriteNode(projectID, nodeID)
	if err != nil {
		return errResp(err.Error())
	}

	payload, _ := json.Marshal(map[string]string{"summary": summary})
	id, err := h.store.Append(store.Event{
		Type:      store.KnowledgeAdded,
		ProjectID: projectID,
		NodeID:    &nodeID,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	})
	err = h.storeWriteOk("knowledge-add", projectID, err)
	if err != nil {
		h.logger.Error("store append failed", "command", "knowledge-add", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "knowledge-add", "project", projectID, "node_id", nodeID, "event_id", id)
	return okResp(EventData{
		EventID:   id,
		EventType: store.KnowledgeAdded,
		NodeID:    nodeID,
		CommitSHA: h.commitSHA,
	})
}

// QueryResult is the data payload for fs query.
type QueryResult struct {
	ProjectID string        `json:"project_id"`
	Query     string        `json:"query"`
	Events    []store.Event `json:"events"`
}

// Query performs FTS5 search on event payloads. AC5.
func (h *Handler) Query(projectID, searchTerm string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if searchTerm == "" {
		return errResp("search term is required")
	}

	events, err := h.store.Search(projectID, searchTerm)
	if err != nil {
		h.logger.Error("store search failed", "command", "query", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "query", "project", projectID, "query", searchTerm, "results", len(events))
	return okResp(QueryResult{
		ProjectID: projectID,
		Query:     searchTerm,
		Events:    events,
	})
}

// LogResult is the data payload for fs log.
type LogResult struct {
	ProjectID string        `json:"project_id"`
	Events    []store.Event `json:"events"`
}

// Log replays events for a project with optional filters.
//
// --node takes the address form, and it is resolved here rather than handed to
// the reader raw, which is where it used to go: "<project>:<node>" then matched
// no node_id and the answer was zero events and exit 0 — a typo, a node from
// another scope and a node with no history all read alike.
//
// An id that resolves to nothing refuses, for the same reason. The emptiness a
// reader can act on is "this node has no events of the kind you asked for"; an
// empty answer to a node that does not exist is a claim the store cannot
// support, and it was the worse half of the defect because it is silent.
func (h *Handler) Log(projectID string, nodeID *string, eventType *store.EventType) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}

	if nodeID != nil {
		scope, id, err := SplitAddress(projectID, *nodeID)
		if err != nil {
			return errResp("log: " + err.Error())
		}
		if scope != projectID {
			return errResp(fmt.Sprintf(
				"log: --node %s names project %s but the log is of project %s", *nodeID, scope, projectID))
		}
		// The node's existence is asked without the --type filter. --type narrows
		// which of a node's events to show; it never decides whether the node is
		// one, and asking both at once would answer "no such node" for a node that
		// simply has no event of that type — the exact distinction this check is
		// here to make.
		own, err := h.store.Replay(projectID, &store.ReplayFilter{NodeID: &id})
		if err != nil {
			h.logger.Error("store replay failed", "command", "log", "error", err)
			return errResp(err.Error())
		}
		if len(own) == 0 {
			return errResp(fmt.Sprintf("log: no node %s in project %s", id, projectID))
		}
		nodeID = &id
	}

	var filter *store.ReplayFilter
	if nodeID != nil || eventType != nil {
		filter = &store.ReplayFilter{}
		if nodeID != nil {
			filter.NodeID = nodeID
		}
		if eventType != nil {
			filter.Types = []store.EventType{*eventType}
		}
	}

	events, err := h.store.Replay(projectID, filter)
	if err != nil {
		h.logger.Error("store replay failed", "command", "log", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "log", "project", projectID, "events", len(events))
	return okResp(LogResult{
		ProjectID: projectID,
		Events:    events,
	})
}

func errResp(msg string) Response {
	return Response{OK: false, Error: msg}
}

func okResp(data any) Response {
	return Response{OK: true, Data: data}
}

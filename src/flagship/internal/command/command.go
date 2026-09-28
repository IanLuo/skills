// Package command validates input, produces typed events, and manages the
// response envelope. It is the gatekeeper between CLI and Event Store.
// (SYSTEM-DESIGN R1, ARCHITECTURE R4)
package command

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/flagship-dev/flagship/internal/knowledge"
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

// TaskInfo represents derived task state for status output.
type TaskInfo struct {
	NodeID       string   `json:"node_id"`
	Goal         string   `json:"goal"`
	Status       string   `json:"status"`
	ParentNodeID string   `json:"parent_node_id,omitempty"`
	Assignee     string   `json:"assignee,omitempty"`
	Decisions    []string `json:"decisions,omitempty"`
}

// WarningType classifies status warnings.
type WarningType string

const (
	WarningUnrecordedCommits WarningType = "unrecorded_commits"
	WarningBareActiveTask    WarningType = "bare_active_task"
	WarningStaleActiveTask   WarningType = "stale_active_task"
)

// Warning is a gap detected by fs status between git activity and the event store.
type Warning struct {
	Type    WarningType `json:"type"`
	Message string      `json:"message"`
	TaskIDs []string    `json:"task_ids,omitempty"`
	Details any         `json:"details,omitempty"`
}

// StatusResult is the data payload for fs status.
type StatusResult struct {
	ProjectID string                `json:"project_id"`
	Tasks     []TaskInfo             `json:"tasks"`
	Playbooks []*knowledge.Playbook `json:"playbooks,omitempty"`
	Warnings  []Warning              `json:"warnings,omitempty"`
}

// Handler executes commands against a store.
type Handler struct {
	store       *store.Store
	commitSHA   *string
	logger      *slog.Logger
	kbDir       string
	projectRoot string
	engineName  string // bound distribution-engine name for engine_scope filtering
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

// Close releases the underlying store.
func (h *Handler) Close() error {
	return h.store.Close()
}

// SetCommitSHA sets the git HEAD sha to embed in events (ARCHITECTURE R4).
func (h *Handler) SetCommitSHA(sha *string) {
	h.commitSHA = sha
}

// SetKBDir sets the knowledge center directory so Status can include playbooks.
func (h *Handler) SetKBDir(dir string) {
	h.kbDir = dir
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

// TaskAdd appends a task-created event. AC2.
func (h *Handler) TaskAdd(projectID, goal string, parentNodeID *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if goal == "" {
		return errResp("goal is required")
	}

	nodeID := newNodeID()
	payload, _ := json.Marshal(map[string]string{"goal": goal})

	id, err := h.store.Append(store.Event{
		Type:         store.TaskCreated,
		ProjectID:    projectID,
		NodeID:       &nodeID,
		ParentNodeID: parentNodeID,
		CommitSHA:    h.commitSHA,
		Payload:      payload,
	})
	if err != nil {
		h.logger.Error("store append failed", "command", "task-add", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "task-add", "project", projectID, "node_id", nodeID, "event_id", id)
	return okResp(EventData{
		EventID:   id,
		EventType: store.TaskCreated,
		NodeID:    nodeID,
		CommitSHA: h.commitSHA,
	})
}

// TaskUpdate appends status-changed and optionally decision-recorded events. AC4.
func (h *Handler) TaskUpdate(projectID, nodeID, status, decision string, commitSHA *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
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

// TaskEdit appends a metadata-changed event. AC11.
func (h *Handler) TaskEdit(projectID, nodeID, newGoal string, commitSHA *string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}

	sha := h.commitSHA
	if commitSHA != nil {
		sha = commitSHA
	}

	// Derive current goal for old_value.
	oldGoal := h.deriveGoal(projectID, nodeID)

	if newGoal != "" {
		payload, _ := json.Marshal(map[string]string{
			"field":     "goal",
			"old_value": oldGoal,
			"new_value": newGoal,
		})
		id, err := h.store.Append(store.Event{
			Type:      store.MetadataChanged,
			ProjectID: projectID,
			NodeID:    &nodeID,
			CommitSHA: sha,
			Payload:   payload,
		})
		if err != nil {
			h.logger.Error("store append failed", "command", "task-edit", "error", err)
			return errResp(err.Error())
		}
		h.logger.Info("command executed", "command", "task-edit", "project", projectID, "node_id", nodeID, "event_id", id)
		return okResp(EventData{
			EventID:   id,
			EventType: store.MetadataChanged,
			NodeID:    nodeID,
			CommitSHA: sha,
		})
	}

	return errResp("nothing to edit: provide --goal")
}

// SetProjectRoot sets the git working directory for gap detection.
func (h *Handler) SetProjectRoot(dir string) {
	h.projectRoot = dir
}

// SetEngineName sets the distribution-engine name for engine_scope filtering.
func (h *Handler) SetEngineName(name string) {
	h.engineName = name
}

// StatusOpts holds optional filters for Status.
type StatusOpts struct {
	Assignee string // filter tasks by assignee
}

// Status derives current state for all tasks in a project. AC3.
// Delegates tree-building to the query engine.
// When a knowledge center directory is set, all playbooks are included in the
// response so callers don't need a separate kb match step.
// Includes warnings about gaps between git activity and recorded events.
func (h *Handler) Status(projectID string, opts ...StatusOpts) Response {
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

	// Apply --assignee filter if provided.
	if len(opts) > 0 && opts[0].Assignee != "" {
		filtered := result.Tasks[:0]
		for _, t := range result.Tasks {
			if t.Assignee == opts[0].Assignee {
				filtered = append(filtered, t)
			}
		}
		result.Tasks = filtered
	}

	// Detect gaps between git activity and event store.
	result.Warnings = h.detectWarnings(projectID, events, tree)

	// Auto-load playbooks so callers never need a separate `kb match` call.
	if h.kbDir != "" {
		result.Playbooks = h.loadAllPlaybooks()
	}

	return okResp(result)
}

// loadAllPlaybooks reads every playbook from the knowledge center.
// Filters procedure playbooks by engine_scope if an engine name is set
// (DESIGN-capabilities §11 Q5). Non-procedure playbooks and playbooks
// without an engine_scope are always included.
func (h *Handler) loadAllPlaybooks() []*knowledge.Playbook {
	kc, err := knowledge.Open(h.kbDir)
	if err != nil {
		h.logger.Error("knowledge center open failed", "error", err)
		return nil
	}
	names, err := kc.List()
	if err != nil {
		h.logger.Error("knowledge center list failed", "error", err)
		return nil
	}
	var pbs []*knowledge.Playbook
	for _, name := range names {
		pb, err := kc.Get(name)
		if err != nil {
			h.logger.Error("knowledge center get failed", "name", name, "error", err)
			continue
		}
		// Engine-scope filtering: if a procedure playbook has engine_scope set,
		// only include it if it matches the bound distribution-engine name.
		if h.engineName != "" && pb.Type == "procedure" && pb.EngineScope != "" {
			if pb.EngineScope != h.engineName {
				continue
			}
		}
		pbs = append(pbs, pb)
	}
	return pbs
}

// collectTasks flattens the tree into the TaskInfo slice (pre-order).
func collectTasks(node *query.Node, out *[]TaskInfo) {
	*out = append(*out, TaskInfo{
		NodeID:       node.NodeID,
		Goal:         node.Goal,
		Status:       node.Status,
		ParentNodeID: node.ParentID,
		Assignee:     node.Assignee,
		Decisions:    node.Decisions,
	})
	for _, child := range node.Children {
		collectTasks(child, out)
	}
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
func (h *Handler) TaskBlock(projectID, nodeID, reason string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if reason == "" {
		return errResp("--reason is required")
	}

	from := h.deriveStatus(projectID, nodeID)

	blockedPayload, _ := json.Marshal(map[string]string{"reason": reason})
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

	payload, _ := json.Marshal(map[string]string{"summary": summary})
	id, err := h.store.Append(store.Event{
		Type:      store.KnowledgeAdded,
		ProjectID: projectID,
		NodeID:    &nodeID,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	})
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

// TaskAssign assigns a task to an agent via a metadata-changed event.
// Optimistic concurrency: errors if already assigned to a different agent.
func (h *Handler) TaskAssign(projectID, nodeID, agentName string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}
	if agentName == "" {
		return errResp("agent name is required")
	}

	current := h.deriveAssignee(projectID, nodeID)
	if current != "" && current != agentName {
		return errResp(fmt.Sprintf("task already assigned to %q", current))
	}

	payload, _ := json.Marshal(map[string]string{
		"field":     "assignee",
		"old_value": current,
		"new_value": agentName,
	})
	id, err := h.store.Append(store.Event{
		Type:      store.MetadataChanged,
		ProjectID: projectID,
		NodeID:    &nodeID,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	})
	if err != nil {
		h.logger.Error("store append failed", "command", "task-assign", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "task-assign", "project", projectID, "node_id", nodeID, "assignee", agentName)
	return okResp(EventData{
		EventID:   id,
		EventType: store.MetadataChanged,
		NodeID:    nodeID,
		CommitSHA: h.commitSHA,
	})
}

// TaskUnassign removes the assignee from a task via a metadata-changed event.
func (h *Handler) TaskUnassign(projectID, nodeID string) Response {
	if projectID == "" {
		return errResp("project_id is required")
	}
	if nodeID == "" {
		return errResp("node_id is required")
	}

	current := h.deriveAssignee(projectID, nodeID)

	payload, _ := json.Marshal(map[string]string{
		"field":     "assignee",
		"old_value": current,
		"new_value": "",
	})
	id, err := h.store.Append(store.Event{
		Type:      store.MetadataChanged,
		ProjectID: projectID,
		NodeID:    &nodeID,
		CommitSHA: h.commitSHA,
		Payload:   payload,
	})
	if err != nil {
		h.logger.Error("store append failed", "command", "task-unassign", "error", err)
		return errResp(err.Error())
	}

	h.logger.Info("command executed", "command", "task-unassign", "project", projectID, "node_id", nodeID)
	return okResp(EventData{
		EventID:   id,
		EventType: store.MetadataChanged,
		NodeID:    nodeID,
		CommitSHA: h.commitSHA,
	})
}

// deriveAssignee replays metadata-changed events to find the current assignee.
// Mirrors deriveGoal (command.go:397 pattern).
func (h *Handler) deriveAssignee(projectID, nodeID string) string {
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
		if p.Field == "assignee" {
			return p.NewValue
		}
	}
	return ""
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
func (h *Handler) Log(projectID string, nodeID *string, eventType *store.EventType) Response {
	if projectID == "" {
		return errResp("project_id is required")
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

// detectWarnings checks for gaps between git activity and the event store.
// Three warning types:
//  1. unrecorded_commits — git commits exist after the last fs event
//  2. bare_active_task — active task with no decisions or knowledge
//  3. stale_active_task — active task with no events for >24h
func (h *Handler) detectWarnings(projectID string, events []store.Event, tree *query.Tree) []Warning {
	var warnings []Warning

	// Find the timestamp of the most recent event.
	var lastEventTime time.Time
	for _, evt := range events {
		if evt.Timestamp.After(lastEventTime) {
			lastEventTime = evt.Timestamp
		}
	}

	// 1. Unrecorded commits: git commits after the last event.
	if !lastEventTime.IsZero() && h.projectRoot != "" {
		commits := GitLogAfter(h.projectRoot, lastEventTime)
		if len(commits) > 0 {
			// Collect active task IDs for context.
			var activeIDs []string
			for _, node := range tree.Nodes {
				if node.Status == "active" {
					activeIDs = append(activeIDs, node.NodeID)
				}
			}
			msg := fmt.Sprintf("%d commit(s) since last fs event", len(commits))
			warnings = append(warnings, Warning{
				Type:    WarningUnrecordedCommits,
				Message: msg,
				TaskIDs: activeIDs,
				Details: map[string]any{
					"commits":       commits,
					"last_event_at": lastEventTime.Format(time.RFC3339),
				},
			})
		}
	}

	// Build per-node event index for checks 2 and 3.
	type nodeEvents struct {
		hasDecision  bool
		hasKnowledge bool
		lastEvent    time.Time
	}
	nodeMap := make(map[string]*nodeEvents)
	for _, evt := range events {
		if evt.NodeID == nil {
			continue
		}
		nid := *evt.NodeID
		ne, ok := nodeMap[nid]
		if !ok {
			ne = &nodeEvents{}
			nodeMap[nid] = ne
		}
		if evt.Timestamp.After(ne.lastEvent) {
			ne.lastEvent = evt.Timestamp
		}
		switch evt.Type {
		case store.DecisionRecorded:
			ne.hasDecision = true
		case store.KnowledgeAdded:
			ne.hasKnowledge = true
		}
	}

	now := time.Now().UTC()
	for _, node := range tree.Nodes {
		if node.Status != "active" {
			continue
		}
		ne := nodeMap[node.NodeID]

		// 2. Bare active task: no decisions AND no knowledge.
		if ne == nil || (!ne.hasDecision && !ne.hasKnowledge) {
			warnings = append(warnings, Warning{
				Type:    WarningBareActiveTask,
				Message: fmt.Sprintf("Active task %s has no decisions or knowledge recorded", node.NodeID),
				TaskIDs: []string{node.NodeID},
			})
		}

		// 3. Stale active task: last event >24h ago.
		if ne != nil && now.Sub(ne.lastEvent) > 24*time.Hour {
			days := int(now.Sub(ne.lastEvent).Hours() / 24)
			warnings = append(warnings, Warning{
				Type:    WarningStaleActiveTask,
				Message: fmt.Sprintf("Task %s has been active for %d day(s) with no updates", node.NodeID, days),
				TaskIDs: []string{node.NodeID},
			})
		}
	}

	return warnings
}

func errResp(msg string) Response {
	return Response{OK: false, Error: msg}
}

func okResp(data any) Response {
	return Response{OK: true, Data: data}
}

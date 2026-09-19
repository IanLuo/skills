// Package query derives current project state from event replay.
// It owns the tree-building logic and summary generation.
// (SYSTEM-DESIGN R1: Query Engine — reads derived state from events)
package query

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flagship-dev/flagship/internal/store"
)

// Node represents a single task in the project tree.
type Node struct {
	NodeID    string   `json:"node_id"`
	Goal      string   `json:"goal"`
	Status    string   `json:"status"`
	ParentID  string   `json:"parent_id,omitempty"`
	Orphan    bool     `json:"orphan,omitempty"`
	Children  []*Node  `json:"children,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
	Knowledge []string `json:"knowledge,omitempty"`
}

// Tree is the derived project state.
type Tree struct {
	ProjectID string           `json:"project_id"`
	Roots     []*Node          `json:"roots"`
	Nodes     map[string]*Node `json:"-"` // lookup by node_id
}

// Engine reads the event store and derives state.
type Engine struct {
	store *store.Store
}

// NewEngine creates a query engine backed by the given store.
func NewEngine(s *store.Store) *Engine {
	return &Engine{store: s}
}

// DeriveTree replays all events for a project and builds the task tree.
func (e *Engine) DeriveTree(projectID string) (*Tree, error) {
	events, err := e.store.Replay(projectID, nil)
	if err != nil {
		return nil, fmt.Errorf("query: replay: %w", err)
	}

	return BuildTree(projectID, events), nil
}

// BuildTree constructs a Tree from a slice of events. Exported for use
// by the command layer (to avoid re-querying the store).
func BuildTree(projectID string, events []store.Event) *Tree {
	tree := &Tree{
		ProjectID: projectID,
		Nodes:     make(map[string]*Node),
	}

	// Maintain insertion order for roots.
	var rootOrder []string

	for _, evt := range events {
		if evt.NodeID == nil {
			continue
		}
		nid := *evt.NodeID

		switch evt.Type {
		case store.TaskCreated:
			var p struct{ Goal string }
			_ = json.Unmarshal(evt.Payload, &p)
			parentID := ""
			if evt.ParentNodeID != nil {
				parentID = *evt.ParentNodeID
			}
			node := &Node{
				NodeID:   nid,
				Goal:     p.Goal,
				Status:   "pending",
				ParentID: parentID,
			}
			tree.Nodes[nid] = node
			if parentID == "" {
				rootOrder = append(rootOrder, nid)
			}

		case store.StatusChanged:
			var p struct{ To string }
			_ = json.Unmarshal(evt.Payload, &p)
			node := tree.ensureNode(nid)
			node.Status = p.To

		case store.DecisionRecorded:
			var p struct{ Summary string }
			_ = json.Unmarshal(evt.Payload, &p)
			node := tree.ensureNode(nid)
			node.Decisions = append(node.Decisions, p.Summary)

		case store.KnowledgeAdded:
			var p struct{ Summary string }
			_ = json.Unmarshal(evt.Payload, &p)
			node := tree.ensureNode(nid)
			node.Knowledge = append(node.Knowledge, p.Summary)

		case store.MetadataChanged:
			var p struct {
				Field    string `json:"field"`
				NewValue string `json:"new_value"`
			}
			_ = json.Unmarshal(evt.Payload, &p)
			if p.Field == "goal" {
				node := tree.ensureNode(nid)
				node.Goal = p.NewValue
			}

		default:
			// TaskBlocked, TaskUnblocked — status derived via StatusChanged.
			// Ensure node exists for orphan events.
			tree.ensureNode(nid)
		}
	}

	// Link children to parents. A node whose parent id is not present in the
	// project is an orphan: no root reaches it, so it would vanish from any
	// walk from the roots. Mark it and let it stand as a root of its own, so
	// fs status agrees with fs unfinished about which nodes exist.
	children := make(map[string]bool)
	for _, node := range tree.Nodes {
		if node.ParentID == "" {
			continue
		}
		if parent, ok := tree.Nodes[node.ParentID]; ok {
			parent.Children = append(parent.Children, node)
			children[node.NodeID] = true
			continue
		}
		node.Orphan = true
	}

	// Build roots list: nodes without a parent, preserving insertion order.
	// Nodes in rootOrder were explicitly created without a parent.
	seen := make(map[string]bool)
	for _, nid := range rootOrder {
		if node, ok := tree.Nodes[nid]; ok {
			tree.Roots = append(tree.Roots, node)
			seen[nid] = true
		}
	}
	// Add every node that is not a child: orphan-event nodes (no task-created)
	// and orphaned nodes (a parent id with no matching node).
	for nid, node := range tree.Nodes {
		if !seen[nid] && !children[nid] {
			tree.Roots = append(tree.Roots, node)
		}
	}

	return tree
}

// ensureNode returns the node for nid, creating it if needed (orphan events).
func (t *Tree) ensureNode(nid string) *Node {
	if n, ok := t.Nodes[nid]; ok {
		return n
	}
	n := &Node{
		NodeID: nid,
		Status: "pending",
	}
	t.Nodes[nid] = n
	return n
}

// Summary returns a concise text summary of the project tree, suitable for
// inclusion in an LLM context prompt.
func (e *Engine) Summary(projectID string) (string, error) {
	tree, err := e.DeriveTree(projectID)
	if err != nil {
		return "", err
	}
	return FormatSummary(tree), nil
}

// FormatSummary renders a Tree as a concise text summary.
func FormatSummary(tree *Tree) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s\n", tree.ProjectID)

	if len(tree.Roots) == 0 {
		b.WriteString("No tasks.\n")
		return b.String()
	}

	for _, root := range tree.Roots {
		formatNode(&b, root, 0)
	}
	return b.String()
}

func formatNode(b *strings.Builder, node *Node, depth int) {
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s- [%s] %s (%s)\n", indent, node.NodeID, node.Goal, node.Status)

	for _, d := range node.Decisions {
		fmt.Fprintf(b, "%s  decision: %s\n", indent, d)
	}
	for _, k := range node.Knowledge {
		fmt.Fprintf(b, "%s  knowledge: %s\n", indent, k)
	}
	for _, child := range node.Children {
		formatNode(b, child, depth+1)
	}
}

// Package orchestrator reads project store + knowledge center, composes context,
// checks prerequisites, selects skill, and spawns agents via distribution engine.
// (SYSTEM-DESIGN R1: Orchestrator; ARCHITECTURE R2)
package orchestrator

import (
	"fmt"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/engine"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/store"
)

// PrereqChecker returns true if a prerequisite step is satisfied.
// Default implementation always returns false (conservative).
type PrereqChecker func(step string) bool

// Result is what RunTask returns to the operator.
type Result struct {
	AgentOutput string `json:"agent_output"`
	TaskID      string `json:"task_id"`
	ProjectID   string `json:"project_id"`
}

// Orchestrator is the brain of flagship.
type Orchestrator struct {
	store         *store.Store
	kb            *knowledge.Center
	engine        engine.Engine
	prereqChecker PrereqChecker
	cmdHandler    *command.Handler
}

// New creates an orchestrator.
func New(s *store.Store, kb *knowledge.Center, eng engine.Engine) *Orchestrator {
	return &Orchestrator{
		store:  s,
		kb:     kb,
		engine: eng,
		// Default: prerequisites are not satisfied.
		prereqChecker: func(step string) bool { return false },
	}
}

// SetPrereqChecker sets a custom prerequisite checker.
func (o *Orchestrator) SetPrereqChecker(fn PrereqChecker) {
	o.prereqChecker = fn
}

// SetCommandHandler sets the command handler used to write events (e.g. task-blocked).
func (o *Orchestrator) SetCommandHandler(h *command.Handler) {
	o.cmdHandler = h
}

// RunTask orchestrates a single task: check prerequisites, compose prompt,
// spawn agent, read result, close pane, return summary.
func (o *Orchestrator) RunTask(projectID, nodeID, taskType string) (*Result, error) {
	// 1. Derive the project tree and find the target task.
	qe := query.NewEngine(o.store)
	tree, err := qe.DeriveTree(projectID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: derive tree: %w", err)
	}

	node, ok := tree.Nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("orchestrator: task %q not found in project %q", nodeID, projectID)
	}

	// 2. Check prerequisites (AC14).
	if err := o.checkPrerequisites(taskType); err != nil {
		return nil, err
	}

	// 3. Compose prompt: task goal + project context + procedures + skill.
	prompt := o.composePrompt(tree, node, taskType)

	// 4. Resolve skill.
	skill := o.resolveSkill(taskType)

	// 5. Spawn agent.
	handle, err := o.engine.Spawn(engine.SpawnConfig{
		Kind:   "pi", // Default agent kind; knowledge center can override.
		Prompt: prompt,
		Skill:  skill,
		CWD:    "", // Caller should set if needed.
		Keep:   false,
	})
	if err != nil {
		o.appendTaskBlocked(projectID, nodeID, fmt.Sprintf("spawn failed: %v", err))
		return nil, fmt.Errorf("orchestrator: spawn: %w", err)
	}

	// 6. Read result.
	output, err := o.engine.Read(handle)
	if err != nil {
		// Close pane on read failure.
		_ = o.engine.Close(handle)
		o.appendTaskBlocked(projectID, nodeID, fmt.Sprintf("read failed: %v", err))
		return nil, fmt.Errorf("orchestrator: read: %w", err)
	}

	// 7. Close pane (temp mode — SPEC R3 primary flow step 9).
	_ = o.engine.Close(handle)

	return &Result{
		AgentOutput: output,
		TaskID:      nodeID,
		ProjectID:   projectID,
	}, nil
}

// appendTaskBlocked appends a task-blocked event via the command handler.
// G5: On engine failure, record the failure reason before returning.
func (o *Orchestrator) appendTaskBlocked(projectID, nodeID, reason string) {
	if o.cmdHandler == nil {
		return
	}
	o.cmdHandler.TaskBlock(projectID, nodeID, reason)
}

// checkPrerequisites looks for a "task-prerequisites" playbook and verifies
// each step. Blocks if any step is unsatisfied (SPEC AC14).
func (o *Orchestrator) checkPrerequisites(taskType string) error {
	pb, err := o.kb.Get("task-prerequisites")
	if err != nil {
		// No prerequisite playbook — no blocking.
		return nil
	}

	// Only apply if it is an entry protocol whose applies_when this task type
	// satisfies — the same question fs dispatch asks of a plan. This path is
	// unreachable from the CLI and stays that way; it is kept compiling against
	// the schema it shares with the gates.
	if pb.Phase != knowledge.PhaseEntry || !pb.Matches(map[string]any{tagType: taskType}) {
		return nil
	}

	var missing []string
	for _, step := range pb.Steps {
		if !o.prereqChecker(step.Body) {
			missing = append(missing, step.Body)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("orchestrator: prerequisite check failed — missing: %s",
			strings.Join(missing, "; "))
	}
	return nil
}

// composePrompt builds the agent prompt from task goal, project context,
// and procedures from the knowledge center.
func (o *Orchestrator) composePrompt(tree *query.Tree, node *query.Node, taskType string) string {
	var b strings.Builder

	// Task goal.
	fmt.Fprintf(&b, "## Task\nGoal: %s\nTask ID: %s\nStatus: %s\n\n",
		node.Goal, node.NodeID, node.Status)

	// Task decisions and knowledge.
	if len(node.Decisions) > 0 {
		b.WriteString("### Prior Decisions\n")
		for _, d := range node.Decisions {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		b.WriteString("\n")
	}
	if len(node.Knowledge) > 0 {
		b.WriteString("### Knowledge\n")
		for _, k := range node.Knowledge {
			fmt.Fprintf(&b, "- %s\n", k)
		}
		b.WriteString("\n")
	}

	// Project context (summary of all tasks — gives sibling context).
	b.WriteString("## Project Context\n")
	b.WriteString(query.FormatSummary(tree))
	b.WriteString("\n")

	// Procedures from knowledge center.
	procedures := o.findProcedures(taskType)
	if len(procedures) > 0 {
		b.WriteString("## Procedures\n")
		for _, pb := range procedures {
			fmt.Fprintf(&b, "### %s\n", pb.Name)
			for _, step := range pb.Steps {
				fmt.Fprintf(&b, "- %s\n", step.Body)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

// findProcedures returns the work protocols whose applies_when the task type
// satisfies — the playbooks a worker would be measured against.
func (o *Orchestrator) findProcedures(taskType string) []*knowledge.Playbook {
	names, err := o.kb.List()
	if err != nil {
		return nil
	}

	var result []*knowledge.Playbook
	for _, name := range names {
		pb, err := o.kb.Get(name)
		if err != nil {
			continue
		}
		if pb.Phase == knowledge.PhaseWork && pb.Matches(map[string]any{tagType: taskType}) {
			result = append(result, pb)
		}
	}
	return result
}

// resolveSkill returns the skill name for the task type. The routing playbooks
// this used to read went with the `type` field: a skill is now a `use` step
// inside the work playbook that needs it, so there is nothing left to route
// through here and the task type is the answer.
func (o *Orchestrator) resolveSkill(taskType string) string {
	return taskType
}

// tagType is the derived tag a task type travels as. It is the dispatch tag set's
// own key (internal/dispatch), restated here because this package is not the
// dispatcher and must not import it.
const tagType = "type"

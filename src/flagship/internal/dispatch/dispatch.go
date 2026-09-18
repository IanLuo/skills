// Package dispatch prepares the cap's dispatch brief for a task type. It reads
// the task-type's prerequisite playbook, runs the mechanical prerequisites
// against the target project's root, records the cap's own node, and returns
// the brief the cap hands to a worker.
//
// It prepares only: it never spawns an agent. Spawning is the cap's step, and
// the brief carries the herdr command for it.
package dispatch

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/registry"
)

// capScope is the project_id of the cap's own event table. It is implicit: a
// scope is just a string, so it is never registered and never created.
const capScope = "cap"

// Item is one playbook step in the brief's checklist. Status is "pass" or
// "fail" for a step dispatch can check mechanically, and "?" for a step only
// the cap can confirm. Check names the mechanical check that produced Status.
type Item struct {
	Step   string `json:"step"`
	Status string `json:"status"`
	Check  string `json:"check,omitempty"`
}

// Brief is the data payload of fs dispatch.
type Brief struct {
	Goal        string   `json:"goal"`
	Project     string   `json:"project"`
	RootPath    string   `json:"root_path"`
	TaskType    string   `json:"task_type"`
	Playbook    string   `json:"playbook"`
	Checklist   []Item   `json:"checklist"`
	LockedDocs  []string `json:"locked_docs"`
	Notes       []string `json:"notes"`
	CapNodeID   string   `json:"cap_node_id"`
	NextCommand string   `json:"next_command"`
}

// Prepare composes the brief for a task type and records the cap's node. It
// returns the CLI response envelope, so failures are reported the same way as
// every other command's.
func Prepare(h *command.Handler, reg *registry.Registry, kc *knowledge.Center, project, taskType, goal string) command.Response {
	if project == "" {
		return errResp("--project is required")
	}
	if taskType == "" {
		return errResp("--type is required")
	}
	if goal == "" {
		return errResp("--goal is required")
	}

	playbookName := taskType + "-prerequisites"
	pb, err := kc.Get(playbookName)
	if err != nil {
		return errResp(fmt.Sprintf(
			"dispatch: prerequisite playbook %s.yaml is missing or unreadable (%v); tell the user and stop — never improvise a procedure for a worker",
			playbookName, err))
	}

	proj, err := reg.Get(project)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}

	brief, err := buildBrief(pb, proj, taskType, goal)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}

	nodeID, err := recordCapNode(h, taskType, goal, project)
	if err != nil {
		return errResp(fmt.Sprintf("dispatch: %v", err))
	}
	brief.CapNodeID = nodeID
	brief.NextCommand = nextCommand(brief)

	return command.Response{OK: true, Data: brief}
}

// buildBrief runs the mechanical prerequisites against the project root and
// pairs every playbook step with the status it can establish.
func buildBrief(pb *knowledge.Playbook, proj registry.Project, taskType, goal string) (*Brief, error) {
	info, err := os.Stat(proj.RootPath)
	if err != nil {
		return nil, fmt.Errorf("project %q root %s is unreadable: %w", proj.Name, proj.RootPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project %q root %s is not a directory", proj.Name, proj.RootPath)
	}

	agentsMD := fileExists(filepath.Join(proj.RootPath, "AGENTS.md"))
	lockedDocs := findLockedDocs(proj.RootPath)

	checklist := make([]Item, 0, len(pb.Steps))
	for _, step := range pb.Steps {
		checklist = append(checklist, classify(step, agentsMD, lockedDocs))
	}

	return &Brief{
		Goal:       goal,
		Project:    proj.Name,
		RootPath:   proj.RootPath,
		TaskType:   taskType,
		Playbook:   pb.Name,
		Checklist:  checklist,
		LockedDocs: lockedDocs,
		Notes: []string{
			"the locked-doc grep matches the marker text anywhere, so it has a known false positive when a repo documents the marker syntax itself",
		},
	}, nil
}

// classify pairs a playbook step with a mechanical check when the step names
// one, and marks it "?" otherwise — only the cap can confirm the rest.
func classify(step string, agentsMD bool, lockedDocs []string) Item {
	lower := strings.ToLower(step)
	switch {
	case strings.Contains(lower, "agents.md"):
		return Item{
			Step:   step,
			Status: passFail(agentsMD),
			Check:  "AGENTS.md exists at the project root",
		}
	case strings.Contains(lower, "grep"):
		return Item{
			Step:   step,
			Status: passFail(len(lockedDocs) > 0),
			Check:  "grep -rl for the specs:locked and design:locked markers in *.md",
		}
	default:
		return Item{Step: step, Status: "?"}
	}
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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

// recordCapNode writes the cap's two events for this dispatch: the node itself
// and the decision naming the project the work went to. It returns the node id
// so the cap can read its own dispatch back.
func recordCapNode(h *command.Handler, taskType, goal, project string) (string, error) {
	added := h.TaskAdd(capScope, fmt.Sprintf("dispatch %s: %s", taskType, goal), nil)
	if !added.OK {
		return "", fmt.Errorf("record cap node: %s", added.Error)
	}
	data, ok := added.Data.(command.EventData)
	if !ok || data.NodeID == "" {
		return "", fmt.Errorf("record cap node: task-created response carried no node id")
	}

	decided := h.TaskUpdate(
		capScope,
		data.NodeID,
		"",
		fmt.Sprintf("dispatch %s: %s to project %s", taskType, goal, project),
		nil,
	)
	if !decided.OK {
		return "", fmt.Errorf("record cap decision: %s", decided.Error)
	}
	return data.NodeID, nil
}

// nextCommand is the herdr line the cap runs to deliver the brief to a worker.
// dispatch does not run it.
func nextCommand(b *Brief) string {
	name := "dispatch-" + b.TaskType
	return fmt.Sprintf(
		`P=$(herdr pane split --current --direction right --cwd %s --no-focus | jq -r '.result.pane.pane_id') && `+
			`herdr agent start %s --kind pi --pane "$P" && `+
			`herdr agent prompt %s %s --wait --timeout 120000`,
		shellQuote(b.RootPath), name, name, shellQuote(renderBrief(b)))
}

// renderBrief is the text the cap hands the worker: goal, project root,
// checklist, and the locked docs.
func renderBrief(b *Brief) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Goal: %s\n", b.Goal)
	fmt.Fprintf(&sb, "Project: %s (root %s)\n", b.Project, b.RootPath)
	sb.WriteString("Checklist:\n")
	for _, item := range b.Checklist {
		fmt.Fprintf(&sb, "- [%s] %s", item.Status, item.Step)
		if item.Check != "" {
			fmt.Fprintf(&sb, " (%s)", item.Check)
		}
		sb.WriteString("\n")
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

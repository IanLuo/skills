// Package main is the CLI entry point for the `fs` binary.
// Single entry point. Subcommands: project, task, status (ARCHITECTURE R4).
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/flagship-dev/flagship/internal/capabilities"
	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/knowledge"
	"github.com/flagship-dev/flagship/internal/registry"
	"github.com/flagship-dev/flagship/internal/store"
)

// logger is the structured logger for the CLI. JSON to stderr (ARCHITECTURE R4).
var logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "project":
		projectCmd(os.Args[2:])
	case "task":
		taskCmd(os.Args[2:])
	case "status":
		statusCmd(os.Args[2:])
	case "query":
		queryCmd(os.Args[2:])
	case "log":
		logCmd(os.Args[2:])
	case "kb":
		kbCmd(os.Args[2:])
	case "resolve":
		resolveCmd(os.Args[2:])
	case "agent":
		agentCmd(os.Args[2:])
	case "migrate":
		migrateCmd(os.Args[2:])
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "fs: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: fs <command> [args]

Commands:
  project create [--name NAME] [--root PATH]   Create a project
  project list                                 List all projects
  project get NAME                             Get project info
  task add --goal GOAL [--parent NODE_ID]       Add a task
  task update NODE_ID --status STATUS [--decision TEXT]  Update task
  task edit NODE_ID --goal GOAL                 Edit task metadata
  task block NODE_ID --reason REASON            Block a task
  task unblock NODE_ID                          Unblock a task
  task knowledge NODE_ID --summary TEXT         Add knowledge to a task
  status [--project PROJECT_ID] [--assignee A]  Show project status
  query SEARCH_TERM [--project PROJECT_ID]      FTS5 search events
  log [--project PROJECT_ID] [--node NODE_ID] [--type TYPE]  Replay events
  kb add --name NAME --file PATH                Add a playbook
  kb get NAME                                   Get a playbook
  kb list                                       List playbooks
  kb match TRIGGER                              Find playbooks by trigger
  kb edit --name NAME --file PATH               Edit a playbook
  kb remove NAME                                Remove a playbook
  resolve <cap>.<op> [--key val ...]             Resolve capability to concrete command
  resolve <cap>                                  Get capability metadata (detail-playbook, ops)
  resolve --check                                Validate capabilities config
  resolve --init                                 Write default capabilities config
  agent register --name N --role R --pane P      Register an agent
  agent get NAME | --self                        Get agent info
  agent list [--role R] [--project P] [--status S] List agents
  agent heartbeat NAME                           Touch last_seen
  agent update NAME [--status S] [--project P]   Update agent
  agent deregister NAME                          Remove agent registration
  migrate PATH [PATH...]                        Import events from old per-project store.db files`)
}

// handler opens the global event store at ~/.fs/flagship.db.
// All projects share one store — cross-project search works out of the box.
// Opportunistic last_seen touch for the calling agent (DESIGN-roles §2.5).
func handler() *command.Handler {
	dbPath := globalDBPath()
	h, err := command.NewHandler(dbPath)
	if err != nil {
		fatal(err.Error())
	}
	h.SetLogger(logger)
	h.SetCommitSHA(command.DetectCommitSHA())

	// Opportunistic last_seen touch (best-effort, per DESIGN-roles §2.5).
	// Reads raw $HERDR_PANE_ID directly — NOT via capabilities.yaml (§10.2).
	paneID := os.Getenv("HERDR_PANE_ID")
	if paneID == "" {
		paneID = os.Getenv("TMUX_PANE") // fallback chain
	}
	if paneID != "" {
		reg := openRegistry()
		reg.TouchAgent(paneID)
		reg.Close()
	}

	return h
}

// globalDBPath returns ~/.fs/flagship.db, creating ~/.fs/ if needed.
func globalDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fatal("cannot determine home directory: " + err.Error())
	}
	dir := filepath.Join(home, ".fs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal("cannot create ~/.fs directory: " + err.Error())
	}
	return filepath.Join(dir, "flagship.db")
}

// detectProjectID returns the project name from --project flag or derives it
// from the git root directory name.
func detectProjectID(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	root := command.DetectProjectRoot()
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			fatal("cannot determine project root: " + err.Error())
		}
	}
	return filepath.Base(root)
}

func projectCmd(args []string) {
	if len(args) < 1 || isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, `Usage: fs project <subcommand>

Subcommands:
  create [--name NAME] [--root PATH]   Create a project
  list                                 List all projects
  get NAME                             Get project info`)
		if len(args) < 1 {
			os.Exit(2)
		}
		return
	}
	switch args[0] {
	case "create":
		projectCreate(args[1:])
	case "list":
		projectList()
	case "get":
		projectGet(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "fs project: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func projectCreate(args []string) {
	name := flagVal(args, "--name", "")
	root := flagVal(args, "--root", "")

	if root == "" {
		r := command.DetectProjectRoot()
		if r == "" {
			var err error
			r, err = os.Getwd()
			if err != nil {
				fatal("cannot determine project root: " + err.Error())
			}
		}
		root = r
	}
	if name == "" {
		name = filepath.Base(root)
	}

	h := handler()
	defer h.Close()

	resp := h.ProjectCreate(name, root)
	if resp.OK {
		reg := openRegistry()
		defer reg.Close()
		if err := reg.Register(name, root); err != nil {
			logger.Error("registry register failed", "project", name, "error", err)
		}
	}
	output(resp)
}

func projectList() {
	reg := openRegistry()
	defer reg.Close()

	projects, err := reg.List()
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]any{"projects": projects}})
}

func projectGet(args []string) {
	if len(args) < 1 {
		fatal("fs project get: NAME required")
	}
	name := args[0]

	reg := openRegistry()
	defer reg.Close()

	p, err := reg.Get(name)
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: p})
}

func taskCmd(args []string) {
	if len(args) < 1 || isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, `Usage: fs task <subcommand>

Subcommands:
  add --goal GOAL [--parent NODE_ID]       Add a task
  update NODE_ID --status STATUS [--decision TEXT]  Update task
  edit NODE_ID --goal GOAL                 Edit task metadata
  block NODE_ID --reason REASON            Block a task
  unblock NODE_ID                          Unblock a task
  knowledge NODE_ID --summary TEXT         Add knowledge to a task
  assign NODE_ID --to AGENT [--project P]  Assign task to agent
  unassign NODE_ID [--project P]           Unassign task`)
		if len(args) < 1 {
			os.Exit(2)
		}
		return
	}
	switch args[0] {
	case "add":
		taskAdd(args[1:])
	case "update":
		taskUpdate(args[1:])
	case "edit":
		taskEdit(args[1:])
	case "block":
		taskBlock(args[1:])
	case "unblock":
		taskUnblock(args[1:])
	case "knowledge":
		taskKnowledge(args[1:])
	case "assign":
		taskAssign(args[1:])
	case "unassign":
		taskUnassign(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "fs task: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func taskAdd(args []string) {
	goal := flagVal(args, "--goal", "")
	parent := flagVal(args, "--parent", "")
	projectID := detectProjectID(flagVal(args, "--project", ""))

	h := handler()
	defer h.Close()

	if goal == "" {
		fatal("--goal is required")
	}

	var parentPtr *string
	if parent != "" {
		parentPtr = &parent
	}

	resp := h.TaskAdd(projectID, goal, parentPtr)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func taskUpdate(args []string) {
	if len(args) < 1 {
		fatal("fs task update: NODE_ID required")
	}
	nodeID := args[0]
	rest := args[1:]
	status := flagVal(rest, "--status", "")
	decision := flagVal(rest, "--decision", "")
	projectID := detectProjectID(flagVal(rest, "--project", ""))

	h := handler()
	defer h.Close()

	resp := h.TaskUpdate(projectID, nodeID, status, decision, nil)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func taskEdit(args []string) {
	if len(args) < 1 {
		fatal("fs task edit: NODE_ID required")
	}
	nodeID := args[0]
	rest := args[1:]
	goal := flagVal(rest, "--goal", "")
	projectID := detectProjectID(flagVal(rest, "--project", ""))

	h := handler()
	defer h.Close()

	resp := h.TaskEdit(projectID, nodeID, goal, nil)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func taskBlock(args []string) {
	if len(args) < 1 {
		fatal("fs task block: NODE_ID required")
	}
	nodeID := args[0]
	rest := args[1:]
	reason := flagVal(rest, "--reason", "")
	projectID := detectProjectID(flagVal(rest, "--project", ""))

	h := handler()
	defer h.Close()

	resp := h.TaskBlock(projectID, nodeID, reason)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func taskUnblock(args []string) {
	if len(args) < 1 {
		fatal("fs task unblock: NODE_ID required")
	}
	nodeID := args[0]
	rest := args[1:]
	projectID := detectProjectID(flagVal(rest, "--project", ""))

	h := handler()
	defer h.Close()

	resp := h.TaskUnblock(projectID, nodeID)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func taskKnowledge(args []string) {
	if len(args) < 1 {
		fatal("fs task knowledge: NODE_ID required")
	}
	nodeID := args[0]
	rest := args[1:]
	summary := flagVal(rest, "--summary", "")
	projectID := detectProjectID(flagVal(rest, "--project", ""))

	h := handler()
	defer h.Close()

	resp := h.KnowledgeAdd(projectID, nodeID, summary)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func taskAssign(args []string) {
	if len(args) < 1 {
		fatal("fs task assign: NODE_ID required")
	}
	nodeID := args[0]
	rest := args[1:]
	agentName := flagVal(rest, "--to", "")
	projectID := detectProjectID(flagVal(rest, "--project", ""))

	if agentName == "" {
		fatal("--to is required")
	}

	// Validate agent exists (FINDING-14: validate in main.go, keep command.go store-only).
	reg := openRegistry()
	if _, err := reg.GetAgent(agentName); err != nil {
		reg.Close()
		output(command.Response{OK: false, Error: fmt.Sprintf("agent %q not found in registry", agentName)})
		return
	}
	reg.Close()

	h := handler()
	defer h.Close()

	resp := h.TaskAssign(projectID, nodeID, agentName)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func taskUnassign(args []string) {
	if len(args) < 1 {
		fatal("fs task unassign: NODE_ID required")
	}
	nodeID := args[0]
	rest := args[1:]
	projectID := detectProjectID(flagVal(rest, "--project", ""))

	h := handler()
	defer h.Close()

	resp := h.TaskUnassign(projectID, nodeID)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func statusCmd(args []string) {
	projectID := detectProjectID(flagVal(args, "--project", ""))
	assignee := flagVal(args, "--assignee", "")

	h := handler()
	defer h.Close()
	h.SetKBDir(kbDir())

	// Provide git root so Status can detect gaps between commits and events.
	if root := command.DetectProjectRoot(); root != "" {
		h.SetProjectRoot(root)
	}

	// Set engine name for engine_scope filtering (DESIGN-capabilities §11 Q5).
	if cfg, err := capabilities.Load(capabilities.DefaultConfigPath()); err == nil {
		h.SetEngineName(cfg.EngineName())
	}

	updateActivity(projectID)
	var resp command.Response
	if assignee != "" {
		resp = h.Status(projectID, command.StatusOpts{Assignee: assignee})
	} else {
		resp = h.Status(projectID)
	}
	output(resp)
}

func kbCmd(args []string) {
	if len(args) < 1 || isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, `Usage: fs kb <subcommand>

Subcommands:
  add --name NAME --file PATH   Add a playbook
  get NAME                      Get a playbook
  list                          List playbooks
  match TRIGGER                 Find playbooks by trigger
  edit --name NAME --file PATH  Edit a playbook
  remove NAME                   Remove a playbook`)
		if len(args) < 1 {
			os.Exit(2)
		}
		return
	}
	switch args[0] {
	case "add":
		kbAdd(args[1:])
	case "get":
		kbGet(args[1:])
	case "list":
		kbList(args[1:])
	case "match":
		kbMatch(args[1:])
	case "edit":
		kbEdit(args[1:])
	case "remove":
		kbRemove(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "fs kb: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func kbAdd(args []string) {
	name := flagVal(args, "--name", "")
	filePath := flagVal(args, "--file", "")
	if name == "" {
		fatal("--name is required")
	}
	if filePath == "" {
		fatal("--file is required")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		fatal("cannot read file: " + err.Error())
	}

	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	if err := kc.Add(name, data); err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]string{"name": name, "action": "added"}})
}

func kbGet(args []string) {
	if len(args) < 1 {
		fatal("fs kb get: NAME required")
	}
	name := args[0]

	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	pb, err := kc.Get(name)
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: pb})
}

func kbList(_ []string) {
	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	names, err := kc.List()
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]any{"playbooks": names}})
}

func kbMatch(args []string) {
	if len(args) < 1 {
		fatal("fs kb match: TRIGGER required")
	}
	trigger := args[0]

	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	matched, err := kc.Match(trigger)
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]any{"trigger": trigger, "playbooks": matched}})
}

func kbEdit(args []string) {
	name := flagVal(args, "--name", "")
	filePath := flagVal(args, "--file", "")
	if name == "" {
		fatal("--name is required")
	}
	if filePath == "" {
		fatal("--file is required")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		fatal("cannot read file: " + err.Error())
	}

	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	if err := kc.Edit(name, data); err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]string{"name": name, "action": "edited"}})
}

func kbRemove(args []string) {
	if len(args) < 1 {
		fatal("fs kb remove: NAME required")
	}
	name := args[0]

	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	if err := kc.Remove(name); err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]string{"name": name, "action": "removed"}})
}

// kbDir returns the knowledge center directory.
// SYSTEM-DESIGN R4: Knowledge center at ~/.fs/kb/.
func kbDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fatal("cannot determine home directory: " + err.Error())
	}
	return filepath.Join(home, ".fs", "kb")
}

// registryPath returns the path to the global database (same as event store).
func registryPath() string {
	return globalDBPath()
}

// openRegistry opens the global project registry.
func openRegistry() *registry.Registry {
	reg, err := registry.Open(registryPath())
	if err != nil {
		fatal(err.Error())
	}
	return reg
}

// updateActivity touches last_activity for a project in the global registry.
// Best-effort: logs error but does not fail the command.
func updateActivity(projectID string) {
	reg := openRegistry()
	defer reg.Close()
	if err := reg.UpdateActivity(projectID); err != nil {
		logger.Error("registry update activity failed", "project", projectID, "error", err)
	}
}

func queryCmd(args []string) {
	projectID := detectProjectID(flagVal(args, "--project", ""))

	// First positional arg (not a flag or flag value) is the search term.
	searchTerm := ""
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			i++ // skip flag value
			continue
		}
		searchTerm = args[i]
		break
	}

	if searchTerm == "" {
		fatal("search term is required: fs query SEARCH_TERM [--project ID]")
	}

	h := handler()
	defer h.Close()

	updateActivity(projectID)
	resp := h.Query(projectID, searchTerm)
	output(resp)
}

func logCmd(args []string) {
	projectID := detectProjectID(flagVal(args, "--project", ""))
	nodeIDStr := flagVal(args, "--node", "")
	eventTypeStr := flagVal(args, "--type", "")

	h := handler()
	defer h.Close()

	var nodeID *string
	if nodeIDStr != "" {
		nodeID = &nodeIDStr
	}

	var eventType *store.EventType
	if eventTypeStr != "" {
		et := store.EventType(eventTypeStr)
		eventType = &et
	}

	updateActivity(projectID)
	resp := h.Log(projectID, nodeID, eventType)
	output(resp)
}

// output prints a Response as JSON to stdout and exits with appropriate code.
func output(resp command.Response) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
	if !resp.OK {
		os.Exit(1)
	}
}

// flagVal extracts a flag value from args. Returns def if not found.
func flagVal(args []string, flag, def string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return def
}

// isHelp returns true if the arg is a help flag.
func isHelp(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "help"
}

func migrateCmd(args []string) {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, `Usage: fs migrate PATH [PATH...]

Import events from old per-project .fs/store.db files into the
unified ~/.fs/flagship.db. Duplicates (same event ID) are skipped.

Example:
  fs migrate /path/to/project1/.fs/store.db /path/to/project2/.fs/store.db`)
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}

	// Collect all positional args as source paths.
	var paths []string
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			paths = append(paths, a)
		}
	}

	h := handler()
	defer h.Close()

	resp := h.Migrate(paths)
	output(resp)
}

func agentCmd(args []string) {
	if len(args) < 1 || isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, `Usage: fs agent <subcommand>

Subcommands:
  register --name N --role R --pane P [--workspace W] [--project P] [--playbook PB]
  get NAME | --self
  list [--role R] [--project P] [--status S]
  heartbeat NAME
  update NAME [--status S] [--project P]
  deregister NAME`)
		if len(args) < 1 {
			os.Exit(2)
		}
		return
	}
	switch args[0] {
	case "register":
		agentRegister(args[1:])
	case "get":
		agentGet(args[1:])
	case "list":
		agentList(args[1:])
	case "heartbeat":
		agentHeartbeat(args[1:])
	case "update":
		agentUpdate(args[1:])
	case "deregister":
		agentDeregister(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "fs agent: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func agentRegister(args []string) {
	name := flagVal(args, "--name", "")
	role := flagVal(args, "--role", "")
	paneID := flagVal(args, "--pane", "")
	workspace := flagVal(args, "--workspace", "")
	project := flagVal(args, "--project", "")
	playbook := flagVal(args, "--playbook", "")

	if name == "" {
		fatal("--name is required")
	}
	if role == "" {
		fatal("--role is required")
	}
	if paneID == "" {
		fatal("--pane is required")
	}

	reg := openRegistry()
	defer reg.Close()

	a := registry.Agent{
		Name:      name,
		Role:      role,
		PaneID:    paneID,
		Workspace: workspace,
		Project:   project,
		Playbook:  playbook,
	}
	if err := reg.RegisterAgent(a); err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]string{"name": name, "action": "registered"}})
}

func agentGet(args []string) {
	if len(args) < 1 {
		fatal("fs agent get: NAME or --self required")
	}

	reg := openRegistry()
	defer reg.Close()

	// --self: identity discovery from $HERDR_PANE_ID (raw, per §10.2).
	if args[0] == "--self" {
		paneID := os.Getenv("HERDR_PANE_ID")
		if paneID == "" {
			paneID = os.Getenv("TMUX_PANE")
		}
		if paneID == "" {
			output(command.Response{OK: false, Error: "no pane ID found in environment (HERDR_PANE_ID or TMUX_PANE)"})
			return
		}
		a, err := reg.GetAgentByPane(paneID)
		if err != nil {
			output(command.Response{OK: false, Error: err.Error()})
			return
		}
		output(command.Response{OK: true, Data: a})
		return
	}

	name := args[0]
	a, err := reg.GetAgent(name)
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: a})
}

func agentList(args []string) {
	reg := openRegistry()
	defer reg.Close()

	f := registry.AgentFilter{
		Role:    flagVal(args, "--role", ""),
		Project: flagVal(args, "--project", ""),
		Status:  flagVal(args, "--status", ""),
	}
	agents, err := reg.ListAgents(f)
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]any{"agents": agents}})
}

func agentHeartbeat(args []string) {
	if len(args) < 1 {
		fatal("fs agent heartbeat: NAME required")
	}
	name := args[0]

	reg := openRegistry()
	defer reg.Close()

	if err := reg.HeartbeatAgent(name); err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]string{"name": name, "action": "heartbeat"}})
}

func agentUpdate(args []string) {
	if len(args) < 1 {
		fatal("fs agent update: NAME required")
	}
	name := args[0]
	rest := args[1:]
	status := flagVal(rest, "--status", "")
	project := flagVal(rest, "--project", "")

	reg := openRegistry()
	defer reg.Close()

	if err := reg.UpdateAgent(name, status, project); err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]string{"name": name, "action": "updated"}})
}

func agentDeregister(args []string) {
	if len(args) < 1 {
		fatal("fs agent deregister: NAME required")
	}
	name := args[0]

	reg := openRegistry()
	defer reg.Close()

	if err := reg.DeregisterAgent(name); err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]string{"name": name, "action": "deregistered"}})
}

func resolveCmd(args []string) {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, `Usage: fs resolve <capability>.<operation> [--key value ...]
       fs resolve <capability>             Get capability metadata (detail-playbook, ops)
       fs resolve --check                  Validate capabilities config
       fs resolve --init                   Write default capabilities config`)
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}

	configPath := capabilities.DefaultConfigPath()

	// --init: write default config.
	if args[0] == "--init" {
		if _, err := os.Stat(configPath); err == nil {
			output(command.Response{OK: false, Error: "capabilities config already exists at " + configPath})
			return
		}
		if err := capabilities.WriteDefaultConfig(configPath); err != nil {
			output(command.Response{OK: false, Error: err.Error()})
			return
		}
		output(command.Response{OK: true, Data: map[string]string{"path": configPath, "action": "created"}})
		return
	}

	cfg, err := capabilities.Load(configPath)
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}

	// --check: validate config.
	if args[0] == "--check" {
		names, err := cfg.Check()
		if err != nil {
			output(command.Response{OK: false, Error: err.Error()})
			return
		}
		output(command.Response{OK: true, Data: map[string]any{"capabilities": names}})
		return
	}

	// Parse <capability>[.<operation>]
	capOp := args[0]
	dotIdx := strings.Index(capOp, ".")
	if dotIdx < 0 {
		// No dot — return capability metadata (name, detail-playbook, ops).
		meta, err := cfg.GetCapability(capOp)
		if err != nil {
			output(command.Response{OK: false, Error: err.Error()})
			return
		}
		output(command.Response{OK: true, Data: meta})
		return
	}
	capName := capOp[:dotIdx]
	opName := capOp[dotIdx+1:]

	// Collect --key value pairs.
	kv := make(map[string]string)
	for i := 1; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") && i+1 < len(args) {
			key := strings.TrimPrefix(args[i], "--")
			kv[key] = args[i+1]
			i++
		}
	}

	result, err := cfg.Resolve(capName, opName, kv)
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]string{"command": result}})
}

func fatal(msg string) {
	resp := command.Response{OK: false, Error: msg}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
	os.Exit(1)
}

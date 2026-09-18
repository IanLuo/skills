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
  status [--project PROJECT_ID]                 Show project status
  query SEARCH_TERM [--project PROJECT_ID]      FTS5 search events
  log [--project PROJECT_ID] [--node NODE_ID] [--type TYPE]  Replay events
  kb add --name NAME --file PATH                Add a playbook
  kb get NAME                                   Get a playbook
  kb list                                       List playbooks
  kb edit --name NAME --file PATH               Edit a playbook
  kb remove NAME                                Remove a playbook`)
}

// handler opens the store and returns a command handler.
// Store location: <project_root>/.fs/store.db (SYSTEM-DESIGN R4).
func handler() (*command.Handler, string) {
	projectRoot := command.DetectProjectRoot()
	if projectRoot == "" {
		// Fallback to cwd
		var err error
		projectRoot, err = os.Getwd()
		if err != nil {
			fatal("cannot determine project root: " + err.Error())
		}
	}

	dbDir := filepath.Join(projectRoot, ".fs")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		fatal("cannot create .fs directory: " + err.Error())
	}

	dbPath := filepath.Join(dbDir, "store.db")
	h, err := command.NewHandler(dbPath)
	if err != nil {
		fatal(err.Error())
	}

	// Inject structured logger (ARCHITECTURE R4: JSON to stderr).
	h.SetLogger(logger)

	// Auto-capture git HEAD (ARCHITECTURE R4).
	h.SetCommitSHA(command.DetectCommitSHA())

	return h, projectRoot
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

	h, projectRoot := handler()
	defer h.Close()

	if root == "" {
		root = projectRoot
	}
	if name == "" {
		// Default to directory name.
		name = filepath.Base(root)
	}

	resp := h.ProjectCreate(name, root)
	if resp.OK {
		// Register in the global registry.
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
  knowledge NODE_ID --summary TEXT         Add knowledge to a task`)
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
	default:
		fmt.Fprintf(os.Stderr, "fs task: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func taskAdd(args []string) {
	goal := flagVal(args, "--goal", "")
	parent := flagVal(args, "--parent", "")
	projectID := flagVal(args, "--project", "")

	h, projectRoot := handler()
	defer h.Close()

	if projectID == "" {
		projectID = filepath.Base(projectRoot)
	}
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
	projectID := flagVal(rest, "--project", "")

	h, projectRoot := handler()
	defer h.Close()

	if projectID == "" {
		projectID = filepath.Base(projectRoot)
	}

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
	projectID := flagVal(rest, "--project", "")

	h, projectRoot := handler()
	defer h.Close()

	if projectID == "" {
		projectID = filepath.Base(projectRoot)
	}

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
	projectID := flagVal(rest, "--project", "")

	h, projectRoot := handler()
	defer h.Close()

	if projectID == "" {
		projectID = filepath.Base(projectRoot)
	}

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
	projectID := flagVal(rest, "--project", "")

	h, projectRoot := handler()
	defer h.Close()

	if projectID == "" {
		projectID = filepath.Base(projectRoot)
	}

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
	projectID := flagVal(rest, "--project", "")

	h, projectRoot := handler()
	defer h.Close()

	if projectID == "" {
		projectID = filepath.Base(projectRoot)
	}

	resp := h.KnowledgeAdd(projectID, nodeID, summary)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func statusCmd(args []string) {
	projectID := flagVal(args, "--project", "")

	h, projectRoot := handler()
	defer h.Close()

	if projectID == "" {
		projectID = filepath.Base(projectRoot)
	}

	updateActivity(projectID)
	resp := h.Status(projectID)
	output(resp)
}

func kbCmd(args []string) {
	if len(args) < 1 || isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, `Usage: fs kb <subcommand>

Subcommands:
  add --name NAME --file PATH   Add a playbook
  get NAME                      Get a playbook
  list                          List playbooks
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

// registryPath returns the path to the global registry database.
func registryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fatal("cannot determine home directory: " + err.Error())
	}
	dir := filepath.Join(home, ".fs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal("cannot create ~/.fs directory: " + err.Error())
	}
	return filepath.Join(dir, "registry.db")
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
	projectID := flagVal(args, "--project", "")

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

	h, projectRoot := handler()
	defer h.Close()

	if projectID == "" {
		projectID = filepath.Base(projectRoot)
	}

	updateActivity(projectID)
	resp := h.Query(projectID, searchTerm)
	output(resp)
}

func logCmd(args []string) {
	projectID := flagVal(args, "--project", "")
	nodeIDStr := flagVal(args, "--node", "")
	eventTypeStr := flagVal(args, "--type", "")

	h, projectRoot := handler()
	defer h.Close()

	if projectID == "" {
		projectID = filepath.Base(projectRoot)
	}

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

func fatal(msg string) {
	resp := command.Response{OK: false, Error: msg}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
	os.Exit(1)
}

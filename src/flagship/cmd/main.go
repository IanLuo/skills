// Package main is the CLI entry point for the `fs` binary.
// Single entry point. Subcommands: project, task, status (ARCHITECTURE R4).
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/flagship-dev/flagship"
	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/dispatch"
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
	case "bootstrap":
		checkArgs("bootstrap", os.Args[2:])
		bootstrapCmd(os.Args[2:])
	case "project":
		projectCmd(os.Args[2:])
	case "task":
		taskCmd(os.Args[2:])
	case "status":
		checkArgs("status", os.Args[2:])
		statusCmd(os.Args[2:])
	case "query":
		checkArgs("query", os.Args[2:])
		queryCmd(os.Args[2:])
	case "log":
		checkArgs("log", os.Args[2:])
		logCmd(os.Args[2:])
	case "kb":
		kbCmd(os.Args[2:])
	case "dispatch":
		checkArgs("dispatch", os.Args[2:])
		dispatchCmd(os.Args[2:])
	case "close":
		checkArgs("close", os.Args[2:])
		closeCmd(os.Args[2:])
	case "unfinished":
		checkArgs("unfinished", os.Args[2:])
		unfinishedCmd()
	case "pending":
		checkArgs("pending", os.Args[2:])
		pendingCmd()
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
  unfinished                                   Every task not done, across every scope
  pending                                      Every dispatch not closed out, with
                                                what the cap should do about each:
                                                ready, running, unseen, gone,
                                                unlinked
  query SEARCH_TERM [--project PROJECT_ID]      FTS5 search events
  log [--project PROJECT_ID] [--node NODE_ID] [--type TYPE]  Replay events
  bootstrap                                     Seed ~/.fs/kb from the playbooks
                                                shipped in the binary
  kb add --name NAME --file PATH                Add a playbook
  kb get NAME                                   Get a playbook
  kb list                                       List playbooks and their state
  kb prompt                                     Print the cap's standing rules:
                                                the procedure playbooks with
                                                trigger cap, concatenated. Plain
                                                text on stdout, for
                                                pi --append-system-prompt
  kb diff NAME                                  Diff the shipped default against
                                                the playbook on disk
  kb reset NAME --yes                           Restore the shipped default
  kb edit --name NAME --file PATH               Edit a playbook
  kb remove NAME                                Remove a playbook
  dispatch --project P --type TYPE --goal GOAL [--confirm] [--deliver] [--worktree WS]
                                               Prepare a dispatch brief; --deliver
                                               also hands it to a worker pane;
                                               --worktree names the herdr
                                               worktree workspace to tear down
                                               at close
  close --node NODE [--worker P:NODE] --decision TEXT [--confirm]
                                               Close out a dispatch: run its
                                               cleanup gate, then close its pane,
                                               tab, and worktree, then mark the
                                               node done. The worker node comes
                                               from the delivery record; --worker
                                               is only a check against it.
                                               --confirm answers the gate's ask
                                               steps
  close --node NODE --abandoned --reason TEXT  Close a never-delivered dispatch`)
}

// fsHome returns ~/.fs, creating it if needed. Every persistent artifact lives
// here: the registry, the shared event store, and the knowledge center.
func fsHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fatal("cannot determine home directory: " + err.Error())
	}
	dir := filepath.Join(home, ".fs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal("cannot create ~/.fs directory: " + err.Error())
	}
	return dir
}

// storePath returns the shared event store. Every project's events live in this
// one file, partitioned by project_id — no per-repo .fs directory.
func storePath() string {
	return filepath.Join(fsHome(), "store.db")
}

// projectRoot returns the git repo root, or the working directory outside a repo.
func projectRoot() string {
	if root := command.DetectProjectRoot(); root != "" {
		return root
	}
	wd, err := os.Getwd()
	if err != nil {
		fatal("cannot determine project root: " + err.Error())
	}
	return wd
}

// registryRoot is the registered root_path of a project, or "" when the
// project is not registered — the implicit cap scope and abandoned scopes never
// are.
func registryRoot(reg *registry.Registry, name string) string {
	if p, err := reg.Get(name); err == nil {
		return p.RootPath
	}
	return ""
}

// resolveProject returns the project a command targets and the root its events
// belong to. An explicit name wins — an unregistered scope such as cap is
// allowed, it simply has no known root. Otherwise it is the registered project
// owning the working directory. A directory that resolves to no registered
// project is refused rather than named into existence: falling back to the
// directory's basename would silently target a partition that was never created.
func resolveProject(explicit string) (name, root string) {
	reg := openRegistry()
	defer reg.Close()

	if explicit != "" {
		return explicit, registryRoot(reg, explicit)
	}

	cwdRoot := projectRoot()
	name, ok, err := reg.Resolve(cwdRoot)
	if err != nil {
		fatal("cannot resolve the project for " + cwdRoot + ": " + err.Error())
	}
	if !ok {
		fatal("no project is registered for " + cwdRoot +
			": pass --project <name> to target one, or run fs project create to register this directory")
	}
	if root := registryRoot(reg, name); root != "" {
		return name, root
	}
	return name, cwdRoot
}

// handler opens the shared store and resolves the project a command targets,
// with the commit sha read from that project's root.
func handler(projectID string) (*command.Handler, string) {
	name, root := resolveProject(projectID)
	return openHandler(root), name
}

// openHandler opens the shared store with the CLI's logger and the commit sha
// of root — the project the event is about, not the terminal's location. It
// deliberately does not touch the registry: commands such as fs unfinished must
// keep working after a registry wipe. An empty root means the process cwd.
func openHandler(root string) *command.Handler {
	h, err := command.NewHandler(storePath())
	if err != nil {
		fatal(err.Error())
	}

	// Inject structured logger (ARCHITECTURE R4: JSON to stderr).
	h.SetLogger(logger)

	// Auto-capture the referenced project's git HEAD (ARCHITECTURE R4).
	h.SetCommitSHA(command.DetectCommitSHAIn(root))

	return h
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
		checkArgs("project create", args[1:])
		projectCreate(args[1:])
	case "list":
		checkArgs("project list", args[1:])
		projectList()
	case "get":
		checkArgs("project get", args[1:])
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
		root = projectRoot()
	}
	if name == "" {
		// Default to directory name.
		name = filepath.Base(root)
	}

	// The event describes the project being created, so its sha comes from the
	// root being created.
	h := openHandler(root)
	defer h.Close()

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
		checkArgs("task add", args[1:])
		taskAdd(args[1:])
	case "update":
		checkArgs("task update", args[1:])
		taskUpdate(args[1:])
	case "edit":
		checkArgs("task edit", args[1:])
		taskEdit(args[1:])
	case "block":
		checkArgs("task block", args[1:])
		taskBlock(args[1:])
	case "unblock":
		checkArgs("task unblock", args[1:])
		taskUnblock(args[1:])
	case "knowledge":
		checkArgs("task knowledge", args[1:])
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

	h, projectID := handler(projectID)
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
	projectID := flagVal(rest, "--project", "")

	h, projectID := handler(projectID)
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
	projectID := flagVal(rest, "--project", "")

	h, projectID := handler(projectID)
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
	projectID := flagVal(rest, "--project", "")

	h, projectID := handler(projectID)
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
	projectID := flagVal(rest, "--project", "")

	h, projectID := handler(projectID)
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
	projectID := flagVal(rest, "--project", "")

	h, projectID := handler(projectID)
	defer h.Close()

	resp := h.KnowledgeAdd(projectID, nodeID, summary)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func statusCmd(args []string) {
	projectID := flagVal(args, "--project", "")

	h, projectID := handler(projectID)
	defer h.Close()

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
  list                          List playbooks and their state against the
                                shipped defaults
  prompt                        Print the cap's standing rules: the procedure
                                playbooks with trigger cap, concatenated as
                                plain text on stdout — pipe it into a single
                                pi --append-system-prompt argument
  diff NAME                     Diff the shipped default against the playbook
                                on disk
  reset NAME --yes              Restore the shipped default
  edit --name NAME --file PATH  Edit a playbook
  remove NAME                   Remove a playbook`)
		if len(args) < 1 {
			os.Exit(2)
		}
		return
	}
	switch args[0] {
	case "add":
		checkArgs("kb add", args[1:])
		kbAdd(args[1:])
	case "get":
		checkArgs("kb get", args[1:])
		kbGet(args[1:])
	case "list":
		checkArgs("kb list", args[1:])
		kbList(args[1:])
	case "prompt":
		checkArgs("kb prompt", args[1:])
		kbPrompt(args[1:])
	case "diff":
		checkArgs("kb diff", args[1:])
		kbDiff(args[1:])
	case "reset":
		checkArgs("kb reset", args[1:])
		kbReset(args[1:])
	case "edit":
		checkArgs("kb edit", args[1:])
		kbEdit(args[1:])
	case "remove":
		checkArgs("kb remove", args[1:])
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

// kbList reports every playbook and how it relates to the shipped defaults, so
// a drifted contract is visible without anyone remembering to check.
func kbList(_ []string) {
	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	states, err := kc.States(shippedKB())
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]any{"playbooks": states}})
}

// kbPrompt prints the cap's standing rules — the procedure playbooks with
// trigger cap, concatenated. It is the delivery path for the playbooks: piping
// it into `pi --append-system-prompt` is the entire integration, so stdout
// carries the prompt and nothing else, and a failure goes to stderr rather than
// the JSON envelope every other command uses.
func kbPrompt(_ []string) {
	kc, err := knowledge.Open(kbDir())
	if err != nil {
		promptError(err)
	}

	prompt, err := kc.Prompt(shippedKB())
	if err != nil {
		promptError(err)
	}
	fmt.Print(prompt)
}

// promptError reports a failed command whose stdout is a payload the caller
// pipes onward. The JSON envelope would land in the prompt, so the error goes
// to stderr instead.
func promptError(err error) {
	fmt.Fprintln(os.Stderr, "fs kb prompt: "+err.Error())
	os.Exit(1)
}

// kbDiff shows what a playbook on disk has that its shipped default does not,
// and the other way around. An empty diff is a playbook that has not drifted.
func kbDiff(args []string) {
	if len(args) < 1 {
		fatal("fs kb diff: NAME required")
	}
	name := args[0]

	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	diff, err := kc.Diff(shippedKB(), name)
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]string{"name": name, "diff": diff}})
}

// kbReset puts the shipped default back. It refuses without --yes: resetting
// discards the user's edits, which is not something to do by accident.
func kbReset(args []string) {
	if len(args) < 1 {
		fatal("fs kb reset: NAME required")
	}
	name := args[0]
	if !hasFlag(args, "--yes") {
		output(command.Response{OK: false, Error: fmt.Sprintf(
			"fs kb reset: refusing to overwrite the playbook %q without --yes; run fs kb diff %s to see what would be discarded",
			name, name)})
		return
	}

	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	path, err := kc.Reset(shippedKB(), name)
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]string{"name": name, "action": "reset", "path": path}})
}

// bootstrapCmd materialises the playbooks shipped in the binary into ~/.fs/kb.
// It is safe on every install: what is absent is created and what is there is
// left alone, so a playbook the user edited is never quietly replaced.
func bootstrapCmd(_ []string) {
	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	outcomes, err := kc.Seed(shippedKB())
	if err != nil {
		output(command.Response{OK: false, Error: err.Error()})
		return
	}
	output(command.Response{OK: true, Data: map[string]any{
		"kb_path":   kbDir(),
		"playbooks": outcomes,
	}})
}

// shippedKB is the playbooks embedded in the binary, rooted at the KB
// directory. They are the reference every kb command compares against.
func shippedKB() fs.FS {
	kb, err := flagship.KB()
	if err != nil {
		fatal("fs: embedded playbooks: " + err.Error())
	}
	return kb
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

// kbDir returns the knowledge center directory. SYSTEM-DESIGN R4: ~/.fs/kb/.
func kbDir() string {
	return filepath.Join(fsHome(), "kb")
}

// registryPath returns the path to the global registry database.
func registryPath() string {
	return filepath.Join(fsHome(), "registry.db")
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

	h, projectID := handler(projectID)
	defer h.Close()

	updateActivity(projectID)
	resp := h.Query(projectID, searchTerm)
	output(resp)
}

func logCmd(args []string) {
	projectID := flagVal(args, "--project", "")
	nodeIDStr := flagVal(args, "--node", "")
	eventTypeStr := flagVal(args, "--type", "")

	h, projectID := handler(projectID)
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

// unfinishedCmd lists every node that is not done, across every scope. Scopes
// come from the store's events, so a scope that was never registered — or whose
// registry row was lost — still reports its unfinished work.
func unfinishedCmd() {
	// Nothing is referenced, so the sha is the cwd's — nil when it is not a repo.
	h := openHandler("")
	defer h.Close()

	output(h.Unfinished())
}

// pendingCmd answers "what is waiting on the cap": every dispatch in the cap's
// scope that is not done, classified against its delivery record, its worker's
// node, and herdr. It is a read — it writes nothing.
func pendingCmd() {
	h := openHandler("")
	defer h.Close()

	output(dispatch.Pending(h, dispatch.NewHerdrCLI()))
}

// dispatchCmd prepares the cap's dispatch brief and records the cap's node.
// Without --deliver it stops there, and the brief carries the herdr command for
// the cap to run. With --deliver it also splits the pane, starts the worker,
// sends the brief, and records the pane binding.
func dispatchCmd(args []string) {
	project := flagVal(args, "--project", "")
	taskType := flagVal(args, "--type", "")
	goal := flagVal(args, "--goal", "")
	worktree := flagVal(args, "--worktree", "")
	confirm := hasFlag(args, "--confirm")
	deliver := hasFlag(args, "--deliver")

	if project == "" {
		fatal("--project is required")
	}
	if taskType == "" {
		fatal("--type is required")
	}
	if goal == "" {
		fatal("--goal is required")
	}

	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	reg := openRegistry()
	defer reg.Close()

	// The cap's events describe the project being dispatched to, so the sha
	// comes from that project's root, not the cap's terminal. An unregistered
	// target leaves the root empty and is reported by Prepare below.
	// The cap's scope is implicit — a project_id, never a registry row.
	h := openHandler(registryRoot(reg, project))
	defer h.Close()

	resp := dispatch.Prepare(h, reg, kc, project, taskType, goal, confirm)
	if resp.OK {
		brief := resp.Data.(*dispatch.Brief)
		brief.Worktree = worktree
		if deliver {
			resp = dispatch.Deliver(h, dispatch.NewHerdrCLI(), brief)
		}
	}
	output(resp)
}

// closeCmd is the close-out gate: it refuses to close a dispatch whose worker
// node is not done, runs the exit gate its task type declares, then closes the
// pane, tab, and worktree from the recorded delivery and marks the cap's node
// done.
func closeCmd(args []string) {
	req := dispatch.CloseRequest{
		NodeID:    flagVal(args, "--node", ""),
		Worker:    flagVal(args, "--worker", ""),
		Decision:  flagVal(args, "--decision", ""),
		Confirm:   hasFlag(args, "--confirm"),
		Abandoned: hasFlag(args, "--abandoned"),
		Reason:    flagVal(args, "--reason", ""),
	}

	// The cleanup gate runs in the worker project's root, so the registry is
	// always needed — and the close-out's events describe that project, so the
	// sha comes from its root too.
	reg := openRegistry()
	defer reg.Close()

	root := ""
	if req.Worker != "" {
		if workerProject, _, ok := strings.Cut(req.Worker, ":"); ok {
			root = registryRoot(reg, workerProject)
		}
	}
	h := openHandler(root)
	defer h.Close()

	kc, err := knowledge.Open(kbDir())
	if err != nil {
		fatal(err.Error())
	}

	output(dispatch.Close(h, dispatch.NewHerdrCLI(), reg, kc, req))
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

// hasFlag reports whether a boolean flag is present. flagVal cannot answer
// this: it consumes the next argument, which a trailing --confirm does not have.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
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

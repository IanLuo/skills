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
	"github.com/flagship-dev/flagship/internal/dispatch"
	"github.com/flagship-dev/flagship/internal/engine/herdr"
	"github.com/flagship-dev/flagship/internal/logging"
	"github.com/flagship-dev/flagship/internal/query"
	"github.com/flagship-dev/flagship/internal/registry"
	"github.com/flagship-dev/flagship/internal/store"
)

// logger is the structured logger for the CLI: JSON to stderr (ARCHITECTURE R4),
// and the same lines teed to the running log under ~/.fs/logs. It is built in
// main once the level is known; the file sink is best-effort and never gates.
var logger *slog.Logger

// stripGlobalVerbose pulls the global -v/--verbose flag out of the args before
// the per-subcommand allowlist runs. checkArgs refuses unknown flags, and -v
// must work for every subcommand, so it is handled here, once, rather than
// added to every subcommand's spec.
func stripGlobalVerbose(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	verbose := false
	for _, a := range args {
		if a == "-v" || a == "--verbose" {
			verbose = true
			continue
		}
		out = append(out, a)
	}
	return out, verbose
}

// logDir is ~/.fs/logs, resolved without creating anything: the sink creates
// the directory lazily on its first write, so a --help or a command that never
// logs touches nothing on disk.
func logDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".fs", "logs")
}

func main() {
	args, verbose := stripGlobalVerbose(os.Args[1:])
	os.Args = append([]string{os.Args[0]}, args...)

	level, err := logging.ResolveLevel(os.Getenv("FS_LOG_LEVEL"), verbose)
	if err != nil {
		fatal(err.Error())
	}
	logger = logging.NewLogger(logDir(), level)

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
		checkArgs("status", os.Args[2:])
		statusCmd(os.Args[2:])
	case "query":
		checkArgs("query", os.Args[2:])
		queryCmd(os.Args[2:])
	case "log":
		checkArgs("log", os.Args[2:])
		logCmd(os.Args[2:])
	case "dispatch":
		checkArgs("dispatch", os.Args[2:])
		dispatchCmd(os.Args[2:])
	case "reviews":
		checkArgs("reviews", os.Args[2:])
		reviewsCmd(os.Args[2:])
	case "close":
		checkArgs("close", os.Args[2:])
		closeCmd(os.Args[2:])
	case "script":
		scriptCmd(os.Args[2:])
	case "version":
		checkArgs("version", os.Args[2:])
		versionCmd()
	case "unfinished":
		checkArgs("unfinished", os.Args[2:])
		unfinishedCmd()
	case "gaps":
		checkArgs("gaps", os.Args[2:])
		gapsCmd()
	case "pending":
		checkArgs("pending", os.Args[2:])
		pendingCmd()
	case "cap":
		capCmd(os.Args[2:])
	case "report":
		checkArgs("report", os.Args[2:])
		reportCmd(os.Args[2:])
	case "tell":
		checkArgs("tell", os.Args[2:])
		tellCmd(os.Args[2:])
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
  task add --goal GOAL [--parent NODE_ID]       Add a task; --kind names what it
     [--check "SHELL COMMAND"]                  is (work, dispatch, gap — work
     [--severity LEVEL]                         by default), --found-by
                                                PROJECT:NODE records what a gap
                                                was found by, --check names a
                                                shell command that would show
                                                the node's claim is over, and
                                                --severity sets a gap's weight
                                                (severe, defect, prose)
  task get NODE_ID [--project P] [--kind KIND]  Show one task's derived state;
                                                exit 1 when no such node exists,
                                                or when --kind names a different
                                                kind
  task update NODE_ID --status STATUS [--decision TEXT]  Update task
  task edit NODE_ID [--goal GOAL] [--kind KIND] Edit task metadata: its goal,
     [--severity LEVEL]                         its structural kind, a gap's
                                                weight, or any combination; a
                                                kind that leaves gap clears the
                                                weight with it
  task merge NODE_ID --into SURVIVOR            Fold a node into another: the
     [--reason TEXT]                            absorbed node is done and names
                                                what took it in, the survivor
                                                names what it took in; neither
                                                may be done, and a node that has
                                                absorbed one may not be absorbed.
                                                Both operands take the address
                                                form and each is resolved in the
                                                scope it names, so a fold may
                                                span two scopes; --project is
                                                only the default for an operand
                                                that carries none
  task block NODE_ID --reason REASON            Block a task; --check names a
                                                shell command that would show
                                                the condition is over, re-run by
                                                every reader that renders a status
  task unblock NODE_ID                          Unblock a task
  task knowledge NODE_ID --summary TEXT         Add knowledge to a task
  status [--project PROJECT_ID]                 Show project status
  unfinished                                   Every task not done, across every
                                                scope, grouped by kind
  gaps                                         Every kind=gap node — the backlog —
                                                header first, then open ones
                                                before done, heaviest first, with
                                                found_by and severity
  pending                                      Every dispatch not closed out, in
                                                EVERY scope — each entry names the
                                                scope its node stands in — with
                                                what the cap should do about each:
                                                ready, blocked, running, unknown,
                                                unseen, gone, unlinked — blocked
                                                being a worker parked on a prompt
                                                only a person can answer, which
                                                running does not distinguish from
                                                thinking
                                                — the undispatched gap count, and
                                                the cap's own address and liveness
                                                beside every open report
  cap claim [--reconstructed "WHAT YOU READ"]  Become the cap: register this pane
                                                as the scope's address. Refused
                                                while a live cap holds it, naming
                                                it; a take-over of a dead one
                                                requires --reconstructed
  cap who                                       Print the cap's address and whether
                                                herdr still has it; exit 0 with a
                                                live cap, 1 without
  report PROJECT:NODE --line "ONE LINE"         A worker's report to the cap:
     [--blocked|--question|--done]              written to the store first and
                                                always, then sent to the live cap
                                                best-effort. --blocked, --question
                                                and --done notify; no kind is
                                                silent. A failure to notify is not
                                                a failed report: exit 0 says
                                                "recorded, not notified"
  tell PROJECT:NODE "TEXT"                      The cap's reply, routed through
                                                that dispatch's delivery record
  query SEARCH_TERM [--project PROJECT_ID]      FTS5 search events
  log [--project PROJECT_ID] [--node NODE_ID] [--type TYPE]  Replay events; --node
                                                takes a node address — a bare
                                                t-<hex> id, or <project>:<node>
                                                — and the address supplies the
                                                scope when --project did not
  dispatch --project P --type TYPE --goal GOAL
           [--integrates MEMBER_NODE] [--reviews CAP_NODE]
           [--fixes CAP_NODE]...
           [--allow-unresolved] [--deliver] [--worktree WS]
           [--worktree-new]
                                               A dispatch is a worker node, a
                                               rendered brief, and a herdr
                                               delivery — no plan and no gate.
                                               The launch config (engine=herdr,
                                               agent=pi, and the provider route)
                                               is frozen in the binary. --deliver
                                               splits the pane, creates the
                                               worker's node, starts its agent
                                               and hands over the brief;
                                               without it only the brief is
                                               recorded and printed.
                                               --integrates names the member cap
                                               node this integration merges,
                                               recorded on the dispatch's own
                                               task-created payload. --reviews
                                               names the cap node whose change
                                               this dispatch reads — a different
                                               link from --integrates, recorded
                                               the same way. --fixes names a cap
                                               node this dispatch repairs, which
                                               is open while the fix is made;
                                               repeatable. --allow-unresolved
                                               proceeds while another dispatch
                                               is open and undelivered, and
                                               records which ones.
  reviews CAP_NODE                             Exit 0 and print the done review
                                               dispatch that names this cap node
                                               as the one it reads; exit 1 when
                                               none does, or when one does and is
                                               not done yet
  close --node NODE [--worker P:NODE] --decision TEXT
                                               Close out a dispatch: record one
                                               evidence line — the sha HEAD
                                               stands at, the base, and the
                                               verdict that carries the suite
                                               result — then close its pane and
                                               mark the node done. It refuses
                                               while the worker's node is not
                                               done, and while HEAD has not moved
                                               past the revision the dispatch was
                                               prepared at. The worker node comes
                                               from the delivery record; --worker
                                               is only a check against it
  close --node NODE --abandoned --reason TEXT  Close a dispatch without a verdict,
                                               delivered or not: a never-delivered
                                               one records "abandoned, never
                                               delivered: <reason>", a delivered
                                               one records "abandoned after
                                               delivery to <project>:<node>" and
                                               its pane is torn down
  script NAME [ARGS...]                       Run a script the binary ships —
                                               read out of the binary, written
                                               nowhere — and exit with its status
  version                                      Print the revision this binary
                                               was built from, whether that
                                               tree was dirty, and the file it
                                               is running from. A build with no
                                               stamp says "unknown"`)
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
  add --goal GOAL [--parent NODE_ID] [--kind KIND] [--found-by PROJECT:NODE]
      [--check "SHELL COMMAND"] [--severity LEVEL]
                                           Add a task; --kind is work (default),
                                           dispatch or gap; --found-by names
                                           the node a gap was found by; --check
                                           names a shell command that would
                                           show the node's claim is over, re-run
                                           by every reader that renders a status;
                                           --severity sets a gap's weight —
                                           severe, defect or prose
  get NODE_ID [--project PROJECT_ID] [--kind KIND]
                                           Show one task's derived state; exit
                                           1 when no such node exists, or when
                                           --kind names a different kind
  update NODE_ID --status STATUS [--decision TEXT]  Update task
  edit NODE_ID [--goal GOAL] [--kind KIND] [--severity LEVEL]
                                           Edit task metadata; a kind that
                                           leaves gap clears the weight with it
  merge NODE_ID --into SURVIVOR [--reason TEXT]
                                           Fold a node into another: the
                                           absorbed node is done and names what
                                           took it in, the survivor names what
                                           it took in. Neither may be done, and
                                           a node that has absorbed one may not
                                           be absorbed
  block NODE_ID --reason REASON [--check "SHELL COMMAND"]
                                           Block a task; --check names a shell
                                           command that would show the
                                           condition is over, re-run by
                                           every reader that renders a status
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
	case "get":
		checkArgs("task get", args[1:])
		taskGet(args[1:])
	case "update":
		checkArgs("task update", args[1:])
		taskUpdate(args[1:])
	case "edit":
		checkArgs("task edit", args[1:])
		taskEdit(args[1:])
	case "merge":
		checkArgs("task merge", args[1:])
		taskMerge(args[1:])
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
	kind := query.Kind(flagVal(args, "--kind", ""))
	foundBy := flagVal(args, "--found-by", "")
	check := flagVal(args, "--check", "")
	severity := query.Severity(flagVal(args, "--severity", ""))

	h, projectID := handler(projectID)
	defer h.Close()
	guardCoordinating(h, projectID)

	if goal == "" {
		fatal("--goal is required")
	}

	// An absent --kind means the default. TaskAddKind refuses anything that is
	// not a kind, and refuses --severity on anything that is not a gap, so
	// neither a typo nor a misplaced weight can be stored.
	if kind == "" {
		kind = query.KindWork
	}

	var parentPtr *string
	if parent != "" {
		parentPtr = &parent
	}

	resp := h.TaskAddKind(projectID, goal, kind, foundBy, "", "", check, severity, parentPtr)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

// taskGet prints one node's derived state. It is a read with an exit code: it
// answers "does this node exist, and is it the kind I think it is?" the way a
// gate can use it, instead of leaving a playbook to parse fs status's JSON in
// shell. --kind is the filter the integrate entry gate names: an integration
// integrates a member's dispatch node, so the node it was given must be one.
func taskGet(args []string) {
	pos := positionals("task get", args)
	if len(pos) < 1 {
		fatal("fs task get: NODE_ID required")
	}
	nodeID := pos[0]
	projectID := flagVal(args, "--project", "")
	kind := query.Kind(flagVal(args, "--kind", ""))

	if kind != "" && !query.ValidKind(kind) {
		fatal(fmt.Sprintf("fs task get: invalid kind %q (valid: work, dispatch, gap)", kind))
	}

	projectID, nodeID = nodeArg(projectID, nodeID)

	// The project's root is where a blocked node's recorded check runs, the same
	// way fs status resolves it: one node's derived state must not disagree with
	// the tree it was copied from.
	name, root := resolveProject(projectID)
	h := openHandler(root)
	defer h.Close()

	resp := h.StatusIn(name, root)
	if !resp.OK {
		output(resp)
		return
	}

	for _, task := range resp.Data.(command.StatusResult).Tasks {
		if task.NodeID != nodeID {
			continue
		}
		if kind != "" && task.Kind != kind {
			output(command.Response{OK: false, Error: fmt.Sprintf(
				"fs task get: node %s in project %s is a %q node, not a %q node",
				nodeID, name, task.Kind, kind)})
			return
		}
		output(command.Response{OK: true, Data: task})
		return
	}
	output(command.Response{OK: false, Error: fmt.Sprintf(
		"fs task get: no node %s in project %s", nodeID, name)})
}

func taskUpdate(args []string) {
	pos := positionals("task update", args)
	if len(pos) < 1 {
		fatal("fs task update: NODE_ID required")
	}
	nodeID := pos[0]
	status := flagVal(args, "--status", "")
	decision := flagVal(args, "--decision", "")
	projectID := flagVal(args, "--project", "")

	projectID, nodeID = nodeArg(projectID, nodeID)

	h, projectID := handler(projectID)
	defer h.Close()
	guardCoordinating(h, projectID)

	resp := h.TaskUpdate(projectID, nodeID, status, decision, nil)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func taskEdit(args []string) {
	pos := positionals("task edit", args)
	if len(pos) < 1 {
		fatal("fs task edit: NODE_ID required")
	}
	nodeID := pos[0]
	goal := flagVal(args, "--goal", "")
	projectID := flagVal(args, "--project", "")

	projectID, nodeID = nodeArg(projectID, nodeID)

	h, projectID := handler(projectID)
	defer h.Close()
	guardCoordinating(h, projectID)

	resp := h.TaskEdit(projectID, nodeID, goal,
		query.Kind(flagVal(args, "--kind", "")),
		query.Severity(flagVal(args, "--severity", "")), nil)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

// taskMerge folds one node into another — two gaps about one defect, or two
// nodes that are one thing. Both halves of the record are written by the
// handler; this is the argument shape and the guard.
func taskMerge(args []string) {
	pos := positionals("task merge", args)
	if len(pos) < 1 {
		fatal("fs task merge: NODE_ID required")
	}
	nodeID := pos[0]
	into := flagVal(args, "--into", "")
	reason := flagVal(args, "--reason", "")
	projectID := flagVal(args, "--project", "")

	projectID, nodeID = nodeArg(projectID, nodeID)

	h, projectID := handler(projectID)
	defer h.Close()

	// A fold's two ends may be in two scopes — that is the fold triage needs —
	// so the guard is asked of every scope this merge will write in, not only
	// the one it resolved. Otherwise a worker's pane could reach the cap's scope
	// by naming it in --into, which is the write the guard exists to refuse.
	guardCoordinating(h, projectID)
	for _, ref := range []string{nodeID, into} {
		if scope := projectOfAddress(ref); scope != "" {
			guardCoordinating(h, scope)
		}
	}

	resp := h.TaskMerge(projectID, nodeID, into, reason, nil)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func taskBlock(args []string) {
	pos := positionals("task block", args)
	if len(pos) < 1 {
		fatal("fs task block: NODE_ID required")
	}
	nodeID := pos[0]
	reason := flagVal(args, "--reason", "")
	check := flagVal(args, "--check", "")
	projectID := flagVal(args, "--project", "")

	projectID, nodeID = nodeArg(projectID, nodeID)

	h, projectID := handler(projectID)
	defer h.Close()
	guardCoordinating(h, projectID)

	resp := h.TaskBlockWithCheck(projectID, nodeID, reason, check)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func taskUnblock(args []string) {
	pos := positionals("task unblock", args)
	if len(pos) < 1 {
		fatal("fs task unblock: NODE_ID required")
	}
	nodeID := pos[0]
	projectID := flagVal(args, "--project", "")

	projectID, nodeID = nodeArg(projectID, nodeID)

	h, projectID := handler(projectID)
	defer h.Close()
	guardCoordinating(h, projectID)

	resp := h.TaskUnblock(projectID, nodeID)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

func taskKnowledge(args []string) {
	pos := positionals("task knowledge", args)
	if len(pos) < 1 {
		fatal("fs task knowledge: NODE_ID required")
	}
	nodeID := pos[0]
	summary := flagVal(args, "--summary", "")
	projectID := flagVal(args, "--project", "")

	projectID, nodeID = nodeArg(projectID, nodeID)

	h, projectID := handler(projectID)
	defer h.Close()
	guardCoordinating(h, projectID)

	resp := h.KnowledgeAdd(projectID, nodeID, summary)
	if resp.OK {
		updateActivity(projectID)
	}
	output(resp)
}

// statusCmd shows one project's derived state. A blocked node's recorded check
// runs in the project's own root — the same re-verification every reader that
// renders a status does — so they agree on whether a block's condition still
// holds.
func statusCmd(args []string) {
	projectID := flagVal(args, "--project", "")

	name, root := resolveProject(projectID)
	h := openHandler(root)
	defer h.Close()

	updateActivity(name)
	output(h.StatusIn(name, root))
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

	// --node takes the address form, the way a positional does: the address
	// supplies the scope when --project did not, and an explicit --project beats
	// the qualifier and is refused by the command layer when the two disagree.
	projectID, nodeIDStr = nodeArg(projectID, nodeIDStr)

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

// unfinishedCmd lists every node that is not done, grouped by kind, across every
// scope. Scopes come from the store's events, so a scope that was never
// registered — or whose registry row was lost — still reports its unfinished
// work.
func unfinishedCmd() {
	// Nothing is referenced, so the sha is the cwd's — nil when it is not a repo.
	h := openHandler("")
	defer h.Close()

	// A recorded block check runs in its scope's project root, so a resolved
	// condition reads as resolved. A scope with no registry row — cap, most of
	// all — has no root, and the check runs in this process's directory.
	reg := openRegistry()
	defer reg.Close()

	output(h.Unfinished(func(scope string) string { return registryRoot(reg, scope) }))
}

// gapsCmd lists every kind=gap node across every scope, open ones first and the
// heaviest of those first, with the node that found it and its weight, behind a
// header counting what is being shown — the backlog, read from the store rather
// than remembered. It is a read: it writes nothing.
//
// A gap's recorded check is re-run here the same way every reader that renders
// a status re-runs it, in the gap's own project root, so a claim the check has
// just denied reads as expired in the list the cap reads its backlog with.
// A scope with no registry row — cap, most of all — has no root, and the check
// runs in this process's directory.
func gapsCmd() {
	h := openHandler("")
	defer h.Close()

	reg := openRegistry()
	defer reg.Close()

	output(h.Gaps(func(scope string) string { return registryRoot(reg, scope) }))
}

// pendingCmd answers "what is waiting on the cap": every dispatch in the cap's
// scope that is not done, classified against its delivery record, its worker's
// node, and herdr. It is a read — it writes nothing.
func pendingCmd() {
	h := openHandler("")
	defer h.Close()

	output(dispatch.Pending(h, herdr.New()))
}

// capCmd is fs cap: the claim that gives the scope an address, and the read
// that says whether it is still there. Like fs task, the bare word only
// dispatches.
func capCmd(args []string) {
	if len(args) < 1 || isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, `Usage: fs cap <subcommand>

Subcommands:
  claim [--reconstructed "WHAT YOU READ"]   Register this pane as the cap's
                                            address: refused while a live cap
                                            holds the scope, naming it, and a
                                            take-over when herdr no longer has
                                            the registered pane
  who                                       Print the address and whether herdr
                                            still has it; exit 0 with a live
                                            cap, 1 without`)
		if len(args) < 1 {
			os.Exit(2)
		}
		return
	}

	switch args[0] {
	case "claim":
		checkArgs("cap claim", args[1:])
		capClaimCmd(args[1:])
	case "who":
		checkArgs("cap who", args[1:])
		capWhoCmd()
	default:
		fmt.Fprintf(os.Stderr, "fs cap: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// capClaimCmd makes this session the cap. The pane and tab are read from the
// environment herdr gives the session; --reconstructed is what a take-over
// requires, and what it read before taking the scope.
func capClaimCmd(args []string) {
	h := openHandler("")
	defer h.Close()
	guardCoordinating(h, "")

	output(dispatch.Claim(h, herdr.New(), dispatch.ClaimRequest{
		PaneID:        os.Getenv("HERDR_PANE_ID"),
		TabID:         os.Getenv("HERDR_TAB_ID"),
		Reconstructed: flagVal(args, "--reconstructed", ""),
	}))
}

// capWhoCmd prints the address and its liveness. Exit 0 iff a live cap exists:
// no claim and a dead one both exit 1, and say which they are.
func capWhoCmd() {
	h := openHandler("")
	defer h.Close()

	status, err := dispatch.CapStatusOf(h, herdr.New())
	if err != nil {
		fatal("fs cap who: " + err.Error())
	}
	output(command.Response{OK: true, Data: status})
	if status.State != dispatch.CapLive {
		os.Exit(1)
	}
}

// reportCmd is the worker's report: fs writes the event and then, best-effort,
// notifies the live cap — in that order, in one command. The kind is the only
// switch, and a boolean flag is how a kind is named.
func reportCmd(args []string) {
	pos := positionals("report", args)
	if len(pos) < 1 {
		fatal("fs report: PROJECT:NODE required")
	}
	nodeID := pos[0]
	projectID := flagVal(args, "--project", "")

	projectID, nodeID = nodeArg(projectID, nodeID)

	h, projectID := handler(projectID)
	defer h.Close()

	output(dispatch.Report(h, herdr.New(), dispatch.ReportRequest{
		Project: projectID,
		Node:    nodeID,
		Kind:    reportKind(args),
		Line:    flagVal(args, "--line", ""),
	}))
}

// reportKind is the kind the flags name: --blocked, --question or --done, and
// none at all for a plain note. There is deliberately no flag that forces a
// notification for a kind that does not notify — a worker who could interrupt at
// will could spend the cap's context back up.
func reportKind(args []string) string {
	for _, kind := range []struct{ flag, name string }{
		{"--blocked", command.ReportBlocked},
		{"--question", command.ReportQuestion},
		{"--done", command.ReportDone},
	} {
		if hasFlag(args, kind.flag) {
			return kind.name
		}
	}
	return ""
}

// tellCmd is the cap's reply, resolved through the worker's delivery record.
func tellCmd(args []string) {
	if len(args) < 2 {
		fatal("fs tell: PROJECT:NODE and TEXT required")
	}
	projectID, nodeID := nodeArg("", args[0])

	h, projectID := handler(projectID)
	defer h.Close()
	guardCoordinating(h, "")

	output(dispatch.Tell(h, herdr.New(), dispatch.TellRequest{
		Project: projectID,
		Node:    nodeID,
		Text:    args[1],
	}))
}

// guardCoordinating refuses a coordinating act from the pane this process runs
// in, when fs holds a delivery record for it. scope is the project the act
// writes to, and "" for an act that coordinates whatever its scope — a
// dispatch, a close, a claim, a reply.
//
// The guard is fs's and not a playbook's: a worker cannot reach a coordinating
// act by not knowing a rule. It reads HERDR_PANE_ID and nothing else, and a
// pane it holds no record for is unclaimed rather than a worker.
func guardCoordinating(h *command.Handler, scope string) {
	if scope != "" && !dispatch.IsCapScope(scope) {
		return
	}
	if err := dispatch.Guard(h, os.Getenv("HERDR_PANE_ID")); err != nil {
		fatal(err.Error())
	}
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
	worktreeNew := hasFlag(args, "--worktree-new")
	integrates := flagVal(args, "--integrates", "")
	reviews := flagVal(args, "--reviews", "")
	fixes := multiFlag(args, "--fixes")
	allowUnresolved := hasFlag(args, "--allow-unresolved")
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
	if worktree != "" && worktreeNew {
		fatal("--worktree names an existing workspace and --worktree-new creates one; a dispatch runs in one worktree, not both")
	}

	reg := openRegistry()
	defer reg.Close()

	// The cap's events describe the project being dispatched to, so the sha
	// comes from that project's root, not the cap's terminal. An unregistered
	// target leaves the root empty and is reported by Prepare below.
	// The cap's scope is implicit — a project_id, never a registry row.
	h := openHandler(registryRoot(reg, project))
	defer h.Close()
	guardCoordinating(h, "")

	resp := dispatch.Prepare(h, reg, dispatch.PrepareRequest{
		Project:         project,
		TaskType:        taskType,
		Goal:            goal,
		Integrates:      integrates,
		Reviews:         reviews,
		Fixes:           fixes,
		AllowUnresolved: allowUnresolved,
	})
	if resp.OK {
		brief := resp.Data.(*dispatch.Brief)
		// DEBUG carries the launch config a dispatcher has to get right and the
		// node the cap just made: the two things a diagnosis starts from.
		logger.Debug("brief composed",
			"command", "dispatch",
			"project", project,
			"type", taskType,
			"cap_node", brief.CapNodeID,
			"engine", brief.Engine,
			"agent", brief.Agent,
			"agent_args", strings.Join(brief.AgentArgs, ","))
		brief.Worktree = worktree
		brief.WorktreeNew = worktreeNew
		if deliver {
			resp = dispatch.Deliver(h, herdr.New(), brief)
		}
	}
	output(resp)
}

// closeCmd closes out a dispatch: it refuses a worker node that is not done and a
// tree whose HEAD has not moved, records one evidence line on the node, closes
// the recorded pane, and marks the cap's node done.
func closeCmd(args []string) {
	req := dispatch.CloseRequest{
		NodeID:    flagVal(args, "--node", ""),
		Worker:    flagVal(args, "--worker", ""),
		Decision:  flagVal(args, "--decision", ""),
		Abandoned: hasFlag(args, "--abandoned"),
		Reason:    flagVal(args, "--reason", ""),
	}

	// The evidence line measures the worker project's tree, so the registry is
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
	guardCoordinating(h, "")

	output(dispatch.Close(h, herdr.New(), reg, req))
}

// reviewsCmd answers a close's question: was this dispatch's change reviewed? It
// exits 0 printing the done review dispatch that names this node, or 1 naming
// what it found instead. A review is a node kind with a link, never a gate: the
// question is whether a done review names this node, and nothing else is read.
func reviewsCmd(args []string) {
	if len(args) < 1 {
		fatal("fs reviews: CAP_NODE required")
	}

	h := openHandler("")
	defer h.Close()

	output(dispatch.Reviews(h, args[0]))
}

// nodeArg splits a "<project>:<node>" node argument into the project it names
// and the bare node id — but only when no --project was given: an explicit
// --project is the statement the command resolves by. A qualifier that names
// another project is not answered by either, so it is refused here, in
// refuseProjectConflict, before the command runs — it is a conflict between two
// of the caller's beliefs, and the refusal says which one is stale. A colon
// never appears in a node id (they are "t-<hex>"), so reading one as a
// qualifier is safe.
//
// An address with no node in it is not split. Cutting "cap:" to ("cap", "")
// would hand the command layer an empty node id, which every reader reads as
// "no node was named" — so `fs log --node "cap:"` answered with the whole scope
// while the same address beside an explicit --project refused: one address, two
// answers. The scope the address named is still supplied, and the reference goes
// on whole, so the one resolver sees it and refuses it the same way on both
// paths.
//
// An address with no project in it — `--node :t-x` — names a node and no scope,
// and is not a second belief for the guard to weigh against --project. Only the
// node half is addressed and the flag supplies the scope, exactly as it does for
// a bare id; the blank half is dropped here, before the guard, so the guard is
// never handed a project that is not a name.
func nodeArg(projectID, nodeID string) (string, string) {
	if projectID != "" {
		if project, node, ok := strings.Cut(nodeID, ":"); ok && project == "" && node != "" {
			nodeID = node
		}
		refuseProjectConflict(projectID, nodeID)
		return projectID, nodeID
	}
	project, node, ok := strings.Cut(nodeID, ":")
	if !ok {
		return projectID, nodeID
	}
	if node == "" {
		return project, nodeID
	}
	return project, node
}

// refuseProjectConflict refuses an invocation that named two projects and left
// one of them stale. `--project P` and an address naming Q are two beliefs about
// one node, and when they disagree the command is answered by neither — so it is
// refused, and the refusal is diagnostic rather than merely ambiguous: the node
// is resolved in both scopes and the message says where it actually is, so the
// caller is told which belief is out of date instead of being left to work it
// out. A refusal that said only "these conflict" would make the caller do that
// work, and the two records it would reach for are the two it just gave.
//
// It is a no-op for an invocation that named one project, or two that agree, and
// for a command whose --project is a default rather than an assertion — see
// projectDefault. An address whose node half is empty (`cap:`) is left to the
// command, which refuses it as naming no node.
func refuseProjectConflict(projectID, nodeID string) {
	cmd := strings.Replace(commandName(), "-", " ", 1)
	spec, known := cmdSpecs[cmd]
	if !known || spec.projectDefault {
		return
	}
	addressProject, node, ok := strings.Cut(nodeID, ":")
	if !ok || node == "" || addressProject == projectID {
		return
	}

	here := scopeHoldsNode(addressProject, node)
	there := scopeHoldsNode(projectID, node)
	var why string
	switch {
	case here && there:
		why = fmt.Sprintf("%s is in both, so neither half is the stale one", node)
	case here:
		why = fmt.Sprintf("%s is in %s, so --project is the stale half", node, addressProject)
	case there:
		why = fmt.Sprintf("%s is in %s, so the address is the stale half", node, projectID)
	default:
		why = fmt.Sprintf("%s is in neither, so the address names the wrong node", node)
	}
	output(command.Response{OK: false, Error: fmt.Sprintf(
		"fs %s: the address says %s and --project says %s; %s", cmd, addressProject, projectID, why)})
}

// scopeHoldsNode reports whether a scope holds a node with this bare id. It is
// Handler.NodeExists, not Handler.Status: Status re-runs every recorded block
// and claim check to derive a status (check.go, reportedStatus), which is the
// right thing for a reader that is about to show a claim and the wrong thing
// here — a refusal must not run a shell, and this asks about the record rather
// than about whether a claim still holds. Unreadable is reported as "does not
// hold" so a scope that cannot be read is not mistaken for one that named the
// node.
func scopeHoldsNode(scope, node string) bool {
	h, _ := handler(scope)
	defer h.Close()
	holds, err := h.NodeExists(scope, node)
	return err == nil && holds
}

// commandName derives the command a log line belongs to, from the process args:
// "task-add", "project-create", "close", or the bare top-level command.
func commandName() string {
	if len(os.Args) < 2 {
		return "fs"
	}
	name := os.Args[1]
	if (name == "task" || name == "project") && len(os.Args) > 2 && !strings.HasPrefix(os.Args[2], "-") {
		name += "-" + os.Args[2]
	}
	return name
}

// projectOfAddress reads the project half of a `PROJECT:NODE` address, the form
// `close --worker P:N`, `report`'s positional and every other address take. A
// colon never appears in a node id, so reading one as a qualifier is safe — the
// rule nodeArg applies. A value with no colon names no project.
func projectOfAddress(address string) string {
	project, _, ok := strings.Cut(address, ":")
	if !ok {
		return ""
	}
	return project
}

// addressFlagAnywhere reports whether some subcommand declares flag an address —
// whether the CLI gives that flag `PROJECT:NODE` meaning anywhere. It is what a
// glued token is read by, since checkArgs refuses every `--flag=value` form as an
// unknown flag and no command's own declaration ever applies to one — except
// `--node`, which AC4 exempts and invocationProject gates on the command's own
// declaration instead. The gate is this vocabulary rather than the token's shape
// alone, because a flag that is no command's address is not one here either.
func addressFlagAnywhere(flag string) bool {
	for _, spec := range cmdSpecs {
		if spec.addressFlags[flag] {
			return true
		}
	}
	return false
}

// invocationProject is the project the invocation named, for a refusal line: the
// value of `--project`, the address a PROJECT:NODE value carries — `close
// --worker P:N`, or `log --node P:N` — or the first positional of a command
// whose positional *is* an address.
//
// Both address forms are declared in the command's own spec, per argument: the
// positional as `address`, the flag as `addressFlags`, the one nodeArg splits.
// A flag's value is an address only where its command declares it one, so
// `close --node P:N` names no project — that --node is an exact id close
// resolves in the cap scope, and the colon is part of the id. That declaration
// answers for the *value* a flag was handed, and the glued form is the other
// question, answered in the last paragraph.
//
// When the flag and the address name two projects that agree, or only one of
// them names a project at all, that is the project the line carries. When they
// disagree the command is refused as a conflict — one of the two beliefs is
// stale, and the refusal resolves the node and says which — and the line names
// neither: the caller named two, so carrying one would be the guess AC4 forbids
// and would contradict the reason printed beside it.
//
// The arguments are walked the way checkArgs walks them, from the same spec: a
// flag's value is consumed as a value, so one that merely reads like a flag names
// nothing — `fs report P:N --line --project skills` names P, not `skills`. What
// it returns is what the *caller* named and never a derived one, because a
// refusal fires before anything is resolved and cannot know more than its caller
// did; empty means the line carries no project key at all rather than a guessed
// one.
//
// A bare node names no project, and the walk does not stop there: the positional
// and `--worker` forms carry on looking, so `fs task get t-nope --project skills`
// names skills — the flag is read wherever it sits. An empty `--project` does end
// it, because the first `--project` is the one flagVal reads and the later ones
// are already spoken for. The glued `--project=P` names P too, even though
// checkArgs refuses the form outright: the token is flag-shaped and carries the
// name, so it is the caller naming one, where a *value* that merely reads like a
// flag is not.
//
// A glued address flag is that same question put to a different read: `--worker=P:N`
// names P on log as readily as on close, because checkArgs refuses every
// `--flag=value` form as an unknown flag and no command ever resolves one, so the
// command's declaration is not what a refusal has to go on — the caller wrote a
// flag-shaped token carrying the name. The gate is the CLI's own vocabulary, the
// flags some subcommand declares in `addressFlags`, and not the token's shape
// alone: `--goal=fix: this` is flag-shaped too, and reading it as a name would be
// the guessed project AC4 forbids where the caller named none. `--node` is the
// one flag that vocabulary does not reach: AC4 exempts it in both forms (`:60`,
// `:116`), because close's is an exact node id rather than an address and a glued
// token is refused before any command could tell close's from log's, so this
// command's declaration answers for the glued spelling exactly as it does for the
// spaced one.
func invocationProject() string {
	name := strings.Replace(commandName(), "-", " ", 1)
	spec, known := cmdSpecs[name]
	if !known {
		return ""
	}
	args := os.Args[1+len(strings.Fields(name)):]
	count := 0
	address := ""
	flagProject := ""
	flagSeen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			count++
			if count == 1 && spec.address && address == "" {
				address = projectOfAddress(arg)
			}
			continue
		}

		flag, value, inline := strings.Cut(arg, "=")
		addressFlag := spec.addressFlags[flag]
		if !inline {
			// A flag's value is not a flag: consume it the way checkArgs does,
			// whether or not it is this one that names a project.
			if takesValue := spec.flags[flag] || flag == "--project" || addressFlag; !takesValue || i+1 >= len(args) {
				continue
			}
			value, i = args[i+1], i+1
		}
		// The spaced value is the command's to interpret, so its own declaration
		// decides; the glued token is refused before any command resolves it, so
		// the CLI's vocabulary decides instead — except `--node`, which AC4 exempts
		// from that reading in both forms (`fs-logging.md:60`, `:116`): close's
		// `--node` is an exact node id rather than an address so it does not split,
		// and a glued token never reaches close to be told apart from log's, so
		// this command's declaration answers for it instead — the rule the spaced
		// form already takes, which is what makes one flag's two spellings agree.
		addressToken := inline && addressFlagAnywhere(flag) && (flag != "--node" || addressFlag)
		switch {
		case flag == "--project":
			if !flagSeen {
				flagProject, flagSeen = value, true
			}
		case addressFlag, addressToken:
			if address == "" {
				address = projectOfAddress(value)
			}
		}
	}
	if flagSeen {
		if flagProject != "" && address != "" && flagProject != address {
			return ""
		}
		return flagProject
	}
	return address
}

// logRefusal records a refused command: a WARN line naming the command, the
// project the invocation named and the reason, so a refusal is visible in the
// running log even when its envelope is the only thing on stdout. It is
// best-effort and never fails anything; it is a no-op before the logger is built
// (an unparseable FS_LOG_LEVEL refuses first).
func logRefusal(reason string) {
	if logger == nil {
		return
	}
	attrs := []any{"command", commandName()}
	if project := invocationProject(); project != "" {
		attrs = append(attrs, "project", project)
	}
	logger.Warn("command refused", append(attrs, "reason", reason)...)
}

// output prints a Response as JSON to stdout and exits with appropriate code.
func output(resp command.Response) {
	if !resp.OK {
		logRefusal(resp.Error)
	}
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
// this: it consumes the next argument, which a trailing --deliver does not have.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// multiFlag collects the values of a repeatable flag, in the order given. It is
// the plain sibling of cardsFlag: no comma splitting, because --tag, --also and
// --without each name one thing per occurrence, and a term with a comma in it
// would be silently torn in two.
func multiFlag(args []string, flag string) []string {
	var values []string
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			values = append(values, args[i+1])
		}
	}
	return values
}

// isHelp returns true if the arg is a help flag.
func isHelp(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "help"
}

func fatal(msg string) {
	logRefusal(msg)
	resp := command.Response{OK: false, Error: msg}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
	os.Exit(1)
}

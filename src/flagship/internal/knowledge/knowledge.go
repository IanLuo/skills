// Package knowledge implements the Knowledge Center — YAML read/write/parse
// for playbooks. It is the sole owner of config file schema.
//
// A playbook is a protocol for one participant. It declares the moments it takes
// part in as sections (`cap:`, `entry:`, `work:`, `exit:`), the situations it is
// for (`applies_when`), and where it sits in the order; the planner matches those
// declarations against a dispatch's tags, so a playbook can express a situation
// instead of a name.
package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/flagship-dev/flagship/internal/query"
)

// Moments: when a playbook's steps run. The moment is a section, not a field —
// one file is one participant, and the moment decides when its steps run, never
// its position in the file.
const (
	MomentCap   = "cap"   // the cap's standing rules, in the session prompt
	MomentEntry = "entry" // before the worker starts
	MomentWork  = "work"  // the worker's brief
	MomentExit  = "exit"  // at close
)

// canonicalMoments is the fixed order moments run in, and the order a playbook's
// steps are numbered in. A playbook serving two moments carries one `order` used
// in both, and the step indexes never mix them because this order never changes.
var canonicalMoments = []string{MomentCap, MomentEntry, MomentWork, MomentExit}

// Step kinds. A step declares what it is, so nothing has to guess from its
// wording: check runs a shell command and reports pass/fail, do runs a shell
// command as an action, ask is for the cap to confirm, say is guidance for the
// reader, use is a skill hint, and include splices another playbook's steps in
// place.
const (
	KindCheck   = "check"
	KindAsk     = "ask"
	KindSay     = "say"
	KindDo      = "do"
	KindUse     = "use"
	KindInclude = "include"
)

// AllKinds is the closed step vocabulary, in the order errors list it.
var AllKinds = []string{KindSay, KindUse, KindCheck, KindAsk, KindDo, KindInclude}

// allowedKinds is the closed step vocabulary per moment. A gate may assert or
// ask; only exit may act, because a teardown is an action and nothing else can
// verify what it achieved. `include` is allowed in every moment — it is resolved
// away before validation, and what it splices must itself be legal for the
// including section's moment.
var allowedKinds = map[string][]string{
	MomentCap:   {KindSay},
	MomentEntry: {KindCheck, KindAsk, KindSay},
	MomentWork:  {KindSay, KindUse},
	MomentExit:  {KindCheck, KindAsk, KindDo, KindSay},
}

// defaultOrder is where a playbook sits when it does not say. Well below the
// shipped orders, so an unnumbered playbook runs after them.
const defaultOrder = 500

// maxIncludeDepth bounds nesting, so a chain that is not a cycle still cannot
// recurse without end.
const maxIncludeDepth = 8

// Step is one playbook step: a kind plus its body.
type Step struct {
	Kind string `json:"kind" yaml:"kind"`
	Body string `json:"body" yaml:"body"`
}

// Section is one moment's steps, in the order the file spells them. A playbook
// declares one section per moment it takes part in; a moment declared more than
// once contributes its steps in file order via StepsFor.
type Section struct {
	Moment string
	Steps  []Step
}

// Playbook is the structured config entity: a named protocol for one
// participant, the moments it takes part in, the situations it is for, where it
// sits in the order, how the work is realized (engine, agent and the agent's own
// arguments), and its sections.
type Playbook struct {
	Name        string   `json:"name" yaml:"name"`
	AppliesWhen []string `json:"-" yaml:"-"`
	Order       int      `json:"order" yaml:"order"`
	Engine      string   `json:"engine,omitempty" yaml:"engine"`
	Agent       string   `json:"agent,omitempty" yaml:"agent"`
	// AgentArgs are the arguments the agent is started with, one argv element
	// per term, written comma-separated on the field's own line:
	//
	//	agent_args: --permission-mode, auto
	//
	// They are the agent's own command line rather than a name, so nothing here
	// validates their content — `--add-dir /tmp/x` and a term holding a space are
	// both legal, the second being why the separator is a comma. See
	// parsePlaybook's agent_args case for why this is deliberately not
	// validRealizationToken.
	AgentArgs []string `json:"-" yaml:"-"`
	// Creates names the node kind a dispatch of this playbook's situation
	// leaves behind. It is the playbook's declaration of what the node is, so
	// no Go map from task type to kind exists. Empty means the standing
	// default, dispatch; the consumer resolves it rather than storing a
	// default here.
	Creates string `json:"creates,omitempty" yaml:"creates"`
	// Sections holds one entry per declared moment, in file order.
	Sections []Section `json:"-"`
}

// StepsFor returns the steps of one moment's section — every occurrence of that
// moment in the file, in file order — or nil when the playbook does not serve it.
func (pb *Playbook) StepsFor(moment string) []Step {
	var steps []Step
	for _, section := range pb.Sections {
		if section.Moment == moment {
			steps = append(steps, section.Steps...)
		}
	}
	return steps
}

// Moments returns the moments the playbook serves, in canonical order. A moment
// with no steps is not served, so an empty section does not claim a moment.
func (pb *Playbook) Moments() []string {
	var moments []string
	for _, moment := range canonicalMoments {
		if len(pb.StepsFor(moment)) > 0 {
			moments = append(moments, moment)
		}
	}
	return moments
}

// Serves reports whether the playbook has steps for one moment.
func (pb *Playbook) Serves(moment string) bool {
	return len(pb.StepsFor(moment)) > 0
}

// IndexedStep is one step of a resolved playbook, with the moment it runs in and
// its 1-based index within the playbook's flattened step list — the index the
// trace's step id carries, so two moments of one playbook never collide.
type IndexedStep struct {
	Moment string
	Index  int
	Step   Step
}

// IndexedSteps flattens a playbook's sections in canonical moment order,
// numbering steps 1-based across the whole playbook.
func (pb *Playbook) IndexedSteps() []IndexedStep {
	var out []IndexedStep
	index := 0
	for _, moment := range canonicalMoments {
		for _, step := range pb.StepsFor(moment) {
			index++
			out = append(out, IndexedStep{Moment: moment, Index: index, Step: step})
		}
	}
	return out
}

// addStep appends one step to a moment's section, creating it when the moment
// has not been declared yet.
func (pb *Playbook) addStep(moment string, step Step) {
	for i := range pb.Sections {
		if pb.Sections[i].Moment == moment {
			pb.Sections[i].Steps = append(pb.Sections[i].Steps, step)
			return
		}
	}
	pb.Sections = append(pb.Sections, Section{Moment: moment, Steps: []Step{step}})
}

// Matches reports whether every term of applies_when holds for a tag set. An
// empty applies_when matches every dispatch: a playbook that is always loaded.
// A term is `k` (the tag is present) or `k=v` (it is present with that value);
// there is no negation and no range.
func (pb *Playbook) Matches(tags map[string]any) bool {
	for _, term := range pb.AppliesWhen {
		key, want, hasValue := strings.Cut(term, "=")
		got, present := tags[key]
		if !present {
			return false
		}
		if hasValue && fmt.Sprint(got) != want {
			return false
		}
	}
	return true
}

// AppliesWhenString renders the terms the way a playbook file spells them.
func (pb *Playbook) AppliesWhenString() string {
	return strings.Join(pb.AppliesWhen, ", ")
}

// Center manages playbook files on disk, in two scopes: the global playbooks
// under <fs>/kb, and a project's under <fs>/projects/<project>/kb. A project
// playbook with the same name replaces the global one.
type Center struct {
	fsRoot  string // ~/.fs
	project string // "" when no project scope is in play
}

// Open opens the knowledge center rooted at fsRoot (~/.fs). The global directory
// is created; a project's KB is not, because a project with no KB behaves
// exactly as global-only and absence is never an error.
func Open(fsRoot string) (*Center, error) {
	if err := os.MkdirAll(filepath.Join(fsRoot, "kb"), 0o755); err != nil {
		return nil, fmt.Errorf("knowledge: create dir %s: %w", filepath.Join(fsRoot, "kb"), err)
	}
	return &Center{fsRoot: fsRoot}, nil
}

// InProject returns the same center with a project scope, which takes
// precedence over the global one. It is how a caller that only learns the
// project later — close, reading it off the delivery record — asks for the same
// resolution the dispatch used.
func (c *Center) InProject(project string) *Center {
	return &Center{fsRoot: c.fsRoot, project: project}
}

// GlobalDir is the global playbooks' directory.
func (c *Center) GlobalDir() string { return filepath.Join(c.fsRoot, "kb") }

// ProjectDir is a project's playbooks' directory. It is "" when no project
// scope is in play.
func (c *Center) ProjectDir() string {
	if c.project == "" {
		return ""
	}
	return filepath.Join(c.fsRoot, "projects", c.project, "kb")
}

// Scope names where this center writes: the project's KB when it has one, the
// global KB otherwise.
func (c *Center) Scope() string {
	if c.project == "" {
		return ScopeGlobal
	}
	return c.project
}

// Scopes a playbook can live in, as `fs kb list` names them.
const ScopeGlobal = "global"

// writeDir is the directory a write goes to: the scope this center is bound to.
func (c *Center) writeDir() string {
	if dir := c.ProjectDir(); dir != "" {
		return dir
	}
	return c.GlobalDir()
}

// candidates lists the files a lookup may read, in scope order: project first,
// then global.
func (c *Center) candidates(name string) []string {
	var paths []string
	if dir := c.ProjectDir(); dir != "" {
		paths = append(paths, filepath.Join(dir, name+".yaml"))
	}
	paths = append(paths, filepath.Join(c.GlobalDir(), name+".yaml"))
	return paths
}

// Add writes a new playbook file into the scope this center is bound to.
// Returns error if it already exists there.
func (c *Center) Add(name string, data []byte) error {
	if name == "" {
		return fmt.Errorf("knowledge: name is required")
	}

	pb, err := parsePlaybook(name, data)
	if err != nil {
		return err
	}
	if err := Validate(pb); err != nil {
		return err
	}

	path := filepath.Join(c.writeDir(), name+".yaml")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("knowledge: playbook %q already exists", name)
	}

	if err := os.MkdirAll(c.writeDir(), 0o755); err != nil {
		return fmt.Errorf("knowledge: create dir %s: %w", c.writeDir(), err)
	}
	return os.WriteFile(path, data, 0o644)
}

// Get reads and resolves a playbook by name: project scope first, then global,
// with every `include` spliced into place.
func (c *Center) Get(name string) (*Playbook, error) {
	return c.resolve(name, nil)
}

// Has reports whether a playbook named name exists in either scope. It is how a
// caller tells an absent playbook, which a gate treats as no gate, from one that
// is present but unreadable, which a gate must refuse rather than skip.
func (c *Center) Has(name string) bool {
	if name == "" {
		return false
	}
	for _, path := range c.candidates(name) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// scopedPlaybook is one playbook file: its name, where it is, and which scope
// that is. The scope is carried so a reader can say where a playbook came from
// rather than only what it is called.
type scopedPlaybook struct {
	name  string
	path  string
	scope string
}

// scopedNames lists every playbook file in both scopes, sorted by name. A name
// that exists in the project scope is listed once, as the project's: the
// project playbook replaces the global one, so there is one playbook by that
// name and the global file is not consulted.
func (c *Center) scopedNames() ([]scopedPlaybook, error) {
	seen := map[string]bool{}
	var books []scopedPlaybook

	for _, scope := range []struct{ dir, name string }{
		{c.ProjectDir(), c.Scope()},
		{c.GlobalDir(), ScopeGlobal},
	} {
		if scope.dir == "" {
			continue
		}
		entries, err := os.ReadDir(scope.dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("knowledge: list %s: %w", scope.dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := playbookName(e.Name())
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			books = append(books, scopedPlaybook{
				name:  name,
				path:  filepath.Join(scope.dir, e.Name()),
				scope: scope.name,
			})
		}
	}

	sort.Slice(books, func(i, j int) bool { return books[i].name < books[j].name })
	return books, nil
}

// List returns the sorted name of every playbook in both scopes.
func (c *Center) List() ([]string, error) {
	books, err := c.scopedNames()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(books))
	for _, book := range books {
		names = append(names, book.name)
	}
	return names, nil
}

// playbookName is a file name's playbook name, or "" when the file is not a
// playbook. It is the one definition of which files are playbooks, shared with
// the shipped defaults so a name means the same thing on both sides.
func playbookName(fileName string) string {
	for _, ext := range []string{".yaml", ".yml"} {
		if name, ok := strings.CutSuffix(fileName, ext); ok {
			return name
		}
	}
	return ""
}

// Edit overwrites an existing playbook in the scope this center is bound to.
// Returns error if it doesn't exist there.
func (c *Center) Edit(name string, data []byte) error {
	if name == "" {
		return fmt.Errorf("knowledge: name is required")
	}

	pb, err := parsePlaybook(name, data)
	if err != nil {
		return err
	}
	if err := Validate(pb); err != nil {
		return err
	}

	path := filepath.Join(c.writeDir(), name+".yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("knowledge: playbook %q not found", name)
	}

	return os.WriteFile(path, data, 0o644)
}

// Remove deletes a playbook file from the scope this center is bound to.
func (c *Center) Remove(name string) error {
	if name == "" {
		return fmt.Errorf("knowledge: name is required")
	}

	path := filepath.Join(c.writeDir(), name+".yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("knowledge: playbook %q not found", name)
	}

	return os.Remove(path)
}

// read returns the bytes of the playbook a lookup resolves to, naming the path
// when it is present but unreadable: falling back to the global file would hide
// a broken project playbook.
func (c *Center) read(name string) ([]byte, error) {
	_, data, err := c.readFor(name, nil)
	return data, err
}

// readFor returns the path and bytes of the file a lookup resolves to, skipping
// any candidate already on the include stack so an include sees the playbook
// beneath it rather than itself.
func (c *Center) readFor(name string, stack []string) (string, []byte, error) {
	var seen []string
	for _, path := range c.candidates(name) {
		if _, err := os.Stat(path); err == nil {
			seen = append(seen, path)
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", nil, fmt.Errorf("knowledge: stat %s: %w", path, err)
		}
	}
	if len(seen) == 0 {
		return "", nil, fmt.Errorf("knowledge: playbook %q not found", name)
	}

	for _, path := range seen {
		if slices.Contains(stack, path) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil, fmt.Errorf("knowledge: read %s: %w", path, err)
		}
		return path, data, nil
	}

	return "", nil, fmt.Errorf("knowledge: include cycle: %s", c.chain(stack, name))
}

// chain renders an include stack, plus the name that closed the loop, as the
// names a reader recognises.
func (c *Center) chain(stack []string, closing string) string {
	names := make([]string, 0, len(stack)+1)
	for _, path := range stack {
		names = append(names, playbookName(filepath.Base(path)))
	}
	names = append(names, closing)
	return strings.Join(names, " -> ")
}

// resolve reads a playbook and splices its includes, in the scope order the
// lookup uses. The stack is the chain of files being resolved, so a playbook
// that reaches itself is refused by name rather than recursed into.
func (c *Center) resolve(name string, stack []string) (*Playbook, error) {
	if name == "" {
		return nil, fmt.Errorf("knowledge: name is required")
	}
	if len(stack) >= maxIncludeDepth {
		return nil, fmt.Errorf(
			"knowledge: include depth exceeds %d: %s", maxIncludeDepth, c.chain(stack, name))
	}

	path, data, err := c.readFor(name, stack)
	if err != nil {
		return nil, err
	}

	pb, err := parsePlaybook(name, data)
	if err != nil {
		return nil, err
	}

	for si := range pb.Sections {
		section := &pb.Sections[si]
		steps := make([]Step, 0, len(section.Steps))
		for i, step := range section.Steps {
			if step.Kind != KindInclude {
				steps = append(steps, step)
				continue
			}
			included, err := c.resolve(step.Body, append(stack, path))
			if err != nil {
				return nil, fmt.Errorf("knowledge: %s %s step %d (include: %s): %w", name, section.Moment, i+1, step.Body, err)
			}
			for _, is := range included.IndexedSteps() {
				steps = append(steps, is.Step)
			}
		}
		section.Steps = steps
	}

	// A spliced step has to be legal for the section that spliced it, or the
	// include would put a `do` in a gate that may only assert.
	if err := Validate(pb); err != nil {
		return nil, err
	}
	return pb, nil
}

// parsePlaybook parses YAML-like structured data into a Playbook.
// We use a minimal YAML parser to avoid external dependencies.
// Format:
//
//	name: batch
//	applies_when: type=batch
//	order: 10
//	engine: herdr
//	entry:
//	  - check: test -d .git
//	exit:
//	  - check: fs batch $FS_NODE
//
// The moment is a section, not a field: `cap:`, `entry:`, `work:` and `exit:`
// each hold a list of steps, in any combination and any textual order.
//
// name is the name the file is expected to carry, and is used only to say so in
// a refusal; "" leaves the mismatch to be reported by the caller.
func parsePlaybook(name string, data []byte) (*Playbook, error) {
	pb := &Playbook{Order: defaultOrder}
	lines := strings.Split(string(data), "\n")
	moment := ""
	hasOrder := false
	// lastKey is the field the previous line declared, so a `- ` item that lands
	// outside a section can name what it was written under. emptyArgs records an
	// `agent_args:` that named nothing, refused after the loop rather than here:
	// the block-list form that usually follows it deserves the better message.
	lastKey := ""
	emptyArgs := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if strings.HasPrefix(trimmed, "- ") {
			if moment != "" {
				pb.addStep(moment, parseStep(strings.TrimPrefix(trimmed, "- ")))
				continue
			}
			return nil, blockListError(name, lastKey)
		}
		moment = ""

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		lastKey = key

		switch key {
		case "name":
			pb.Name = val
		case "applies_when":
			pb.AppliesWhen = splitTerms(val)
		case "order":
			if n, err := parseOrder(val); err == nil {
				pb.Order = n
				hasOrder = true
			} else {
				return nil, fmt.Errorf("knowledge: playbook %q has order %q, which is not a number", name, val)
			}
		case "engine":
			if val == "" {
				return nil, fmt.Errorf("knowledge: playbook %q declares an empty `engine:`; engine must be a single non-empty token", name)
			}
			pb.Engine = val
		case "agent":
			if val == "" {
				return nil, fmt.Errorf("knowledge: playbook %q declares an empty `agent:`; agent must be a single non-empty token", name)
			}
			pb.Agent = val
		case "agent_args":
			// Comma-separated on the field's own line, parsed by the same
			// splitTerms `applies_when` uses. Comma rather than whitespace
			// because it keeps a space inside a term: each term is one argv
			// element, never a shell string.
			//
			// Deliberately not validRealizationToken. That validator permits
			// only [A-Za-z0-9_.-] and would reject `--add-dir /tmp/x`,
			// `--settings=a=1` and every token holding a space — most of what
			// an agent's command line is made of. It is also the wrong
			// protection: exec.Command passes an argv slice with no shell, so a
			// term's content is inert, and the only place it matters is the
			// printed line, where shellQuote is the guard. herdr is the encoder
			// and refuses what it cannot encode. The two validators are
			// different on purpose; unifying them would break arguments.
			pb.AgentArgs = splitTerms(val)
			emptyArgs = len(pb.AgentArgs) == 0
		case "creates":
			if val == "" {
				return nil, fmt.Errorf("knowledge: playbook %q declares an empty `creates:`; creates names the node kind and must be a single non-empty token", name)
			}
			pb.Creates = val
		case MomentCap, MomentEntry, MomentWork, MomentExit:
			moment = key
		case "phase", "steps", "type", "trigger", "engine_scope":
			return nil, removedFieldError(name, key, val)
		default:
			// A key the schema does not know used to be ignored, which is how
			// `agent_arg:` became a playbook that read as configured and started
			// an agent with none of the arguments it named.
			return nil, fmt.Errorf(
				"knowledge: playbook %q declares the unknown field `%s`; the schema is: name, applies_when, order, engine, agent, agent_args, creates, and the moment sections cap, entry, work and exit",
				name, key)
		}
	}

	if emptyArgs {
		return nil, fmt.Errorf(
			"knowledge: playbook %q declares an empty `agent_args:`; agent_args names the arguments the agent is started with, so a declaration with no terms claims arguments it does not name",
			name)
	}
	if !hasOrder {
		pb.Order = defaultOrder
	}
	if pb.Name == "" && len(pb.Sections) == 0 {
		return nil, fmt.Errorf("knowledge: playbook %q declares nothing — no name, no sections", name)
	}
	if pb.Name != "" && name != "" && pb.Name != name {
		return nil, fmt.Errorf(
			"knowledge: playbook %s declares name %q; a playbook's name must equal its file's", name, pb.Name)
	}
	return pb, nil
}

// blockListError refuses a `- ` item written outside a moment section. The
// parser is line-oriented with `moment` as its only state, so before this
// refusal such an item went nowhere at all: `agent_args:` written as a YAML
// block list parsed as no arguments, and the playbook read as configured while
// starting an agent with none of them. Lists in this schema are comma-separated
// on the field's own line. key is the field the item was written under, empty
// when the file opened with the list.
func blockListError(name, key string) error {
	under := "before any field"
	if key != "" {
		under = fmt.Sprintf("under `%s`", key)
	}
	return fmt.Errorf(
		"knowledge: playbook %q has a `- ` list item %s; a list is written comma-separated on one line — `agent_args: --permission-mode, auto` — because this parser is line-oriented and drops a block list in silence",
		name, under)
}

// removedFieldError refuses a field this schema replaced, naming the replacement
// so an on-disk playbook from the old schema says how to migrate instead of
// half-working. There is deliberately no path that reads one part-way.
func removedFieldError(name, key, val string) error {
	var replacement string
	switch key {
	case "trigger":
		replacement = fmt.Sprintf("the replacement is the `%s:` section", val)
	case "engine_scope":
		replacement = fmt.Sprintf(
			"the replacement is `engine: %s` — the declaration that now exists; use the term `applies_when: engine=%s` only when the intent is to match an engine rather than declare one", val, val)
	case "type":
		replacement = "the replacement is the section of that moment — `entry:` for a prerequisite, " +
			"`exit:` for a cleanup — plus an `applies_when` term naming the work it matches"
	case "phase":
		replacement = fmt.Sprintf("the replacement is the `%s:` section holding the same list", val)
	case "steps":
		replacement = "the replacement is one section per moment — `cap:`, `entry:`, `work:`, `exit:` — each holding its own list"
	}

	field := key
	if val != "" {
		field = key + ": " + val
	}
	msg := fmt.Sprintf("knowledge: playbook %q carries the removed field `%s`; %s",
		name, field, replacement)
	if name != "" {
		msg += fmt.Sprintf("; run `fs kb remove %s` once its replacement is in place", name)
	}
	return errors.New(msg)
}

// splitTerms splits an applies_when value into its terms, dropping empties so a
// trailing comma is not a term nothing can match.
func splitTerms(val string) []string {
	var terms []string
	for _, term := range strings.Split(val, ",") {
		if term = strings.TrimSpace(term); term != "" {
			terms = append(terms, term)
		}
	}
	return terms
}

func parseOrder(val string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(val, "%d", &n); err != nil {
		return 0, err
	}
	return n, nil
}

// parseStep splits a step line at its first ":". A leading token that names a
// known kind selects it; anything else defaults to say, so a step body may hold
// a colon without being read as a kind.
func parseStep(line string) Step {
	if kind, body, ok := strings.Cut(line, ":"); ok {
		if k := strings.TrimSpace(kind); isKind(k) {
			return Step{Kind: k, Body: strings.TrimSpace(body)}
		}
	}
	return Step{Kind: KindSay, Body: strings.TrimSpace(line)}
}

func isKind(kind string) bool {
	return slices.Contains(AllKinds, kind)
}

// validRealizationToken reports whether s is a single non-empty token — the
// shape an `engine:` or `agent:` value must take. It is a name, not an
// expression, so no whitespace and no `=`.
func validRealizationToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '.', r == '_':
		default:
			return false
		}
	}
	return true
}

// Validate holds a resolved playbook to the schema: a name, known moments, at
// least one step, and only the kinds each moment allows. It runs on the write
// path and wherever a playbook is matched, so a broken playbook is refused
// before it runs half its steps.
func Validate(pb *Playbook) error {
	if pb.Name == "" {
		return fmt.Errorf("knowledge: a playbook must declare a name")
	}
	for _, section := range pb.Sections {
		if _, ok := allowedKinds[section.Moment]; !ok {
			return fmt.Errorf(
				"knowledge: playbook %q has moment %q; moments are: %s",
				pb.Name, section.Moment, strings.Join(canonicalMoments, ", "))
		}
	}
	if len(pb.IndexedSteps()) == 0 {
		return fmt.Errorf("knowledge: playbook %q has no steps; a playbook is a protocol, so it has at least one", pb.Name)
	}

	for _, field := range []struct{ name, val string }{
		{"engine", pb.Engine}, {"agent", pb.Agent},
	} {
		if field.val != "" && !validRealizationToken(field.val) {
			return fmt.Errorf(
				"knowledge: playbook %q declares %s %q; %s must be a single non-empty token",
				pb.Name, field.name, field.val, field.name)
		}
	}

	// `creates` names the node kind a dispatch leaves behind, so it must be one
	// of the kinds the store's closed vocabulary knows. The empty default is
	// resolved by the consumer, not stored here.
	if pb.Creates != "" && !query.ValidKind(query.Kind(pb.Creates)) {
		return fmt.Errorf(
			"knowledge: playbook %q declares creates %q; creates must be one of %s (or omitted for the dispatch default)",
			pb.Name, pb.Creates, strings.Join([]string{string(query.KindWork), string(query.KindDispatch), string(query.KindGap), string(query.KindBatch)}, ", "))
	}

	for _, section := range pb.Sections {
		allowed := allowedKinds[section.Moment]
		for i, step := range section.Steps {
			// `include` is legal in every moment. It is spliced away before
			// anything runs — a resolved playbook never carries one — and what it
			// splices is held to this section's moment by the validation resolve
			// runs afterwards. A file being written, though, still carries the
			// include step, so it is permitted here or no playbook that includes
			// another could be written.
			if step.Kind == KindInclude {
				if strings.TrimSpace(step.Body) == "" {
					return fmt.Errorf(
						"knowledge: %s playbook %q step %d has kind %q with an empty body; an include step names the playbook to splice",
						section.Moment, pb.Name, i+1, step.Kind)
				}
				continue
			}
			if !slices.Contains(allowed, step.Kind) {
				return fmt.Errorf(
					"knowledge: %s playbook %q step %d (%q) has kind %q; allowed kinds: %s",
					section.Moment, pb.Name, i+1, step.Body, step.Kind,
					strings.Join(append(slices.Clone(allowed), KindInclude), ", "))
			}
			// A step is named by its kind, so a use or do with nothing in it reads
			// as guidance or an action and carries neither.
			if (step.Kind == KindUse || step.Kind == KindDo) && strings.TrimSpace(step.Body) == "" {
				return fmt.Errorf(
					"knowledge: %s playbook %q step %d has kind %q with an empty body; a %s step names %s",
					section.Moment, pb.Name, i+1, step.Kind, step.Kind,
					map[string]string{KindUse: "a skill and when to use it", KindDo: "the shell command to run"}[step.Kind])
			}
		}
	}
	return nil
}

// MarshalJSON renders a playbook the way its file spells it, so `fs kb get`
// shows the schema a reader edits and not the resolver's internals.
func (pb *Playbook) MarshalJSON() ([]byte, error) {
	type file struct {
		Name        string `json:"name"`
		AppliesWhen string `json:"applies_when,omitempty"`
		Order       int    `json:"order"`
		Engine      string `json:"engine,omitempty"`
		Agent       string `json:"agent,omitempty"`
		AgentArgs   string `json:"agent_args,omitempty"`
		Creates     string `json:"creates,omitempty"`
		Cap         []Step `json:"cap,omitempty"`
		Entry       []Step `json:"entry,omitempty"`
		Work        []Step `json:"work,omitempty"`
		Exit        []Step `json:"exit,omitempty"`
	}
	return json.Marshal(file{
		Name:        pb.Name,
		AppliesWhen: pb.AppliesWhenString(),
		Order:       pb.Order,
		Engine:      pb.Engine,
		Agent:       pb.Agent,
		AgentArgs:   strings.Join(pb.AgentArgs, ", "),
		Creates:     pb.Creates,
		Cap:         pb.StepsFor(MomentCap),
		Entry:       pb.StepsFor(MomentEntry),
		Work:        pb.StepsFor(MomentWork),
		Exit:        pb.StepsFor(MomentExit),
	})
}

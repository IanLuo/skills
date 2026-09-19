// Shipped defaults: the playbooks that travel inside the binary, the stamp that
// records where a playbook on disk came from, and the comparison between the
// two. knowledge owns the KB file schema, so it owns this too — the embedded
// tree is handed in as an fs.FS, never imported from the package that holds it.
package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// stampPrefix opens the comment line stamped into every seeded playbook. The
// hash it carries is the shipped default's body at seed time, which is what
// lets a user's edit and a change to the shipped default be told apart: body
// against stamp is edited, stamp against the current default is stale.
const stampPrefix = "# fs-default: sha256="

// Seed actions: what seeding did to one playbook.
const (
	ActionCreated = "created" // written, stamped, from the shipped default
	ActionKept    = "kept"    // already present; left untouched
)

// Playbook states, relative to the shipped defaults.
const (
	StateDefault = "default" // seeded and unedited; identical to the shipped default
	StateEdited  = "edited"  // differs from the default it was seeded from
	StateStale   = "stale"   // the shipped default has moved on since it was seeded
	StateLocal   = "local"   // no shipped default with that name
)

// SeedOutcome is what seeding did to one shipped default.
type SeedOutcome struct {
	Name   string `json:"name"`
	Action string `json:"action"` // created | kept
	Path   string `json:"path"`
}

// UsedByNone is the used_by value for a playbook no mechanism selects: it is
// inert, and fs kb list says so rather than leaving it looking shipped.
const UsedByNone = "none"

// PlaybookState is one playbook on disk and how it relates to the shipped
// defaults. Edited and stale are independent — a user's edit and an upstream
// change can both be true — so both are reported, with State as the headline.
// UsedBy names the mechanisms that select it; UsedByNone is the warning that
// nothing does.
type PlaybookState struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Edited bool   `json:"edited"`
	Stale  bool   `json:"stale"`
	UsedBy string `json:"used_by"`
	Path   string `json:"path"`
}

// Seed writes every shipped default in defaults into the center, one file per
// playbook, each stamped with the default's hash.
//
// It is idempotent and never overwrites: an absent playbook is written and
// reported created, an existing one is left byte-for-byte alone and reported
// kept. Leaving an existing file alone is the point — a new binary carrying a
// changed default must not rewrite a contract underneath the agent reading it.
// That change is reported as stale by States, and applied only by Reset.
func (c *Center) Seed(defaults fs.FS) ([]SeedOutcome, error) {
	books, err := defaultBodies(defaults)
	if err != nil {
		return nil, err
	}

	outcomes := make([]SeedOutcome, 0, len(books))
	for _, book := range books {
		path := c.path(book.name)
		outcome := SeedOutcome{Name: book.name, Path: path}

		switch _, err := os.Stat(path); {
		case err == nil:
			outcome.Action = ActionKept
		case errors.Is(err, fs.ErrNotExist):
			if err := os.WriteFile(path, stamped(book.body), 0o644); err != nil {
				return nil, fmt.Errorf("knowledge: seed %s: %w", book.name, err)
			}
			outcome.Action = ActionCreated
		default:
			return nil, fmt.Errorf("knowledge: stat %s: %w", path, err)
		}

		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

// States reports every playbook in the center and how it relates to the shipped
// defaults. State is the headline — stale wins over edited, because a moved
// default is the drift this reports — with the two flags carrying the full
// answer when both hold.
func (c *Center) States(defaults fs.FS) ([]PlaybookState, error) {
	names, err := c.List()
	if err != nil {
		return nil, err
	}

	states := make([]PlaybookState, 0, len(names))
	for _, name := range names {
		state, err := c.state(defaults, name)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

// Diff returns the unified diff from the shipped default named name to the
// playbook on disk, stamp line excluded so a pristine seed diffs empty. A
// playbook with no shipped default has nothing to compare to and is an error.
func (c *Center) Diff(defaults fs.FS, name string) (string, error) {
	def, shipped, err := defaultBody(defaults, name)
	if err != nil {
		return "", err
	}
	if !shipped {
		return "", fmt.Errorf("knowledge: no shipped default named %q to diff against", name)
	}

	content, err := c.read(name)
	if err != nil {
		return "", err
	}

	_, body, _ := splitStamp(content)
	return unifiedDiff(
		string(def), string(body),
		name+".yaml (shipped default)", name+".yaml (on disk)",
	), nil
}

// Reset writes the shipped default named name over the playbook on disk,
// stamped as freshly seeded. It is unconditional — asking the user first is the
// caller's job, because only the caller knows whether they meant it.
func (c *Center) Reset(defaults fs.FS, name string) (string, error) {
	def, shipped, err := defaultBody(defaults, name)
	if err != nil {
		return "", err
	}
	if !shipped {
		return "", fmt.Errorf("knowledge: no shipped default named %q to reset to", name)
	}

	path := c.path(name)
	if err := os.WriteFile(path, stamped(def), 0o644); err != nil {
		return "", fmt.Errorf("knowledge: reset %s: %w", name, err)
	}
	return path, nil
}

// state compares one playbook on disk to the shipped default of the same name.
func (c *Center) state(defaults fs.FS, name string) (PlaybookState, error) {
	content, err := c.read(name)
	if err != nil {
		return PlaybookState{}, err
	}

	def, shipped, err := defaultBody(defaults, name)
	if err != nil {
		return PlaybookState{}, err
	}

	state := PlaybookState{Name: name, Path: c.path(name), UsedBy: usedByFor(name, content)}
	if !shipped {
		state.State = StateLocal
		return state, nil
	}

	stampHash, body, stamped := splitStamp(content)
	switch {
	case stamped && hashBody(body) != stampHash:
		state.Edited = true
	case !stamped && hashBody(body) != hashBody(def):
		state.Edited = true // hand-written, and not the shipped default
	}
	state.Stale = stamped && stampHash != hashBody(def)

	switch {
	case state.Stale:
		state.State = StateStale
	case state.Edited:
		state.State = StateEdited
	default:
		state.State = StateDefault
	}
	return state, nil
}

// usedByFor names every mechanism that selects a playbook, derived from the
// playbook itself. Its order is the order the mechanisms are asked for:
// dispatch, then close, then prompt. An empty result is the answer "nothing
// selects it" — the playbook is inert.
//
// A file that does not parse is reported the same way rather than failing the
// whole list: a malformed playbook declares nothing, so nothing selects it, and
// fs kb get is where the parse error surfaces.
func usedByFor(name string, content []byte) string {
	pb, err := parsePlaybook(content)
	if err != nil {
		return UsedByNone
	}

	var selectors []string
	if taskType, ok := strings.CutSuffix(name, prerequisiteSuffix); ok && taskType != "" && pb.TriggerAgrees(taskType) {
		selectors = append(selectors, "dispatch:"+taskType)
	}
	if taskType, ok := strings.CutSuffix(name, cleanupSuffix); ok && taskType != "" && pb.TriggerAgrees(taskType) {
		selectors = append(selectors, "close:"+taskType)
	}
	if pb.Type == "procedure" && pb.Trigger == capTrigger {
		selectors = append(selectors, "prompt:"+capTrigger)
	}
	if len(selectors) == 0 {
		return UsedByNone
	}
	return strings.Join(selectors, ", ")
}

// read returns one playbook's bytes, naming the file when it is absent.
func (c *Center) read(name string) ([]byte, error) {
	path := c.path(name)
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("knowledge: playbook %q not found", name)
		}
		return nil, fmt.Errorf("knowledge: read %s: %w", path, err)
	}
	return content, nil
}

// defaultPlaybook is one shipped default read out of the embedded tree.
type defaultPlaybook struct {
	name string
	body []byte
}

// defaultBodies reads every playbook shipped in defaults, sorted by name so
// seeding is deterministic.
func defaultBodies(defaults fs.FS) ([]defaultPlaybook, error) {
	entries, err := fs.ReadDir(defaults, ".")
	if err != nil {
		return nil, fmt.Errorf("knowledge: read shipped defaults: %w", err)
	}

	books := make([]defaultPlaybook, 0, len(entries))
	for _, entry := range entries {
		name := playbookName(entry.Name())
		if entry.IsDir() || name == "" {
			continue
		}
		body, err := fs.ReadFile(defaults, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("knowledge: read shipped default %s: %w", entry.Name(), err)
		}
		books = append(books, defaultPlaybook{name: name, body: body})
	}

	sort.Slice(books, func(i, j int) bool { return books[i].name < books[j].name })
	return books, nil
}

// defaultBody returns the body of the shipped default named name and whether
// there is one.
func defaultBody(defaults fs.FS, name string) ([]byte, bool, error) {
	for _, ext := range []string{".yaml", ".yml"} {
		body, err := fs.ReadFile(defaults, name+ext)
		if err == nil {
			return body, true, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, false, fmt.Errorf("knowledge: read shipped default %s%s: %w", name, ext, err)
		}
	}
	return nil, false, nil
}

// stamped returns a shipped default's body prefixed with its seed stamp.
func stamped(body []byte) []byte {
	return append([]byte(stampPrefix+hashBody(body)+"\n"), body...)
}

// hashBody is the hash a stamp carries: sha256 of a playbook's body.
func hashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// splitStamp returns the hash a playbook's stamp records and the body with the
// stamp line removed. ok is false when there is no stamp — a file this binary
// never seeded, which is compared to the shipped default as it is now.
func splitStamp(content []byte) (hash string, body []byte, ok bool) {
	lines := strings.SplitAfter(string(content), "\n")
	for i, line := range lines {
		recorded, found := strings.CutPrefix(line, stampPrefix)
		if !found {
			continue
		}
		return strings.TrimSpace(recorded),
			[]byte(strings.Join(lines[:i], "") + strings.Join(lines[i+1:], "")),
			true
	}
	return "", content, false
}

// diffLine is one line of a diff: its operator (space for context, - for the
// shipped default, + for what is on disk) and its text.
type diffLine struct {
	op   byte
	text string
}

// unifiedDiff renders oldText → newText as a unified diff in a single hunk.
// Playbooks are a handful of lines, so one hunk is the whole story and hunk
// splitting would only add code. Identical text renders as "".
func unifiedDiff(oldText, newText, oldName, newName string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	lines := diffLines(oldLines, newLines)

	var b strings.Builder
	if !hunkHasChanges(lines) {
		return ""
	}

	fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldName, newName)
	oldStart, oldCount := lineRange(len(oldLines))
	newStart, newCount := lineRange(len(newLines))
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
	for _, line := range lines {
		fmt.Fprintf(&b, "%c%s\n", line.op, line.text)
	}
	return b.String()
}

// diffLines returns oldLines → newLines as an edit script, computed from their
// longest common subsequence so unchanged lines appear once, as context.
func diffLines(oldLines, newLines []string) []diffLine {
	// lcs[i][j] is the length of the longest common subsequence of oldLines[i:]
	// and newLines[j:].
	lcs := make([][]int, len(oldLines)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	lines := make([]diffLine, 0, len(oldLines)+len(newLines))
	for i, j := 0, 0; i < len(oldLines) || j < len(newLines); {
		switch {
		case i == len(oldLines):
			lines = append(lines, diffLine{'+', newLines[j]})
			j++
		case j == len(newLines):
			lines = append(lines, diffLine{'-', oldLines[i]})
			i++
		case oldLines[i] == newLines[j]:
			lines = append(lines, diffLine{' ', oldLines[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			lines = append(lines, diffLine{'-', oldLines[i]})
			i++
		default:
			lines = append(lines, diffLine{'+', newLines[j]})
			j++
		}
	}
	return lines
}

// hunkHasChanges reports whether an edit script holds anything but context.
func hunkHasChanges(lines []diffLine) bool {
	for _, line := range lines {
		if line.op != ' ' {
			return true
		}
	}
	return false
}

// lineRange is a hunk header's start and count, which unified diff gives as
// "0,0" for an empty file and "1,n" otherwise.
func lineRange(n int) (start, count int) {
	if n == 0 {
		return 0, 0
	}
	return 1, n
}

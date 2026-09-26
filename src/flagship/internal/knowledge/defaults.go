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
	"path/filepath"
	"sort"
	"strings"
)

// stampPrefix opens the comment line stamped into every seeded playbook. The
// hash it carries is the shipped default's body at seed time, which is what
// lets a user's edit and a change to the shipped default be told apart: body
// against stamp is edited, stamp against the current default is stale.
const stampPrefix = "# fs-default: sha256="

// Playbook states, relative to the shipped defaults.
const (
	StateShipped = "shipped" // no file on disk; the shipped default is what runs
	StateDefault = "default" // the copy is unedited; it restates the shipped default
	StateEdited  = "edited"  // the copy differs from the default it was stamped from
	StateStale   = "stale"   // the shipped default has moved on since the copy was stamped
	StateLocal   = "local"   // no shipped default with that name
)

// UsedByNone is the used_by value for a playbook nothing can select: it is
// inert, and fs kb list says so rather than leaving it looking shipped.
const UsedByNone = "none"

// PlaybookState is one playbook and what a reader can do with it.
//
// The two halves answer two questions and are deliberately not merged. Scope and
// Edited answer "is what I wrote live": Scope is where the name RESOLVES, so a
// copy that is not an override reads as shipped whatever its own state, and
// Edited is true exactly when the file is the override that runs. Stale answers
// "is my copy behind": it compares the copy's stamp to the shipped default, and
// is true of a copy that no longer participates as well as of one that does.
//
// Edited and stale are independent — a user's edit and an upstream change can
// both be true — so both are reported, with State as the headline. Path is the
// copy on disk, "" when there is none; UsedBy says which tags load the playbook
// that runs, and UsedByNone is the warning that nothing does.
type PlaybookState struct {
	Name   string `json:"name"`
	Scope  string `json:"scope"`
	State  string `json:"state"`
	Edited bool   `json:"edited"`
	Stale  bool   `json:"stale"`
	UsedBy string `json:"used_by"`
	Path   string `json:"path"`
}

// States reports every playbook this center can resolve — every name the binary
// ships and every file in the two on-disk scopes — with the scope each one
// resolves from, the condition of the copy on disk, and which tags load it.
//
// It is the answer to "what is actually running", which the directory listing
// cannot give: a copy nothing changed is not what a dispatch reads, and a change
// that landed only in the binary is live without anyone having seeded it.
func (c *Center) States() ([]PlaybookState, error) {
	names, err := c.names()
	if err != nil {
		return nil, err
	}

	states := make([]PlaybookState, 0, len(names))
	for _, name := range names {
		state, err := c.state(name)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

// Diff returns the unified diff from the shipped default named name to the copy
// on disk, stamp line excluded so a copy that restates the default diffs empty.
//
// It reads the copy and not what resolution picks, because the two differ
// exactly when it matters: a copy that has fallen behind the default does not
// participate, so a diff taken through resolution would come out empty for the
// one file a reader runs this command to look at. A playbook with no shipped
// default has nothing to compare to, and one with no copy has nothing to
// compare; both are errors rather than an empty diff that reads as "unchanged".
func (c *Center) Diff(name string) (string, error) {
	def, shipped, err := c.shippedBody(name)
	if err != nil {
		return "", err
	}
	if !shipped {
		return "", fmt.Errorf("knowledge: no shipped default named %q to diff against", name)
	}

	path := c.copyPath(name)
	if path == "" {
		return "", fmt.Errorf(
			"knowledge: playbook %q has no copy on disk to diff; run `fs kb reset %s` to write one", name, name)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("knowledge: read %s: %w", path, err)
	}

	_, body, _ := splitStamp(content)
	return unifiedDiff(
		string(def), string(body),
		name+".yaml (shipped default)", name+".yaml (on disk)",
	), nil
}

// Reset writes the shipped default named name to the playbook path in the scope
// this center is bound to, stamped as freshly written — the one command that
// materialises a default on disk, for a reader who wants a copy to edit rather
// than one to run. It creates the scope's directory, so it is how a scope that
// has no KB yet asks for its first playbook.
//
// It is unconditional over what is there: asking the user first is the caller's
// job, because only the caller knows whether they meant it.
func (c *Center) Reset(name string) (string, error) {
	def, shipped, err := c.shippedBody(name)
	if err != nil {
		return "", err
	}
	if !shipped {
		return "", fmt.Errorf("knowledge: no shipped default named %q to reset to", name)
	}

	path := filepath.Join(c.writeDir(), name+".yaml")
	if err := os.MkdirAll(c.writeDir(), 0o755); err != nil {
		return "", fmt.Errorf("knowledge: create dir %s: %w", c.writeDir(), err)
	}
	if err := os.WriteFile(path, stamped(def), 0o644); err != nil {
		return "", fmt.Errorf("knowledge: reset %s: %w", name, err)
	}
	return path, nil
}

// state describes one playbook by name. Every field is read off what a lookup
// does rather than off the directory: Scope is where the name resolves, Path is
// the copy on disk and may be a file that does not participate, and UsedBy comes
// from the playbook that actually runs.
func (c *Center) state(name string) (PlaybookState, error) {
	id, err := c.lookup(name, nil)
	if err != nil {
		return PlaybookState{}, err
	}

	state := PlaybookState{
		Name:  name,
		Scope: c.scopeOf(id),
		Path:  c.copyPath(name),
	}
	if pb, err := c.Get(name); err == nil {
		state.UsedBy = usedByFor(pb)
	} else {
		state.UsedBy = UsedByNone
	}

	if state.Path == "" {
		// Nothing on disk: the shipped default is what runs, and there is no copy
		// to be drifted.
		state.State = StateShipped
		return state, nil
	}

	content, err := os.ReadFile(state.Path)
	if err != nil {
		return PlaybookState{}, fmt.Errorf("knowledge: read %s: %w", state.Path, err)
	}
	def, shipped, err := c.shippedBody(name)
	if err != nil {
		return PlaybookState{}, err
	}
	if !shipped {
		state.State = StateLocal
		return state, nil
	}

	// Edited is the same predicate resolution uses, so what this reports and what
	// a dispatch reads cannot disagree.
	state.Edited = edited(def, content)
	stampHash, _, stamped := splitStamp(content)
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

// edited reports whether a playbook's body is an override of the shipped default
// def rather than a copy of it: a body that differs from the stamp it carries,
// or an unstamped body that differs from the default. It is the one definition
// of what makes a file the user's — resolution uses it to decide whether a file
// participates, reporting uses it to mark the ones that are overrides.
func edited(def, content []byte) bool {
	stampHash, body, stamped := splitStamp(content)
	if stamped {
		return hashBody(body) != stampHash
	}
	return hashBody(body) != hashBody(def)
}

// usedByFor says which tags load a playbook, derived from the playbook that
// resolves: a playbook with a cap section is loaded by the session prompt, every
// other moment by a dispatch whose tags satisfy its applies_when. `always` is a
// playbook with no terms — one that loads for every dispatch. The moments are
// named because a playbook serving two of them (a `batch`'s entry and exit) is
// the reason one file is one participant.
//
// It is the resolved playbook and not the file, because a file on disk is no
// longer necessarily what runs: a copy that restates the default is transparent,
// and a shadow that declares only fields is loaded by the tags its inherited
// sections serve.
func usedByFor(pb *Playbook) string {
	tags := "always"
	if len(pb.AppliesWhen) > 0 {
		tags = strings.Join(pb.AppliesWhen, ",")
	}

	var dispatchMoments []string
	for _, moment := range canonicalMoments {
		if moment == MomentCap {
			continue
		}
		if pb.Serves(moment) {
			dispatchMoments = append(dispatchMoments, moment)
		}
	}

	var parts []string
	if pb.Serves(MomentCap) {
		parts = append(parts, "prompt: "+tags)
	}
	if len(dispatchMoments) > 0 {
		parts = append(parts, "dispatch: "+tags+" ("+strings.Join(dispatchMoments, ", ")+")")
	}
	if len(parts) == 0 {
		return UsedByNone
	}
	return strings.Join(parts, "; ")
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

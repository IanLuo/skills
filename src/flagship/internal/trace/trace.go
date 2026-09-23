// Package trace records what a dispatch actually ran.
//
// One JSONL file per dispatch, under ~/.fs/traces/<project>.<node>.jsonl: the
// plan the dispatch was measured against as its first line, then one line per
// step outcome. The file is filed flat; the project leads the name because a
// person browsing the directory should recognise the file, and a dot separates
// the halves because both contain hyphens. The project the dispatch acted on is
// a field on every line as well — the name is for a person, the field for the
// program. It is append-only, never deleted at close, and never transmitted —
// its whole purpose is to outlive the worker's transcript and the pane.
//
// The package reads and writes; it never gates. A gate reads this file and
// decides; trace only refuses to lose a line, because an outcome that cannot be
// recorded cannot be gated.
//
// Honesty: there is no status meaning "the worker obeyed this". `loaded` says a
// playbook was handed over; nothing here can say it was followed.
package trace

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Step outcome statuses. The vocabulary is closed: a status outside this set is
// not written. There is deliberately no "followed" — see the package comment.
const (
	StatusPass      = "pass"      // a check or do step that succeeded
	StatusFail      = "fail"      // a check or do step that failed
	StatusConfirmed = "confirmed" // an ask the cap answered
	StatusLoaded    = "loaded"    // a work playbook handed to the worker
	StatusSkipped   = "skipped"   // a step the close path deliberately did not run
	StatusRefused   = "refused"   // a step the gate would not accept an answer for
)

// KindPlan is the kind of the trace's first line: the plan the dispatch was
// measured against. It is not a step kind.
const KindPlan = "plan"

// KindLoaded is the kind of a work playbook's `loaded` line. It is not a step
// kind either: it records a handover, and its step is 0.
const KindLoaded = "loaded"

// maxDetail is how much of a step body a line carries. A line stays under 1 KiB;
// the step's *output* is never recorded at all, so a check that prints a secret
// cannot write it here.
const maxDetail = 512

// PlanEntry is one playbook in a dispatch's plan: what it is called, the moments
// it serves, and where it sits in the order. One entry can be both an entry and
// an exit participant — `batch` and `member` each are.
type PlanEntry struct {
	Name    string   `json:"name"`
	Moments []string `json:"moments"`
	Order   int      `json:"order"`
}

// Plan is the payload recorded on the dispatch node as a plan-recorded event,
// and the trace's first line: the ordered playbooks, the tags that selected
// them, and the realization the plan resolved — which engine dispatches it and
// which agent runs it, so which harness ran is reconstructible from the record
// alone. Recording the tags is what makes a mis-tag visible afterwards.
type Plan struct {
	Plan   []PlanEntry    `json:"plan"`
	Tags   map[string]any `json:"tags"`
	Engine string         `json:"engine,omitempty"`
	Agent  string         `json:"agent,omitempty"`
}

// Line is one JSONL record. A plan line carries Tags and Plan; a step line
// carries the id, the playbook, the phase, the 1-based step and its status; a
// `loaded` line carries the playbook, phase work, step 0 and status loaded.
type Line struct {
	TS         string         `json:"ts"`
	ID         string         `json:"id,omitempty"`
	Dispatch   string         `json:"dispatch"`
	Project    string         `json:"project,omitempty"`
	Worker     string         `json:"worker,omitempty"`
	Playbook   string         `json:"playbook,omitempty"`
	Phase      string         `json:"phase,omitempty"`
	Step       int            `json:"step,omitempty"`
	Kind       string         `json:"kind"`
	Status     string         `json:"status,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Detail     string         `json:"detail,omitempty"`
	Tags       map[string]any `json:"tags,omitempty"`
	Plan       []PlanEntry    `json:"plan,omitempty"`
	Engine     string         `json:"engine,omitempty"`
	Agent      string         `json:"agent,omitempty"`
}

// StepID is the trackable id of one step: <dispatch>/<playbook>/<step>.
func StepID(dispatch, playbook string, step int) string {
	return fmt.Sprintf("%s/%s/%d", dispatch, playbook, step)
}

// Log is the trace store: one flat directory holding one file per dispatch.
type Log struct {
	root string // ~/.fs/traces
}

// Open returns the trace store rooted at tracesDir, which is ~/.fs/traces.
// Nothing is created until a line is appended: a machine that never dispatches
// keeps no empty directory.
func Open(tracesDir string) *Log { return &Log{root: tracesDir} }

// Path is one dispatch's trace file: <project>.<node>.jsonl. Writing a line
// knows the project, so it constructs the path directly; reading does not, so
// Read locates the file by globbing the node.
func (l *Log) Path(project, node string) string {
	return filepath.Join(l.root, project+"."+node+".jsonl")
}

// Append writes one line to a dispatch's trace. The write is a refusal if it
// fails: an outcome that cannot be recorded cannot be gated.
func (l *Log) Append(project string, ln Line) error {
	if project == "" {
		return errors.New("trace: no project to write a trace for")
	}
	if ln.Dispatch == "" {
		return errors.New("trace: a line must name its dispatch")
	}
	// The project is set here, on the way in, so no call site can forget it: a
	// line without it would be a record that cannot say which tree it describes.
	ln.Project = project
	if ln.TS == "" {
		ln.TS = time.Now().UTC().Format(time.RFC3339)
	}
	ln.Detail = truncate(ln.Detail, maxDetail)

	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return fmt.Errorf("trace: create %s: %w", l.root, err)
	}

	encoded, err := json.Marshal(ln)
	if err != nil {
		return fmt.Errorf("trace: encode line: %w", err)
	}

	path := l.Path(project, ln.Dispatch)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("trace: open %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("trace: write %s: %w", path, err)
	}
	return nil
}

// Read returns every line of a dispatch's trace, in the order written. The file
// is located from the node alone by globbing traces/*.<node>.jsonl: the node no
// longer spells the path, so the glob is the lookup. A trace that was never
// written is an error naming the node rather than an empty timeline: absence is
// not the same as "nothing ran".
func (l *Log) Read(node string) ([]Line, error) {
	path, err := l.resolve(node)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no trace was written for %q", node)
		}
		return nil, fmt.Errorf("trace: read %s: %w", path, err)
	}
	defer f.Close()

	var lines []Line
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var ln Line
		if err := json.Unmarshal([]byte(raw), &ln); err != nil {
			return nil, fmt.Errorf("trace: %s: unreadable line %q: %w", path, truncate(raw, 80), err)
		}
		lines = append(lines, ln)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("trace: read %s: %w", path, err)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("no trace was written for %q", node)
	}
	return lines, nil
}

// resolve locates a dispatch's trace file from the node alone, by globbing
// traces/*.<node>.jsonl. More than one match refuses naming them rather than
// picking one: the node is the key the caller has, and a key that opens two
// files is a collision to report, not a choice to make.
func (l *Log) resolve(node string) (string, error) {
	pattern := filepath.Join(l.root, "*."+node+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("trace: glob %s: %w", pattern, err)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no trace was written for %q", node)
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("trace: node %q matches %d traces (%s) — refusing to pick one", node, len(matches), strings.Join(matches, ", "))
	}
}

// Render is the text view of one dispatch's timeline: one line per record,
// oldest first, ending with the deviation. The deviation is not computed here —
// the caller compares the plan against the playbooks, because only the caller
// can read them — but it is always printed, so a reader never has to guess
// whether the absence of a line means anything. `deviation: none` is an answer;
// a missing last line is not.
func Render(lines []Line, gaps []string) string {
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(renderLine(ln))
		b.WriteByte('\n')
	}
	if len(gaps) == 0 {
		b.WriteString("deviation: none\n")
		return b.String()
	}
	b.WriteString("deviation: " + strings.Join(gaps, "; ") + "\n")
	return b.String()
}

// renderLine is one record, as compactly as it can be read. A step line leads
// with its status because that is the question a reader brings to a trace: what
// happened here.
func renderLine(ln Line) string {
	switch ln.Kind {
	case KindPlan:
		var plan []string
		for _, e := range ln.Plan {
			plan = append(plan, fmt.Sprintf("%s(%s/%d)", e.Name, strings.Join(e.Moments, "+"), e.Order))
		}
		tags := "no tags"
		if len(ln.Tags) > 0 {
			keys := make([]string, 0, len(ln.Tags))
			for k, v := range ln.Tags {
				keys = append(keys, fmt.Sprintf("%s=%v", k, v))
			}
			sort.Strings(keys)
			tags = strings.Join(keys, ",")
		}
		if len(plan) == 0 {
			return fmt.Sprintf("%s  plan       %s  [%s]  %s  plan: empty", ln.TS, ln.Dispatch, tags, realization(ln))
		}
		return fmt.Sprintf("%s  plan       %s  [%s]  %s  %s", ln.TS, ln.Dispatch, tags, realization(ln), strings.Join(plan, " "))
	case KindLoaded:
		return fmt.Sprintf("%s  loaded     %s  %s", ln.TS, ln.ID, ln.Detail)
	default:
		return fmt.Sprintf("%s  %-10s %s  %s  %s%s", ln.TS, ln.Status, ln.ID, ln.Kind, ln.Detail, duration(ln.DurationMS))
	}
}

// duration renders a step's elapsed time, elided when the step did not measure
// one — a loaded line, or a step recorded by a path that does not time it.
func duration(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return fmt.Sprintf("  (%dms)", ms)
}

// realization renders the resolved engine and agent for a plan line, or nothing
// when a record predates the fields.
func realization(ln Line) string {
	if ln.Engine == "" && ln.Agent == "" {
		return ""
	}
	return fmt.Sprintf("engine=%s agent=%s", ln.Engine, ln.Agent)
}

// PlanOf returns the plan a dispatch's trace recorded, and whether the trace
// carries one.
func PlanOf(lines []Line) (Plan, bool) {
	for _, ln := range lines {
		if ln.Kind == KindPlan {
			return Plan{Plan: ln.Plan, Tags: ln.Tags, Engine: ln.Engine, Agent: ln.Agent}, true
		}
	}
	return Plan{}, false
}

// truncate shortens s to at most n bytes without splitting a rune.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// Package trace records what a dispatch actually ran.
//
// One JSONL file per dispatch, under ~/.fs/projects/<project>/logs/<node>.jsonl:
// the plan the dispatch was measured against as its first line, then one line per
// step outcome. It is append-only, never deleted at close, and never transmitted
// — its whole purpose is to outlive the worker's transcript and the pane.
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

// PlanEntry is one playbook in a dispatch's plan: what it is called, which phase
// it runs in, and where it sits in the order.
type PlanEntry struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
	Order int    `json:"order"`
}

// Plan is the payload recorded on the dispatch node as a plan-recorded event,
// and the trace's first line: the ordered playbooks and the tags that selected
// them. Recording the tags is what makes a mis-tag visible afterwards.
type Plan struct {
	Plan []PlanEntry    `json:"plan"`
	Tags map[string]any `json:"tags"`
}

// Line is one JSONL record. A plan line carries Tags and Plan; a step line
// carries the id, the playbook, the phase, the 1-based step and its status; a
// `loaded` line carries the playbook, phase work, step 0 and status loaded.
type Line struct {
	TS         string         `json:"ts"`
	ID         string         `json:"id,omitempty"`
	Dispatch   string         `json:"dispatch"`
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
}

// StepID is the trackable id of one step: <dispatch>/<playbook>/<step>.
func StepID(dispatch, playbook string, step int) string {
	return fmt.Sprintf("%s/%s/%d", dispatch, playbook, step)
}

// Log is the trace store: every project's logs directory under one root.
type Log struct {
	root string // ~/.fs/projects
}

// Open returns the trace store rooted at projectsDir, which is ~/.fs/projects.
// Nothing is created until a line is appended: a machine that never dispatches
// keeps no empty directory, and a project with no logs is simply a project that
// never dispatched.
func Open(projectsDir string) *Log { return &Log{root: projectsDir} }

// Dir is one project's logs directory.
func (l *Log) Dir(project string) string { return filepath.Join(l.root, project, "logs") }

// Path is one dispatch's trace file.
func (l *Log) Path(project, node string) string {
	return filepath.Join(l.Dir(project), node+".jsonl")
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
	if ln.TS == "" {
		ln.TS = time.Now().UTC().Format(time.RFC3339)
	}
	ln.Detail = truncate(ln.Detail, maxDetail)

	if err := os.MkdirAll(l.Dir(project), 0o755); err != nil {
		return fmt.Errorf("trace: create %s: %w", l.Dir(project), err)
	}

	encoded, err := json.Marshal(ln)
	if err != nil {
		return fmt.Errorf("trace: encode line: %w", err)
	}

	f, err := os.OpenFile(l.Path(project, ln.Dispatch), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("trace: open %s: %w", l.Path(project, ln.Dispatch), err)
	}
	defer f.Close()

	if _, err := f.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("trace: write %s: %w", l.Path(project, ln.Dispatch), err)
	}
	return nil
}

// Read returns every line of a dispatch's trace, in the order written. A trace
// that was never written is an error naming the file rather than an empty
// timeline: absence is not the same as "nothing ran".
func (l *Log) Read(project, node string) ([]Line, error) {
	path := l.Path(project, node)
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

// Projects that have a trace for node, in sorted order. It is how `fs trace`
// finds a dispatch's file when the caller did not name its project.
func (l *Log) ProjectsFor(node string) []string {
	entries, err := os.ReadDir(l.root)
	if err != nil {
		return nil
	}

	var projects []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(l.Path(entry.Name(), node)); err == nil {
			projects = append(projects, entry.Name())
		}
	}
	sort.Strings(projects)
	return projects
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
			plan = append(plan, fmt.Sprintf("%s(%s/%d)", e.Name, e.Phase, e.Order))
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
			return fmt.Sprintf("%s  plan       %s  [%s]  plan: empty", ln.TS, ln.Dispatch, tags)
		}
		return fmt.Sprintf("%s  plan       %s  [%s]  %s", ln.TS, ln.Dispatch, tags, strings.Join(plan, " "))
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

// PlanOf returns the plan a dispatch's trace recorded, and whether the trace
// carries one.
func PlanOf(lines []Line) (Plan, bool) {
	for _, ln := range lines {
		if ln.Kind == KindPlan {
			return Plan{Plan: ln.Plan, Tags: ln.Tags}, true
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

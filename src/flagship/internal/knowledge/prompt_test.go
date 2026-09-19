package knowledge_test

// fs kb prompt renders the cap's standing rules from the playbooks that carry
// them: the procedure playbooks with trigger cap.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/knowledge"
)

const (
	herdrYAML = "name: herdr\ntype: procedure\ntrigger: cap\nengine_scope: herdr\n" +
		"steps:\n  - say: a worker runs in its own tab\n"
	otherCapYAML = "name: aaa-other\ntype: procedure\ntrigger: cap\n" +
		"steps:\n  - say: first by name\n"
	workerCapYAML = "name: dev-task\ntype: procedure\ntrigger: dev-task\n" +
		"steps:\n  - say: worker context, not a standing rule\n"
	prereqYAML = "name: dev-task-prerequisites\ntype: prerequisite\ntrigger: dev-task\n" +
		"steps:\n  - check: true\n"
)

// promptFS is a shipped-defaults tree holding one playbook per seeded name.
func promptFS(names ...string) map[string]string {
	all := map[string]string{
		"herdr.yaml":                  herdrYAML,
		"aaa-other.yaml":              otherCapYAML,
		"dev-task.yaml":               workerCapYAML,
		"dev-task-prerequisites.yaml": prereqYAML,
	}
	files := map[string]string{}
	for _, name := range names {
		files[name+".yaml"] = all[name+".yaml"]
	}
	return files
}

// seedPrompt opens a knowledge center seeded from the given shipped playbooks.
func seedPrompt(t *testing.T, names ...string) (*knowledge.Center, string) {
	t.Helper()
	dir := tempKB(t)
	kc, err := knowledge.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	shipped := shippedFS(promptFS(names...))
	if _, err := kc.Seed(shipped); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	return kc, dir
}

// The prompt is the cap's rules, not every playbook: a prerequisite — a gate
// the dispatcher runs — and a worker-triggered procedure are both excluded.
func TestPromptIncludesOnlyCapTriggeredProcedures(t *testing.T) {
	kc, _ := seedPrompt(t, "herdr", "dev-task", "dev-task-prerequisites")
	shipped := shippedFS(promptFS("herdr", "dev-task", "dev-task-prerequisites"))

	prompt, err := kc.Prompt(shipped)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	if !strings.Contains(prompt, "a worker runs in its own tab") {
		t.Errorf("the herdr playbook's rules are missing:\n%s", prompt)
	}
	for _, excluded := range []string{"worker context, not a standing rule", "check: true", "dev-task-prerequisites"} {
		if strings.Contains(prompt, excluded) {
			t.Errorf("prompt must not carry %q:\n%s", excluded, prompt)
		}
	}
}

// Playbooks come out in name order, so two runs of the same KB produce the same
// prompt and a diff of two prompts means something changed.
func TestPromptOrdersPlaybooksByName(t *testing.T) {
	kc, _ := seedPrompt(t, "herdr", "aaa-other")
	shipped := shippedFS(promptFS("herdr", "aaa-other"))

	prompt, err := kc.Prompt(shipped)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	first, second := strings.Index(prompt, "# aaa-other"), strings.Index(prompt, "# herdr")
	if first < 0 || second < 0 {
		t.Fatalf("both playbooks must have a header:\n%s", prompt)
	}
	if first > second {
		t.Errorf("playbooks must be in name order, got aaa-other at %d and herdr at %d:\n%s", first, second, prompt)
	}
}

// The standing-rules line is what tells the cap these are rules rather than
// context, and each header names its playbook and the stamp it was seeded with,
// so a stale prompt is detectable from the prompt alone.
func TestPromptStatesTheRulesAndEachPlaybooksStamp(t *testing.T) {
	kc, _ := seedPrompt(t, "herdr")
	shipped := shippedFS(promptFS("herdr"))

	prompt, err := kc.Prompt(shipped)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	for _, want := range []string{
		"Standing rules for this session",
		"not suggestions",
		"# herdr — procedure, trigger cap; default, stamp sha256:",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt must contain %q:\n%s", want, prompt)
		}
	}
}

// A playbook whose shipped default has moved on reads stale in the prompt, so
// the cap can tell its rules came from a binary that is no longer current.
func TestPromptMarksAStalePlaybook(t *testing.T) {
	kc, dir := seedPrompt(t, "herdr")
	shipped := shippedFS(promptFS("herdr"))
	older := "name: herdr\ntype: procedure\ntrigger: cap\nsteps:\n  - say: older\n"
	writeFile(t, filepath.Join(dir, "herdr.yaml"), stampOf(older)+older)

	prompt, err := kc.Prompt(shipped)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.Contains(prompt, "# herdr — procedure, trigger cap; stale, stamp sha256:") {
		t.Errorf("a stale playbook must be marked stale in the prompt:\n%s", prompt)
	}
}

// The output is piped whole into one --append-system-prompt argument: a blank
// line would split it into separate blocks, so there are none.
func TestPromptHasNoBlankLines(t *testing.T) {
	kc, _ := seedPrompt(t, "herdr", "aaa-other", "dev-task", "dev-task-prerequisites")
	shipped := shippedFS(promptFS("herdr", "aaa-other", "dev-task", "dev-task-prerequisites"))

	prompt, err := kc.Prompt(shipped)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	for i, line := range strings.Split(strings.TrimSuffix(prompt, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			t.Errorf("line %d is blank; the prompt must pipe as one argument:\n%q", i+1, prompt)
		}
	}
}

// An empty knowledge center is not an error: a fresh HOME has no playbooks yet,
// and the prompt says what to run instead of silently becoming empty.
func TestPromptOnAnEmptyCenterSaysToBootstrap(t *testing.T) {
	kc, err := knowledge.Open(tempKB(t))
	if err != nil {
		t.Fatal(err)
	}

	prompt, err := kc.Prompt(shippedFS(promptFS("herdr")))
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.Contains(prompt, "fs bootstrap") {
		t.Errorf("an empty center must say how to fill it:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Standing rules for this session") {
		t.Errorf("the standing-rules line must be there even with no playbooks:\n%s", prompt)
	}
}

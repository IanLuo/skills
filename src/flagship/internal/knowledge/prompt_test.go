package knowledge_test

// fs kb prompt renders the cap's standing rules from the playbooks that carry
// them: the `phase: cap` playbooks whose applies_when the session's tags satisfy.

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/knowledge"
)

const (
	herdrYAML = "name: herdr\nphase: cap\napplies_when: engine=herdr\n" +
		"steps:\n  - say: a worker runs in its own tab\n"
	otherCapYAML = "name: aaa-other\nphase: cap\n" +
		"steps:\n  - say: first by name\n"
	workerYAML = "name: dev-task\nphase: work\napplies_when: dev-task\n" +
		"steps:\n  - say: worker context, not a standing rule\n"
	entryYAML = "name: code-entry\nphase: entry\napplies_when: code\n" +
		"steps:\n  - check: true\n"
)

// promptFS is a shipped-defaults tree holding one playbook per seeded name.
func promptFS(names ...string) map[string]string {
	all := map[string]string{
		"herdr.yaml":      herdrYAML,
		"aaa-other.yaml":  otherCapYAML,
		"dev-task.yaml":   workerYAML,
		"code-entry.yaml": entryYAML,
	}
	files := map[string]string{}
	for _, name := range names {
		files[name+".yaml"] = all[name+".yaml"]
	}
	return files
}

// seedPrompt opens a knowledge center seeded from the given shipped playbooks,
// and hands back the root so a test can break a file on disk.
func seedPrompt(t *testing.T, names ...string) (*knowledge.Center, string, fs.FS) {
	t.Helper()
	fsRoot := tempFS(t)
	kc, err := knowledge.Open(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	shipped := shippedFS(promptFS(names...))
	if _, err := kc.Seed(shipped); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	return kc, fsRoot, shipped
}

// The prompt is the cap's rules, not every playbook: a work playbook and an
// entry gate are both excluded, and so is a cap playbook whose applies_when the
// session's tags do not satisfy.
func TestPromptIncludesOnlyTheCapPlaybooksTheTagsSelect(t *testing.T) {
	kc, _, shipped := seedPrompt(t, "herdr", "aaa-other", "dev-task", "code-entry")

	prompt, err := kc.Prompt(shipped, map[string]any{"engine": "herdr"})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	for _, want := range []string{"a worker runs in its own tab", "first by name"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt must carry %q:\n%s", want, prompt)
		}
	}
	for _, excluded := range []string{"worker context, not a standing rule", "check: true", "code-entry"} {
		if strings.Contains(prompt, excluded) {
			t.Errorf("prompt must not carry %q:\n%s", excluded, prompt)
		}
	}

	// The engine is a fact about the session, so a cap playbook that asks for it
	// is not loaded for a session that does not have it.
	prompt, err = kc.Prompt(shipped, nil)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.Contains(prompt, "first by name") {
		t.Errorf("a cap playbook with no terms loads for every session:\n%s", prompt)
	}
	if strings.Contains(prompt, "a worker runs in its own tab") {
		t.Errorf("a playbook asking for engine=herdr must not load without that tag:\n%s", prompt)
	}
}

// Playbooks come out in name order, so two runs of the same KB produce the same
// prompt and a diff of two prompts means something changed.
func TestPromptOrdersPlaybooksByName(t *testing.T) {
	kc, _, shipped := seedPrompt(t, "herdr", "aaa-other")

	prompt, err := kc.Prompt(shipped, map[string]any{"engine": "herdr"})
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
// context, and each header names its playbook, its phase and the stamp it was
// seeded with, so a stale prompt is detectable from the prompt alone.
func TestPromptStatesTheRulesAndEachPlaybooksStamp(t *testing.T) {
	kc, _, shipped := seedPrompt(t, "herdr")

	prompt, err := kc.Prompt(shipped, map[string]any{"engine": "herdr"})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	for _, want := range []string{
		"Standing rules for this session",
		"not suggestions",
		"# herdr — phase cap; default, stamp sha256:",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt must contain %q:\n%s", want, prompt)
		}
	}
}

// A playbook whose shipped default has moved on reads stale in the prompt, so
// the cap can tell its rules came from a binary that is no longer current.
func TestPromptMarksAStalePlaybook(t *testing.T) {
	kc, fsRoot, shipped := seedPrompt(t, "herdr")
	older := "name: herdr\nphase: cap\napplies_when: engine=herdr\nsteps:\n  - say: older\n"
	writeFile(t, kbPath(fsRoot, "herdr"), stampOf(older)+older)

	prompt, err := kc.Prompt(shipped, map[string]any{"engine": "herdr"})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.Contains(prompt, "# herdr — phase cap; stale, stamp sha256:") {
		t.Errorf("a stale playbook must be marked stale in the prompt:\n%s", prompt)
	}
}

// The output is piped whole into one --append-system-prompt argument: a blank
// line would split it into separate blocks, so there are none.
func TestPromptHasNoBlankLines(t *testing.T) {
	kc, _, shipped := seedPrompt(t, "herdr", "aaa-other", "dev-task", "code-entry")

	prompt, err := kc.Prompt(shipped, map[string]any{"engine": "herdr"})
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
	kc, err := knowledge.Open(tempFS(t))
	if err != nil {
		t.Fatal(err)
	}

	prompt, err := kc.Prompt(shippedFS(promptFS("herdr")), nil)
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

// A file left from the removed schema is refused rather than skipped: seeding
// never deletes, so after an upgrade the old playbooks are still there, and the
// prompt says which one to remove instead of quietly leaving out a rule the cap
// used to get.
func TestPromptRefusesAPlaybookFromTheRemovedSchema(t *testing.T) {
	kc, fsRoot, shipped := seedPrompt(t, "herdr")
	writeFile(t, kbPath(fsRoot, "superseded"),
		"name: superseded\ntype: prerequisite\ntrigger: superseded\nsteps:\n  - check: true\n")

	_, err := kc.Prompt(shipped, map[string]any{"engine": "herdr"})
	if err == nil {
		t.Fatal("a playbook from the removed schema must be refused, not skipped")
	}
	for _, want := range []string{"removed field", "fs kb remove superseded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err, want)
		}
	}
}

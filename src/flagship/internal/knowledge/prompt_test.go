package knowledge_test

// fs kb prompt renders the cap's standing rules from the playbooks that carry
// them: the `cap:` playbooks whose applies_when the session's tags satisfy. The
// rules travel out of the binary, so a fresh home gets them with nothing
// written to disk.

import (
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/knowledge"
)

const (
	herdrYAML = "name: herdr\napplies_when: engine=herdr\n" +
		"cap:\n  - say: a worker runs in its own tab\n"
	otherCapYAML = "name: aaa-other\n" +
		"cap:\n  - say: first by name\n"
	workerYAML = "name: dev-task\napplies_when: dev-task\n" +
		"work:\n  - say: worker context, not a standing rule\n"
	entryYAML = "name: code-entry\napplies_when: code\n" +
		"entry:\n  - check: true\n"
)

// promptFS is a shipped-defaults tree holding one playbook per named playbook.
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

// openPrompt opens a center whose shipped tree holds the given playbooks, and
// hands back the root so a test can write a file on disk beneath them.
func openPrompt(t *testing.T, names ...string) (*knowledge.Center, string) {
	t.Helper()
	fsRoot := tempFS(t)
	return openWith(t, fsRoot, promptFS(names...)), fsRoot
}

// The prompt is the cap's rules, not every playbook: a work playbook and an
// entry gate are both excluded, and so is a cap playbook whose applies_when the
// session's tags do not satisfy.
func TestPromptIncludesOnlyTheCapPlaybooksTheTagsSelect(t *testing.T) {
	kc, _ := openPrompt(t, "herdr", "aaa-other", "dev-task", "code-entry")

	prompt, err := kc.Prompt(map[string]any{"engine": "herdr"})
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
	prompt, err = kc.Prompt(nil)
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
	kc, _ := openPrompt(t, "herdr", "aaa-other")

	prompt, err := kc.Prompt(map[string]any{"engine": "herdr"})
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
// context, and each header names its playbook, its phase and where the rules
// came from, so a rule read out of the binary is distinguishable from one read
// out of a file the user wrote.
func TestPromptStatesTheRulesAndTheScopeTheyCameFrom(t *testing.T) {
	kc, _ := openPrompt(t, "herdr")

	prompt, err := kc.Prompt(map[string]any{"engine": "herdr"})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	for _, want := range []string{
		"Standing rules for this session",
		"not suggestions",
		"# herdr — moments cap; shipped",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt must contain %q:\n%s", want, prompt)
		}
	}
}

// A copy that has fallen behind the default does not run — the binary's default
// does — and the header says both halves: the rules came from the shipped
// default, and the file underneath it is the user's, stale, and carries the
// stamp of the default it was written from. Without that a cap reading its own
// prompt would think its edit was live.
func TestPromptNamesTheRunningScopeAndTheCopyBeneathIt(t *testing.T) {
	kc, fsRoot := openPrompt(t, "herdr")
	older := "name: herdr\napplies_when: engine=herdr\ncap:\n  - say: older\n"
	writeFile(t, kbPath(fsRoot, "herdr"), stampOf(older)+older)

	prompt, err := kc.Prompt(map[string]any{"engine": "herdr"})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.Contains(prompt, "# herdr — moments cap; shipped, stale, stamp sha256:") {
		t.Errorf("a stale copy must be named as one, beneath the scope that runs:\n%s", prompt)
	}
	if !strings.Contains(prompt, "a worker runs in its own tab") {
		t.Errorf("the running steps are the shipped default's:\n%s", prompt)
	}
	if strings.Contains(prompt, "older") {
		t.Errorf("the stale copy's steps do not run and must not be printed:\n%s", prompt)
	}
}

// An override is what runs, and the header names the scope it came from rather
// than the default it shadows.
func TestPromptNamesAnOverrideAsItsOwnScope(t *testing.T) {
	kc, fsRoot := openPrompt(t, "herdr")
	mine := "name: herdr\napplies_when: engine=herdr\ncap:\n  - say: my own rule\n"
	writeFile(t, kbPath(fsRoot, "herdr"), stampOf(herdrYAML)+mine)

	prompt, err := kc.Prompt(map[string]any{"engine": "herdr"})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.Contains(prompt, "# herdr — moments cap; global, edited, stamp sha256:") {
		t.Errorf("an override must be named as the scope it resolves from:\n%s", prompt)
	}
	if !strings.Contains(prompt, "my own rule") || strings.Contains(prompt, "a worker runs in its own tab") {
		t.Errorf("the override's steps are the ones that run:\n%s", prompt)
	}
}

// The output is piped whole into one --append-system-prompt argument: a blank
// line would split it into separate blocks, so there are none.
func TestPromptHasNoBlankLines(t *testing.T) {
	kc, _ := openPrompt(t, "herdr", "aaa-other", "dev-task", "code-entry")

	prompt, err := kc.Prompt(map[string]any{"engine": "herdr"})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	for i, line := range strings.Split(strings.TrimSuffix(prompt, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			t.Errorf("line %d is blank; the prompt must pipe as one argument:\n%q", i+1, prompt)
		}
	}
}

// A session with no matching cap playbook is not an error and not an empty
// prompt: the standing-rules line is there and the file says why nothing
// follows it, rather than coming out blank.
func TestPromptWithNoMatchingCapPlaybookSaysSo(t *testing.T) {
	kc, _ := openPrompt(t, "herdr")

	prompt, err := kc.Prompt(nil)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.Contains(prompt, "No playbook with a `cap:` section matches") {
		t.Errorf("a prompt with no rules must say so:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Standing rules for this session") {
		t.Errorf("the standing-rules line must be there even with no playbooks:\n%s", prompt)
	}
}

// A file left from the removed schema is refused rather than skipped: nothing
// deletes it, so after an upgrade the old playbooks are still there, and the
// prompt says which one to remove instead of quietly leaving out a rule the cap
// used to get.
func TestPromptRefusesAPlaybookFromTheRemovedSchema(t *testing.T) {
	kc, fsRoot := openPrompt(t, "herdr")
	writeFile(t, kbPath(fsRoot, "superseded"),
		"name: superseded\ntype: prerequisite\ntrigger: superseded\nsteps:\n  - check: true\n")

	_, err := kc.Prompt(map[string]any{"engine": "herdr"})
	if err == nil {
		t.Fatal("a playbook from the removed schema must be refused, not skipped")
	}
	for _, want := range []string{"removed field", "fs kb remove superseded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err, want)
		}
	}
}

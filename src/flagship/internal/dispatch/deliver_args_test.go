package dispatch

import (
	"regexp"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/trace"
)

// herdrName is herdr's grammar for a live agent name, spelled as herdr spells
// it, so the test asserts the rule herdr enforces rather than what fs thinks it
// might be.
var herdrName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// The name reaches herdr in the argv, and herdr refuses a name outside its
// grammar with invalid_agent_name — an error that names neither fs nor the limit
// that was hit. So the check is fs's, and the boundary is exact: a type of
// maxTaskType characters is the longest that fits, and one more is refused
// naming what the limit is and why it is not herdr's thirty-two.
func TestValidTaskTypeRefusesATypeTheNameCannotCarry(t *testing.T) {
	node := "t-4dd390d5"
	fits := strings.Repeat("a", maxTaskType)

	got, err := workerAgent(fits, node)
	if err != nil {
		t.Fatalf("workerAgent(%q, %q) = %v, want the longest name that fits", fits, node, err)
	}
	if len(got) != agentNameMax || !herdrName.MatchString(got) {
		t.Errorf("workerAgent(%q, %q) = %q (%d characters), want %d characters matching herdr's grammar",
			fits, node, got, len(got), agentNameMax)
	}

	over := strings.Repeat("a", maxTaskType+1)
	if err := validTaskType(over); err == nil {
		t.Errorf("validTaskType(%q) = nil, want a refusal naming the limit", over)
	}
	if _, err := workerAgent(over, node); err == nil {
		t.Errorf("workerAgent(%q, %q) = nil error, want the builder to refuse the same name", over, node)
	}
}

// The alphabet is herdr's, and the type sits inside the name rather than beside
// it — so a type holding a character herdr's names do not is refused by fs,
// naming what the type may hold, instead of reaching herdr as a name that is
// refused whole.
func TestValidTaskTypeRefusesAnAlphabetHerdrDoesNotTake(t *testing.T) {
	for _, taskType := range []string{"Dev-Task", "dev task", "dev.task", "dev/task", "dév-task"} {
		if err := validTaskType(taskType); err == nil {
			t.Errorf("validTaskType(%q) = nil, want a refusal naming the characters herdr's names take", taskType)
		}
	}

	// Underscores and digits are herdr's, and a type that uses only them is one
	// this refuses nothing about.
	if err := validTaskType("dev_2-task"); err != nil {
		t.Errorf("validTaskType(dev_2-task) = %v, want nil", err)
	}
}

// The node is the other half of the name and it comes from fs itself, so a node
// long enough to leave no room for the type is refused rather than cut — and it
// is a refusal and not a panic: nothing in the builder slices to a computed
// index. The boundary is the length at which the name would reach one character
// past agentNameMax.
func TestWorkerAgentRefusesANodeWithNoRoomLeft(t *testing.T) {
	room := agentNameMax - len("dispatch-dev-task-")

	fits := strings.Repeat("a", room)
	if got, err := workerAgent("dev-task", fits); err != nil || len(got) != agentNameMax {
		t.Errorf("workerAgent(dev-task, %q) = (%q, %v), want a name of exactly %d characters", fits, got, err, agentNameMax)
	}

	over := strings.Repeat("a", room+1)
	if _, err := workerAgent("dev-task", over); err == nil {
		t.Errorf("workerAgent(dev-task, %q) = nil error, want a refusal naming herdr's limit", over)
	}
}

// renderBrief is the text Deliver prompts the worker with, so it is where the
// plan, the obligations, the gate's own output and the entry phase's say steps
// have to appear: the brief's fields say what was resolved, and this is what the
// worker is told. Asserted here rather than beside the Prepare tests because
// renderBrief is unexported, and the external package can reach it only through
// a delivery.
func TestRenderBriefCarriesThePlanTheCheckAndTheSaySteps(t *testing.T) {
	brief := &Brief{
		Goal:      "sample",
		Project:   "skills",
		RootPath:  "/tmp/skills",
		TaskType:  "dev-task",
		CapNodeID: "t-cap",
		Plan: []trace.PlanEntry{
			{Name: "code-entry", Moments: []string{"entry"}, Order: 10},
			{Name: "code-work", Moments: []string{"work"}, Order: 20},
		},
		Obligations: []string{"run the suite before you claim the work is done"},
		Skills:      []string{"tdd"},
		Context:     []string{"read AGENTS.md before editing"},
		Checklist:   []Item{{Body: "echo reworded", Status: "pass", Output: "reworded"}},
	}

	text := renderBrief(brief)
	for _, want := range []string{
		"Your node: (not created yet",
		"- code-entry (entry, order 10)",
		"- code-work (work, order 20)",
		"run the suite before you claim the work is done",
		"- tdd",
		"read AGENTS.md before editing",
		"reworded",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("renderBrief must carry %q, got:\n%s", want, text)
		}
	}
}

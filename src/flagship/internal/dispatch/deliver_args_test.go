package dispatch

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// herdrName is herdr's grammar for a live agent name, spelled as herdr spells
// it, so the test asserts the rule herdr enforces rather than what fs thinks it
// might be.
var herdrName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// The name reaches herdr in the argv, and herdr refuses a name outside its
// grammar with invalid_agent_name — an error that names neither fs nor the limit
// that was hit. The limit arrives sooner than the arithmetic suggests: `dispatch-`
// is nine characters, the worker's node is a `t-` plus eight, so a task type of
// thirteen already overflows the thirty-two. The type is shortened to fit; the
// node is not, because the node is what makes one dispatch's name its own, and a
// bound that trimmed it would trade a loud refusal for a quiet collision.
func TestWorkerAgentFitsHerdrsNameLimit(t *testing.T) {
	node := "t-4dd390d5"
	longType := "research-large-surface-area"

	unbounded := "dispatch-" + longType + "-" + node
	if len(unbounded) <= agentNameMax {
		t.Fatalf("this test needs a task type that overflows herdr's limit; %q is %d characters", unbounded, len(unbounded))
	}

	got := workerAgent(longType, node)
	if !herdrName.MatchString(got) {
		t.Errorf("workerAgent(%q, %q) = %q, which herdr refuses", longType, node, got)
	}
	if !strings.HasSuffix(got, "-"+node) {
		t.Errorf("workerAgent(%q, %q) = %q, want the node kept whole", longType, node, got)
	}
	if len(got) > agentNameMax {
		t.Errorf("workerAgent(%q, %q) = %q (%d characters), want at most %d", longType, node, got, len(got), agentNameMax)
	}

	// A name that already fits is left exactly as it is: the shortening is a
	// fallback for a type that overflows, not a rewrite of every name.
	if short := workerAgent("dev-task", node); short != "dispatch-dev-task-"+node {
		t.Errorf("workerAgent(dev-task, %q) = %q, want it unchanged", node, short)
	}
}

// The argv herdr is given, asserted separately from the string nextCommand
// prints. They are two emitters of one command and only one of them is a code
// path, so a flag added to one and not the other leaves a worker started
// correctly by `fs dispatch --deliver` and a copy-pasted line that still stalls
// — with nothing failing, because both are valid on their own.
//
// This test is in the package rather than beside the external ones because
// agentStartArgs is unexported: what a caller sees of it is the effect, and the
// argv shape is the thing under test.
func TestAgentStartArgsPutsTheSeparatorLastOnlyWhenThereAreArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "no arguments",
			want: []string{"agent", "start", "dispatch-dev-task", "--kind", "claude", "--pane", "w1:p9"},
		},
		{
			name: "arguments across the separator",
			args: []string{"--permission-mode", "auto"},
			want: []string{
				"agent", "start", "dispatch-dev-task", "--kind", "claude", "--pane", "w1:p9",
				"--", "--permission-mode", "auto",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := agentStartArgs("dispatch-dev-task", "claude", "w1:p9", tc.args)
			if !slices.Equal(got, tc.want) {
				t.Errorf("agentStartArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

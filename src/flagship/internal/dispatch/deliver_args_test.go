package dispatch

import (
	"slices"
	"testing"
)

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

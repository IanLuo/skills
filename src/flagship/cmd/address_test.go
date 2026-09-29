package main

import (
	"strings"
	"testing"
)

// fs log --node is one of the two paths this card exists for: it handed the
// reference to the reader raw, so "<project>:<node>" matched no node_id and the
// answer was zero events and exit 0 — a typo, a node in another scope and a node
// with no history all read alike. These pin the wiring at the boundary, which is
// where the flag becomes an address: --node goes through the same nodeArg the
// positionals do, so the address supplies the scope when --project did not, and
// an explicit --project beats it.
//
// None of these needs a registered project: the scope an address names is all
// the command resolves by, and a store with no events answers every one of them
// without one.
func TestLogNodeTakesTheAddressForm(t *testing.T) {
	bin := getFS(t)

	for _, tc := range []struct {
		name string
		args []string
		want []string // every one of these must appear in the refusal
	}{
		{
			// --project skills and an address naming cap is the caller saying two
			// scopes for one read, and it is refused naming both rather than
			// answered by either.
			name: "an address naming another scope than --project",
			args: []string{"log", "--project", "skills", "--node", "cap:t-00000000"},
			want: []string{"skills", "cap"},
		},
		{
			// The address supplies the scope when --project did not: this reads
			// skills, and says so, rather than falling back to the cwd.
			name: "a qualified address with no --project",
			args: []string{"log", "--node", "skills:t-00000000"},
			want: []string{"t-00000000", "skills"},
		},
		{
			// An id that resolves to nothing refuses. Exit 0 with no events is the
			// answer a reader cannot act on.
			name: "a bare id that names no node",
			args: []string{"log", "--project", "skills", "--node", "t-00000000"},
			want: []string{"t-00000000", "skills"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runFSCapture(t, bin, t.TempDir(), testHome(t), nil, tc.args...)
			if code != 1 {
				t.Fatalf("exit %d, want 1 (refused):\n%s%s", code, stdout, stderr)
			}
			reason, _ := refusalLine(t, stderr)["reason"].(string)
			for _, want := range tc.want {
				if !strings.Contains(reason, want) {
					t.Errorf("refusal %s = %q, want it to name %q", tc.args, reason, want)
				}
			}
		})
	}
}

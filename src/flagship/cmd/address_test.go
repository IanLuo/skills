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

// An address with no node in it is refused on both paths, with one reason. The
// qualifier made "cap:" look like an address, and the flag made it look like a
// node — and the two readings landed in different places: the scope was read
// whole and exited 0 when --project was absent, and refused when it was present.
// A question nobody asked gets an answer either way, which is worse than the
// empty read this form was made to remove; what it must not do is one of those
// on one path and the other on another.
func TestLogNodeWithNoNodeRefusesTheSameWayWithAndWithoutProject(t *testing.T) {
	bin := getFS(t)

	reasons := make([]string, 0, 2)
	for _, args := range [][]string{
		{"log", "--node", "cap:"},
		{"log", "--project", "cap", "--node", "cap:"},
	} {
		// A working directory that resolves to no project: the scope can only come
		// from the address, so a path that never reaches the resolver is caught
		// rather than answered by whatever directory the test ran in.
		stdout, stderr, code := runFSCapture(t, bin, t.TempDir(), testHome(t), nil, args...)
		if code != 1 {
			t.Fatalf("%v: exit %d, want 1 (refused):\n%s%s", args, code, stdout, stderr)
		}
		reason, _ := refusalLine(t, stderr)["reason"].(string)
		if !strings.Contains(reason, "names no node") {
			t.Errorf("%v: reason = %q, want it to say the address names no node", args, reason)
		}
		reasons = append(reasons, reason)
	}
	if reasons[0] != reasons[1] {
		t.Errorf("one address, two answers: %q without --project, %q with", reasons[0], reasons[1])
	}
}

// An address whose project half is empty names a node and no scope — `--node
// :t-x`. It is not a second belief about the scope for the conflict guard to
// weigh against --project: the flag supplies the scope, and only the node half
// is addressed, exactly as a bare id would be. Reading the blank qualifier as a
// disagreement refused the invocation, and from a directory that resolves to no
// project it refused with "no project is registered" to a caller who had just
// passed --project — the worse half, because the refusal named the one thing the
// caller had already supplied.
func TestLogNodeAddressWithoutAProjectResolvesByTheFlag(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)
	dir := t.TempDir()

	// A node in scope alpha, created without registering a project: the store is
	// keyed by the scope its events carry, not by the registry.
	resp, code := runFS(t, bin, dir, "task", "add", "--project", "alpha", "--goal", "x", "--kind", "work")
	if code != 0 {
		t.Fatalf("task add exit %d: %v", code, resp)
	}
	node, _ := resp["data"].(map[string]any)["node_id"].(string)
	if node == "" {
		t.Fatalf("no node created in alpha: %v", resp)
	}

	// The flag names the scope the node is in, so the read resolves by it and
	// returns the node's events rather than a refusal about a blank name.
	resolved, code := runFS(t, bin, dir, "log", "--node", ":"+node, "--project", "alpha")
	if code != 0 {
		t.Fatalf("log --node :%s --project alpha exit %d, want 0 (resolved by the flag): %v",
			node, code, resolved)
	}
	events, _ := resolved["data"].(map[string]any)["events"].([]any)
	if len(events) == 0 {
		t.Errorf("log resolved by the flag but read no events: %v", resolved)
	}

	// With --project naming a scope that does not hold the node, the answer is
	// about the node — not "no project is registered" for a flag that was passed,
	// and not a conflict over the blank project half.
	stdout, stderr, code := runFSCapture(t, bin, t.TempDir(), home, nil,
		"log", "--node", ":"+node, "--project", "bravo")
	if code != 1 {
		t.Fatalf("log from an unregistered cwd exit %d, want 1:\n%s%s", code, stdout, stderr)
	}
	reason, _ := refusalLine(t, stderr)["reason"].(string)
	if strings.Contains(reason, "no project is registered") {
		t.Errorf("a caller who passed --project was told to pass --project: %q", reason)
	}
	if strings.Contains(reason, "the address says") {
		t.Errorf("an address naming no project was read as a conflict: %q", reason)
	}
	for _, want := range []string{node, "bravo"} {
		if !strings.Contains(reason, want) {
			t.Errorf("refusal %q does not name %q", reason, want)
		}
	}
}

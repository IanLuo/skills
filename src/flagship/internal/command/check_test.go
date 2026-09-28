// The re-run's own contract: what the shell a check runs in is given, and what
// a check that does not finish reads as. Both are about RunShell, which the
// reader path and the gates share.
package command_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/command"
	"github.com/flagship-dev/flagship/internal/query"
)

// A gap whose check addresses the binary the documented way — "$FS_BIN", since
// fs is not on PATH — must read as expired once the condition is over. This is
// the convention every card and playbook in this repo uses; without an FS_BIN in
// the re-run's shell the check exits 127, the claim never expires, and nothing
// says why.
func TestAGapCheckAddressingFSBinReadsAsExpired(t *testing.T) {
	h := setup(t)
	check := `[ -x "$FS_BIN" ]`
	nodeID := h.TaskAddKind("proj", "the widget is broken", query.KindGap, "", "", "", check, "", nil).
		Data.(command.EventData).NodeID

	want := "pending (claim no longer holds — " + check + " exited 0)"
	for _, task := range h.StatusIn("proj", t.TempDir()).Data.(command.StatusResult).Tasks {
		if task.NodeID != nodeID {
			continue
		}
		if task.Status != want {
			t.Errorf("status = %q, want %q", task.Status, want)
		}
		return
	}
	t.Fatalf("no task %s in status", nodeID)
}

// The FS_BIN the re-run's shell gets is the reader's own binary, which is the
// contract the entry gate and the exit steps already give their steps: a check
// runs the binary that is reading it, not one a PATH lookup happens to find.
func TestRunShellGivesTheCheckTheReadersOwnBinary(t *testing.T) {
	want, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	if want, err = filepath.Abs(want); err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}

	out, err := command.RunShell(`printf %s "$FS_BIN"`, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("a check reading $FS_BIN did not run: %v (%q)", err, out)
	}
	if out != want {
		t.Errorf("$FS_BIN = %q, want the reader's own binary %q", out, want)
	}
}

// A caller that names FS_BIN keeps it, and the shell sees it once. The gates
// export it deliberately — empty when os.Executable cannot be read, which a step
// must treat as missing — and a second entry would leave which one wins to the
// libc.
func TestRunShellKeepsAnFSBinTheCallerNamed(t *testing.T) {
	dir := t.TempDir()

	out, err := command.RunShell(`env | grep -c '^FS_BIN='`, dir, []string{"FS_BIN=/a/step/named/binary"})
	if err != nil {
		t.Fatalf("counting FS_BIN entries: %v (%q)", err, out)
	}
	if strings.TrimSpace(out) != "1" {
		t.Errorf("the shell sees %s FS_BIN entries, want exactly 1", strings.TrimSpace(out))
	}

	out, err = command.RunShell(`printf %s "$FS_BIN"`, dir, []string{"FS_BIN="})
	if err != nil {
		t.Fatalf("running with an empty FS_BIN: %v", err)
	}
	if out != "" {
		t.Errorf("$FS_BIN = %q, want the caller's empty value kept", out)
	}
}

// A check that does not finish is not a claim that held, and it is not one that
// is over: the re-run stops it at the deadline and says so, so a reader is not
// left waiting with no output and is not told a condition is over that nobody
// finished asking about.
func TestACheckThatDoesNotFinishReadsAsItsOwnState(t *testing.T) {
	h := setup(t)
	check := "sleep 30"
	nodeID := h.TaskAddKind("proj", "the widget is broken", query.KindGap, "", "", "", check, "", nil).
		Data.(command.EventData).NodeID

	want := "pending [check did not finish — " + check + " exceeded " + command.CheckTimeout.String() + "]"
	for _, task := range h.StatusIn("proj", t.TempDir()).Data.(command.StatusResult).Tasks {
		if task.NodeID != nodeID {
			continue
		}
		if task.Status != want {
			t.Errorf("status = %q, want %q", task.Status, want)
		}
		return
	}
	t.Fatalf("no task %s in status", nodeID)
}

// A resolved reading and an unresolved one are opposite — the first says the
// recorded condition is over, the second that nobody finished asking about it —
// so a reader must be able to tell them apart without reading the words. The
// resolved reading is parenthesised and the unresolved one bracketed, and the
// rule holds for a block's condition as much as for a node's claim: a reader
// keyed on "the status is decorated" would otherwise read a timeout as an
// expiry.
func TestATimedOutReadingIsBracketedAndAnExpiredClaimIsNot(t *testing.T) {
	h := setup(t)
	over := h.TaskAddKind("proj", "the widget is fixed", query.KindGap, "", "", "", "true", "", nil).
		Data.(command.EventData).NodeID

	check := "sleep 30"
	parked := h.TaskAddKind("proj", "the widget is broken", query.KindGap, "", "", "", "", "", nil).
		Data.(command.EventData).NodeID
	if resp := h.TaskBlockWithCheck("proj", parked, "waiting on the widget", check); !resp.OK {
		t.Fatalf("block: %s", resp.Error)
	}

	got := make(map[string]string)
	for _, task := range h.StatusIn("proj", t.TempDir()).Data.(command.StatusResult).Tasks {
		got[task.NodeID] = task.Status
	}

	if expired := got[over]; !strings.Contains(expired, "(claim no longer holds") {
		t.Errorf("expired claim = %q, want a parenthesised resolved reading", expired)
	}

	want := "blocked [check did not finish — " + check + " exceeded " + command.CheckTimeout.String() + "]"
	if got[parked] != want {
		t.Errorf("block whose check did not finish = %q, want %q", got[parked], want)
	}
}

// The deadline is the re-run's alone, and it does not read a fast failure as
// anything new: a check that exits non-zero has answered, and the claim holds.
func TestACheckThatFailsQuicklyStillHolds(t *testing.T) {
	h := setup(t)
	nodeID := h.TaskAddKind("proj", "the widget is broken", query.KindGap, "", "", "", "exit 1", "", nil).
		Data.(command.EventData).NodeID

	for _, task := range h.StatusIn("proj", t.TempDir()).Data.(command.StatusResult).Tasks {
		if task.NodeID != nodeID {
			continue
		}
		if task.Status != "pending" {
			t.Errorf("status = %q, want plain pending", task.Status)
		}
		return
	}
	t.Fatalf("no task %s in status", nodeID)
}

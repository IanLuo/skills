package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runFSCapture runs the fs binary with an explicit HOME and extra environment,
// returning stdout, stderr and the exit code separately — what the unwritable-
// sink property (AC6) is measured against, where runFS's stdout-only parse is
// not enough.
func runFSCapture(t *testing.T, bin, dir, home string, extraEnv []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "HOME="+home), extraEnv...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("exec %v: %v", args, err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// setupDispatchRoot registers a project whose root passes the code-entry gate's
// two checks, so a dispatch resolves a plan and reaches the plan-resolved DEBUG
// line.
func setupDispatchRoot(t *testing.T, bin, home, name string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# agents\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "prd.md"), []byte("<!-- specs:locked: prd -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runFSCapture(t, bin, root, home, nil, "project", "create", "--name", name, "--root", root); code != 0 {
		t.Fatalf("project create %s exit %d", name, code)
	}
	return root
}

// A dispatch stderr that carries the plan-resolved DEBUG line proves the level
// moved; the same run without -v must not.
func TestCLIVerboseWritesDebugLines(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)
	writeKBPlaybook(t, home, "code-entry", dispatchPlaybook)

	setupDispatchRoot(t, bin, home, "verbosedefault")
	_, defaultStderr, code := runFSCapture(t, bin, t.TempDir(), home, nil,
		"dispatch", "--project", "verbosedefault", "--type", "dev-task", "--goal", "sample")
	if code != 0 {
		t.Fatalf("default dispatch exit %d: %s", code, defaultStderr)
	}
	if strings.Contains(defaultStderr, `"level":"DEBUG"`) || strings.Contains(defaultStderr, "plan resolved") {
		t.Errorf("default dispatch wrote a DEBUG line:\n%s", defaultStderr)
	}

	setupDispatchRoot(t, bin, home, "verboseflag")
	_, verboseStderr, code := runFSCapture(t, bin, t.TempDir(), home, nil,
		"dispatch", "-v", "--project", "verboseflag", "--type", "dev-task", "--goal", "sample", "--allow-unresolved")
	if code != 0 {
		t.Fatalf("-v dispatch exit %d: %s", code, verboseStderr)
	}
	if !strings.Contains(verboseStderr, `"level":"DEBUG"`) || !strings.Contains(verboseStderr, "plan resolved") {
		t.Errorf("-v dispatch did not write the plan-resolved DEBUG line:\n%s", verboseStderr)
	}
}

// FS_LOG_LEVEL=debug turns the level up without the flag.
func TestCLIFSLogLevelEnvWritesDebug(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)
	writeKBPlaybook(t, home, "code-entry", dispatchPlaybook)

	setupDispatchRoot(t, bin, home, "envdebug")
	_, stderr, code := runFSCapture(t, bin, t.TempDir(), home, []string{"FS_LOG_LEVEL=debug"},
		"dispatch", "--project", "envdebug", "--type", "dev-task", "--goal", "sample")
	if code != 0 {
		t.Fatalf("dispatch exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "plan resolved") {
		t.Errorf("FS_LOG_LEVEL=debug dispatch did not write the DEBUG line:\n%s", stderr)
	}
}

// The flag overrides the environment: FS_LOG_LEVEL=error with -v still writes
// DEBUG.
func TestCLIFlagOverridesEnvLevel(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)
	writeKBPlaybook(t, home, "code-entry", dispatchPlaybook)

	setupDispatchRoot(t, bin, home, "flagwins")
	_, stderr, code := runFSCapture(t, bin, t.TempDir(), home, []string{"FS_LOG_LEVEL=error"},
		"dispatch", "-v", "--project", "flagwins", "--type", "dev-task", "--goal", "sample")
	if code != 0 {
		t.Fatalf("dispatch exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "plan resolved") {
		t.Errorf("flag did not override FS_LOG_LEVEL=error:\n%s", stderr)
	}
}

// An unparseable FS_LOG_LEVEL is refused, naming the accepted values, rather
// than silently falling back.
func TestCLIUnparseableLogLevelRefused(t *testing.T) {
	bin := getFS(t)
	home := testHome(t)

	stdout, _, code := runFSCapture(t, bin, t.TempDir(), home, []string{"FS_LOG_LEVEL=bogus"}, "status")
	if code != 1 {
		t.Fatalf("exit %d, want 1 (refusal)", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("stdout is not an envelope: %v\n%s", err, stdout)
	}
	if resp["ok"] != false {
		t.Fatalf("ok = %v, want false", resp["ok"])
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "accepted: debug, info, warn, error") {
		t.Errorf("error %q does not name the accepted values", errMsg)
	}
}

// refusalLine returns the WARN line a refused command wrote to stderr, as the
// JSON object the running log is made of.
func refusalLine(t *testing.T, stderr string) map[string]any {
	t.Helper()
	for _, raw := range strings.Split(stderr, "\n") {
		if !strings.Contains(raw, `"msg":"command refused"`) {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("refusal line is not one JSON object: %v\n%s", err, raw)
		}
		return line
	}
	t.Fatalf("no refusal line on stderr:\n%s", stderr)
	return nil
}

// AC4: a refusal line carries `project` when — and only when — the invocation
// named one, which is what makes filtering by project a jq predicate over the
// whole file rather than over the part of it that happened to succeed. The
// project is read from the caller's own arguments: `--project P`, the project
// half of a `PROJECT:NODE` address (`close --worker P:N`, or the first positional
// of a command whose positional is one — `task get P:N`, `report P:N`). An
// invocation that names none carries no key at all, rather than a guessed one.
func TestCLIRefusalLineCarriesTheProjectTheInvocationNamed(t *testing.T) {
	bin := getFS(t)

	cases := []struct {
		name string
		args []string
		want string // the project key's value; "" means the key must be absent
		code int    // the refusal's exit code: 1 for what the command refused, 2 for
		// a usage error — refused before the command runs, and a refusal all the same
	}{
		{"--project",
			[]string{"dispatch", "--project", "nosuchproject", "--type", "dev-task", "--goal", "x"},
			"nosuchproject", 1},
		{"a --worker address",
			[]string{"close", "--node", "t-nope", "--worker", "nosuchproject:t-nope", "--decision", "d", "--confirm"},
			"nosuchproject", 1},
		{"report's positional address",
			[]string{"report", "nosuchproject:t-nope", "--line", "x"},
			"nosuchproject", 1},
		{"a task address",
			[]string{"task", "get", "nosuchproject:t-nope"},
			"nosuchproject", 1},
		{"a task update address",
			[]string{"task", "update", "nosuchproject:t-nope", "--status", "done"},
			"nosuchproject", 1},
		// log's --node is an address the way a positional is — nodeArg splits it
		// and the scope it names is the one the log is of — so the invocation named
		// a project even though the address arrived in a flag.
		{"log's --node address",
			[]string{"log", "--node", "nosuchproject:t-nope"},
			"nosuchproject", 1},
		// close's --node is not an address: close resolves it with capNode, always
		// in the cap scope, so nothing is split and the invocation named no project.
		// A line carrying one would name a project the command did not use.
		{"close's --node is a bare id, not an address",
			[]string{"close", "--node", "nosuchproject:t-nope", "--decision", "d", "--confirm"},
			"", 1},
		{"a flag value that reads like a flag",
			[]string{"report", "nosuchproject:t-nope", "--line", "--project", "skills"},
			"nosuchproject", 2},
		{"a project flag after a bare positional",
			[]string{"task", "get", "t-nope", "--project", "skills"},
			"skills", 1},
		// The flag beats the address, because that is what the command did: nodeArg
		// gives --project precedence and refuses a qualifier that contradicts it. A
		// line that named the positional's project would contradict its own reason.
		{"a project flag that beats the positional's address",
			[]string{"task", "get", "skills:t-nope", "--project", "nosuchproject"},
			"nosuchproject", 1},
		{"a project flag that beats report's positional address",
			[]string{"report", "skills:t-nope", "--line", "x", "--project", "nosuchproject"},
			"nosuchproject", 1},
		{"a project flag after a bare --worker",
			[]string{"close", "--node", "t-nope", "--worker", "t-nope", "--decision", "d", "--confirm", "--project", "skills"},
			"skills", 2},
		{"named none",
			[]string{"task", "get", "t-nope"},
			"", 1},
		{"a bare --worker names none",
			[]string{"close", "--node", "t-nope", "--worker", "t-nope", "--decision", "d", "--confirm"},
			"", 1},
		{"a glued --project=P",
			[]string{"task", "get", "t-nope", "--project=skills"},
			"skills", 2},
		{"a glued --worker=P:N",
			[]string{"close", "--node", "t-nope", "--worker=nosuchproject:t-nope", "--decision", "d", "--confirm"},
			"nosuchproject", 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runFSCapture(t, bin, t.TempDir(), testHome(t), nil, tc.args...)
			if code != tc.code {
				t.Fatalf("exit %d, want %d (refusal):\n%s%s", code, tc.code, stdout, stderr)
			}
			line := refusalLine(t, stderr)
			project, present := line["project"]
			if tc.want == "" {
				if present {
					t.Errorf("refusal line carries project %v, want no project key: %v", project, line)
				}
				return
			}
			if !present || project != tc.want {
				t.Errorf("refusal line project = %v (present %v), want %q: %v",
					project, present, tc.want, line)
			}
		})
	}
}

// AC6: a log write failure must never fail a command. Pointing the sink at an
// unwritable path leaves the command's exit code and stdout unchanged; the
// worst that reaches stderr is a WARN.
func TestCLIUnwritableSinkDoesNotChangeExitOrStdout(t *testing.T) {
	bin := getFS(t)
	dir := t.TempDir()

	cleanHome := t.TempDir()
	cleanStdout, _, cleanCode := runFSCapture(t, bin, dir, cleanHome, nil, "unfinished")
	if cleanCode != 0 {
		t.Fatalf("clean unfinished exit %d: %s", cleanCode, cleanStdout)
	}

	blockedHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(blockedHome, ".fs"), 0o755); err != nil {
		t.Fatal(err)
	}
	// ~/.fs/logs is a regular file, so the sink cannot create the directory.
	if err := os.WriteFile(filepath.Join(blockedHome, ".fs", "logs"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runFSCapture(t, bin, dir, blockedHome, nil, "unfinished")
	if code != cleanCode {
		t.Fatalf("exit %d, want %d (unchanged)", code, cleanCode)
	}
	if stdout != cleanStdout {
		t.Errorf("stdout changed under an unwritable sink:\nblocked: %s\nclean:   %s", stdout, cleanStdout)
	}
	if !strings.Contains(stderr, `"level":"INFO"`) {
		t.Errorf("stderr lost its normal lines under an unwritable sink:\n%s", stderr)
	}
	if strings.Contains(stderr, `"level":"ERROR"`) {
		t.Errorf("stderr carries an ERROR under an unwritable sink, want at most a WARN:\n%s", stderr)
	}
}

// What the binary knows about its own build.
//
// A build is evidence about one tree, and a binary that cannot name that tree
// gives a reader holding a different one nothing to check a conclusion against.
// Three builds of this tool sat on one machine, all different, and a fix that
// worked was concluded twice to have failed because the build under test
// predated it. Nothing in the artifact said which build it was, so the mistake
// was invisible until someone diffed hashes by hand.
//
// So the flake stamps the build and `fs version` reports it. There are three
// answers and all three are said plainly: a revision when the tree was a clean
// git checkout, the same revision with the tree marked dirty when it was not,
// and "unknown" when the flake could read no revision at all (a path or a
// tarball, where nix exposes neither rev nor dirtyRev). A build id that can be
// wrong is worse than none, so nothing here guesses.
//
// The path matters as much as the revision, and for the same reason: "which
// file is the tool" had three answers in that session — the documented path, a
// nix build byproduct, and whatever a fresh build landed in — and no way to ask
// the question of the file in hand.
package main

import (
	"os"
	"path/filepath"

	"github.com/flagship-dev/flagship/internal/command"
)

// The stamp, written in at link time by flake.nix:
//
//	-X main.buildRevision=<the tree's revision>
//	-X main.buildDirty=true|false|unknown
//
// Both default to "unknown", which is what a plain `go build` produces — what
// the tests build, and what a developer builds. A build with no stamp says it
// has none rather than reporting a revision it was not built from.
var (
	buildRevision = "unknown"
	buildDirty    = "unknown"
)

// buildInfo is the data payload of `fs version`. The field names are read by a
// shell check (the staleness gate in this repo's project scope) as well as by a
// human, so they are spelled out rather than abbreviated.
type buildInfo struct {
	Revision string `json:"revision"`
	Dirty    string `json:"dirty"`
	Path     string `json:"path"`
}

// versionCmd answers which build this is: `fs version`.
func versionCmd() {
	output(command.Response{OK: true, Data: buildInfo{
		Revision: buildRevision,
		Dirty:    buildDirty,
		Path:     execPath(),
	}})
}

// execPath is the file this process is running from, absolute and resolved
// through symlinks.
//
// Resolved, because both callers need the file rather than the spelling: the
// agent-facing route to this binary is a symlink into the repo
// (~/.claude/skills/flagship/scripts/fs), and the check that asks whether this
// build is current has to find the checkout the binary belongs to from it. It
// is also made absolute here because os.Executable is platform-dependent about
// that — /proc/self/exe on Linux is always resolved, while macOS returns the
// path as it was invoked.
func execPath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

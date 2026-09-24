package flagship

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
)

// scriptsSource is where the embedded scripts live inside the binary.
const scriptsSource = "defaults/scripts"

// MaterializeScripts writes the embedded scripts to ~/.fs/scripts and returns
// that directory, so a playbook can address one as "$FS_SCRIPTS/<name>".
//
// It is the counterpart to bootstrap and differs from it deliberately. A
// playbook in ~/.fs/kb becomes the user's once seeded — bootstrap creates what
// is absent and never overwrites — but a script is fs's own code: shipped with
// the binary, never edited by hand. So it is written whenever it differs, which
// means a fixed script takes effect on the next dispatch with no `kb reset`
// equivalent to remember, and no way for ~/.fs to drift from the binary running
// it.
//
// This exists because a shipped playbook's check step was naming a repo-relative
// path: `batch.yaml` ran `bin/check-parallel.sh`, which resolved in exactly one
// project. A playbook the binary ships is run in every project, so a command it
// names has to be one the binary ships too.
//
// It is called on the path that builds a plan's environment, so the directory is
// there whenever a step runs. A failure is returned, not swallowed: the caller
// reports it as an empty FS_SCRIPTS rather than a path that does not exist, so a
// step that needs a script can tell it is missing.
func MaterializeScripts() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".fs", "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	entries, err := fs.ReadDir(embedded, scriptsSource)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		want, err := fs.ReadFile(embedded, scriptsSource+"/"+entry.Name())
		if err != nil {
			return "", err
		}
		path := filepath.Join(dir, entry.Name())
		if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, want) {
			continue
		}
		if err := os.WriteFile(path, want, 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}

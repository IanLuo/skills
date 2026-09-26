package flagship

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// scriptsSource is where the embedded scripts live inside the binary.
const scriptsSource = "defaults/scripts"

// Script returns the shipped script named name, so `fs script` can run it with
// none of it reaching the disk.
//
// This is the rule a shipped playbook already follows, applied to the one
// artifact that could not simply be read the same way: a playbook is *parsed by
// fs*, but a script is *executed by the shell*, and `bash "$FS_SCRIPTS/x.sh"`
// needs a real path — which is the entire reason a materialized copy once
// existed. It does not need to be a file: the body runs through `bash -c`
// instead, and the directory goes away with the reason for it.
//
// An unknown name is an error naming every script the binary does ship. The
// caller is a playbook step that named a script which is gone, and a step that
// silently runs nothing is a gate that silently passes.
func Script(name string) ([]byte, error) {
	scripts, err := fs.Sub(embedded, scriptsSource)
	if err != nil {
		return nil, err
	}
	body, err := fs.ReadFile(scripts, name)
	if err == nil {
		return body, nil
	}
	names, err := scriptNames(scripts)
	if err != nil {
		return nil, err
	}
	shipped := ""
	if len(names) > 0 {
		shipped = "; the binary ships " + strings.Join(names, ", ")
	}
	return nil, fmt.Errorf("no script named %q%s", name, shipped)
}

// scriptNames lists the shipped scripts, sorted so the refusal above reads the
// same way twice.
func scriptNames(scripts fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(scripts, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Package flagship ships the playbooks that live in ~/.fs/kb and the scripts
// that live in ~/.fs/scripts inside the fs binary.
//
// Both are embedded from defaults/, so the cap's operating contract is
// versioned with the code instead of being a hand-written file outside git:
// `fs bootstrap` materialises the playbooks, `fs kb` compares them against what
// is on disk, and MaterializeScripts writes the scripts.
package flagship

import (
	"embed"
	"io/fs"
)

// embedded holds defaults/kb, one YAML playbook per file, and
// defaults/scripts, one shell script per file. Adding either is dropping a file
// in there — nothing in this package changes.
//
//go:embed defaults
var embedded embed.FS

// KB returns the embedded playbooks, rooted at the KB directory so a playbook
// addresses as "<name>.yaml". The directory is embedded above, so the only way
// Sub can fail is if that directive and this path disagree — a build-time
// mistake, reported rather than panicked.
func KB() (fs.FS, error) {
	return fs.Sub(embedded, "defaults/kb")
}

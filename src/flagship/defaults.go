// Package flagship ships the scripts the binary can run, inside the binary.
//
// They are embedded from defaults/scripts, so whatever a caller runs with
// `fs script <name>` is versioned with the code instead of being a hand-written
// file outside git — and nothing is written to disk: Script hands a shipped
// script's body to `fs script` to run.
package flagship

import (
	"embed"
)

// embedded holds defaults/scripts, one shell script per file. Adding one is
// dropping a file in there — nothing in this package changes.
//
//go:embed defaults
var embedded embed.FS

// Package memory is fs's only client of shared memstore.
//
// It owns four things and nothing else: the `fs:` scope guard, the
// `task:`/`session:`/`claim:`/`ev:` key construction, the arithmetic that turns
// row timestamps into liveness, and the single clock read. It does not know what
// a task is — goals, checks and states belong to internal/coord.
//
// The seam exists because the claim protocol is correct only while every racer
// builds the same key. Key construction lives here and nowhere else, so there is
// one place to read and one place to break.
package memory

import (
	"fmt"
	"strings"
)

// ScopePrefix is the namespace fs owns inside shared memstore. No other scope is
// read or written, and every row fs writes carries it.
const ScopePrefix = "fs:"

// Origin is the provenance stamped on every row fs writes. memstore's unique
// index over (origin, origin_id) is the primitive both of fs's invariants are
// built on, so this value is part of the correctness argument, not a label.
const Origin = "fs"

// Scope is the memstore scope of one project's coordination rows.
func Scope(project string) string { return ScopePrefix + project }

// Project extracts the project name from an fs scope. A scope that is not fs's
// is reported as unknown, so a caller cannot accidentally treat another
// service's scope as a project.
func Project(scope string) (string, bool) {
	if !strings.HasPrefix(scope, ScopePrefix) {
		return "", false
	}
	project := scope[len(ScopePrefix):]
	if project == "" {
		return "", false
	}
	return project, true
}

// ValidProject refuses a project name that cannot be a scope component. The
// separator check is not decorative: a project holding a ':' would make its
// scope indistinguishable from a deeper scope, and the scope guard would be
// reading someone else's rows.
func ValidProject(project string) error {
	if strings.TrimSpace(project) == "" {
		return fmt.Errorf("no project scope: --project is required")
	}
	if strings.TrimSpace(project) != project {
		return fmt.Errorf("project %q has leading or trailing space", project)
	}
	if strings.ContainsAny(project, ": /\t\r\n") {
		return fmt.Errorf("project %q may not contain ':' '/' or whitespace", project)
	}
	return nil
}

// ValidID refuses a task or session identity that would make a key ambiguous.
// Every fs key is `prefix:<task>:<session>:<n>` split on ':', so an identity
// holding a ':' would parse as a different row's key.
func ValidID(field, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("no %s identity", field)
	}
	if strings.TrimSpace(id) != id {
		return fmt.Errorf("%s %q has leading or trailing space", field, id)
	}
	if strings.ContainsAny(id, ": \t\r\n") {
		return fmt.Errorf("%s %q may not contain ':' or whitespace", field, id)
	}
	return nil
}

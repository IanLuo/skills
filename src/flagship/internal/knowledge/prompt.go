// The cap's standing rules, assembled from the playbooks that carry them.
//
// A playbook nobody loads is inert. A `phase: cap` playbook is the cap's
// operating contract, and Prompt is the one path that puts it in front of the
// agent — so the rule travels with the binary that ships it, instead of being
// retyped into a prompt by hand every session.
//
// The output is plain text with no blank lines, because it is piped whole into
// a single --append-system-prompt argument.
package knowledge

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Prompt renders the cap's standing rules: every `phase: cap` playbook whose
// applies_when the session's tags satisfy, in name order, each preceded by a
// header naming it, its phase, its state against the shipped defaults and the
// stamp it was seeded with.
//
// The tags are the caller's, because they describe the session and not the
// schema: the engine the cap dispatches through is a fact about the running
// session, and a playbook carries herdr-only rules by asking for that engine.
//
// The header is what makes a stale prompt detectable. It says which playbooks
// were included and where each came from, so a binary whose shipped defaults
// have moved on since the playbook was seeded is visible in the prompt itself,
// not only in fs kb list.
func (c *Center) Prompt(defaults fs.FS, tags map[string]any) (string, error) {
	books, err := c.scopedNames()
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Standing rules for this session, from the cap's playbooks. " +
		"They are not suggestions: apply them on every turn.\n")

	included := 0
	for _, book := range books {
		pb, err := c.Get(book.name)
		if err != nil {
			return "", err
		}
		if pb.Phase != PhaseCap || !pb.Matches(tags) {
			continue
		}

		st, err := c.state(defaults, book)
		if err != nil {
			return "", err
		}
		content, err := os.ReadFile(book.path)
		if err != nil {
			return "", fmt.Errorf("knowledge: read %s: %w", book.path, err)
		}

		included++
		fmt.Fprintf(&b, "# %s — phase %s; %s\n", book.name, pb.Phase, provenance(st, content))
		for _, step := range pb.Steps {
			if step.Kind == KindSay {
				fmt.Fprintf(&b, "- %s\n", step.Body)
				continue
			}
			fmt.Fprintf(&b, "- %s: %s\n", step.Kind, step.Body)
		}
	}

	if included == 0 {
		fmt.Fprintf(&b, "# No `phase: cap` playbook is installed; run fs bootstrap.\n")
	}
	return b.String(), nil
}

// provenance describes where a playbook came from: what state it is in relative
// to the shipped default of the same name, and the stamp the seeding binary
// left at the top of the file. An unstamped playbook was never seeded by this
// binary.
func provenance(st PlaybookState, content []byte) string {
	stamp := "unstamped"
	if hash, _, ok := splitStamp(content); ok {
		stamp = "stamp sha256:" + hash
	}
	return st.State + ", " + stamp
}

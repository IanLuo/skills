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
	"os"
	"strings"
)

// Prompt renders the cap's standing rules: every playbook with a `cap:` section
// whose applies_when the session's tags satisfy, in name order, each preceded by
// a header naming it, its moments, and where it came from.
//
// The tags are the caller's, because they describe the session and not the
// schema: the engine the cap dispatches through is a fact about the running
// session, and a playbook carries herdr-only rules by asking for that engine.
//
// The header is what makes where a rule came from visible in the prompt itself,
// not only in fs kb list: the scope the playbook resolved from, the state of the
// copy on disk beneath it when there is one, and the stamp that copy carries. A
// rule the cap is reading out of the binary says `shipped`; one it is reading
// out of a file the user wrote says which file's scope.
func (c *Center) Prompt(tags map[string]any) (string, error) {
	books, err := c.names()
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Standing rules for this session, from the cap's playbooks. " +
		"They are not suggestions: apply them on every turn.\n")

	included := 0
	for _, name := range books {
		pb, err := c.Get(name)
		if err != nil {
			return "", err
		}
		if !pb.Serves(MomentCap) || !pb.Matches(tags) {
			continue
		}

		st, err := c.state(name)
		if err != nil {
			return "", err
		}
		var content []byte
		if st.Path != "" {
			if content, err = os.ReadFile(st.Path); err != nil {
				return "", fmt.Errorf("knowledge: read %s: %w", st.Path, err)
			}
		}

		included++
		fmt.Fprintf(&b, "# %s — moments %s; %s\n", name, strings.Join(pb.Moments(), "+"), provenance(st, content))
		for _, step := range pb.StepsFor(MomentCap) {
			if step.Kind == KindSay {
				fmt.Fprintf(&b, "- %s\n", step.Body)
				continue
			}
			fmt.Fprintf(&b, "- %s: %s\n", step.Kind, step.Body)
		}
	}

	if included == 0 {
		fmt.Fprintf(&b, "# No playbook with a `cap:` section matches this session's tags.\n")
	}
	return b.String(), nil
}

// provenance describes where a playbook came from: the scope it resolved from,
// the state of the copy on disk beneath it, and the stamp that copy carries. A
// playbook with no file on disk resolved from the binary, and the two halves of
// its description are the same word.
func provenance(st PlaybookState, content []byte) string {
	if st.Path == "" {
		return StateShipped
	}
	stamp := "unstamped"
	if hash, _, ok := splitStamp(content); ok {
		stamp = "stamp sha256:" + hash
	}
	return st.Scope + ", " + st.State + ", " + stamp
}

// The cap's standing rules, assembled from the playbooks that carry them.
//
// A playbook nobody loads is inert. A procedure playbook with trigger cap is
// the cap's operating contract, and Prompt is the one path that puts it in
// front of the agent — so the rule travels with the binary that ships it,
// instead of being retyped into a prompt by hand every session.
//
// The output is plain text with no blank lines, because it is piped whole into
// a single --append-system-prompt argument.
package knowledge

import (
	"fmt"
	"io/fs"
	"strings"
)

// capTrigger is the trigger whose procedure playbooks are the cap's standing
// rules.
const capTrigger = "cap"

// Prompt renders the cap's standing rules: every procedure playbook with
// trigger cap, in name order, each preceded by a header naming it together with
// its state against the shipped defaults and the stamp it was seeded with.
//
// That header is what makes a stale prompt detectable. It says which playbooks
// were included and where each came from, so a binary whose shipped defaults
// have moved on since the playbook was seeded is visible in the prompt itself,
// not only in fs kb list.
func (c *Center) Prompt(defaults fs.FS) (string, error) {
	names, err := c.List()
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Standing rules for this session, from the cap's playbooks. " +
		"They are not suggestions: apply them on every turn.\n")

	included := 0
	for _, name := range names {
		content, err := c.read(name)
		if err != nil {
			return "", err
		}
		pb, err := parsePlaybook(content)
		if err != nil {
			return "", err
		}
		if pb.Type != "procedure" || pb.Trigger != capTrigger {
			continue
		}
		st, err := c.state(defaults, name)
		if err != nil {
			return "", err
		}

		included++
		fmt.Fprintf(&b, "# %s — procedure, trigger %s; %s\n", name, capTrigger, provenance(st, content))
		for _, step := range pb.Steps {
			if step.Kind == KindSay {
				fmt.Fprintf(&b, "- %s\n", step.Body)
				continue
			}
			fmt.Fprintf(&b, "- %s: %s\n", step.Kind, step.Body)
		}
	}

	if included == 0 {
		fmt.Fprintf(&b, "# No procedure playbook with trigger %s is installed; run fs bootstrap.\n", capTrigger)
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

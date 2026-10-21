package dispatch

// The launch config: what a dispatch starts a worker on.
//
// It is frozen here, in code, by the fs contract (R8) — `engine=herdr`,
// `agent=pi`, and the provider route the agent's own command line names. It used
// to be parsed out of playbooks, which meant that a project without the right
// playbook, or a playbook edited between dispatch and delivery, produced a
// worker started with no arguments: a 401, an empty turn, and a dispatch
// reported as running. A missing file cannot make a constant empty.
//
// The route is a name and a path, never a secret: the credential behind it is
// the one dependency this config cannot carry (R7).
const (
	// LaunchEngine is the dispatcher every dispatch runs through.
	LaunchEngine = "herdr"
	// LaunchAgent is the kind of agent that carries a brief.
	LaunchAgent = "pi"
)

// launchArgs is the agent's own command line, one argv element per entry. It is
// returned as a fresh slice so a caller that appends to it cannot edit the
// config every later dispatch reads.
func launchArgs() []string {
	return []string{"--provider", "magpie", "--model", "opencode-go/deepseek-v4.1-flash"}
}

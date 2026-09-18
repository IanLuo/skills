// Package engine defines the distribution engine interface.
// Engine adapters translate spawn intent into engine-specific calls.
// (ARCHITECTURE R2: Engine Adapters layer; SYSTEM-DESIGN R3)
package engine

// SpawnConfig describes what agent to spawn and how.
type SpawnConfig struct {
	Kind   string // Agent kind: "pi", "claude", "codex", etc.
	Prompt string // Initial prompt to send the agent.
	Skill  string // Global skill name to activate.
	CWD    string // Working directory for the agent.
	Keep   bool   // If true, keep the pane open after agent finishes.
}

// AgentHandle identifies a running agent session.
type AgentHandle struct {
	ID     string // Engine-specific identifier (e.g., pane ID).
	Engine string // Engine name (e.g., "herdr").
}

// ErrUnavailable indicates the engine is not available.
type ErrUnavailable string

func (e ErrUnavailable) Error() string { return string(e) }

// Engine is the distribution engine interface.
// V1: herdr. Others possible via additional adapters.
// (ARCHITECTURE decision 4: pluggable boundary)
type Engine interface {
	// Spawn starts an agent with the given config. Returns a handle to interact with it.
	Spawn(config SpawnConfig) (AgentHandle, error)

	// Read retrieves the agent's output/response.
	Read(handle AgentHandle) (string, error)

	// Close terminates the agent session and cleans up.
	Close(handle AgentHandle) error
}

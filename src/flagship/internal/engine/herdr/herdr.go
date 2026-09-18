// Package herdr implements the engine.Engine interface via herdr CLI.
// It shells out to herdr commands: pane split, agent start, agent prompt,
// agent read, pane close. (SYSTEM-DESIGN R3, ARCHITECTURE decision 4)
package herdr

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/flagship-dev/flagship/internal/engine"
)

// Runner abstracts command execution for testing.
type Runner interface {
	Run(args ...string) (string, error)
}

// CLIRunner executes herdr CLI commands.
type CLIRunner struct{}

// Run executes `herdr <args>` and returns stdout.
func (c *CLIRunner) Run(args ...string) (string, error) {
	cmd := exec.Command("herdr", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("herdr: %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ErrMock is a simple error type for test mocks.
type ErrMock string

func (e ErrMock) Error() string { return string(e) }

// Adapter implements engine.Engine via herdr CLI.
type Adapter struct {
	runner Runner
}

// NewAdapter creates a herdr adapter with the given command runner.
func NewAdapter(runner Runner) *Adapter {
	return &Adapter{runner: runner}
}

// New creates a herdr adapter using the real CLI runner.
func New() *Adapter {
	return &Adapter{runner: &CLIRunner{}}
}

// Spawn splits a pane, starts an agent, and sends the prompt.
func (a *Adapter) Spawn(config engine.SpawnConfig) (engine.AgentHandle, error) {
	// 1. Split a pane.
	splitArgs := []string{"pane", "split"}
	if config.CWD != "" {
		splitArgs = append(splitArgs, "--cwd", config.CWD)
	}
	if config.Keep {
		splitArgs = append(splitArgs, "--keep")
	}

	paneID, err := a.runner.Run(splitArgs...)
	if err != nil {
		return engine.AgentHandle{}, fmt.Errorf("herdr: spawn pane: %w", err)
	}

	// 2. Start the agent.
	startArgs := []string{"agent", "start", "--pane", paneID, "--kind", config.Kind}
	if config.Skill != "" {
		startArgs = append(startArgs, "--skill", config.Skill)
	}

	if _, err := a.runner.Run(startArgs...); err != nil {
		return engine.AgentHandle{}, fmt.Errorf("herdr: start agent: %w", err)
	}

	// 3. Send the prompt.
	if config.Prompt != "" {
		promptArgs := []string{"agent", "prompt", "--pane", paneID, config.Prompt}
		if _, err := a.runner.Run(promptArgs...); err != nil {
			return engine.AgentHandle{}, fmt.Errorf("herdr: send prompt: %w", err)
		}
	}

	return engine.AgentHandle{ID: paneID, Engine: "herdr"}, nil
}

// Read retrieves the agent's output.
func (a *Adapter) Read(handle engine.AgentHandle) (string, error) {
	return a.runner.Run("agent", "read", "--pane", handle.ID)
}

// Close terminates the pane.
func (a *Adapter) Close(handle engine.AgentHandle) error {
	_, err := a.runner.Run("pane", "close", "--pane", handle.ID)
	return err
}

package herdr_test

import (
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/engine"
	"github.com/flagship-dev/flagship/internal/engine/herdr"
)

// mockRunner captures commands instead of executing them.
type mockRunner struct {
	calls  []string
	output string
	err    error
}

func (m *mockRunner) Run(args ...string) (string, error) {
	m.calls = append(m.calls, strings.Join(args, " "))
	return m.output, m.err
}

func TestInterfaceCompliance(t *testing.T) {
	// Verify herdr.Adapter implements engine.Engine at compile time.
	var _ engine.Engine = (*herdr.Adapter)(nil)
}

func TestSpawnCommandConstruction(t *testing.T) {
	m := &mockRunner{output: "pane-42"}
	a := herdr.NewAdapter(m)

	config := engine.SpawnConfig{
		Kind:   "pi",
		Prompt: "implement auth",
		Skill:  "dev-task",
		CWD:    "/tmp/project",
		Keep:   false,
	}

	handle, err := a.Spawn(config)
	if err != nil {
		t.Fatal(err)
	}
	if handle.ID != "pane-42" {
		t.Errorf("handle.ID: %s", handle.ID)
	}
	if handle.Engine != "herdr" {
		t.Errorf("handle.Engine: %s", handle.Engine)
	}

	// Should have called: pane split, agent start, agent prompt.
	if len(m.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d: %v", len(m.calls), m.calls)
	}

	// 1. pane split
	if !strings.Contains(m.calls[0], "pane split") {
		t.Errorf("call[0]: %s", m.calls[0])
	}
	if !strings.Contains(m.calls[0], "/tmp/project") {
		t.Errorf("call[0] missing CWD: %s", m.calls[0])
	}

	// 2. agent start
	if !strings.Contains(m.calls[1], "agent start") {
		t.Errorf("call[1]: %s", m.calls[1])
	}
	if !strings.Contains(m.calls[1], "pi") {
		t.Errorf("call[1] missing kind: %s", m.calls[1])
	}

	// 3. agent prompt
	if !strings.Contains(m.calls[2], "agent prompt") {
		t.Errorf("call[2]: %s", m.calls[2])
	}
	if !strings.Contains(m.calls[2], "implement auth") {
		t.Errorf("call[2] missing prompt: %s", m.calls[2])
	}
}

func TestSpawnWithKeep(t *testing.T) {
	m := &mockRunner{output: "pane-99"}
	a := herdr.NewAdapter(m)

	config := engine.SpawnConfig{
		Kind: "claude",
		CWD:  "/tmp",
		Keep: true,
	}

	_, err := a.Spawn(config)
	if err != nil {
		t.Fatal(err)
	}

	// pane split should include --keep flag.
	if !strings.Contains(m.calls[0], "--keep") {
		t.Errorf("call[0] missing --keep: %s", m.calls[0])
	}
}

func TestSpawnWithSkill(t *testing.T) {
	m := &mockRunner{output: "pane-1"}
	a := herdr.NewAdapter(m)

	config := engine.SpawnConfig{
		Kind:  "pi",
		Skill: "dev-task",
		CWD:   "/tmp",
	}

	_, err := a.Spawn(config)
	if err != nil {
		t.Fatal(err)
	}

	// agent start should include skill.
	if !strings.Contains(m.calls[1], "--skill dev-task") {
		t.Errorf("call[1] missing skill: %s", m.calls[1])
	}
}

func TestRead(t *testing.T) {
	m := &mockRunner{output: "agent output here"}
	a := herdr.NewAdapter(m)

	handle := engine.AgentHandle{ID: "pane-42", Engine: "herdr"}
	result, err := a.Read(handle)
	if err != nil {
		t.Fatal(err)
	}
	if result != "agent output here" {
		t.Errorf("result: %s", result)
	}

	if len(m.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.calls))
	}
	if !strings.Contains(m.calls[0], "agent read") {
		t.Errorf("call[0]: %s", m.calls[0])
	}
	if !strings.Contains(m.calls[0], "pane-42") {
		t.Errorf("call[0] missing pane ID: %s", m.calls[0])
	}
}

func TestClose(t *testing.T) {
	m := &mockRunner{}
	a := herdr.NewAdapter(m)

	handle := engine.AgentHandle{ID: "pane-42", Engine: "herdr"}
	err := a.Close(handle)
	if err != nil {
		t.Fatal(err)
	}

	if len(m.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.calls))
	}
	if !strings.Contains(m.calls[0], "pane close") {
		t.Errorf("call[0]: %s", m.calls[0])
	}
	if !strings.Contains(m.calls[0], "pane-42") {
		t.Errorf("call[0] missing pane ID: %s", m.calls[0])
	}
}

func TestSpawnError(t *testing.T) {
	m := &mockRunner{err: herdr.ErrMock("herdr not running")}
	a := herdr.NewAdapter(m)

	_, err := a.Spawn(engine.SpawnConfig{Kind: "pi", CWD: "/tmp"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "herdr not running") {
		t.Errorf("error: %v", err)
	}
}

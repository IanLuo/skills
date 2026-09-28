package capabilities_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flagship-dev/flagship/internal/capabilities"
)

// writeFile writes content to dir/name, creating intermediate dirs.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// l2HerdrProcedure is the L2 playbook content for tests.
const l2HerdrProcedure = `name: herdr-procedure
type: procedure
trigger: herdr

capability-impl:

  distribution-engine:
    env-check: 'test "${HERDR_ENV:-}" = 1'
    preconditions:
      - env: HERDR_ENV=1
      - binary: herdr
    env-aliases:
      PANE_ID: HERDR_PANE_ID
      WORKSPACE_ID: HERDR_WORKSPACE_ID
      TAB_ID: HERDR_TAB_ID
      ENGINE_ENV: HERDR_ENV
    flags:
      focus_flag:
        "true": ""
        "false": "--no-focus"
      wait_flag:
        "true": "--wait"
        "false": ""
      timeout_flag:
        "true": "--timeout {timeout}"
        "false": ""
    operations:
      spawn-pane: >-
        herdr pane split --current --direction {direction}
        --cwd {cwd} {focus_flag}
      start-agent: >-
        herdr agent start {name} --kind {kind} --pane {pane}
      prompt-agent: >-
        herdr agent prompt {name} "{prompt}" {wait_flag} {timeout_flag}
      read-agent: >-
        herdr agent read {name} --source recent-unwrapped --lines {lines}
      wait-agent: >-
        herdr agent wait {name} --until {until} --timeout {timeout}
      agent-status: >-
        herdr agent get {name}
      list-agents: >-
        herdr agent list
      close-pane: >-
        herdr pane close {pane}
      pane-layout: >-
        herdr pane layout --pane {pane}
      list-panes: >-
        herdr pane list --workspace {workspace}
      list-workspaces: >-
        herdr workspace list
      create-tab: >-
        herdr tab create --workspace {workspace}

  signal:
    operations:
      send-to-agent: >-
        herdr agent prompt {target} "{message}"

steps:
  - id: verify-env
    action: 'test "${HERDR_ENV:-}" = 1'
`

// setupTestEnv creates a temp dir with L1 config and L2 playbook, returns loaded config.
func setupTestEnv(t *testing.T) *capabilities.Config {
	t.Helper()
	dir := t.TempDir()

	// Write L1 config.
	writeFile(t, dir, "capabilities.yaml", string(capabilities.DefaultConfig()))

	// Write L2 playbook in kb/ subdir.
	writeFile(t, dir, "kb/herdr-procedure.yaml", l2HerdrProcedure)

	cfg, err := capabilities.Load(filepath.Join(dir, "capabilities.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.SetKBDir(filepath.Join(dir, "kb"))
	return cfg
}

func TestLoadMissingConfig(t *testing.T) {
	_, err := capabilities.Load("/nonexistent/capabilities.yaml")
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "fs resolve --init") {
		t.Errorf("error should mention --init: %v", err)
	}
}

func TestLoadDefaultConfig(t *testing.T) {
	cfg := setupTestEnv(t)
	if cfg.Version != 1 {
		t.Errorf("version: %d", cfg.Version)
	}
	if len(cfg.Capabilities) != 4 {
		t.Errorf("expected 4 capabilities, got %d", len(cfg.Capabilities))
	}
}

func TestCheck(t *testing.T) {
	cfg := setupTestEnv(t)
	names, err := cfg.Check()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(names) != 4 {
		t.Errorf("expected 4 capabilities, got %d", len(names))
	}
}

func TestEngineName(t *testing.T) {
	cfg := setupTestEnv(t)
	if cfg.EngineName() != "herdr" {
		t.Errorf("engine name: %s", cfg.EngineName())
	}
}

func TestResolveUnknownCapability(t *testing.T) {
	cfg := setupTestEnv(t)
	_, err := cfg.Resolve("nonexistent", "op", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown capability") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveUnknownOperation(t *testing.T) {
	cfg := setupTestEnv(t)
	_, err := cfg.Resolve("distribution-engine", "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown operation") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveMissingKeys(t *testing.T) {
	cfg := setupTestEnv(t)
	_, err := cfg.Resolve("distribution-engine", "close-pane", nil)
	if err == nil {
		t.Fatal("expected error for missing keys")
	}
	if !strings.Contains(err.Error(), "missing required keys") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Table-driven substitution tests — verifies SAME outputs as before L1/L2 split.
func TestResolveSubstitution(t *testing.T) {
	cfg := setupTestEnv(t)

	tests := []struct {
		cap    string
		op     string
		args   map[string]string
		expect string
	}{
		{
			cap:    "distribution-engine",
			op:     "list-agents",
			args:   nil,
			expect: "herdr agent list",
		},
		{
			cap:    "distribution-engine",
			op:     "list-workspaces",
			args:   nil,
			expect: "herdr workspace list",
		},
		{
			cap:    "distribution-engine",
			op:     "close-pane",
			args:   map[string]string{"pane": "w1:p5"},
			expect: "herdr pane close w1:p5",
		},
		{
			cap:    "distribution-engine",
			op:     "agent-status",
			args:   map[string]string{"name": "impl-auth"},
			expect: "herdr agent get impl-auth",
		},
		{
			cap:    "distribution-engine",
			op:     "read-agent",
			args:   map[string]string{"name": "impl-auth", "lines": "120"},
			expect: "herdr agent read impl-auth --source recent-unwrapped --lines 120",
		},
		{
			cap:    "distribution-engine",
			op:     "start-agent",
			args:   map[string]string{"name": "impl-auth", "pane": "w1:p5"},
			expect: "herdr agent start impl-auth --kind pi --pane w1:p5",
		},
		{
			cap:    "distribution-engine",
			op:     "start-agent",
			args:   map[string]string{"name": "impl-auth", "pane": "w1:p5", "kind": "claude"},
			expect: "herdr agent start impl-auth --kind claude --pane w1:p5",
		},
		{
			cap:    "signal",
			op:     "send-to-agent",
			args:   map[string]string{"target": "captain", "message": "task done"},
			expect: `herdr agent prompt captain "task done"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.cap+"."+tt.op, func(t *testing.T) {
			result, err := cfg.Resolve(tt.cap, tt.op, tt.args)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if result != tt.expect {
				t.Errorf("\nexpected: %s\n     got: %s", tt.expect, result)
			}
		})
	}
}

// Test flag expansion (flags from L2, args use base name from CLI).
func TestResolveFlagExpansion(t *testing.T) {
	cfg := setupTestEnv(t)

	tests := []struct {
		name   string
		op     string
		args   map[string]string
		expect string
	}{
		{
			name: "spawn-pane with focus false",
			op:   "spawn-pane",
			args: map[string]string{
				"cwd":       "/proj",
				"direction": "right",
				"focus":     "false",
			},
			expect: "herdr pane split --current --direction right --cwd /proj --no-focus",
		},
		{
			name: "spawn-pane with focus true",
			op:   "spawn-pane",
			args: map[string]string{
				"cwd":       "/proj",
				"direction": "right",
				"focus":     "true",
			},
			expect: "herdr pane split --current --direction right --cwd /proj",
		},
		{
			name: "spawn-pane direction down",
			op:   "spawn-pane",
			args: map[string]string{
				"cwd":       "/proj",
				"direction": "down",
				"focus":     "false",
			},
			expect: "herdr pane split --current --direction down --cwd /proj --no-focus",
		},
		{
			name: "spawn-pane default focus (omitted → --no-focus)",
			op:   "spawn-pane",
			args: map[string]string{
				"cwd":       "/proj",
				"direction": "right",
			},
			expect: "herdr pane split --current --direction right --cwd /proj --no-focus",
		},
		{
			name: "prompt-agent with wait+timeout",
			op:   "prompt-agent",
			args: map[string]string{
				"name":    "impl-auth",
				"prompt":  "do the work",
				"wait":    "true",
				"timeout": "300000",
			},
			expect: `herdr agent prompt impl-auth "do the work" --wait --timeout 300000`,
		},
		{
			name: "prompt-agent without wait",
			op:   "prompt-agent",
			args: map[string]string{
				"name":    "impl-auth",
				"prompt":  "do the work",
				"wait":    "false",
				"timeout": "",
			},
			expect: `herdr agent prompt impl-auth "do the work"`,
		},
		{
			name: "flags also work with explicit _flag suffix keys",
			op:   "spawn-pane",
			args: map[string]string{
				"cwd":        "/proj",
				"direction":  "right",
				"focus_flag": "false",
			},
			expect: "herdr pane split --current --direction right --cwd /proj --no-focus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := cfg.Resolve("distribution-engine", tt.op, tt.args)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if result != tt.expect {
				t.Errorf("\nexpected: %s\n     got: %s", tt.expect, result)
			}
		})
	}
}

// Test shell escaping.
func TestShellEscaping(t *testing.T) {
	cfg := setupTestEnv(t)

	result, err := cfg.Resolve("signal", "send-to-agent", map[string]string{
		"target":  "captain",
		"message": `SIGNAL: task t-abc done; echo "pwned"`,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(result, `\"pwned\"`) {
		t.Errorf("double-quotes not escaped: %s", result)
	}

	result2, err := cfg.Resolve("signal", "send-to-agent", map[string]string{
		"target":  "captain",
		"message": `SIGNAL: $(rm -rf /)`,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(result2, `\$(rm`) {
		t.Errorf("dollar not escaped: %s", result2)
	}
}

// Test shell escaping for unquoted placeholders.
func TestShellEscapingUnquoted(t *testing.T) {
	cfg := setupTestEnv(t)

	result, err := cfg.Resolve("distribution-engine", "agent-status", map[string]string{
		"name": "impl auth", // space = needs escaping
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(result, "'impl auth'") {
		t.Errorf("unquoted placeholder not shell-escaped: %s", result)
	}
}

// Test coding-agent value provider.
func TestResolveCodingAgent(t *testing.T) {
	cfg := setupTestEnv(t)

	kind, err := cfg.Resolve("coding-agent", "kind-default", nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if kind != "pi" {
		t.Errorf("expected pi, got %s", kind)
	}

	list, err := cfg.Resolve("coding-agent", "kind-list", nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(list, "pi") || !strings.Contains(list, "claude") {
		t.Errorf("unexpected kind-list: %s", list)
	}
}

// Test scheduler operations (inline fallback — no L2 playbook).
func TestResolveScheduler(t *testing.T) {
	cfg := setupTestEnv(t)

	result, err := cfg.Resolve("scheduler", "heartbeat-arm", map[string]string{
		"interval": "25m",
		"prompt":   "HEARTBEAT: run heartbeat scan",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(result, "CronCreate") {
		t.Errorf("expected CronCreate in result: %s", result)
	}
	if !strings.Contains(result, "interval=25m") {
		t.Errorf("missing interval: %s", result)
	}
}

// Test env-alias resolution (from L2).
func TestEnvAliasResolution(t *testing.T) {
	cfg := setupTestEnv(t)

	t.Setenv("HERDR_PANE_ID", "w1:p3")

	result, err := cfg.Resolve("distribution-engine", "pane-layout", map[string]string{
		"pane": "$PANE_ID",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(result, "w1:p3") {
		t.Errorf("env alias not resolved: %s", result)
	}
}

// Test env-alias fallback (§10.3).
func TestEnvAliasFallback(t *testing.T) {
	cfg := setupTestEnv(t)

	t.Setenv("HERDR_PANE_ID", "w1:p9")

	result, err := cfg.Resolve("distribution-engine", "pane-layout", map[string]string{
		"pane": "$HERDR_PANE_ID",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(result, "w1:p9") {
		t.Errorf("env fallback not resolved: %s", result)
	}
}

// Test WriteDefaultConfig.
func TestWriteDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "capabilities.yaml")
	if err := capabilities.WriteDefaultConfig(path); err != nil {
		t.Fatalf("WriteDefaultConfig: %v", err)
	}
	cfg, err := capabilities.Load(path)
	if err != nil {
		t.Fatalf("Load written config: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("version: %d", cfg.Version)
	}
}

// Test invalid YAML.
func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "capabilities.yaml", "{{invalid yaml")
	_, err := capabilities.Load(filepath.Join(dir, "capabilities.yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// Test empty capabilities.
func TestLoadEmptyCapabilities(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "capabilities.yaml", "version: 1\n")
	_, err := capabilities.Load(filepath.Join(dir, "capabilities.yaml"))
	if err == nil {
		t.Fatal("expected error for empty capabilities")
	}
}

// Test that L2 resolution produces same outputs as the old all-in-L1 approach.
func TestL2ResolutionMatchesBaseline(t *testing.T) {
	cfg := setupTestEnv(t)

	// These are the exact outputs from the old binary (captured as baseline).
	tests := []struct {
		name   string
		cap    string
		op     string
		args   map[string]string
		expect string
	}{
		{
			name:   "spawn-pane baseline",
			cap:    "distribution-engine",
			op:     "spawn-pane",
			args:   map[string]string{"direction": "right", "cwd": "/tmp", "focus": "false"},
			expect: "herdr pane split --current --direction right --cwd /tmp --no-focus",
		},
		{
			name:   "prompt-agent baseline",
			cap:    "distribution-engine",
			op:     "prompt-agent",
			args:   map[string]string{"name": "foo", "prompt": "hi", "wait": "true", "timeout": "5000"},
			expect: `herdr agent prompt foo "hi" --wait --timeout 5000`,
		},
		{
			name:   "scheduler heartbeat-arm baseline (inline fallback)",
			cap:    "scheduler",
			op:     "heartbeat-arm",
			args:   map[string]string{"interval": "25m", "prompt": "HEARTBEAT: run heartbeat scan"},
			expect: `CronCreate interval=25m prompt="HEARTBEAT: run heartbeat scan"`,
		},
		{
			name:   "signal send-to-agent baseline (L2 via shared playbook)",
			cap:    "signal",
			op:     "send-to-agent",
			args:   map[string]string{"target": "captain", "message": "task done"},
			expect: `herdr agent prompt captain "task done"`,
		},
		{
			name:   "start-agent kind auto-fill baseline",
			cap:    "distribution-engine",
			op:     "start-agent",
			args:   map[string]string{"name": "impl-auth", "pane": "w1:p5"},
			expect: "herdr agent start impl-auth --kind pi --pane w1:p5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := cfg.Resolve(tt.cap, tt.op, tt.args)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if result != tt.expect {
				t.Errorf("\nexpected: %s\n     got: %s", tt.expect, result)
			}
		})
	}
}

// Test GetCapability returns operations from L2.
func TestGetCapabilityFromL2(t *testing.T) {
	cfg := setupTestEnv(t)

	meta, err := cfg.GetCapability("distribution-engine")
	if err != nil {
		t.Fatalf("GetCapability: %v", err)
	}
	if meta["detail_playbook"] != "herdr-procedure" {
		t.Errorf("detail_playbook: %v", meta["detail_playbook"])
	}
	ops, ok := meta["operations"].([]string)
	if !ok {
		t.Fatalf("operations not []string: %T", meta["operations"])
	}
	// Check that known operations are present.
	opSet := make(map[string]bool, len(ops))
	for _, o := range ops {
		opSet[o] = true
	}
	for _, want := range []string{"spawn-pane", "start-agent", "prompt-agent", "list-agents"} {
		if !opSet[want] {
			t.Errorf("missing operation %q in %v", want, ops)
		}
	}
}

// Test that missing L2 playbook gives a clear error.
func TestMissingL2Playbook(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "capabilities.yaml", string(capabilities.DefaultConfig()))
	// Don't write the L2 playbook — it should fail.

	cfg, err := capabilities.Load(filepath.Join(dir, "capabilities.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.SetKBDir(filepath.Join(dir, "kb")) // empty dir

	_, err = cfg.Resolve("distribution-engine", "spawn-pane", map[string]string{
		"direction": "right", "cwd": "/tmp", "focus": "false",
	})
	if err == nil {
		t.Fatal("expected error for missing L2 playbook")
	}
	if !strings.Contains(err.Error(), "L2 detail playbook not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Test inline fallback (scheduler has no L2, templates in L1).
func TestInlineFallbackNoL2(t *testing.T) {
	cfg := setupTestEnv(t)

	// scheduler.heartbeat-cancel has no placeholders.
	result, err := cfg.Resolve("scheduler", "heartbeat-cancel", nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result != "CronCancel" {
		t.Errorf("expected CronCancel, got %s", result)
	}
}

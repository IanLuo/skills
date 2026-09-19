// Package knowledge implements the Knowledge Center — YAML read/write/parse
// for playbooks. It is the sole owner of config file schema.
// (SYSTEM-DESIGN R1, R2; ARCHITECTURE R3)
package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Step kinds. A step declares what it is, so the dispatcher never has to guess
// from its wording: check runs a shell command, ask is for the cap to confirm,
// say is worker context and not part of the gate.
const (
	KindCheck = "check"
	KindAsk   = "ask"
	KindSay   = "say"
)

// allowedKinds is the closed step vocabulary per playbook type. A type absent
// here (e.g. convention) carries no step-kind rule. routing is validated by
// validateRouting, which also constrains the step count and body.
//
// prerequisite guards entry and cleanup guards exit; both are gates, so both
// admit the same kinds. A gate that only speaks is not a gate.
var allowedKinds = map[string][]string{
	"prerequisite": {KindCheck, KindAsk},
	"cleanup":      {KindCheck, KindAsk},
	"procedure":    {KindSay, KindCheck},
}

// Step is one playbook step: a kind plus its body.
type Step struct {
	Kind string `json:"kind" yaml:"kind"`
	Body string `json:"body" yaml:"body"`
}

// Playbook is the structured config entity (SYSTEM-DESIGN R2).
type Playbook struct {
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type" yaml:"type"`
	Trigger     string `json:"trigger" yaml:"trigger"`
	Steps       []Step `json:"steps" yaml:"steps"`
	EngineScope string `json:"engine_scope,omitempty" yaml:"engine_scope,omitempty"`
}

// Center manages playbook files on disk.
type Center struct {
	dir string
}

// Open creates or opens a knowledge center at the given directory.
func Open(dir string) (*Center, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("knowledge: create dir %s: %w", dir, err)
	}
	return &Center{dir: dir}, nil
}

// Add writes a new playbook file. Returns error if it already exists.
func (c *Center) Add(name string, data []byte) error {
	if name == "" {
		return fmt.Errorf("knowledge: name is required")
	}

	pb, err := parsePlaybook(data)
	if err != nil {
		return err
	}
	// Ensure the name field matches.
	if pb.Name != "" && pb.Name != name {
		return fmt.Errorf("knowledge: name in file (%s) does not match --name (%s)", pb.Name, name)
	}
	if err := validatePlaybook(name, pb); err != nil {
		return err
	}

	path := c.path(name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("knowledge: playbook %q already exists", name)
	}

	return os.WriteFile(path, data, 0o644)
}

// Get reads and parses a playbook by name.
func (c *Center) Get(name string) (*Playbook, error) {
	if name == "" {
		return nil, fmt.Errorf("knowledge: name is required")
	}

	path := c.path(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("knowledge: playbook %q not found", name)
		}
		return nil, fmt.Errorf("knowledge: read %s: %w", name, err)
	}

	return parsePlaybook(data)
}

// Has reports whether a playbook named name exists on disk. It is how a caller
// tells an absent playbook, which a gate treats as no gate, from one that is
// present but unreadable, which a gate must refuse rather than skip.
func (c *Center) Has(name string) bool {
	if name == "" {
		return false
	}
	_, err := os.Stat(c.path(name))
	return err == nil
}

// List returns sorted names of all playbooks.
func (c *Center) List() ([]string, error) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return nil, fmt.Errorf("knowledge: list: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if name := playbookName(e.Name()); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// playbookName is a file name's playbook name, or "" when the file is not a
// playbook. It is the one definition of which files are playbooks, shared with
// the shipped defaults so a name means the same thing on both sides.
func playbookName(fileName string) string {
	for _, ext := range []string{".yaml", ".yml"} {
		if name, ok := strings.CutSuffix(fileName, ext); ok {
			return name
		}
	}
	return ""
}

// Edit overwrites an existing playbook. Returns error if it doesn't exist.
func (c *Center) Edit(name string, data []byte) error {
	if name == "" {
		return fmt.Errorf("knowledge: name is required")
	}

	pb, err := parsePlaybook(data)
	if err != nil {
		return err
	}
	if err := validatePlaybook(name, pb); err != nil {
		return err
	}

	path := c.path(name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("knowledge: playbook %q not found", name)
	}

	return os.WriteFile(path, data, 0o644)
}

// Remove deletes a playbook file.
func (c *Center) Remove(name string) error {
	if name == "" {
		return fmt.Errorf("knowledge: name is required")
	}

	path := c.path(name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("knowledge: playbook %q not found", name)
	}

	return os.Remove(path)
}

func (c *Center) path(name string) string {
	return filepath.Join(c.dir, name+".yaml")
}

// parsePlaybook parses YAML-like structured data into a Playbook.
// We use a minimal YAML parser to avoid external dependencies.
// Format:
//
//	name: value
//	type: value
//	trigger: value
//	steps:
//	  - check: test -f AGENTS.md
//	  - ask: is the acceptance check stated?
//	  - say: read AGENTS.md before editing
//	engine_scope: value
func parsePlaybook(data []byte) (*Playbook, error) {
	pb := &Playbook{}
	lines := strings.Split(string(data), "\n")
	inSteps := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if inSteps {
			if strings.HasPrefix(trimmed, "- ") {
				step := strings.TrimPrefix(trimmed, "- ")
				pb.Steps = append(pb.Steps, parseStep(step))
				continue
			}
			inSteps = false
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "name":
			pb.Name = val
		case "type":
			pb.Type = val
		case "trigger":
			pb.Trigger = val
		case "steps":
			inSteps = true
		case "engine_scope":
			pb.EngineScope = val
		}
	}

	if pb.Name == "" && pb.Type == "" && pb.Trigger == "" && len(pb.Steps) == 0 {
		return nil, fmt.Errorf("knowledge: invalid playbook — no recognizable fields")
	}

	return pb, nil
}

// parseStep splits a step line at its first ":". A leading token that names a
// known kind selects it; anything else — including a colon further into prose —
// defaults to say, so playbooks written before kinds existed keep parsing.
func parseStep(line string) Step {
	if kind, body, ok := strings.Cut(line, ":"); ok {
		if k := strings.TrimSpace(kind); isKind(k) {
			return Step{Kind: k, Body: strings.TrimSpace(body)}
		}
	}
	return Step{Kind: KindSay, Body: strings.TrimSpace(line)}
}

func isKind(kind string) bool {
	switch kind {
	case KindCheck, KindAsk, KindSay:
		return true
	}
	return false
}

// validatePlaybook enforces the closed step vocabulary for a playbook's type.
// It runs on the write path only: a playbook already on disk is read as-is.
func validatePlaybook(name string, pb *Playbook) error {
	if pb.Type == "routing" {
		return validateRouting(name, pb)
	}
	allowed, ok := allowedKinds[pb.Type]
	if !ok {
		return nil
	}
	for i, step := range pb.Steps {
		if !slices.Contains(allowed, step.Kind) {
			return fmt.Errorf(
				"knowledge: %s playbook %q step %d (%q) has kind %q; allowed kinds: %s",
				pb.Type, name, i+1, step.Body, step.Kind, strings.Join(allowed, ", "))
		}
	}
	return nil
}

// validateRouting holds a routing playbook to exactly one say step whose body
// is the skill name.
func validateRouting(name string, pb *Playbook) error {
	switch {
	case len(pb.Steps) == 0:
		return fmt.Errorf(
			"knowledge: routing playbook %q has no steps; a routing playbook is exactly one step — the skill name, kind %s",
			name, KindSay)
	case len(pb.Steps) > 1:
		return fmt.Errorf(
			"knowledge: routing playbook %q step 2 (%q) is extra; a routing playbook is exactly one step (allowed kinds: %s)",
			name, pb.Steps[1].Body, KindSay)
	}

	step := pb.Steps[0]
	if step.Kind != KindSay {
		return fmt.Errorf(
			"knowledge: routing playbook %q step (%q) has kind %q; a routing step is the skill name, kind %s (allowed kinds: %s)",
			name, step.Body, step.Kind, KindSay, KindSay)
	}
	if step.Body == "" || strings.ContainsAny(step.Body, " \t") {
		return fmt.Errorf(
			"knowledge: routing playbook %q step body %q must be a single skill name (allowed kinds: %s)",
			name, step.Body, KindSay)
	}
	return nil
}

// MarshalJSON implements custom JSON marshaling (for CLI output).
func (pb *Playbook) MarshalJSON() ([]byte, error) {
	type Alias Playbook
	return json.Marshal((*Alias)(pb))
}

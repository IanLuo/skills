// Package knowledge implements the Knowledge Center — YAML read/write/parse
// for playbooks. It is the sole owner of config file schema.
// (SYSTEM-DESIGN R1, R2; ARCHITECTURE R3)
package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Playbook is the structured config entity (SYSTEM-DESIGN R2).
type Playbook struct {
	Name        string   `json:"name" yaml:"name"`
	Type        string   `json:"type" yaml:"type"`
	Trigger     string   `json:"trigger" yaml:"trigger"`
	Steps       []string `json:"steps" yaml:"steps"`
	EngineScope string   `json:"engine_scope,omitempty" yaml:"engine_scope,omitempty"`
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
		n := e.Name()
		if strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml") {
			names = append(names, strings.TrimSuffix(strings.TrimSuffix(n, ".yaml"), ".yml"))
		}
	}
	sort.Strings(names)
	return names, nil
}

// Edit overwrites an existing playbook. Returns error if it doesn't exist.
func (c *Center) Edit(name string, data []byte) error {
	if name == "" {
		return fmt.Errorf("knowledge: name is required")
	}

	if _, err := parsePlaybook(data); err != nil {
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
//	  - step one
//	  - step two
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
				pb.Steps = append(pb.Steps, strings.TrimSpace(step))
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

// MarshalJSON implements custom JSON marshaling (for CLI output).
func (pb *Playbook) MarshalJSON() ([]byte, error) {
	type Alias Playbook
	return json.Marshal((*Alias)(pb))
}

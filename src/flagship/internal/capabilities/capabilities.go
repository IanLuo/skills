// Package capabilities resolves abstract capability operations to concrete
// commands via ~/.fs/capabilities.yaml (L1 interface) and L2 detail playbooks
// (~/.fs/kb/<name>.yaml). See DESIGN-capabilities.md §2-§4.
//
// L1 capabilities.yaml declares: tool binding + detail-playbook pointer.
// L2 detail playbooks hold ALL implementation: operations, command templates,
// flags, env-aliases, env-check, preconditions.
//
// Exception: capabilities with detail-playbook: "" keep operations inline in L1.
package capabilities

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level capabilities.yaml structure.
type Config struct {
	Version      int                    `yaml:"version"`
	Capabilities map[string]*Capability `yaml:"capabilities"`

	// l2Cache caches loaded L2 playbook impl sections keyed by playbook name.
	l2Cache map[string]*L2Playbook

	// kbDir is the directory where L2 playbooks are read from (default: ~/.fs/kb/).
	kbDir string
}

// Capability is a single capability block in L1.
type Capability struct {
	Name           string `yaml:"name"`
	DetailPlaybook string `yaml:"detail-playbook"`

	// Inline fields — only used when detail-playbook is "" (inline fallback).
	Operations map[string]string `yaml:"operations"`

	// coding-agent is a value provider, not a command producer.
	KindDefault string   `yaml:"kind-default"`
	KindList    []string `yaml:"kind-list"`
}

// L2Playbook is the parsed L2 detail playbook (only the capability-impl section).
type L2Playbook struct {
	CapabilityImpl map[string]*L2CapImpl `yaml:"capability-impl"`
}

// L2CapImpl is the per-capability implementation section in an L2 playbook.
type L2CapImpl struct {
	EnvCheck      string                       `yaml:"env-check"`
	Preconditions []map[string]string          `yaml:"preconditions"`
	Flags         map[string]map[string]string `yaml:"flags"`
	Operations    map[string]string            `yaml:"operations"`
	EnvAliases    map[string]string            `yaml:"env-aliases"`
}

// DefaultConfigPath returns ~/.fs/capabilities.yaml.
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".fs", "capabilities.yaml")
}

// DefaultKBDir returns ~/.fs/kb/.
func DefaultKBDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".fs", "kb")
}

// Load reads and parses a capabilities config from disk.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("capabilities config not found at %s — run 'fs resolve --init' to create a default", path)
		}
		return nil, fmt.Errorf("capabilities: read %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("capabilities: parse %s: %w", path, err)
	}

	if cfg.Capabilities == nil {
		return nil, fmt.Errorf("capabilities: no capabilities defined in %s", path)
	}

	cfg.l2Cache = make(map[string]*L2Playbook)
	cfg.kbDir = DefaultKBDir()

	return &cfg, nil
}

// SetKBDir overrides the directory for L2 playbook resolution (for testing).
func (c *Config) SetKBDir(dir string) {
	c.kbDir = dir
}

// Check validates that all capabilities are present. If a capability points to
// an L2 playbook, validates that the playbook file exists and is parseable.
func (c *Config) Check() ([]string, error) {
	if len(c.Capabilities) == 0 {
		return nil, fmt.Errorf("no capabilities defined")
	}

	var names []string
	for name := range c.Capabilities {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// loadL2 loads and caches the L2 detail playbook by name.
func (c *Config) loadL2(playbookName string) (*L2Playbook, error) {
	if pb, ok := c.l2Cache[playbookName]; ok {
		return pb, nil
	}

	path := filepath.Join(c.kbDir, playbookName+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("L2 detail playbook not found at %s", path)
		}
		return nil, fmt.Errorf("L2 playbook: read %s: %w", path, err)
	}

	var pb L2Playbook
	if err := yaml.Unmarshal(data, &pb); err != nil {
		return nil, fmt.Errorf("L2 playbook: parse %s: %w", path, err)
	}

	c.l2Cache[playbookName] = &pb
	return &pb, nil
}

// getImpl resolves the implementation (template, flags, env-aliases) for a
// capability+operation. Reads from L2 if detail-playbook is set, else from
// inline L1 operations.
func (c *Config) getImpl(capName string, cap *Capability, opName string) (template string, flags map[string]map[string]string, envAliases map[string]string, err error) {
	if cap.DetailPlaybook != "" {
		// L2 resolution.
		l2, err := c.loadL2(cap.DetailPlaybook)
		if err != nil {
			return "", nil, nil, fmt.Errorf("%s.%s: %w", capName, opName, err)
		}
		if l2.CapabilityImpl == nil {
			return "", nil, nil, fmt.Errorf("%s.%s: L2 playbook %q has no capability-impl section", capName, opName, cap.DetailPlaybook)
		}
		impl, ok := l2.CapabilityImpl[capName]
		if !ok {
			avail := make([]string, 0, len(l2.CapabilityImpl))
			for k := range l2.CapabilityImpl {
				avail = append(avail, k)
			}
			sort.Strings(avail)
			return "", nil, nil, fmt.Errorf("%s.%s: capability %q not found in L2 playbook %q (available: %s)", capName, opName, capName, cap.DetailPlaybook, strings.Join(avail, ", "))
		}
		tmpl, ok := impl.Operations[opName]
		if !ok {
			avail := make([]string, 0, len(impl.Operations))
			for k := range impl.Operations {
				avail = append(avail, k)
			}
			sort.Strings(avail)
			return "", nil, nil, fmt.Errorf("unknown operation %q on capability %q (available: %s)", opName, capName, strings.Join(avail, ", "))
		}
		return tmpl, impl.Flags, impl.EnvAliases, nil
	}

	// Inline fallback (detail-playbook is "").
	if cap.Operations == nil {
		return "", nil, nil, fmt.Errorf("capability %q has no operations (no detail-playbook and no inline operations)", capName)
	}
	tmpl, ok := cap.Operations[opName]
	if !ok {
		avail := make([]string, 0, len(cap.Operations))
		for k := range cap.Operations {
			avail = append(avail, k)
		}
		sort.Strings(avail)
		return "", nil, nil, fmt.Errorf("unknown operation %q on capability %q (available: %s)", opName, capName, strings.Join(avail, ", "))
	}
	// Inline capabilities have no flags or env-aliases.
	return tmpl, nil, nil, nil
}

// getOperationNames returns sorted operation names for a capability.
// Reads from L2 if detail-playbook is set, else from inline L1.
func (c *Config) getOperationNames(capName string, cap *Capability) ([]string, error) {
	if cap.DetailPlaybook != "" {
		l2, err := c.loadL2(cap.DetailPlaybook)
		if err != nil {
			return nil, err
		}
		if l2.CapabilityImpl != nil {
			if impl, ok := l2.CapabilityImpl[capName]; ok {
				ops := make([]string, 0, len(impl.Operations))
				for k := range impl.Operations {
					ops = append(ops, k)
				}
				sort.Strings(ops)
				return ops, nil
			}
		}
		return nil, nil
	}
	// Inline fallback.
	if cap.Operations != nil {
		ops := make([]string, 0, len(cap.Operations))
		for k := range cap.Operations {
			ops = append(ops, k)
		}
		sort.Strings(ops)
		return ops, nil
	}
	return nil, nil
}

// GetCapability returns metadata for a capability (name, detail-playbook, operations).
func (c *Config) GetCapability(capName string) (map[string]any, error) {
	cap, ok := c.Capabilities[capName]
	if !ok {
		avail := make([]string, 0, len(c.Capabilities))
		for k := range c.Capabilities {
			avail = append(avail, k)
		}
		sort.Strings(avail)
		return nil, fmt.Errorf("unknown capability %q (available: %s)", capName, strings.Join(avail, ", "))
	}

	result := map[string]any{
		"capability":      capName,
		"name":            cap.Name,
		"detail_playbook": cap.DetailPlaybook,
	}

	ops, err := c.getOperationNames(capName, cap)
	if err != nil {
		return nil, err
	}
	if len(ops) > 0 {
		result["operations"] = ops
	}

	return result, nil
}

// EngineName returns the bound distribution-engine name (e.g. "herdr").
func (c *Config) EngineName() string {
	if cap, ok := c.Capabilities["distribution-engine"]; ok {
		return cap.Name
	}
	return ""
}

// placeholderRE matches {placeholder} patterns.
var placeholderRE = regexp.MustCompile(`\{([a-z_]+)\}`)

// Resolve resolves capability.operation with the given args into a concrete command.
func (c *Config) Resolve(capName, opName string, args map[string]string) (string, error) {
	cap, ok := c.Capabilities[capName]
	if !ok {
		avail := make([]string, 0, len(c.Capabilities))
		for k := range c.Capabilities {
			avail = append(avail, k)
		}
		sort.Strings(avail)
		return "", fmt.Errorf("unknown capability %q (available: %s)", capName, strings.Join(avail, ", "))
	}

	// coding-agent is a value provider — handle special ops.
	if capName == "coding-agent" {
		return c.resolveCodingAgent(cap, opName)
	}

	// Resolve template, flags, and env-aliases from L2 (or inline fallback).
	template, flags, envAliases, err := c.getImpl(capName, cap, opName)
	if err != nil {
		return "", err
	}

	// Auto-fill {kind} from coding-agent.kind-default if not provided.
	if _, hasKind := args["kind"]; !hasKind {
		if strings.Contains(template, "{kind}") {
			if ca, ok := c.Capabilities["coding-agent"]; ok && ca.KindDefault != "" {
				if args == nil {
					args = make(map[string]string)
				}
				args["kind"] = ca.KindDefault
			}
		}
	}

	// Expand env aliases in args: $PANE_ID → value of $HERDR_PANE_ID.
	args = expandEnvAliases(envAliases, args)

	// Substitute placeholders.
	result, err := substitute(flags, template, args)
	if err != nil {
		return "", fmt.Errorf("%s.%s: %w", capName, opName, err)
	}

	return result, nil
}

// resolveCodingAgent handles value-provider operations.
func (c *Config) resolveCodingAgent(cap *Capability, opName string) (string, error) {
	switch opName {
	case "kind-default":
		if cap.KindDefault == "" {
			return "", fmt.Errorf("coding-agent.kind-default not configured")
		}
		return cap.KindDefault, nil
	case "kind-list":
		if len(cap.KindList) == 0 {
			return "", fmt.Errorf("coding-agent.kind-list not configured")
		}
		return strings.Join(cap.KindList, ", "), nil
	default:
		return "", fmt.Errorf("unknown operation %q on coding-agent (available: kind-default, kind-list)", opName)
	}
}

// expandEnvAliases resolves env-alias mapped values. When an arg value starts
// with $, check if it's a canonical alias and resolve to the real env var value.
func expandEnvAliases(envAliases map[string]string, args map[string]string) map[string]string {
	if envAliases == nil || args == nil {
		return args
	}

	result := make(map[string]string, len(args))
	for k, v := range args {
		if strings.HasPrefix(v, "$") {
			varName := v[1:]
			if realVar, ok := envAliases[varName]; ok {
				result[k] = os.Getenv(realVar)
				continue
			}
			// Not an alias — try as a literal env var (§10.3 fallback).
			if envVal := os.Getenv(varName); envVal != "" {
				result[k] = envVal
				continue
			}
		}
		result[k] = v
	}
	return result
}

// quotedPlaceholderRE matches "{placeholder}" patterns (placeholder inside double-quotes in template).
var quotedPlaceholderRE = regexp.MustCompile(`"\{([a-z_]+)\}"`)

// substitute replaces {placeholder} tokens in a template with arg values,
// applying flag expansion and shell-escaping.
func substitute(flags map[string]map[string]string, template string, args map[string]string) (string, error) {
	// Find all placeholders in the template.
	matches := placeholderRE.FindAllStringSubmatch(template, -1)
	needed := make(map[string]bool)
	for _, m := range matches {
		needed[m[1]] = true
	}

	// Check all needed placeholders have values (after flag expansion).
	var missing []string
	for ph := range needed {
		_, inArgs := args[ph]
		_, isFlag := flags[ph]
		// A _flag placeholder is also satisfiable by the base-name arg.
		if !inArgs && strings.HasSuffix(ph, "_flag") {
			base := strings.TrimSuffix(ph, "_flag")
			_, inArgs = args[base]
		}
		if !inArgs && !isFlag {
			missing = append(missing, ph)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("missing required keys: %s", strings.Join(missing, ", "))
	}

	// Identify which placeholders are already inside "..." in the template.
	quotedPHs := make(map[string]bool)
	for _, m := range quotedPlaceholderRE.FindAllStringSubmatch(template, -1) {
		quotedPHs[m[1]] = true
	}

	// First pass: replace "{ph}" → quoted value (preserving template quotes).
	result := quotedPlaceholderRE.ReplaceAllStringFunc(template, func(match string) string {
		ph := match[2 : len(match)-2] // strip " { } "
		if v, ok := args[ph]; ok {
			return `"` + escapeForDoubleQuotes(v) + `"`
		}
		return match
	})

	// Second pass: replace remaining {ph} (not inside quotes).
	result = placeholderRE.ReplaceAllStringFunc(result, func(match string) string {
		ph := match[1 : len(match)-1] // strip { }

		if quotedPHs[ph] {
			return match
		}

		// Check if this is a flag placeholder.
		if flagMap, ok := flags[ph]; ok {
			argVal := args[ph]
			if argVal == "" && strings.HasSuffix(ph, "_flag") {
				base := strings.TrimSuffix(ph, "_flag")
				argVal = args[base]
			}
			if argVal == "" {
				argVal = "false" // default for boolean flags
			}
			if expansion, ok := flagMap[argVal]; ok {
				return expandFlagInner(expansion, args)
			}
			// Boolean coercion for non-boolean values.
			_, hasTrue := flagMap["true"]
			_, hasFalse := flagMap["false"]
			if hasTrue && hasFalse && argVal != "true" && argVal != "false" {
				return expandFlagInner(flagMap["true"], args)
			}
			return shellEscape(argVal)
		}

		// Regular placeholder.
		if v, ok := args[ph]; ok {
			return shellEscape(v)
		}
		return match
	})

	result = collapseSpaces(result)
	return result, nil
}

// expandFlagInner substitutes inner placeholders in a flag expansion string.
func expandFlagInner(expansion string, args map[string]string) string {
	return placeholderRE.ReplaceAllStringFunc(expansion, func(innerMatch string) string {
		innerPH := innerMatch[1 : len(innerMatch)-1]
		if v, ok := args[innerPH]; ok {
			return shellEscape(v)
		}
		return innerMatch
	})
}

// escapeForDoubleQuotes escapes a string for use inside double quotes.
func escapeForDoubleQuotes(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch c {
		case '"', '\\', '$', '`':
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// shellEscape wraps a value in single quotes if it contains shell metacharacters.
func shellEscape(s string) string {
	if s == "" {
		return s
	}
	safe := true
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '/' || c == ':' || c == ',' || c == '=' || c == '+' || c == '@') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	escaped := strings.ReplaceAll(s, "'", `'\''`)
	return "'" + escaped + "'"
}

// collapseSpaces collapses runs of spaces into a single space and trims.
func collapseSpaces(s string) string {
	parts := strings.Fields(s)
	return strings.Join(parts, " ")
}

// DefaultConfig returns the default capabilities.yaml content (L1 interface-only).
func DefaultConfig() []byte {
	return []byte(`# ~/.fs/capabilities.yaml — INTERFACE / DECLARATION ONLY
# Each capability declares: tool binding + detail-playbook pointer.
# ALL implementation detail (operations, templates, flags, env-aliases,
# env-check, preconditions) lives in the L2 detail-playbook.
# Exception: capabilities with detail-playbook: "" keep operations inline.

version: 1

capabilities:

  distribution-engine:
    name: herdr
    detail-playbook: herdr-procedure

  coding-agent:
    name: pi
    detail-playbook: ""
    kind-default: pi
    kind-list:
      - pi
      - claude
      - codex
      - copilot

  scheduler:
    name: claude-cron
    detail-playbook: ""
    operations:
      heartbeat-arm: >-
        CronCreate interval={interval} prompt="{prompt}"
      heartbeat-cancel: >-
        CronCancel

  signal:
    name: herdr-prompt
    detail-playbook: herdr-procedure
`)
}

// WriteDefaultConfig writes the default config to path, creating parent dirs.
func WriteDefaultConfig(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("capabilities: create dir %s: %w", dir, err)
	}
	return os.WriteFile(path, DefaultConfig(), 0o644)
}

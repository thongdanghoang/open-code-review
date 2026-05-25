// Package rules loads system review rules and matches file paths against glob patterns.
package rules

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Resolver resolves a review rule for a file path.
type Resolver interface {
	Resolve(path string) string
}

// PathRule is a single pattern→rule entry preserving declaration order.
type PathRule struct {
	Pattern string
	Rule    string
}

// SystemRule holds review rules loaded from an external JSON config.
type SystemRule struct {
	DefaultRule string     `json:"default_rule"`
	PathRules   []PathRule // ordered; first match wins
}

// UnmarshalJSON preserves the key order from JSON's path_rule_map object.
func (r *SystemRule) UnmarshalJSON(data []byte) error {
	// Decode default_rule normally.
	var wrapper struct {
		DefaultRule string `json:"default_rule"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return err
	}
	r.DefaultRule = wrapper.DefaultRule

	// Use json.Decoder with UseNumber to preserve order of path_rule_map keys.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	mapData, ok := raw["path_rule_map"]
	if !ok || len(mapData) == 0 || string(mapData) == "null" {
		return nil
	}

	// Parse ordered keys using a streaming decoder.
	dec := json.NewDecoder(strings.NewReader(string(mapData)))
	// Read opening '{'
	t, err := dec.Token()
	if err != nil {
		return fmt.Errorf("expected '{' in path_rule_map: %w", err)
	}
	if t != json.Delim('{') {
		return fmt.Errorf("expected '{' in path_rule_map, got %v", t)
	}
	for dec.More() {
		// Read key
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("read path_rule_map key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("expected string key in path_rule_map, got %T", keyTok)
		}
		// Read value
		var value string
		if err := dec.Decode(&value); err != nil {
			return fmt.Errorf("read path_rule_map value for %q: %w", key, err)
		}
		r.PathRules = append(r.PathRules, PathRule{Pattern: key, Rule: value})
	}
	return nil
}

//go:embed system_rules.json
var defaultSystemRules []byte

// LoadDefault parses the embedded system_rules.json.
func LoadDefault() (*SystemRule, error) {
	var rule SystemRule
	if err := json.Unmarshal(defaultSystemRules, &rule); err != nil {
		return nil, fmt.Errorf("unmarshal default system rules: %w", err)
	}
	return &rule, nil
}

// Resolve returns the rule text for a given file path.
// Patterns with brace expansion like "*.{go,py}" are expanded into "*.go", "*.py".
// The first match wins; if none match, it falls back to DefaultRule.
// Supports full glob syntax including ** for recursive directory matching.
func (r *SystemRule) Resolve(path string) string {
	for _, pr := range r.PathRules {
		expanded := expandBraces(pr.Pattern)
		for _, p := range expanded {
			if matched, _ := doublestar.Match(p, path); matched {
				return pr.Rule
			}
		}
	}
	return r.DefaultRule
}

// expandBraces turns "{a,b,c}" style patterns into individual strings.
// e.g. "*.go.{java,kotlin}" → ["*.go.java", "*.go.kotlin"].
// If no braces exist, returns the original pattern unchanged.
func expandBraces(s string) []string {
	openIdx := strings.IndexByte(s, '{')
	if openIdx < 0 {
		return []string{s}
	}

	closeIdx := strings.IndexByte(s[openIdx:], '}')
	if closeIdx < 0 {
		return []string{s}
	}
	closeIdx += openIdx

	prefix := s[:openIdx]
	suffix := s[closeIdx+1:]
	options := strings.Split(s[openIdx+1:closeIdx], ",")

	results := make([]string, 0, len(options))
	for _, opt := range options {
		results = append(results, prefix+opt+suffix)
	}
	return results
}

// ProjectRuleEntry is a single entry in .open-code-review/rule.json.
type ProjectRuleEntry struct {
	Path string `json:"path"`
	Rule string `json:"rule"`
}

// ProjectRule holds rules loaded from <repoDir>/.open-code-review/rule.json.
type ProjectRule struct {
	Rules []ProjectRuleEntry `json:"rules"`
}

// composedResolver implements Resolver with layered priority.
type composedResolver struct {
	custom  *ProjectRule // highest: --rule flag
	project *ProjectRule // high: .open-code-review/rule.json
	global  *ProjectRule // low: ~/.open-code-review/rule.json
	system  *SystemRule  // lowest: embedded default
}

// NewResolver builds a Resolver with the following priority:
//  1. Custom rule file specified via --rule flag (first match wins)
//  2. Project-local .open-code-review/rule.json (first match wins)
//  3. Global ~/.open-code-review/rule.json (first match wins)
//  4. Embedded system default rules
func NewResolver(repoDir, customRulePath string) (Resolver, error) {
	sysRule, err := LoadDefault()
	if err != nil {
		return nil, err
	}

	var customRule *ProjectRule
	if customRulePath != "" {
		cr, err := loadRuleFile(customRulePath)
		if err != nil {
			return nil, err
		}
		customRule = cr
	}

	var projectRule *ProjectRule
	if repoDir != "" {
		pr, err := loadProjectRule(repoDir)
		if err != nil {
			return nil, err
		}
		projectRule = pr
	}

	globalRule, err := loadGlobalRule()
	if err != nil {
		return nil, err
	}

	return &composedResolver{custom: customRule, project: projectRule, global: globalRule, system: sysRule}, nil
}

func loadGlobalRule() (*ProjectRule, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	path := filepath.Join(home, ".open-code-review", "rule.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read global rule %s: %w", path, err)
	}
	var pr ProjectRule
	if err := json.Unmarshal(data, &pr); err != nil {
		return nil, fmt.Errorf("unmarshal global rule: %w", err)
	}
	return &pr, nil
}

func loadRuleFile(path string) (*ProjectRule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rule file %s: %w", path, err)
	}
	var pr ProjectRule
	if err := json.Unmarshal(data, &pr); err != nil {
		return nil, fmt.Errorf("unmarshal rule file %s: %w", path, err)
	}
	return &pr, nil
}

func loadProjectRule(repoDir string) (*ProjectRule, error) {
	path := filepath.Join(repoDir, ".open-code-review", "rule.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read project rule %s: %w", path, err)
	}
	var pr ProjectRule
	if err := json.Unmarshal(data, &pr); err != nil {
		return nil, fmt.Errorf("unmarshal project rule: %w", err)
	}
	return &pr, nil
}

// Resolve checks each layer in priority order; first match wins.
func (c *composedResolver) Resolve(path string) string {
	if rule := matchProjectRule(c.custom, path); rule != "" {
		return rule
	}
	if rule := matchProjectRule(c.project, path); rule != "" {
		return rule
	}
	if rule := matchProjectRule(c.global, path); rule != "" {
		return rule
	}
	return c.system.Resolve(path)
}

func matchProjectRule(pr *ProjectRule, path string) string {
	if pr == nil {
		return ""
	}
	for _, entry := range pr.Rules {
		expanded := expandBraces(entry.Path)
		for _, p := range expanded {
			if matched, _ := doublestar.Match(p, path); matched {
				return entry.Rule
			}
		}
	}
	return ""
}

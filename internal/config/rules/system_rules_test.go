package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandBraces_NoBraces(t *testing.T) {
	got := expandBraces("*.java")
	if len(got) != 1 || got[0] != "*.java" {
		t.Errorf("expected [*.java], got %v", got)
	}
}

func TestExpandBraces_SingleGroup(t *testing.T) {
	got := expandBraces("*.{go,py}")
	want := []string{"*.go", "*.py"}
	if len(got) != len(want) {
		t.Fatalf("expected %d items, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestExpandBraces_MultipleOptions(t *testing.T) {
	got := expandBraces("**/*.{ts,js,tsx,jsx}")
	want := []string{"**/*.ts", "**/*.js", "**/*.tsx", "**/*.jsx"}
	if len(got) != len(want) {
		t.Fatalf("expected %d items, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestExpandBraces_UnclosedBrace(t *testing.T) {
	got := expandBraces("*.{go,py")
	if len(got) != 1 || got[0] != "*.{go,py" {
		t.Errorf("expected original pattern, got %v", got)
	}
}

func TestResolve_DefaultRules(t *testing.T) {
	rule, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}

	tests := []struct {
		path       string
		wantSubstr string // substring that should appear in the matched rule
	}{
		{"src/main/java/com/example/foo.java", "逻辑错误识别"},
		{"foo.java", "逻辑错误识别"},
		{"internal/agent/agent.go", "逻辑问题"},
		{"scripts/deploy.py", "逻辑问题"},
		{"src/main/resources/mapper/usermapper.xml", "SQL逻辑错误识别"},
		{"src/main/resources/dao/userdao.xml", "SQL逻辑错误识别"},
		{"pom.xml", "snapshot"},
		{"submodule/pom.xml", "snapshot"},
		{"src/main/resources/application.properties", "配置错误识别"},
		{"frontend/package.json", "latest"},
		{"config/app.yaml", "yaml-key"},
		{"deploy/values.yml", "yaml-key"},
		{"src/components/app.tsx", "React"},
		{"lib/utils.ts", "TypeScript"},
		{"app.kt", "空安全"},
		{"src/main/handler.cpp", "智能指针"},
		{"driver.c", "malloc"},
		{"ios/ViewController.m", "数组越界"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := rule.Resolve(tt.path)
			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("Resolve(%q): expected rule containing %q, got %q",
					tt.path, tt.wantSubstr, truncate(got, 80))
			}
		})
	}
}

func TestResolve_FallbackToDefault(t *testing.T) {
	rule, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}

	paths := []string{
		"readme.md",
		"docs/architecture.txt",
		"Makefile",
		"src/unknown.rs",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			got := rule.Resolve(path)
			if got != rule.DefaultRule {
				t.Errorf("Resolve(%q): expected DefaultRule, got %q", path, truncate(got, 80))
			}
		})
	}
}

func TestResolve_CustomRule_FirstMatchWins(t *testing.T) {
	rule := &SystemRule{
		DefaultRule: "default",
		PathRules: []PathRule{
			{Pattern: "**/special.java", Rule: "special-rule"},
			{Pattern: "**/*.java", Rule: "java-rule"},
		},
	}

	// special.java matches both patterns, but "special-rule" is first.
	got := rule.Resolve("src/special.java")
	if got != "special-rule" {
		t.Errorf("expected special-rule, got %q", got)
	}

	// Other java files match the second pattern.
	got = rule.Resolve("src/foo.java")
	if got != "java-rule" {
		t.Errorf("expected java-rule, got %q", got)
	}
}

func TestResolve_CustomRule_DefaultFallback(t *testing.T) {
	rule := &SystemRule{
		DefaultRule: "fallback-rule",
		PathRules: []PathRule{
			{Pattern: "**/*.java", Rule: "java-rule"},
		},
	}

	got := rule.Resolve("main.go")
	if got != "fallback-rule" {
		t.Errorf("expected fallback-rule, got %q", got)
	}
}

func TestResolve_CaseSensitivity(t *testing.T) {
	rule := &SystemRule{
		DefaultRule: "default",
		PathRules: []PathRule{
			{Pattern: "**/*.java", Rule: "java-rule"},
		},
	}

	// agent.go calls strings.ToLower(newPath) before Resolve,
	// so uppercase extensions should NOT match if not lowercased.
	got := rule.Resolve("Foo.Java")
	if got != "default" {
		t.Errorf("expected default for uppercase extension, got %q", got)
	}

	got = rule.Resolve("foo.java")
	if got != "java-rule" {
		t.Errorf("expected java-rule for lowercase, got %q", got)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func TestNewResolver_DefaultOnly(t *testing.T) {
	resolver, err := NewResolver(t.TempDir(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	got := resolver.Resolve("src/main.java")
	if !strings.Contains(got, "逻辑错误识别") {
		t.Errorf("expected system default java rule, got %q", truncate(got, 80))
	}
}

func TestNewResolver_ProjectFileMissing(t *testing.T) {
	resolver, err := NewResolver(t.TempDir(), "")
	if err != nil {
		t.Fatalf("NewResolver should not fail when project rule is missing: %v", err)
	}
	got := resolver.Resolve("readme.md")
	if got == "" {
		t.Errorf("expected non-empty default rule")
	}
}

func TestNewResolver_ProjectRuleHighestPriority(t *testing.T) {
	dir := t.TempDir()
	ocrDir := filepath.Join(dir, ".open-code-review")
	if err := os.MkdirAll(ocrDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ruleJSON := `{"rules":[{"path":"force-api/**/*.java","rule":"project-java-rule"}]}`
	if err := os.WriteFile(filepath.Join(ocrDir, "rule.json"), []byte(ruleJSON), 0o644); err != nil {
		t.Fatalf("write rule.json: %v", err)
	}

	resolver, err := NewResolver(dir, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	tests := []struct {
		path string
		want string
	}{
		{"force-api/src/foo.java", "project-java-rule"},
		{"other/src/bar.java", "逻辑错误识别"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := resolver.Resolve(tt.path)
			if !strings.Contains(got, tt.want) {
				t.Errorf("Resolve(%q) = %q, want containing %q", tt.path, truncate(got, 80), tt.want)
			}
		})
	}
}

func TestNewResolver_ProjectRuleFallsBackToSystem(t *testing.T) {
	dir := t.TempDir()
	ocrDir := filepath.Join(dir, ".open-code-review")
	if err := os.MkdirAll(ocrDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ruleJSON := `{"rules":[{"path":"special/**/*.go","rule":"special-go-rule"}]}`
	if err := os.WriteFile(filepath.Join(ocrDir, "rule.json"), []byte(ruleJSON), 0o644); err != nil {
		t.Fatalf("write rule.json: %v", err)
	}

	resolver, err := NewResolver(dir, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	got := resolver.Resolve("other/main.go")
	if !strings.Contains(got, "逻辑问题") {
		t.Errorf("expected system go rule, got %q", truncate(got, 80))
	}
}

func TestNewResolver_CustomRuleOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	customRule := `{"rules":[{"path":"**/*.go","rule":"custom-go-rule"}]}`
	customPath := filepath.Join(dir, "custom_rules.json")
	if err := os.WriteFile(customPath, []byte(customRule), 0o644); err != nil {
		t.Fatalf("write custom rule: %v", err)
	}

	resolver, err := NewResolver(t.TempDir(), customPath)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	got := resolver.Resolve("main.go")
	if got != "custom-go-rule" {
		t.Errorf("expected custom-go-rule, got %q", got)
	}
	// --rule not matched → falls through to system default
	got = resolver.Resolve("readme.md")
	if !strings.Contains(got, "错别字") {
		t.Errorf("expected system default rule, got %q", truncate(got, 80))
	}
}

func TestNewResolver_CustomOverridesProject(t *testing.T) {
	// Setup --rule file (highest priority)
	customDir := t.TempDir()
	customRule := `{"rules":[{"path":"**/*.java","rule":"custom-java-rule"}]}`
	customPath := filepath.Join(customDir, "custom_rules.json")
	if err := os.WriteFile(customPath, []byte(customRule), 0o644); err != nil {
		t.Fatalf("write custom rule: %v", err)
	}

	// Setup project rule with narrower pattern
	repoDir := t.TempDir()
	ocrDir := filepath.Join(repoDir, ".open-code-review")
	if err := os.MkdirAll(ocrDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	projRule := `{"rules":[{"path":"force-api/**/*.java","rule":"project-java-rule"},{"path":"**/*.go","rule":"project-go-rule"}]}`
	if err := os.WriteFile(filepath.Join(ocrDir, "rule.json"), []byte(projRule), 0o644); err != nil {
		t.Fatalf("write rule.json: %v", err)
	}

	resolver, err := NewResolver(repoDir, customPath)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	tests := []struct {
		path string
		want string
	}{
		{"force-api/src/foo.java", "custom-java-rule"}, // --rule wins (highest priority)
		{"other/src/bar.java", "custom-java-rule"},     // --rule wins
		{"main.go", "project-go-rule"},                 // --rule misses → project wins
		{"readme.md", "错别字"},                           // all miss → system default
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := resolver.Resolve(tt.path)
			if !strings.Contains(got, tt.want) {
				t.Errorf("Resolve(%q) = %q, want containing %q", tt.path, truncate(got, 80), tt.want)
			}
		})
	}
}

func TestNewResolver_ProjectFileMalformed(t *testing.T) {
	dir := t.TempDir()
	ocrDir := filepath.Join(dir, ".open-code-review")
	if err := os.MkdirAll(ocrDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ocrDir, "rule.json"), []byte("{invalid json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := NewResolver(dir, "")
	if err == nil {
		t.Errorf("expected error for malformed project rule.json")
	}
}

func TestNewResolver_BraceExpansionInProjectRule(t *testing.T) {
	dir := t.TempDir()
	ocrDir := filepath.Join(dir, ".open-code-review")
	if err := os.MkdirAll(ocrDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ruleJSON := `{"rules":[{"path":"src/**/*.{java,kt}","rule":"jvm-rule"}]}`
	if err := os.WriteFile(filepath.Join(ocrDir, "rule.json"), []byte(ruleJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	resolver, err := NewResolver(dir, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	tests := []struct {
		path string
		want string
	}{
		{"src/main/foo.java", "jvm-rule"},
		{"src/main/bar.kt", "jvm-rule"},
		{"src/main/baz.go", "逻辑问题"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := resolver.Resolve(tt.path)
			if !strings.Contains(got, tt.want) {
				t.Errorf("Resolve(%q) = %q, want containing %q", tt.path, truncate(got, 80), tt.want)
			}
		})
	}
}

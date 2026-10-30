package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	t.Setenv("PATH", "") // deterministic detection: only config dirs count
	return home
}

func TestParseAgentSelection(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"", 0, false},
		{"all", 4, false},
		{"none", 0, false},
		{"claude,zcode", 2, false},
		{" claude , KIMI ", 2, false},
		{"chatgpt", 0, true},
	}
	for _, c := range cases {
		got, err := parseAgentSelection(c.in)
		if c.in == "" {
			if err != nil || got != nil {
				t.Fatalf("%q: want nil,nil got %v,%v", c.in, got, err)
			}
			continue
		}
		if c.wantErr {
			if err == nil {
				t.Fatalf("%q: want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if len(got) != c.want {
			t.Fatalf("%q: got %d entries, want %d", c.in, len(got), c.want)
		}
	}
}

func TestDiscoverAgentsFromConfigDirs(t *testing.T) {
	home := testHome(t)
	for _, d := range []string{".claude", ".zcode", ".kimi-code", ".dsh"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	specs := discoverAgents(home)
	if len(specs) != 4 {
		t.Fatalf("got %d specs", len(specs))
	}
	for _, s := range specs {
		if !s.Detected {
			t.Errorf("%s not detected from its config dir", s.ID)
		}
	}
	if got := len(detectedAgents(specs)); got != 4 {
		t.Fatalf("detectedAgents = %d", got)
	}
}

func TestClaudeHooksIdempotentAndReversible(t *testing.T) {
	home := testHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	original := `{"env":{"KEEP":"1"},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo custom"}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := discoverAgents(home)[0]
	for i := 0; i < 2; i++ {
		a := wireClaude(spec, true)
		if a.Warning != "" {
			t.Fatalf("run %d: %s", i, a.Warning)
		}
	}
	data, _ := os.ReadFile(path)
	if n := strings.Count(string(data), agentbusMarker); n != 5 {
		t.Fatalf("agentbus hooks = %d, want 5\n%s", n, data)
	}
	if !strings.Contains(string(data), "echo custom") {
		t.Fatal("custom hook was removed")
	}
	if !strings.Contains(string(data), `"KEEP": "1"`) && !strings.Contains(string(data), `"KEEP":"1"`) {
		// JSON may or may not be re-indented on the first write; the key must survive.
		t.Fatalf("unrelated config was lost: %s", data)
	}
	wireClaude(spec, false)
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), agentbusMarker) {
		t.Fatalf("uninstall left agentbus hooks: %s", data)
	}
	if !strings.Contains(string(data), "echo custom") {
		t.Fatalf("uninstall removed the custom hook: %s", data)
	}
	if fileExists(path + ".agentbus-bak") {
		backup, _ := os.ReadFile(path + ".agentbus-bak")
		if string(backup) != original {
			t.Fatalf("backup is not the original file: %s", backup)
		}
	}
}

func TestZCodeHooks(t *testing.T) {
	home := testHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".zcode", "cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".zcode", "cli", "config.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"enabled":false,"events":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := discoverAgents(home)[1]
	if a := wireZcode(spec, true); a.Warning != "" {
		t.Fatal(a.Warning)
	}
	var cfg map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	hooks := cfg["hooks"].(map[string]any)
	if hooks["enabled"] != true {
		t.Fatal("hooks.enabled was not set")
	}
	events := hooks["events"].(map[string]any)
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"} {
		if _, ok := events[ev]; !ok {
			t.Errorf("missing event %s", ev)
		}
	}
	wireZcode(spec, true)
	data, _ = os.ReadFile(path)
	if n := strings.Count(string(data), agentbusMarker); n != 4 {
		t.Fatalf("agentbus hooks = %d, want 4", n)
	}
	wireZcode(spec, false)
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), agentbusMarker) {
		t.Fatalf("uninstall left hooks: %s", data)
	}
}

func TestKimiLegacyBlockReplaced(t *testing.T) {
	home := testHome(t)
	dir := filepath.Join(home, ".kimi-code")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.toml")
	legacy := "model = \"x\"\n# agentbus hooks (managed by agentbus; remove this block to uninstall)\n[[hooks]]\nevent = \"Stop\"\ncommand = \"/old/agentbus hook\"\ntimeout = 5\n"
	if err := os.WriteFile(cfg, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := discoverAgents(home)[2]
	if a := wireKimi(spec, home, true); a.Warning != "" {
		t.Fatal(a.Warning)
	}
	data, _ := os.ReadFile(cfg)
	if strings.Contains(string(data), "/old/agentbus") {
		t.Fatalf("legacy block survived:\n%s", data)
	}
	if n := strings.Count(string(data), "# >>> agentbus"); n != 1 {
		t.Fatalf("managed blocks = %d\n%s", n, data)
	}
	if !strings.Contains(string(data), `model = "x"`) {
		t.Fatalf("unrelated config lost:\n%s", data)
	}
	agentsMD, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(agentsMD), "<!-- agentbus:begin -->") || !strings.Contains(string(agentsMD), "AgentBus") {
		t.Fatalf("AGENTS.md contract missing:\n%s", agentsMD)
	}
	// Idempotent.
	before, _ := os.ReadFile(cfg)
	if a := wireKimi(spec, home, true); a.Changed {
		t.Fatalf("second run rewrote the config: %s", a.Action)
	}
	after, _ := os.ReadFile(cfg)
	if string(before) != string(after) {
		t.Fatal("second run changed config content")
	}
	// Reverse.
	if a := wireKimi(spec, home, false); a.Warning != "" {
		t.Fatal(a.Warning)
	}
	data, _ = os.ReadFile(cfg)
	if strings.Contains(string(data), agentbusMarker) || strings.Contains(string(data), "# >>> agentbus") {
		t.Fatalf("uninstall left a block:\n%s", data)
	}
	if strings.TrimSpace(string(data)) != `model = "x"` {
		t.Fatalf("uninstall damaged config:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("uninstall left AGENTS.md: %v", err)
	}
}

func TestKimiUninstallRemovesCreatedFiles(t *testing.T) {
	home := testHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".kimi-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := discoverAgents(home)[2]
	if a := wireKimi(spec, home, true); a.Warning != "" {
		t.Fatal(a.Warning)
	}
	wireKimi(spec, home, false)
	if fileExists(filepath.Join(home, ".kimi-code", "config.toml")) {
		t.Fatal("uninstall left an empty config.toml")
	}
	if fileExists(filepath.Join(home, ".kimi-code", "AGENTS.md")) {
		t.Fatal("uninstall left an empty AGENTS.md")
	}
}

func TestDSHPluginExtracted(t *testing.T) {
	home := testHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".dsh"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := discoverAgents(home)[3]
	a := wireDSH(spec, true)
	dest := filepath.Join(home, dshPluginDest)
	if !fileExists(filepath.Join(dest, "package.json")) || !fileExists(filepath.Join(dest, "lib", "index.js")) {
		t.Fatalf("plugin not extracted: %+v", a)
	}
	if a.Warning == "" {
		t.Fatal("expected a warning because dsh is not on PATH")
	}
	if agentConfigured(spec, home) {
		t.Fatal("an extracted plugin alone must not count as wired")
	}
	// A profile that references the package does count.
	profilePkg := filepath.Join(home, ".dsh", "profiles", "web", "package.json")
	if err := os.MkdirAll(filepath.Dir(profilePkg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePkg, []byte(`{"dependencies":{"dsh-agentbus":"file:..."}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !agentConfigured(spec, home) {
		t.Fatal("configured check should see the profile dependency")
	}
	wireDSH(spec, false)
	if fileExists(filepath.Join(dest, "package.json")) {
		t.Fatal("uninstall left the plugin")
	}
}

func TestInstallSkills(t *testing.T) {
	home := testHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	actions := installSkills(home)
	if len(actions) != 2 { // shared + claude
		t.Fatalf("got %d skill actions: %+v", len(actions), actions)
	}
	shared := filepath.Join(home, ".agents", "skills", "agentbus", "SKILL.md")
	data, err := os.ReadFile(shared)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 1000 || !strings.Contains(string(data), "agentbus") {
		t.Fatalf("embedded skill looks wrong (%d bytes)", len(data))
	}
	for _, a := range installSkills(home) {
		if a.Changed {
			t.Fatalf("second install reported a change: %+v", a)
		}
	}
}

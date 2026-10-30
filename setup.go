package main

// agentbus setup: detect the AI CLIs installed on this machine, let the user
// pick which ones to wire, and configure them in one pass. The command is the
// single installer behind both `scripts/install.sh` and the npm package, so a
// from-source install and `npm i -g agentbus-cli && agentbus setup` take the
// same code path.
//
// Everything it writes is idempotent: agentbus-owned hook entries are replaced,
// other entries survive, and each touched file is backed up once before the
// first rewrite. `agentbus setup --uninstall` removes what setup added.

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/windviki/agentbus/internal/server"
)

//go:embed skills/agentbus/SKILL.md
var skillFS embed.FS

//go:embed integrations/dsh/plugin
var dshPluginFS embed.FS

const (
	skillRelPath   = "skills/agentbus/SKILL.md"
	dshPluginRel   = "integrations/dsh/plugin"
	dshPluginID    = "agentbus"
	dshPluginDest  = ".agentbus/integrations/dsh-plugin"
	agentbusMarker = "agentbus hook"
)

type agentSpec struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Method     string   `json:"method"`
	Dir        string   `json:"-"`
	Dirs       []string `json:"-"`
	Bins       []string `json:"-"`
	Config     string   `json:"config"`
	Detected   bool     `json:"detected"`
	Evidence   string   `json:"evidence,omitempty"`
	Configured bool     `json:"configured"`
}

func discoverAgents(home string) []agentSpec {
	join := func(parts ...string) string { return filepath.Join(append([]string{home}, parts...)...) }
	specs := []agentSpec{
		{
			ID: "claude", Name: "Claude Code", Method: "hooks",
			Dir: join(".claude"), Bins: []string{"claude"},
			Config: join(".claude", "settings.json"),
		},
		{
			ID: "zcode", Name: "ZCode", Method: "hooks",
			Dir: join(".zcode"), Bins: []string{"zcode"},
			Config: join(".zcode", "cli", "config.json"),
		},
		{
			ID: "kimi", Name: "Kimi Code", Method: "hooks + AGENTS.md",
			Dir: join(".kimi-code"), Dirs: []string{join(".kimi")}, Bins: []string{"kimi", "kimi-code"},
			Config: join(".kimi-code", "config.toml"),
		},
		{
			ID: "dsh", Name: "DSH", Method: "native plugin",
			Dir: dshHomeDir(home), Bins: []string{"dsh"},
			Config: filepath.Join(dshHomeDir(home), "profiles", "web", "cordis.patch.yml"),
		},
	}
	for i := range specs {
		s := &specs[i]
		var evidence []string
		for _, d := range append([]string{s.Dir}, s.Dirs...) {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				evidence = append(evidence, "config dir "+tildeDir(home, d))
			}
		}
		for _, b := range s.Bins {
			if p, err := exec.LookPath(b); err == nil {
				evidence = append(evidence, b+" on PATH ("+p+")")
				break
			}
		}
		s.Detected = len(evidence) > 0
		s.Evidence = strings.Join(evidence, "; ")
		s.Configured = agentConfigured(*s, home)
	}
	return specs
}

// dshHomeDir honors DSH_HOME so setup does not touch ~/.dsh when the user
// keeps DSH state elsewhere.
func dshHomeDir(home string) string {
	if h := strings.TrimSpace(os.Getenv("DSH_HOME")); h != "" {
		return h
	}
	return filepath.Join(home, ".dsh")
}

// tildeDir renders a path below home as ~/... for readable output.
func tildeDir(home, path string) string {
	if rel, err := filepath.Rel(home, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return "~/" + filepath.ToSlash(rel)
	}
	return path
}

func agentConfigured(s agentSpec, home string) bool {
	switch s.ID {
	case "claude", "zcode":
		data, err := os.ReadFile(s.Config)
		return err == nil && strings.Contains(string(data), agentbusMarker)
	case "kimi":
		for _, p := range []string{
			filepath.Join(home, ".kimi-code", "config.toml"),
			filepath.Join(home, ".kimi", "config.toml"),
		} {
			if data, err := os.ReadFile(p); err == nil && strings.Contains(string(data), agentbusMarker) {
				return true
			}
		}
		for _, p := range []string{
			filepath.Join(home, ".kimi-code", "config.toml"),
			filepath.Join(home, ".kimi", "config.toml"),
		} {
			if data, err := os.ReadFile(p); err == nil && strings.Contains(string(data), "# >>> agentbus") {
				return true
			}
		}
		data, err := os.ReadFile(filepath.Join(home, ".kimi-code", "AGENTS.md"))
		return err == nil && strings.Contains(string(data), "<!-- agentbus:begin -->")
	case "dsh":
		// The extracted plugin is only an asset; the profile has to reference
		// it (bundle patch or node_modules) before it is actually wired.
		for _, p := range []string{
			s.Config,
			filepath.Join(dshHomeDir(home), "profiles", "web", "package.json"),
			filepath.Join(dshHomeDir(home), "profiles", "web", "node_modules", "dsh-agentbus", "package.json"),
		} {
			if data, err := os.ReadFile(p); err == nil && strings.Contains(string(data), dshPluginID) {
				return true
			}
		}
		return false
	}
	return false
}

type setupAction struct {
	Agent   string `json:"agent"`
	Action  string `json:"action"`
	Target  string `json:"target,omitempty"`
	Changed bool   `json:"changed"`
	Note    string `json:"note,omitempty"`
	Warning string `json:"warning,omitempty"`
}

type setupReport struct {
	Version   string        `json:"version"`
	Home      string        `json:"home"`
	DryRun    bool          `json:"dryRun"`
	Uninstall bool          `json:"uninstall"`
	Detected  []agentSpec   `json:"detected"`
	Actions   []setupAction `json:"actions"`
	Warnings  []string      `json:"warnings,omitempty"`
}

type setupOptions struct {
	Yes       bool
	DryRun    bool
	JSON      bool
	Uninstall bool
	NoSkill   bool
	Agents    string
	Home      string
}

func cmdSetup(args []string) int {
	fs := newFlagSet("setup")
	opts := setupOptions{}
	fs.BoolVar(&opts.Yes, "yes", false, "non-interactive: wire every detected agent (for scripts/CI)")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "show what would change without writing anything")
	fs.BoolVar(&opts.JSON, "json", false, "machine-readable report")
	fs.BoolVar(&opts.Uninstall, "uninstall", false, "remove the wiring agentbus added")
	fs.BoolVar(&opts.NoSkill, "no-skill", false, "do not install the agentbus skill")
	fs.StringVar(&opts.Agents, "agents", "", "comma-separated: claude,zcode,kimi,dsh,all,none")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	if fs.NArg() > 0 {
		return fail("usage: agentbus setup [--yes] [--dry-run] [--agents claude,zcode,...] [--uninstall] [--json]")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fail("cannot determine home directory: %v", err)
	}
	opts.Home = home
	if opts.JSON {
		opts.Yes = true
	}

	specs := discoverAgents(home)
	selected, err := selectAgents(specs, opts)
	if err != nil {
		return fail("%v", err)
	}

	rep := setupReport{Version: server.Version, Home: home, DryRun: opts.DryRun, Uninstall: opts.Uninstall, Detected: specs}
	if !opts.JSON {
		printSetupBanner(specs, opts, selected)
	}

	var actions []setupAction
	if opts.Uninstall && opts.DryRun {
		if !opts.NoSkill {
			actions = append(actions, planUninstallSkills(home)...)
		}
		for _, s := range selected {
			actions = append(actions, planUninstallAgent(s, home))
		}
	} else if opts.Uninstall {
		if !opts.NoSkill && len(selected) > 0 {
			actions = append(actions, uninstallSkills(home)...)
		}
		for _, s := range selected {
			actions = append(actions, uninstallAgent(s, home))
		}
	} else if opts.DryRun {
		if !opts.NoSkill {
			actions = append(actions, planSkills(home)...)
		}
		for _, s := range selected {
			actions = append(actions, planAgent(s, home))
		}
	} else {
		if !opts.NoSkill {
			actions = append(actions, installSkills(home)...)
		}
		for _, s := range selected {
			actions = append(actions, installAgent(s, home))
		}
	}

	for _, a := range actions {
		if a.Target != "" {
			a.Target = tildeDir(home, a.Target)
		}
		rep.Actions = append(rep.Actions, a)
		if a.Warning != "" {
			rep.Warnings = append(rep.Warnings, a.Warning)
		}
	}
	if !opts.Uninstall && !opts.DryRun {
		if _, err := exec.LookPath("agentbus"); err != nil {
			exe, _ := os.Executable()
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("`agentbus` is not on PATH (binary: %s); hooks invoke it by name — add that directory to PATH", filepath.Dir(exe)))
		}
	}
	return finishSetup(&opts, &rep)
}

// selectAgents resolves --agents / --yes / the interactive prompt into the
// ordered list of agents to act on.
func selectAgents(specs []agentSpec, opts setupOptions) ([]agentSpec, error) {
	if opts.Uninstall && strings.TrimSpace(opts.Agents) == "" {
		return specs, nil // remove everything we know about
	}
	wanted, err := parseAgentSelection(opts.Agents)
	if err != nil {
		return nil, err
	}
	if wanted != nil {
		if len(wanted) == 0 {
			return []agentSpec{}, nil
		}
		var out []agentSpec
		for _, s := range specs {
			if wanted[s.ID] {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no agent matched --agents=%s", opts.Agents)
		}
		return out, nil
	}
	if opts.Yes || opts.DryRun || opts.JSON || !stdinIsTTY() {
		return detectedAgents(specs), nil
	}

	fmt.Println()
	fmt.Println("  Integrate which agents?")
	for i, s := range specs {
		state := "not detected"
		if s.Detected {
			state = s.Method
		}
		if s.Configured {
			state += ", already wired"
		}
		fmt.Printf("    %d) %-12s %s\n", i+1, s.Name, state)
	}
	fmt.Print("\n  Numbers separated by commas, a=all, n=none [default: all detected]: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "a", "all", "y", "yes":
		return detectedAgents(specs), nil
	case "n", "none", "no":
		return []agentSpec{}, nil
	}
	var out []agentSpec
	seen := map[string]bool{}
	for _, field := range strings.Split(line, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		idx := 0
		if _, err := fmt.Sscanf(field, "%d", &idx); err != nil || idx < 1 || idx > len(specs) {
			return nil, fmt.Errorf("invalid selection %q", field)
		}
		s := specs[idx-1]
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, s)
	}
	return out, nil
}

func detectedAgents(specs []agentSpec) []agentSpec {
	var out []agentSpec
	for _, s := range specs {
		if s.Detected {
			out = append(out, s)
		}
	}
	return out
}

func parseAgentSelection(csv string) (map[string]bool, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil, nil
	}
	known := map[string]bool{"claude": true, "zcode": true, "kimi": true, "dsh": true}
	switch strings.ToLower(csv) {
	case "all":
		return known, nil
	case "none":
		return map[string]bool{}, nil
	}
	out := map[string]bool{}
	for _, field := range strings.Split(csv, ",") {
		id := strings.ToLower(strings.TrimSpace(field))
		if !known[id] {
			return nil, fmt.Errorf("unknown agent %q (want claude, zcode, kimi, dsh, all or none)", field)
		}
		out[id] = true
	}
	return out, nil
}

func stdinIsTTY() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func printSetupBanner(specs []agentSpec, opts setupOptions, selected []agentSpec) {
	fmt.Printf("agentbus setup — v%s\n\n", server.Version)
	if opts.Uninstall {
		fmt.Println("  Removing agentbus wiring (bus state and message history are kept).")
		return
	}
	fmt.Println("  Detected:")
	any := false
	for _, s := range specs {
		mark := "  ·"
		detail := "not found"
		if s.Detected {
			mark = "  ✓"
			detail = s.Method + " — " + s.Evidence
			any = true
		}
		if s.Configured {
			detail += "  [already wired]"
		}
		fmt.Printf("%s %-12s %s\n", mark, s.Name, detail)
	}
	if !any {
		fmt.Println("\n  No supported agent detected. The shared skill will still be installed;")
		fmt.Println("  any CLI can call `agentbus` or run `agentbus mcp` (see docs/integrations/generic.md).")
	}
	if len(selected) == 0 {
		fmt.Println("\n  Nothing selected — installing the shared skill only.")
	}
}

func finishSetup(opts *setupOptions, rep *setupReport) int {
	if opts.JSON {
		data, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(data))
		return 0
	}
	fmt.Println()
	if len(rep.Actions) == 0 {
		fmt.Println("  Nothing to do.")
	} else {
		fmt.Println("  Changes:")
		for _, a := range rep.Actions {
			mark := "  ·"
			if a.Changed {
				mark = "  +"
			}
			target := ""
			if a.Target != "" {
				target = "  " + a.Target
			}
			note := ""
			if a.Note != "" {
				note = " (" + a.Note + ")"
			}
			fmt.Printf("%s %-7s %-24s%s%s\n", mark, a.Agent, a.Action, target, note)
		}
	}
	if len(rep.Warnings) > 0 {
		fmt.Println("\n  Warnings:")
		for _, w := range rep.Warnings {
			fmt.Println("  ! " + w)
		}
	}
	if !opts.Uninstall {
		fmt.Println("\n  Next: start a new agent session, then run `agentbus doctor`.")
		fmt.Println("  Dashboard: run `agentbus status` for the URL.")
		fmt.Println("  Re-run `agentbus setup --yes` after reinstalling or upgrading agentbus.")
	}
	fmt.Println()
	return 0
}

// --- shared skill -----------------------------------------------------------

func skillBytes() []byte {
	data, err := skillFS.ReadFile(skillRelPath)
	if err != nil {
		return nil
	}
	return data
}

func skillTargets(home string) []string {
	targets := []string{filepath.Join(home, ".agents", "skills", "agentbus", "SKILL.md")}
	add := func(dir string) {
		if st, err := os.Stat(filepath.Join(home, dir)); err == nil && st.IsDir() {
			targets = append(targets, filepath.Join(home, dir, "skills", "agentbus", "SKILL.md"))
		}
	}
	add(".claude")
	add(".kimi-code")
	add(".kimi")
	// ZCode scans ~/.agents/skills, so a second copy would duplicate the
	// catalog entry; deliberately no ~/.zcode/skills copy.
	return targets
}

func installSkills(home string) []setupAction {
	data := skillBytes()
	var out []setupAction
	for _, path := range skillTargets(home) {
		if len(data) == 0 {
			out = append(out, setupAction{Agent: "skill", Action: "embedded skill missing", Target: path, Warning: "the binary was built without the embedded skill"})
			continue
		}
		changed, err := writeIfDifferent(path, data, 0o644)
		a := setupAction{Agent: "skill", Action: "install", Target: path, Changed: changed}
		if err != nil {
			a.Warning = fmt.Sprintf("skill %s: %v", path, err)
		}
		out = append(out, a)
	}
	return out
}

func planSkills(home string) []setupAction {
	var out []setupAction
	for _, path := range skillTargets(home) {
		out = append(out, setupAction{Agent: "skill", Action: "would install", Target: path, Changed: !fileExists(path)})
	}
	return out
}

func uninstallSkills(home string) []setupAction {
	var out []setupAction
	for _, path := range skillTargets(home) {
		dir := filepath.Dir(path)
		if err := os.RemoveAll(dir); err != nil {
			out = append(out, setupAction{Agent: "skill", Action: "remove", Target: dir, Warning: fmt.Sprintf("skill %s: %v", dir, err)})
			continue
		}
		out = append(out, setupAction{Agent: "skill", Action: "remove", Target: dir, Changed: true})
	}
	_ = os.Remove(filepath.Join(home, ".agents", "skills"))
	return out
}

// --- per-agent install ------------------------------------------------------

func installAgent(s agentSpec, home string) setupAction {
	switch s.ID {
	case "claude":
		return wireClaude(s, true)
	case "zcode":
		return wireZcode(s, true)
	case "kimi":
		return wireKimi(s, home, true)
	case "dsh":
		return wireDSH(s, true)
	}
	return setupAction{Agent: s.ID, Action: "unknown"}
}

func planAgent(s agentSpec, home string) setupAction {
	switch s.ID {
	case "claude", "zcode":
		return setupAction{Agent: s.ID, Action: "would merge hooks", Target: s.Config, Changed: !s.Configured}
	case "kimi":
		return setupAction{Agent: s.ID, Action: "would merge hooks + AGENTS.md", Target: s.Config, Changed: !s.Configured}
	case "dsh":
		return setupAction{Agent: s.ID, Action: "would install native plugin", Target: filepath.Join(home, dshPluginDest), Changed: !s.Configured}
	}
	return setupAction{Agent: s.ID, Action: "unknown"}
}

func planUninstallSkills(home string) []setupAction {
	var out []setupAction
	for _, path := range skillTargets(home) {
		dir := filepath.Dir(path)
		out = append(out, setupAction{Agent: "skill", Action: "would remove", Target: dir, Changed: fileExists(path)})
	}
	return out
}

func planUninstallAgent(s agentSpec, home string) setupAction {
	switch s.ID {
	case "claude", "zcode":
		return setupAction{Agent: s.ID, Action: "would remove hooks", Target: s.Config, Changed: s.Configured}
	case "kimi":
		return setupAction{Agent: s.ID, Action: "would remove hooks + AGENTS.md", Target: s.Config, Changed: s.Configured}
	case "dsh":
		return setupAction{Agent: s.ID, Action: "would remove plugin", Target: filepath.Join(home, dshPluginDest), Changed: s.Configured}
	}
	return setupAction{Agent: s.ID, Action: "unknown"}
}

func uninstallAgent(s agentSpec, home string) setupAction {
	switch s.ID {
	case "claude":
		return wireClaude(s, false)
	case "zcode":
		return wireZcode(s, false)
	case "kimi":
		return wireKimi(s, home, false)
	case "dsh":
		return wireDSH(s, false)
	}
	return setupAction{Agent: s.ID, Action: "unknown"}
}

// --- Claude Code / ZCode JSON hooks ----------------------------------------

var claudeHookEvents = []struct {
	Event   string
	Timeout int
}{
	{"SessionStart", 15},
	{"UserPromptSubmit", 5},
	{"PostToolUse", 5},
	{"Stop", 5},
	{"SessionEnd", 5},
}

var zcodeHookEvents = []struct {
	Event   string
	Timeout int
}{
	{"SessionStart", 15000},
	{"UserPromptSubmit", 5000},
	{"PostToolUse", 5000},
	{"Stop", 5000},
}

func wireClaude(s agentSpec, install bool) setupAction {
	a := setupAction{Agent: "claude", Action: map[bool]string{true: "wire hooks", false: "remove hooks"}[install], Target: s.Config}
	cfg, existed, err := readJSONConfig(s.Config)
	if err != nil {
		a.Warning = err.Error()
		return a
	}
	if !existed && !install {
		a.Action, a.Changed = "no hooks file", false
		return a
	}
	hooks, err := hookMap(cfg, "hooks")
	if err != nil {
		a.Warning = fmt.Sprintf("%s: %v", s.Config, err)
		return a
	}
	for _, ev := range claudeHookEvents {
		kept := filterHookEntries(hooks[ev.Event])
		if install {
			kept = append(kept, map[string]any{"hooks": []any{
				map[string]any{"type": "command", "command": agentbusMarker, "timeout": ev.Timeout},
			}})
		}
		if len(kept) == 0 {
			delete(hooks, ev.Event)
		} else {
			hooks[ev.Event] = kept
		}
	}
	changed, err := writeJSONIfChanged(s.Config, cfg, existed)
	if err != nil {
		a.Warning = err.Error()
		return a
	}
	a.Changed = changed
	if changed {
		a.Note = backupNote(s.Config)
	}
	return a
}

func wireZcode(s agentSpec, install bool) setupAction {
	a := setupAction{Agent: "zcode", Action: map[bool]string{true: "wire hooks", false: "remove hooks"}[install], Target: s.Config}
	cfg, existed, err := readJSONConfig(s.Config)
	if err != nil {
		a.Warning = err.Error()
		return a
	}
	if !existed && !install {
		a.Action, a.Changed = "no hooks file", false
		return a
	}
	hooks, err := hookMap(cfg, "hooks")
	if err != nil {
		a.Warning = fmt.Sprintf("%s: %v", s.Config, err)
		return a
	}
	if install {
		hooks["enabled"] = true
	}
	events, err := hookMap(hooks, "events")
	if err != nil {
		a.Warning = fmt.Sprintf("%s: hooks.events: %v", s.Config, err)
		return a
	}
	for _, ev := range zcodeHookEvents {
		kept := filterHookEntries(events[ev.Event])
		if install {
			kept = append(kept, map[string]any{"hooks": []any{
				map[string]any{"type": "command", "command": agentbusMarker, "timeoutMs": ev.Timeout},
			}})
		}
		if len(kept) == 0 {
			delete(events, ev.Event)
		} else {
			events[ev.Event] = kept
		}
	}
	if len(events) == 0 {
		delete(hooks, "events")
	}
	changed, err := writeJSONIfChanged(s.Config, cfg, existed)
	if err != nil {
		a.Warning = err.Error()
		return a
	}
	a.Changed = changed
	if changed {
		a.Note = backupNote(s.Config)
	}
	return a
}

func hookMap(parent map[string]any, key string) (map[string]any, error) {
	v, ok := parent[key]
	if !ok || v == nil {
		m := map[string]any{}
		parent[key] = m
		return m, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%q is not an object", key)
	}
	return m, nil
}

// filterHookEntries drops the entries agentbus owns and keeps everything else.
func filterHookEntries(v any) []any {
	arr, _ := v.([]any)
	var kept []any
	for _, e := range arr {
		data, _ := json.Marshal(e)
		if strings.Contains(string(data), agentbusMarker) {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

func readJSONConfig(path string) (map[string]any, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, true, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, true, fmt.Errorf("%s is not valid JSON (%v); fix it or merge the hooks manually", tildeConfig(path), err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, true, nil
}

// writeJSONIfChanged compares the serialized result with the original bytes so
// a second `agentbus setup` is a no-op (no rewrite, no backup churn).
func writeJSONIfChanged(path string, cfg map[string]any, existed bool) (bool, error) {
	after, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	after = append(after, '\n')
	original, _ := os.ReadFile(path)
	if string(after) == string(original) || (!existed && len(cfg) == 0) {
		return false, nil
	}
	if err := backupOnce(path); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	return true, os.WriteFile(path, after, mode)
}

func backupNote(path string) string {
	if fileExists(path + ".agentbus-bak") {
		return "backup: " + filepath.Base(path) + ".agentbus-bak"
	}
	return ""
}

// backupOnce keeps the original file for recovery; later runs never overwrite
// the first backup.
func backupOnce(path string) error {
	if !fileExists(path) || fileExists(path+".agentbus-bak") {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path+".agentbus-bak", data, st.Mode().Perm())
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func tildeConfig(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		return tildeDir(home, path)
	}
	return path
}

// --- Kimi Code: TOML hooks + AGENTS.md --------------------------------------

func wireKimi(s agentSpec, home string, install bool) setupAction {
	cfgPath := s.Config
	if _, err := os.Stat(cfgPath); err != nil {
		alt := filepath.Join(home, ".kimi", "config.toml")
		if fileExists(alt) {
			cfgPath = alt
		}
	}
	a := setupAction{Agent: "kimi", Action: map[bool]string{true: "wire hooks", false: "remove hooks"}[install], Target: cfgPath}
	data, err := os.ReadFile(cfgPath)
	existedBefore := err == nil
	if err != nil && !os.IsNotExist(err) {
		a.Warning = err.Error()
		return a
	}
	text, hadBlock := stripKimiBlock(string(data))
	if install {
		next := text
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		if next != "" {
			next += "\n"
		}
		next += kimiBlock()
		if next != string(data) {
			if err := writeTextWithBackup(cfgPath, []byte(next), 0o600); err != nil {
				a.Warning = err.Error()
				return a
			}
			a.Changed = true
			a.Note = backupNote(cfgPath)
		}
	} else if !hadBlock {
		// no managed block in this file; AGENTS.md may still need cleanup below
	} else if strings.TrimSpace(text) == "" && existedBefore {
		if err := os.Remove(cfgPath); err != nil {
			a.Warning = err.Error()
			return a
		}
		a.Changed = true
	} else if text != string(data) {
		if err := writeTextWithBackup(cfgPath, []byte(text), 0o600); err != nil {
			a.Warning = err.Error()
			return a
		}
		a.Changed = true
	}

	agentsPath := filepath.Join(home, ".kimi-code", "AGENTS.md")
	if st, err := os.Stat(filepath.Join(home, ".kimi")); err == nil && st.IsDir() {
		if _, err := os.Stat(filepath.Join(home, ".kimi-code")); os.IsNotExist(err) {
			agentsPath = filepath.Join(home, ".kimi", "AGENTS.md")
		}
	}
	existing, _ := os.ReadFile(agentsPath)
	current := string(existing)
	next := current
	foundBlock := false
	if install {
		next = upsertMarkerBlock(current, "<!-- agentbus:begin -->", "<!-- agentbus:end -->", primerText())
	} else {
		next, foundBlock = stripMarkerBlock(current, "<!-- agentbus:begin -->", "<!-- agentbus:end -->")
	}
	if !install && !foundBlock {
		return a
	}
	if next != current {
		if !install && strings.TrimSpace(next) == "" {
			if err := os.Remove(agentsPath); err != nil && !os.IsNotExist(err) {
				a.Warning = err.Error()
				return a
			}
			a.Changed = true
		} else {
			if err := writeTextWithBackup(agentsPath, []byte(next), 0o644); err != nil {
				a.Warning = err.Error()
				return a
			}
			a.Changed = true
			a.Note = joinNotes(a.Note, "instructions: "+tildeDir(home, agentsPath))
		}
	}
	return a
}

func joinNotes(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func writeTextWithBackup(path string, data []byte, mode fs.FileMode) error {
	if err := backupOnce(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

func kimiBlock() string {
	cmd := agentbusCommand()
	var b strings.Builder
	b.WriteString("# >>> agentbus (managed by `agentbus setup`; remove with `agentbus setup --uninstall`)\n")
	for _, ev := range []struct {
		Event   string
		Timeout int
	}{
		{"SessionStart", 15},
		{"UserPromptSubmit", 5},
		{"PostToolUse", 5},
		{"Stop", 5},
		{"SessionEnd", 5},
	} {
		fmt.Fprintf(&b, "[[hooks]]\nevent = %q\ncommand = %q\ntimeout = %d\n\n", ev.Event, cmd, ev.Timeout)
	}
	b.WriteString("# <<< agentbus\n")
	return b.String()
}

func agentbusCommand() string {
	if _, err := exec.LookPath("agentbus"); err == nil {
		return "agentbus hook"
	}
	if exe, err := os.Executable(); err == nil {
		// Quote the path: hook commands go through a shell, so a path with
		// spaces must stay one word. TOML/JSON escaping is applied by the
		// caller's %q.
		return fmt.Sprintf("%q hook", exe)
	}
	return "agentbus hook"
}

func stripKimiBlock(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	var out []string
	found := false
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "# >>> agentbus") {
			found = true
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "# <<< agentbus") {
				i++
			}
			continue
		}
		// Legacy block written by scripts/install.sh: marker then stanzas to EOF.
		if strings.HasPrefix(t, "# agentbus hooks (managed by agentbus") {
			found = true
			break
		}
		out = append(out, lines[i])
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n", found
}

func upsertMarkerBlock(text, begin, end, body string) string {
	if i := strings.Index(text, begin); i >= 0 {
		if j := strings.Index(text[i:], end); j >= 0 {
			return text[:i] + begin + "\n" + body + end + text[i+j+len(end):]
		}
	}
	sep := ""
	if text != "" && !strings.HasSuffix(text, "\n") {
		sep = "\n"
	}
	if text != "" && !strings.HasSuffix(text, "\n\n") {
		sep += "\n"
	}
	return text + sep + begin + "\n" + body + end + "\n"
}

func stripMarkerBlock(text, begin, end string) (string, bool) {
	i := strings.Index(text, begin)
	if i < 0 {
		return text, false
	}
	j := strings.Index(text[i:], end)
	if j < 0 {
		return text, false
	}
	next := text[:i] + text[i+j+len(end):]
	return strings.TrimRight(next, "\n") + "\n", true
}

// --- DSH native plugin ------------------------------------------------------

func wireDSH(s agentSpec, install bool) setupAction {
	home, _ := os.UserHomeDir()
	dest := filepath.Join(home, dshPluginDest)
	a := setupAction{Agent: "dsh", Action: map[bool]string{true: "install plugin", false: "remove plugin"}[install], Target: dest}
	if !install {
		if _, err := exec.LookPath("dsh"); err == nil {
			if out, err := runCommand(120*time.Second, "dsh", "plugin", "--profile", "web", "remove", "dsh-agentbus"); err != nil {
				a.Warning = "dsh plugin remove: " + shortOutput(out, err)
			} else {
				a.Note = "dsh plugin removed"
			}
		}
		// pnpm removes the dependency; the profile's bundle list is ours to clean.
		if warn := setDSHProfileBundle(home, false); warn != "" && a.Warning == "" {
			a.Warning = warn
		}
		if err := os.RemoveAll(dest); err != nil {
			a.Warning = err.Error()
			return a
		}
		a.Changed = true
		return a
	}

	if err := extractFS(dshPluginFS, dshPluginRel, dest); err != nil {
		a.Warning = fmt.Sprintf("extract plugin: %v", err)
		return a
	}
	a.Changed = true
	if _, err := exec.LookPath("dsh"); err != nil {
		a.Warning = "dsh not on PATH: the plugin was extracted but not registered. Run: dsh plugin --profile web add " + dest
		return a
	}
	out, err := runCommand(180*time.Second, "dsh", "plugin", "--profile", "web", "add", dest)
	if err != nil {
		a.Warning = "could not register the DSH plugin automatically: " + shortOutput(out, err) +
			". Run: dsh plugin --profile web add " + dest + " (see docs/integrations/dsh.md)"
		return a
	}
	if warn := setDSHProfileBundle(home, true); warn != "" {
		a.Warning = warn
		return a
	}
	a.Note = "registered with the web profile"
	return a
}

// setDSHProfileBundle adds or removes dsh-agentbus from the web profile's
// bundle list. `dsh plugin add` manages package.json dependencies, but the
// bundle list is what the loader actually mounts, so leaving a removed bundle
// behind would break the next DSH boot.
func setDSHProfileBundle(home string, present bool) string {
	path := filepath.Join(dshHomeDir(home), "profiles", "web", "package.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if present {
			return "DSH profile package.json not found; the plugin may need a manual loader row (see docs/integrations/dsh.md)"
		}
		return ""
	}
	if err != nil {
		return "DSH profile: " + err.Error()
	}
	var pkg map[string]any
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "DSH profile package.json is not valid JSON: " + err.Error()
	}
	changed := false
	dsh, _ := pkg["dsh"].(map[string]any)
	if dsh == nil {
		dsh = map[string]any{}
		if present {
			pkg["dsh"] = dsh
		}
	}
	raw, _ := dsh["profile"].(map[string]any)
	if raw == nil {
		raw = map[string]any{}
		if present {
			dsh["profile"] = raw
		}
	}
	var bundles []any
	if arr, ok := raw["bundles"].([]any); ok {
		bundles = arr
	}
	next := make([]any, 0, len(bundles)+1)
	found := false
	for _, b := range bundles {
		name, _ := b.(string)
		if name == "dsh-agentbus" {
			found = true
			if !present {
				changed = true
				continue
			}
		}
		next = append(next, b)
	}
	if present && !found {
		next = append(next, "dsh-agentbus")
		changed = true
	}
	if !changed {
		return ""
	}
	raw["bundles"] = next
	out, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return "DSH profile: " + err.Error()
	}
	out = append(out, '\n')
	if err := backupOnce(path); err != nil {
		return "DSH profile backup: " + err.Error()
	}
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(path, out, mode); err != nil {
		return "DSH profile: " + err.Error()
	}
	return ""
}

func runCommand(timeout time.Duration, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
		return string(out), err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return string(out), fmt.Errorf("timed out after %s", timeout)
	}
}

func shortOutput(out string, err error) string {
	out = strings.TrimSpace(out)
	if len(out) > 400 {
		out = "..." + out[len(out)-400:]
	}
	if out == "" {
		return err.Error()
	}
	return out
}

// extractFS copies an embedded directory tree to dest, replacing an earlier
// copy so upgrades pick up the new plugin.
func extractFS(fsys fs.FS, root, dest string) error {
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// writeIfDifferent avoids touching mtimes when the content already matches.
func writeIfDifferent(path string, data []byte, mode fs.FileMode) (bool, error) {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(data) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, data, mode)
}

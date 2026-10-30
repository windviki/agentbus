// agentbus is a local message bus for concurrent AI coding-agent sessions
// (claude code / zcode / kimi / dsh / ...). A tiny daemon owns unique
// session names, presence and per-session inboxes over a unix socket; hooks
// deliver messages into agent contexts with zero token cost when idle.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/windviki/agentbus/internal/bus"
	"github.com/windviki/agentbus/internal/client"
	"github.com/windviki/agentbus/internal/mcp"
	"github.com/windviki/agentbus/internal/procinfo"
	"github.com/windviki/agentbus/internal/proto"
	"github.com/windviki/agentbus/internal/server"
	"github.com/windviki/agentbus/internal/web"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "serve":
		return cmdServe(rest)
	case "stop":
		return cmdStop(rest)
	case "status":
		return cmdStatus(rest)
	case "hook":
		return cmdHook(rest)
	case "register":
		return cmdRegister(rest)
	case "who":
		return cmdWho(rest)
	case "peers":
		return cmdPeers(rest)
	case "announce":
		return cmdAnnounce(rest)
	case "send":
		return cmdSend(rest)
	case "ask":
		return cmdAsk(rest)
	case "reply":
		return cmdReply(rest)
	case "notify":
		return cmdNotify(rest)
	case "primer":
		return cmdPrimer(rest)
	case "mcp":
		return cmdMcp(rest)
	case "inbox":
		return cmdInbox(rest)
	case "leave":
		return cmdLeave(rest)
	case "log":
		return cmdLog(rest)
	case "doctor":
		return cmdDoctor(rest)
	case "setup":
		return cmdSetup(rest)
	case "version", "--version", "-v":
		fmt.Println(server.Version)
		return 0
	case "help", "--help", "-h":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "agentbus: unknown command %q\n\n", cmd)
		usage(os.Stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `agentbus — local message bus for concurrent AI agent sessions

Usage:
  agentbus hook                              # the hook entrypoint: reads hook JSON on
                                             # stdin, registers/polls/leaves, prints
                                             # hook-output JSON (nothing when idle)
  agentbus register [--name bob] [--agent A] # join manually (name auto-generated if omitted)
  agentbus who [-v|--json]                   # list live sessions + what they're doing
  agentbus peers [--cwd DIR] [--json]        # live sessions in YOUR worktree / repo
  agentbus announce "fixing auth in wt-auth" # publish your status
  agentbus send <name|all> "message"         # message a peer / broadcast
                                             #   --kind chat|request|reply|notice|urgent
  agentbus ask <name> "question" [--wait 120] # send a request and wait for the reply
  agentbus reply "message" [--to <name>]     # answer the latest peer message
  agentbus notify <name|all> "information"   # one-way notice (no reply expected)
  agentbus primer                            # print the session contract a harness
                                             # should load (system prompt / AGENTS.md)
  agentbus inbox [--wait 60] [--peek]        # read your messages
  agentbus leave                             # leave the bus (automatic on exit anyway)
  agentbus log [-n 20]                       # recent bus traffic (all sessions)
  agentbus mcp                               # run as a stdio MCP server (tools for
                                             # harnesses without hooks: kimi/dsh/...)
  agentbus setup [--yes] [--dry-run]          # detect installed agents, then wire the
                                             # ones you pick (hooks/plugin/skill).
                                             # --agents claude,zcode,kimi,dsh
                                             # --uninstall removes agentbus wiring
  agentbus status | doctor                   # daemon state / installation checks
  agentbus serve [--detach] | stop           # daemon lifecycle (auto-spawns anyway)

Communication patterns: send (direct or broadcast via "all"), ask (request-
reply, blocks for the answer), notify (one-way), urgent kind (wakes an idle
session via the Stop hook — the only kind allowed to interrupt).

Session identity: taken from $AGENTBUS_SESSION_ID / $CLAUDE_SESSION_ID / the
hook JSON session_id, or from the owning agent process. Pass --session <key>
to override; humans without a session send as "user".

Env: AGENTBUS_HOME (default ~/.agentbus), AGENTBUS_AGENT (force agent label),
     AGENTBUS_NAME (preferred name for hook registration),
     AGENTBUS_PRIMER=0 (omit the session contract from the join summary),
     AGENTBUS_WAKE (default request = urgent + direct request; urgent; all),
     AGENTBUS_WEB_PORT (dashboard port, default 8787), AGENTBUS_WEB=0 (disable),
     AGENTBUS_WEB_TOKEN (require bearer token on the dashboard/A2A HTTP API).

Web dashboard: http://127.0.0.1:8787/ — behind code-server use /proxy/8787/.
A2A subset: GET /a2a/<name>/.well-known/agent-card.json, POST /a2a/<name>
with JSON-RPC message/send (text parts).
`)
}

func busHome() string {
	if h := os.Getenv("AGENTBUS_HOME"); h != "" {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil {
		h = "."
	}
	return filepath.Join(h, ".agentbus")
}

// sessionKey resolves the stable identity for session-scoped commands.
func sessionKey(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if k := hookSessionKey(""); k != "" {
		return k
	}
	return ""
}

// hookSessionKey prefers env vars (cheapest), then the hook stdin payload's
// session_id, then the owning agent process. IDs are normalized to a single
// "sid:" prefix so a caller that already speaks the canonical form (for
// example the DSH plugin passing "sid:session-…") is not double-prefixed.
func hookSessionKey(stdinSid string) string {
	sidKey := func(v string) string {
		if strings.HasPrefix(v, "sid:") {
			return v
		}
		return "sid:" + v
	}
	if v := os.Getenv("AGENTBUS_SESSION_ID"); v != "" {
		return sidKey(v)
	}
	if v := os.Getenv("CLAUDE_SESSION_ID"); v != "" {
		return sidKey(v)
	}
	// Claude Code exports this to Bash tool subprocesses (hooks receive the
	// same value as session_id). Without it, CLI calls made by the agent are
	// attributed to the human "user" because the pid fallback does not match
	// the sid-keyed registration.
	if v := os.Getenv("CLAUDE_CODE_SESSION_ID"); v != "" {
		return sidKey(v)
	}
	if stdinSid != "" {
		return sidKey(stdinSid)
	}
	if a := procinfo.Detect(); a.Pid > 0 {
		// The agent process may be known to the hooks (sid) even when this CLI
		// invocation is a plain Bash subprocess with no session env (ZCode).
		if sid := lookupPidMap(busHome(), a.Pid, a.Start); sid != "" {
			return sid
		}
		return fmt.Sprintf("pid:%d:%s", a.Pid, a.Start)
	}
	return ""
}

// pidMapPath is the per-agent-process identity file written by hook runs.
// Filename includes the process start time so a reused pid cannot inherit an
// old session.
func pidMapPath(home string, pid int, start string) string {
	return filepath.Join(home, "pidmap", fmt.Sprintf("%d-%s", pid, start))
}

// recordPidMap lets a later CLI call made from inside the agent (a Bash tool
// subprocess with no session env) recover the sid identity by walking back to
// the agent process. ZCode exposes no session variable to its Bash tool.
func recordPidMap(home, key string) {
	if !strings.HasPrefix(key, "sid:") {
		return
	}
	a := procinfo.Detect()
	if a.Pid <= 0 || a.Start == "" {
		return
	}
	dir := filepath.Join(home, "pidmap")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := pidMapPath(home, a.Pid, a.Start)
	if existing, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(existing)) == key {
		return // already mapped
	}
	_ = os.WriteFile(path, []byte(key+"\n"), 0o600)
}

func lookupPidMap(home string, pid int, start string) string {
	if pid <= 0 || start == "" {
		return ""
	}
	b, err := os.ReadFile(pidMapPath(home, pid, start))
	if err != nil {
		return ""
	}
	key := strings.TrimSpace(string(b))
	if strings.HasPrefix(key, "sid:") {
		return key
	}
	return ""
}

func detectAgent() (pid int, pidStart, label string) {
	a := procinfo.Detect()
	if a.Label == "" {
		a.Label = "unknown"
	}
	return a.Pid, a.Start, a.Label
}

func gitBranch(dir string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitRepo returns the git common dir for dir, so two worktrees of the same
// repository compare equal. Empty for non-git directories.
func gitRepo(dir string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			return p
		}
	}
	// Older git: fall back to the top-level directory.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel2()
	out, err = exec.CommandContext(ctx2, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// samePath reports whether a and b are the same directory or one contains the
// other (a session started in a subdirectory of the workspace).
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(a, b+sep) || strings.HasPrefix(b, a+sep)
}

// classifyPeers splits other sessions into same worktree, same repo (a
// different worktree), and unrelated. The caller knows its own cwd/repo.
func classifyPeers(cwd, repo string, sessions []bus.Session) (same, sameRepo, other []bus.Session) {
	for _, s := range sessions {
		exact := cwd != "" && filepath.Clean(cwd) == filepath.Clean(s.Cwd)
		switch {
		case exact || (repo != "" && s.Repo == repo && samePath(cwd, s.Cwd)):
			// Same directory, or a nested directory of the same git worktree.
			same = append(same, s)
		case repo != "" && s.Repo == repo:
			sameRepo = append(sameRepo, s)
		default:
			other = append(other, s)
		}
	}
	return
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "agentbus: "+format+"\n", a...)
	return 1
}

// --- commands ---------------------------------------------------------------

func cmdServe(args []string) int {
	fs := newFlagSet("serve")
	detach := fs.Bool("detach", false, "spawn the daemon in the background")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	home := busHome()
	if *detach {
		if err := client.EnsureRunning(home); err != nil {
			return fail("%v", err)
		}
		fmt.Printf("[agentbus] daemon running at %s\n", client.SocketPath(home))
		return 0
	}
	if err := server.Run(home); err != nil {
		return fail("%v", err)
	}
	return 0
}

func cmdStop(args []string) int {
	home := busHome()
	resp, err := client.Call(home, false, proto.Request{Op: "shutdown"})
	if err != nil {
		fmt.Println("[agentbus] daemon not running")
		return 0
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	// The daemon finishes writing state and removing the socket after replying;
	// wait briefly so "stop; status" never reports a ghost daemon.
	deadline := time.Now().Add(2 * time.Second)
	for client.Alive(home) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Println("[agentbus] daemon stopped")
	return 0
}

func cmdStatus(args []string) int {
	fs := newFlagSet("status")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	home := busHome()
	resp, err := client.Call(home, false, proto.Request{Op: "status"})
	if err != nil {
		fmt.Printf("[agentbus] daemon not running (%s) — start with: agentbus serve --detach\n", client.SocketPath(home))
		return 1
	}
	if *asJSON {
		printJSON(resp)
		return 0
	}
	fmt.Printf("[agentbus] daemon %s up %s · %d live session(s) · home %s\n",
		resp.Version, dur(time.Duration(resp.Uptime)*time.Second), len(resp.Sessions), home)
	if resp.WebAddr != "" {
		fmt.Printf("[agentbus] web: http://%s/ (code-server: /proxy/%s/)\n",
			resp.WebAddr, strings.TrimPrefix(resp.WebAddr, "127.0.0.1:"))
	}
	for _, s := range resp.Sessions {
		fmt.Printf("  %s\n", rosterLine(s))
	}
	return 0
}

// kimiRosterMarker is a per-session marker so Kimi's first prompt gets the
// roster exactly once (Kimi ignores SessionStart stdout).
func kimiRosterMarker(home, key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(home, "roster", fmt.Sprintf("%x", sum))
}

func kimiFirstRoster(home, key, cwd string) {
	marker := kimiRosterMarker(home, key)
	if _, err := os.Stat(marker); err == nil {
		return
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	pid, pidStart, label := detectAgent()
	repo := gitRepo(cwd)
	resp, err := client.Call(home, true, proto.Request{
		Op: "register", ID: key, Want: os.Getenv("AGENTBUS_NAME"), Agent: label, Cwd: cwd,
		Repo: repo, Branch: gitBranch(cwd), Pid: pid, PidStart: pidStart,
	})
	if err != nil || !resp.Ok {
		return
	}
	fmt.Print(joinSummary(resp, cwd, repo))
	_ = os.MkdirAll(filepath.Dir(marker), 0o700)
	_ = os.WriteFile(marker, []byte("1\n"), 0o600)
}

// cmdHook is the single hook entrypoint wired into agent configs. It must
// never break the host session: all failures exit 0 silently (logged to
// $AGENTBUS_HOME/hook.log).
func cmdHook(args []string) int {
	in, _ := io.ReadAll(os.Stdin)
	var h struct {
		EventName  string `json:"hook_event_name"`
		SessionID  string `json:"session_id"`
		Cwd        string `json:"cwd"`
		ClientType string `json:"client_type"`
	}
	_ = json.Unmarshal(in, &h)
	home := busHome()
	key := hookSessionKey(h.SessionID)
	if key == "" {
		return 0 // not called from inside an agent session
	}
	recordPidMap(home, key)
	// Kimi's hook contract differs from Claude Code / ZCode: plain stdout is
	// appended to the model context, and Stop blocks by exiting 2 with the
	// reason on stderr (no JSON envelope).
	kimi := h.ClientType == "kimi_code_cli"
	switch h.EventName {
	case "SessionStart":
		pid, pidStart, label := detectAgent()
		cwd := h.Cwd
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		resp, err := client.Call(home, true, proto.Request{
			Op: "register", ID: key, Want: os.Getenv("AGENTBUS_NAME"), Agent: label, Cwd: cwd,
			Repo: gitRepo(cwd), Branch: gitBranch(cwd), Pid: pid, PidStart: pidStart,
		})
		if err != nil || !resp.Ok {
			hookFail(home, err, resp)
			return 0
		}
		if !kimi {
			fmt.Print(hookContextJSON("SessionStart", joinSummary(resp, cwd, gitRepo(cwd))))
		}
	case "UserPromptSubmit":
		if kimi {
			kimiFirstRoster(home, key, h.Cwd)
		}
		if msgs := pollForHook(home, key); len(msgs) > 0 {
			if kimi {
				fmt.Print(incomingText(msgs))
			} else {
				fmt.Print(hookContextJSON(h.EventName, incomingText(msgs)))
			}
		}
	case "PostToolUse", "PostToolUseFailure":
		// Kimi treats these as observation-only: stdout is ignored, so polling
		// here would consume messages without showing them. Leave mail queued
		// for the next prompt, the Stop wake, or an explicit `agentbus inbox`.
		if kimi {
			return 0
		}
		if msgs := pollForHook(home, key); len(msgs) > 0 {
			fmt.Print(hookContextJSON(h.EventName, incomingText(msgs)))
		}
	case "Stop":
		// Delivery ladder: an idle session is woken by urgent messages and by
		// requests addressed directly to it (a question needs an answer).
		// AGENTBUS_WAKE=urgent restores the old urgent-only ladder;
		// AGENTBUS_WAKE=all wakes on anything. Peek first so messages that do
		// not qualify stay queued for the next natural boundary.
		peek := false
		resp, err := client.Call(home, true, proto.Request{Op: "poll", ID: key, Ack: &peek})
		if err != nil || !resp.Ok || len(resp.Messages) == 0 {
			return 0
		}
		if !shouldWakeIdle(resp.Messages, resp.Name) {
			return 0
		}
		take := true
		resp, err = client.Call(home, true, proto.Request{Op: "poll", ID: key, Ack: &take})
		if err != nil || !resp.Ok || len(resp.Messages) == 0 {
			return 0
		}
		if kimi {
			fmt.Fprint(os.Stderr, stopReason(resp.Messages))
			return 2
		}
		fmt.Print(stopBlockJSON(stopReason(resp.Messages)))
	case "SessionEnd":
		if kimi {
			_ = os.Remove(kimiRosterMarker(home, key))
		}
		_, _ = client.Call(home, true, proto.Request{Op: "leave", ID: key})
	}
	return 0
}

// wakeMode interprets AGENTBUS_WAKE. Default ("request") wakes an idle session
// for urgent messages and direct requests; "urgent" keeps the old urgent-only
// behavioural; "all" wakes for any message.
func wakeMode() string {
	switch mode := strings.ToLower(strings.TrimSpace(os.Getenv("AGENTBUS_WAKE"))); mode {
	case "urgent":
		return "urgent"
	case "all":
		return "all"
	default:
		return "request"
	}
}

// shouldWakeIdle decides whether the Stop hook may interrupt an idle session.
// Keep the policy in one place: the hook calls it, and the injected text
// (stopReason / incomingText) explains exactly what the model should do.
func shouldWakeIdle(msgs []bus.Msg, name string) bool {
	switch wakeMode() {
	case "all":
		return len(msgs) > 0
	case "urgent":
		return bus.HasUrgent(msgs)
	default:
		return bus.WakesIdle(msgs, name)
	}
}

// pollForHook polls once; if the daemon forgot us (state wiped), it silently
// re-registers and retries.
func pollForHook(home, key string) []bus.Msg {
	resp, err := client.Call(home, true, proto.Request{Op: "poll", ID: key})
	if err != nil {
		hookFail(home, err, resp)
		return nil
	}
	if resp.Ok {
		return resp.Messages
	}
	if strings.Contains(resp.Error, "unknown session") {
		pid, pidStart, label := detectAgent()
		if reg, err2 := client.Call(home, true, proto.Request{Op: "register", ID: key, Agent: label, Pid: pid, PidStart: pidStart}); err2 == nil && reg.Ok {
			if resp, err = client.Call(home, true, proto.Request{Op: "poll", ID: key}); err == nil && resp.Ok {
				return resp.Messages
			}
		}
		return nil
	}
	hookFail(home, nil, resp)
	return nil
}

func hookFail(home string, err error, resp proto.Response) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if resp.Error != "" {
		msg = resp.Error
	}
	if msg == "" {
		return
	}
	f, e := os.OpenFile(filepath.Join(home, "hook.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if e != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), msg)
}

func cmdRegister(args []string) int {
	fs := newFlagSet("register")
	name := fs.String("name", "", "requested session name (auto-generated when omitted)")
	agent := fs.String("agent", "", "agent label, e.g. claude-code, zcode, kimi, dsh")
	session := fs.String("session", "", "session key override")
	cwdF := fs.String("cwd", "", "working directory (default: $PWD)")
	pidF := fs.Int("pid", 0, "override agent pid (testing)")
	pidStart := fs.String("pid-start", "", "override agent pid starttime (testing)")
	printName := fs.Bool("print-name", false, "print only the resolved name (for scripts)")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	key := sessionKey(*session)
	if key == "" {
		return fail("no agent session detected; run inside an agent session or pass --session <key>")
	}
	pid, pstart, label := detectAgent()
	if *pidF != 0 {
		pid, pstart = *pidF, *pidStart
	}
	if *agent != "" {
		label = *agent
	}
	cwd := *cwdF
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	resp, err := client.Call(busHome(), true, proto.Request{
		Op: "register", ID: key, Want: *name, Agent: label, Cwd: cwd,
		Repo: gitRepo(cwd), Branch: gitBranch(cwd), Pid: pid, PidStart: pstart,
	})
	if err != nil {
		return fail("%v", err)
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	if *printName {
		fmt.Println(resp.Name)
		return 0
	}
	if *asJSON {
		printJSON(resp)
		return 0
	}
	fmt.Print(joinSummary(resp, cwd, gitRepo(cwd)))
	return 0
}

func cmdWho(args []string) int {
	fs := newFlagSet("who")
	verbose := fs.Bool("v", false, "")
	verbose2 := fs.Bool("verbose", false, "")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	resp, err := client.Call(busHome(), true, proto.Request{Op: "who"})
	if err != nil {
		return fail("%v", err)
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	if *asJSON {
		printJSON(resp)
		return 0
	}
	printRoster(resp.Sessions, *verbose || *verbose2)
	return 0
}

// cmdPeers shows only the sessions that matter for conflict avoidance: the
// same worktree, then other worktrees of the same repo, then everyone else.
func cmdPeers(args []string) int {
	fs := newFlagSet("peers")
	cwdF := fs.String("cwd", "", "workspace directory (default: $PWD)")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	cwd := *cwdF
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	resp, err := client.Call(busHome(), true, proto.Request{Op: "who"})
	if err != nil {
		return fail("%v", err)
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	key := sessionKey("")
	var sessions []bus.Session
	for _, s := range resp.Sessions {
		if key != "" && s.ID == key {
			continue
		}
		sessions = append(sessions, s)
	}
	for i := range sessions {
		if sessions[i].Repo == "" && sessions[i].Cwd != "" {
			sessions[i].Repo = gitRepo(sessions[i].Cwd)
		}
	}
	repo := gitRepo(cwd)
	same, sameRepo, other := classifyPeers(cwd, repo, sessions)
	if *asJSON {
		printJSON(map[string]any{
			"cwd": cwd, "repo": repo,
			"same_worktree": same, "same_repo": sameRepo, "other": other,
		})
		return 0
	}
	fmt.Printf("[agentbus] workspace %s%s\n", tilde(cwd), func() string {
		if repo != "" {
			return " · repo " + tilde(repo)
		}
		return ""
	}())
	if len(same) == 0 && len(sameRepo) == 0 {
		fmt.Println("[agentbus] no parallel session in this workspace")
	} else {
		if len(same) > 0 {
			fmt.Printf("SAME WORKTREE (%d) — coordinate before editing shared files\n", len(same))
			printRoster(same, false)
		}
		if len(sameRepo) > 0 {
			fmt.Printf("SAME REPO, OTHER WORKTREE (%d)\n", len(sameRepo))
			printRoster(sameRepo, false)
		}
	}
	if len(other) > 0 {
		fmt.Printf("OTHER LIVE SESSIONS (%d)\n", len(other))
		printRoster(other, false)
	}
	return 0
}

func cmdAnnounce(args []string) int {
	fs := newFlagSet("announce")
	session := fs.String("session", "", "session key override")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	if fs.NArg() < 1 {
		return fail("usage: agentbus announce \"<what you're doing>\"")
	}
	key := sessionKey(*session)
	if key == "" {
		return fail("no agent session detected; run inside an agent session or pass --session <key>")
	}
	status := strings.Join(fs.Args(), " ")
	resp, err := client.Call(busHome(), true, proto.Request{Op: "announce", ID: key, Status: status})
	if err != nil {
		return fail("%v", err)
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	fmt.Printf("[agentbus] status of '%s' set to: %s\n", resp.Name, truncFor(status, 80))
	return 0
}

// guardBody rejects a body that starts with "--". Go's flag package stops
// parsing at the first positional argument, so `send bob "hi" --kind reply`
// would silently fold the flag into the message body. Fail loudly instead.
func guardBody(body string) error {
	if strings.HasPrefix(strings.TrimSpace(body), "--") {
		return fmt.Errorf(`message must not start with "--" — put flags before the recipient, e.g. agentbus send --kind reply bob "text"`)
	}
	return nil
}

func cmdSend(args []string) int {
	fs := newFlagSet("send")
	session := fs.String("session", "", "session key override")
	from := fs.String("from", "", "sender name override (humans default to \"user\")")
	kind := fs.String("kind", "chat", "chat|request|reply|notice|urgent")
	kindAlias := fs.String("type", "", "alias for --kind")
	replyTo := fs.String("reply-to", "", "messageId this replies to")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	if fs.NArg() < 2 {
		return fail("usage: agentbus send <name|all> \"message\"")
	}
	if *kindAlias != "" {
		*kind = *kindAlias
	}
	to, body := fs.Arg(0), strings.Join(fs.Args()[1:], " ")
	if err := guardBody(body); err != nil {
		return fail("%v", err)
	}
	req := proto.Request{Op: "send", To: to, Body: body, Kind: *kind, ReplyTo: *replyTo}
	if key := sessionKey(*session); key != "" {
		req.ID = key
	} else if *from != "" {
		req.From = *from
	}
	resp, err := client.Call(busHome(), true, req)
	if err != nil {
		return fail("%v", err)
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	switch {
	case resp.Dup:
		fmt.Println("[agentbus] identical message sent moments ago — suppressed")
	case to == "all" || to == "#all":
		if strings.EqualFold(*kind, "request") {
			fmt.Printf("[agentbus] broadcast request delivered to %d session(s) — replies are optional and it will not wake idle sessions\n", resp.Recipients)
		} else {
			fmt.Printf("[agentbus] broadcast delivered to %d session(s)\n", resp.Recipients)
		}
	case strings.EqualFold(*kind, "request"):
		fmt.Printf("[agentbus] request delivered to '%s' (a reply is expected; an idle peer is woken)\n", strings.ToLower(to))
	default:
		fmt.Printf("[agentbus] delivered to '%s'\n", strings.ToLower(to))
	}
	return 0
}

// cmdAsk: request-reply. Sends kind=request and blocks (long-poll) for the
// peer's answer, which is printed and thereby consumed (no double delivery).
func cmdAsk(args []string) int {
	fs := newFlagSet("ask")
	session := fs.String("session", "", "session key override")
	waitS := fs.Int("wait", 120, "seconds to wait for the reply")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	if fs.NArg() < 2 {
		return fail("usage: agentbus ask <name> \"question\"")
	}
	key := sessionKey(*session)
	if key == "" {
		return fail("no agent session detected; run inside an agent session or pass --session <key>")
	}
	to, body := fs.Arg(0), strings.Join(fs.Args()[1:], " ")
	if err := guardBody(body); err != nil {
		return fail("%v", err)
	}
	resp, err := client.Call(busHome(), true, proto.Request{
		Op: "ask", ID: key, To: to, Body: body, WaitMs: *waitS * 1000,
	})
	if err != nil {
		return fail("%v", err)
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	if len(resp.Messages) == 0 {
		fmt.Printf("[agentbus] no reply from '%s' within %ds — the request stays queued; a directed request wakes an idle peer, so it may be busy or not running hooks. Check `agentbus who`, then carry on and read any late answer with `agentbus inbox --peek`.\n",
			strings.ToLower(to), *waitS)
		return 1
	}
	m := resp.Messages[0]
	fmt.Printf("[agentbus] reply from %s:\n%s\n", m.From, m.Body)
	return 0
}

// cmdReply: answer the most recent peer message (kind=reply with replyTo set),
// so agents do not need to track message ids themselves.
func cmdReply(args []string) int {
	fs := newFlagSet("reply")
	session := fs.String("session", "", "session key override")
	to := fs.String("to", "", "reply to this peer (default: the most recent peer message)")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	if fs.NArg() < 1 {
		return fail("usage: agentbus reply \"message\" [--to <name>]")
	}
	key := sessionKey(*session)
	if key == "" {
		return fail("no agent session detected; run inside an agent session or pass --session <key>")
	}
	body := strings.Join(fs.Args(), " ")
	if err := guardBody(body); err != nil {
		return fail("%v", err)
	}
	resp, err := client.Call(busHome(), true, proto.Request{Op: "reply", ID: key, To: *to, Body: body})
	if err != nil {
		return fail("%v", err)
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	fmt.Printf("[agentbus] replied to %s\n", resp.Peer)
	return 0
}

// cmdPrimer prints the session contract for harnesses that can load it into a
// system prompt / AGENTS.md instead of relying on hook injection.
func cmdPrimer(args []string) int {
	fmt.Print(primerText())
	return 0
}

// cmdNotify: one-way notice (kind=notice) — delivered at the next natural
// boundary, never wakes an idle session, no reply expected.
func cmdNotify(args []string) int {
	fs := newFlagSet("notify")
	session := fs.String("session", "", "session key override")
	from := fs.String("from", "", "sender name override")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	if fs.NArg() < 2 {
		return fail("usage: agentbus notify <name|all> \"information\"")
	}
	to, body := fs.Arg(0), strings.Join(fs.Args()[1:], " ")
	if err := guardBody(body); err != nil {
		return fail("%v", err)
	}
	req := proto.Request{Op: "send", To: to, Body: body, Kind: "notice"}
	if key := sessionKey(*session); key != "" {
		req.ID = key
	} else if *from != "" {
		req.From = *from
	}
	resp, err := client.Call(busHome(), true, req)
	if err != nil {
		return fail("%v", err)
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	fmt.Printf("[agentbus] notice delivered (%d session(s))\n", resp.Recipients)
	return 0
}

// cmdMcp runs agentbus as a stdio MCP server — the deep-integration path for
// harnesses that support MCP tools but not hooks (kimi, dsh, ...).
func cmdMcp(args []string) int {
	if err := mcp.Run(); err != nil {
		return fail("mcp: %v", err)
	}
	return 0
}

func cmdInbox(args []string) int {
	fs := newFlagSet("inbox")
	session := fs.String("session", "", "session key override")
	waitS := fs.Int("wait", 0, "long-poll up to N seconds")
	peek := fs.Bool("peek", false, "read without marking delivered")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	key := sessionKey(*session)
	if key == "" {
		return fail("no agent session detected; run inside an agent session or pass --session <key>")
	}
	ack := !*peek
	resp, err := client.Call(busHome(), true, proto.Request{
		Op: "poll", ID: key, Ack: &ack, WaitMs: *waitS * 1000,
	})
	if err != nil {
		return fail("%v", err)
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	if *asJSON {
		printJSON(resp)
		return 0
	}
	if len(resp.Messages) == 0 {
		if *waitS > 0 {
			fmt.Println("[agentbus] no messages yet (waited)")
		} else {
			fmt.Println("[agentbus] no new messages")
		}
		return 0
	}
	fmt.Print(incomingText(resp.Messages))
	return 0
}

func cmdLeave(args []string) int {
	fs := newFlagSet("leave")
	session := fs.String("session", "", "session key override")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	key := sessionKey(*session)
	if key == "" {
		return fail("no agent session detected; run inside an agent session or pass --session <key>")
	}
	resp, err := client.Call(busHome(), true, proto.Request{Op: "leave", ID: key})
	if err != nil {
		return fail("%v", err)
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	fmt.Println("[agentbus] left the bus")
	return 0
}

func cmdLog(args []string) int {
	fs := newFlagSet("log")
	n := fs.Int("n", 20, "number of messages")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	resp, err := client.Call(busHome(), true, proto.Request{Op: "log", N: *n})
	if err != nil {
		return fail("%v", err)
	}
	if !resp.Ok {
		return fail("%s", resp.Error)
	}
	if len(resp.Recent) == 0 {
		fmt.Println("[agentbus] no messages yet")
		return 0
	}
	for _, m := range resp.Recent {
		fmt.Printf("%s %s\n", time.Unix(m.Ts, 0).Format("01-02 15:04:04"), msgCompact(m))
	}
	return 0
}

func cmdDoctor(args []string) int {
	home := busHome()
	ok := true
	note := func(good bool, format string, a ...any) {
		mark := "[!!]"
		if good {
			mark = "[ok]"
		} else {
			ok = false
		}
		fmt.Printf("%s %s\n", mark, fmt.Sprintf(format, a...))
	}

	self, err := os.Executable()
	note(err == nil, "binary: %s (v%s)", self, server.Version)

	if err := os.MkdirAll(home, 0o700); err != nil {
		note(false, "bus home %s: %v", home, err)
	} else {
		note(true, "bus home: %s (writable)", home)
	}

	started := false
	if !client.Alive(home) {
		if _, err := client.Call(home, true, proto.Request{Op: "ping"}); err == nil {
			started = true
		}
	}
	if client.Alive(home) {
		extra := ""
		if started {
			extra = " (just auto-started)"
		}
		note(true, "daemon: reachable at %s%s", client.SocketPath(home), extra)
	} else {
		note(false, "daemon: NOT reachable at %s (see %s)", client.SocketPath(home), filepath.Join(home, "daemon.log"))
	}
	if resp, err := client.Call(home, true, proto.Request{Op: "who"}); err == nil && resp.Ok {
		note(true, "sessions: %d live", len(resp.Sessions))
	}
	if web.ServeHTTPCheck() {
		fmt.Printf("[ok] web: http://%s/ (code-server: /proxy/%s/)\n", web.Addr(), strings.TrimPrefix(web.Addr(), "127.0.0.1:"))
	} else if os.Getenv("AGENTBUS_WEB") == "0" {
		fmt.Println("[--] web: disabled (AGENTBUS_WEB=0)")
	} else {
		fmt.Printf("[!!] web: not reachable on %s (port busy? see AGENTBUS_WEB_PORT)\n", web.Addr())
	}

	userHome, _ := os.UserHomeDir()
	specs := discoverAgents(userHome)
	wired := 0
	for _, s := range specs {
		switch {
		case s.Configured:
			wired++
			fmt.Printf("[ok] integration: %-12s wired (%s)\n", s.Name, s.Method)
		case s.Detected:
			fmt.Printf("[--] integration: %-12s detected but not wired — `agentbus setup`\n", s.Name)
		}
	}
	if wired == 0 {
		fmt.Println("[--] integrations: none wired — run `agentbus setup` (or `agentbus setup --yes`)")
	}

	skillPaths := []string{
		filepath.Join(userHome, ".agents", "skills", "agentbus", "SKILL.md"),
		filepath.Join(userHome, ".claude", "skills", "agentbus", "SKILL.md"),
		filepath.Join(userHome, ".kimi-code", "skills", "agentbus", "SKILL.md"),
		filepath.Join(userHome, ".kimi", "skills", "agentbus", "SKILL.md"),
	}
	found := false
	for _, p := range skillPaths {
		if _, err := os.Stat(p); err == nil {
			found = true
		}
	}
	if !found {
		fmt.Println("[--] skill: not installed — `agentbus setup` installs it")
	}

	if p, err := exec.LookPath("agentbus"); err == nil {
		fmt.Printf("[ok] PATH: agentbus resolves to %s\n", p)
	} else {
		fmt.Printf("[!!] PATH: agentbus not found — hooks must be able to run `agentbus`; add %s to PATH\n", filepath.Dir(self))
	}

	if ok {
		fmt.Println("doctor: all core checks passed")
		return 0
	}
	return 1
}

func printJSON(v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
}

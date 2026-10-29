// agentbus is a local message bus for concurrent AI coding-agent sessions
// (claude code / zcode / kimi / dsh / ...). A tiny daemon owns unique
// session names, presence and per-session inboxes over a unix socket; hooks
// deliver messages into agent contexts with zero token cost when idle.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentbus/internal/bus"
	"agentbus/internal/client"
	"agentbus/internal/procinfo"
	"agentbus/internal/proto"
	"agentbus/internal/server"
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
	case "announce":
		return cmdAnnounce(rest)
	case "send":
		return cmdSend(rest)
	case "inbox":
		return cmdInbox(rest)
	case "leave":
		return cmdLeave(rest)
	case "log":
		return cmdLog(rest)
	case "doctor":
		return cmdDoctor(rest)
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
  agentbus announce "fixing auth in wt-auth" # publish your status
  agentbus send <name|all> "message"         # message a peer / broadcast
  agentbus inbox [--wait 60] [--peek]        # read your messages
  agentbus leave                             # leave the bus (automatic on exit anyway)
  agentbus log [-n 20]                       # recent bus traffic (all sessions)
  agentbus status | doctor                   # daemon state / installation checks
  agentbus serve [--detach] | stop           # daemon lifecycle (auto-spawns anyway)

Session identity: taken from $AGENTBUS_SESSION_ID / $CLAUDE_SESSION_ID / the
hook JSON session_id, or from the owning agent process. Pass --session <key>
to override; humans without a session send as "user".

Env: AGENTBUS_HOME (default ~/.agentbus), AGENTBUS_AGENT (force agent label),
     AGENTBUS_NAME (preferred name for hook registration).
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
// session_id, then the owning agent process.
func hookSessionKey(stdinSid string) string {
	if v := os.Getenv("AGENTBUS_SESSION_ID"); v != "" {
		return "sid:" + v
	}
	if v := os.Getenv("CLAUDE_SESSION_ID"); v != "" {
		return "sid:" + v
	}
	if stdinSid != "" {
		return "sid:" + stdinSid
	}
	if a := procinfo.Detect(); a.Pid > 0 {
		return fmt.Sprintf("pid:%d:%s", a.Pid, a.Start)
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
	for _, s := range resp.Sessions {
		fmt.Printf("  %s\n", rosterLine(s))
	}
	return 0
}

// cmdHook is the single hook entrypoint wired into agent configs. It must
// never break the host session: all failures exit 0 silently (logged to
// $AGENTBUS_HOME/hook.log).
func cmdHook(args []string) int {
	in, _ := io.ReadAll(os.Stdin)
	var h struct {
		EventName string `json:"hook_event_name"`
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
	}
	_ = json.Unmarshal(in, &h)
	home := busHome()
	key := hookSessionKey(h.SessionID)
	if key == "" {
		return 0 // not called from inside an agent session
	}
	switch h.EventName {
	case "SessionStart":
		pid, pidStart, label := detectAgent()
		cwd := h.Cwd
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		resp, err := client.Call(home, true, proto.Request{
			Op: "register", ID: key, Want: os.Getenv("AGENTBUS_NAME"), Agent: label, Cwd: cwd,
			Branch: gitBranch(cwd), Pid: pid, PidStart: pidStart,
		})
		if err != nil || !resp.Ok {
			hookFail(home, err, resp)
			return 0
		}
		fmt.Print(hookContextJSON("SessionStart", joinSummary(resp)))
	case "UserPromptSubmit", "PostToolUse", "PostToolUseFailure":
		if msgs := pollForHook(home, key); len(msgs) > 0 {
			fmt.Print(hookContextJSON(h.EventName, incomingText(msgs)))
		}
	case "Stop":
		if msgs := pollForHook(home, key); len(msgs) > 0 {
			fmt.Print(stopBlockJSON(stopReason(msgs)))
		}
	case "SessionEnd":
		_, _ = client.Call(home, true, proto.Request{Op: "leave", ID: key})
	}
	return 0
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
		Branch: gitBranch(cwd), Pid: pid, PidStart: pstart,
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
	fmt.Print(joinSummary(resp))
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

func cmdSend(args []string) int {
	fs := newFlagSet("send")
	session := fs.String("session", "", "session key override")
	from := fs.String("from", "", "sender name override (humans default to \"user\")")
	typ := fs.String("type", "chat", "message type")
	if err := fs.Parse(args); err != nil {
		return fail("bad flags: %v", err)
	}
	if fs.NArg() < 2 {
		return fail("usage: agentbus send <name|all> \"message\"")
	}
	to, body := fs.Arg(0), strings.Join(fs.Args()[1:], " ")
	req := proto.Request{Op: "send", To: to, Body: body, Type: *typ}
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
		fmt.Printf("[agentbus] broadcast delivered to %d session(s)\n", resp.Recipients)
	default:
		fmt.Printf("[agentbus] delivered to '%s'\n", strings.ToLower(to))
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

	userHome, _ := os.UserHomeDir()
	skillPaths := []string{
		filepath.Join(userHome, ".agents", "skills", "agentbus", "SKILL.md"),
		filepath.Join(userHome, ".claude", "skills", "agentbus", "SKILL.md"),
		filepath.Join(userHome, ".zcode", "skills", "agentbus", "SKILL.md"),
	}
	found := false
	for _, p := range skillPaths {
		if _, err := os.Stat(p); err == nil {
			found = true
		}
	}
	note(found, "skill: %s (install: scripts/install.sh)", map[bool]string{true: "installed", false: "not installed"}[found])

	hookCfgs := []string{
		filepath.Join(userHome, ".claude", "settings.json"),
		filepath.Join(userHome, ".zcode", "cli", "config.json"),
	}
	hooked := false
	for _, p := range hookCfgs {
		if data, err := os.ReadFile(p); err == nil && strings.Contains(string(data), "agentbus hook") {
			hooked = true
		}
	}
	if !hooked {
		fmt.Println("[--] hooks: no agentbus hooks configured — messages arrive only when the agent polls (see integrations/)")
	}

	pathEnv := os.Getenv("PATH")
	if strings.Contains(pathEnv+":"+string(filepath.ListSeparator), filepath.Dir(self)+string(filepath.ListSeparator)) || strings.Contains(pathEnv, filepath.Dir(self)) {
		fmt.Println("[--] PATH: binary directory is on PATH")
	} else {
		fmt.Printf("[--] PATH: %s not on PATH — hooks must be able to run `agentbus`\n", filepath.Dir(self))
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

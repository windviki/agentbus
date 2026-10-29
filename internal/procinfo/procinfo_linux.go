//go:build linux

// Package procinfo discovers which CLI agent process (if any) owns the
// current process, by walking the /proc ancestor chain. This powers two
// things: a stable per-session identity key ("pid:<pid>:<start>") and free
// liveness checks for the bus (a session is alive while its agent process
// lives, even when the agent is idle and fires no hooks).
package procinfo

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Agent describes the detected owning agent process.
type Agent struct {
	Pid   int
	Start string // /proc starttime field, guards against PID reuse
	Label string // claude-code, zcode, kimi, dsh, ...
}

// Skip processes that merely wrap the command we care about.
var skipComms = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "fish": true, "dash": true,
	"ksh": true, "ash": true, "csh": true, "tcsh": true, "nu": true,
	"env": true, "timeout": true, "nice": true, "nohup": true,
	"setsid": true, "sudo": true, "doas": true, "stdbuf": true,
	"watch": true, "xargs": true, "script": true,
}

// Markers are matched (case-insensitively, as substrings) against the
// ancestor's full cmdline. Order matters: more specific first.
var markers = []struct{ needle, label string }{
	{"zcode", "zcode"},
	{"kimi", "kimi"},
	{"dsh", "dsh"},
	{"opencode", "opencode"},
	{"codex", "codex"},
	{"gemini", "gemini"},
	{"qoder", "qoder"},
	{"aider", "aider"},
	{"amp", "amp"},
	{"claude", "claude-code"},
}

type stat struct {
	comm  string
	ppid  int
	start string
}

// Detect walks the ancestor chain and returns the first process that looks
// like a known CLI agent. AGENTBUS_AGENT, when set, overrides the detected
// label. Returns a zero Agent when run from a plain shell (which means: human
// user, identity falls back to "user").
func Detect() Agent {
	envLabel := os.Getenv("AGENTBUS_AGENT")
	pid := os.Getppid()
	for depth := 0; depth < 16 && pid > 1; depth++ {
		st, err := readStat(pid)
		if err != nil {
			break
		}
		if !skipComms[st.comm] {
			label := envLabel
			if label == "" {
				label = matchLabel(readCmdline(pid))
			}
			if label != "" {
				return Agent{Pid: pid, Start: st.start, Label: label}
			}
		}
		pid = st.ppid
	}
	return Agent{}
}

// Alive reports whether pid is still the same process (starttime match).
func Alive(pid int, start string) bool {
	if pid <= 0 {
		return false
	}
	st, err := readStat(pid)
	if err != nil {
		return false
	}
	return start == "" || st.start == start
}

func matchLabel(cmdline string) string {
	cl := strings.ToLower(cmdline)
	for _, m := range markers {
		if strings.Contains(cl, m.needle) {
			return m.label
		}
	}
	return ""
}

func readStat(pid int) (stat, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return stat{}, err
	}
	s := string(data)
	open := strings.IndexByte(s, '(')
	closeIdx := strings.LastIndexByte(s, ')')
	if open < 0 || closeIdx < open {
		return stat{}, fmt.Errorf("bad stat line for %d", pid)
	}
	fields := strings.Fields(s[closeIdx+1:])
	if len(fields) < 20 {
		return stat{}, fmt.Errorf("short stat line for %d", pid)
	}
	ppid, _ := strconv.Atoi(fields[1])
	return stat{comm: s[open+1 : closeIdx], ppid: ppid, start: fields[19]}, nil
}

func readCmdline(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(string(data), "\x00", " ")
}

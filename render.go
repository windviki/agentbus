package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"agentbus/internal/bus"
	"agentbus/internal/proto"
)

// hookContextJSON wraps text as hook output that Claude Code / ZCode inject
// into the conversation as additional context. The schema is strict, so emit
// exactly this shape and nothing else.
func hookContextJSON(event, text string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     event,
			"additionalContext": text,
		},
	})
	return b.String()
}

// joinSummary is the one-time SessionStart injection: your name plus who else
// is live and what they say they're doing.
func joinSummary(resp proto.Response) string {
	var b strings.Builder
	renamed := ""
	if resp.RenamedFrom != "" {
		renamed = fmt.Sprintf(" (name %q was taken)", resp.RenamedFrom)
	}
	fmt.Fprintf(&b, "[agentbus] this session joined the local agent bus as '%s' [%s]%s.\n", resp.Name, resp.Agent, renamed)
	if n := len(resp.Sessions); n > 0 {
		fmt.Fprintf(&b, "[agentbus] %d other live agent session%s — before starting overlapping work, coordinate:\n", n, plural(n))
		for _, s := range resp.Sessions {
			fmt.Fprintf(&b, "- %s\n", rosterLine(s))
		}
		b.WriteString("[agentbus] talk: agentbus send <name> \"...\" · announce yourself: agentbus announce \"doing X in <worktree>\" · roster: agentbus who\n")
	} else {
		b.WriteString("[agentbus] no other live sessions. Announce your work once you start: agentbus announce \"doing X in <worktree>\"\n")
	}
	return b.String()
}

func rosterLine(s bus.Session) string {
	where := tilde(s.Cwd)
	if s.Branch != "" && s.Branch != "HEAD" {
		where += "@" + s.Branch
	}
	parts := []string{s.Name, s.Agent, where, ago(s.LastSeen) + " ago"}
	if s.Status != "" {
		parts = append(parts, "“"+truncFor(s.Status, 90)+"”")
	}
	return strings.Join(parts, " · ")
}

// incomingText renders newly delivered messages (compact — this text becomes
// agent context).
func incomingText(msgs []bus.Msg) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[agentbus] %d new message%s:\n", len(msgs), plural(len(msgs)))
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s] %s\n", time.Unix(m.Ts, 0).Format("15:04"), msgCompact(m))
	}
	needsReply := false
	from := "them"
	for _, m := range msgs {
		if m.Kind == bus.KindRequest || m.Kind == bus.KindUrgent {
			needsReply = true
		}
		if m.From != "" && m.From != "user" {
			from = m.From
		}
	}
	if needsReply {
		fmt.Fprintf(&b, "[agentbus] reply promptly: agentbus send %s \"...\" · roster: agentbus who\n", from)
	} else {
		b.WriteString("[agentbus] no reply needed unless relevant · roster: agentbus who\n")
	}
	return b.String()
}

// stopReason is the Stop-hook continuation reason: the agent is woken with
// this text when urgent messages arrive while it was about to go idle.
func stopReason(msgs []bus.Msg) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Urgent agentbus message%s arrived for you:\n", plural(len(msgs)))
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s] %s\n", time.Unix(m.Ts, 0).Format("15:04"), msgCompact(m))
	}
	b.WriteString("Handle it now (reply via `agentbus send <name> \"...\"`), then continue or wrap up your task.\n")
	return b.String()
}

func msgCompact(m bus.Msg) string {
	kind := m.Kind
	if kind == "" {
		kind = bus.KindChat
	}
	badge := ""
	if kind != bus.KindChat {
		badge = "[" + kind + "] "
	}
	re := ""
	if m.ReplyTo != "" {
		re = " (↩ " + m.ReplyTo + ")"
	}
	return fmt.Sprintf("%s%s→%s: %s%s", badge, m.From, m.To, m.Body, re)
}

func printRoster(sessions []bus.Session, verbose bool) {
	if len(sessions) == 0 {
		fmt.Println("[agentbus] no live agent sessions (just you)")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	if verbose {
		fmt.Fprintln(w, "NAME\tAGENT\tSTATUS\tCWD\tBRANCH\tPID\tSEEN")
		for _, s := range sessions {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
				s.Name, s.Agent, truncFor(s.Status, 48), tilde(s.Cwd), s.Branch, s.Pid, ago(s.LastSeen)+" ago")
		}
	} else {
		fmt.Fprintln(w, "NAME\tAGENT\tSTATUS\tWHERE\tSEEN")
		for _, s := range sessions {
			where := tilde(s.Cwd)
			if s.Branch != "" && s.Branch != "HEAD" {
				where += "@" + s.Branch
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				s.Name, s.Agent, truncFor(s.Status, 48), where, ago(s.LastSeen)+" ago")
		}
	}
	w.Flush()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func ago(ts int64) string {
	d := time.Since(time.Unix(ts, 0)).Truncate(time.Minute)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func dur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func tilde(p string) string {
	if p == "" {
		return "-"
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}

func truncFor(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return bus.Trunc(s, n-1) + "…"
}

// stopBlockJSON is the Stop-hook continuation request: the host injects the
// reason and lets the agent keep working (Claude Code / ZCode schema).
func stopBlockJSON(reason string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]string{"decision": "block", "reason": reason})
	return b.String()
}

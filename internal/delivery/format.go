package delivery

import (
	"fmt"
	"strings"
	"time"

	"github.com/windviki/agentbus/internal/bus"
)

func IncomingText(msgs []bus.Msg) string {
	var b strings.Builder
	peer, human := false, false
	for _, m := range msgs {
		if isHumanMsg(m) {
			human = true
			continue
		}
		if m.From != "" {
			peer = true
		}
	}
	mustReply, maybeReply := replySenders(msgs)
	action := ""
	switch {
	case bus.HasUrgent(msgs):
		action = " — URGENT, handle now"
	case len(mustReply) > 0:
		action = fmt.Sprintf(" — %d direct request%s need%s a reply", len(mustReply), plural(len(mustReply)), needs(len(mustReply)))
	}
	switch {
	case peer && human:
		fmt.Fprintf(&b, "[agentbus] %d message%s (mixed: peer agents and the human user)%s:\n", len(msgs), plural(len(msgs)), action)
	case peer:
		fmt.Fprintf(&b, "[agentbus] %d peer message%s (from other agent sessions, not the user)%s:\n", len(msgs), plural(len(msgs)), action)
	default:
		fmt.Fprintf(&b, "[agentbus] %d message%s from the human user (via the bus)%s:\n", len(msgs), plural(len(msgs)), action)
	}
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s] %s\n", time.Unix(m.Ts, 0).Format("15:04"), MsgCompact(m))
		if context := workContext(m); context != "" {
			fmt.Fprintf(&b, "  context: %s · id %s\n", context, m.ID)
		}
	}
	if len(mustReply) > 0 {
		if bus.HasUrgent(msgs) {
			b.WriteString("[agentbus] handle the urgent item, then reply (pre-authorized, one line):\n")
		} else {
			b.WriteString("[agentbus] ACTION REQUIRED — direct request needs a one-line reply (pre-authorized):\n")
		}
		for _, from := range mustReply {
			fmt.Fprintf(&b, "[agentbus]   %s\n", replyCommand(from))
		}
		if len(maybeReply) > 0 {
			b.WriteString("[agentbus] broadcast request: reply only if it concerns your work.\n")
		}
	} else if len(maybeReply) > 0 {
		b.WriteString("[agentbus] broadcast request: reply only if it concerns your work: ")
		for i, from := range maybeReply {
			if i > 0 {
				b.WriteString(" · ")
			}
			b.WriteString(replyCommand(from))
		}
		b.WriteString("\n")
	} else {
		b.WriteString("[agentbus] no reply needed — act only if it affects your work · roster: agentbus who\n")
	}
	b.WriteString("[agentbus] if this changes current work, use the supplied context, adjust the existing plan and announce revised scope; preserve the user's goal and do not restart\n")
	return b.String()
}

func workContext(m bus.Msg) string {
	if m.Context == nil {
		return ""
	}
	var parts []string
	where := m.Context.Cwd
	if m.Context.Branch != "" && m.Context.Branch != "HEAD" {
		where += "@" + m.Context.Branch
	}
	if where != "" {
		parts = append(parts, where)
	}
	if m.Context.Status != "" {
		parts = append(parts, bus.Trunc(m.Context.Status, 120))
	}
	return strings.Join(parts, " · ")
}

func isHumanMsg(m bus.Msg) bool {
	return m.From == "user" || m.From == "user-web"
}

func replySenders(msgs []bus.Msg) (must, maybe []string) {
	mustSeen, maybeSeen := map[string]bool{}, map[string]bool{}
	add := func(dst *[]string, seen map[string]bool, from string) {
		if from == "" || seen[from] {
			return
		}
		seen[from] = true
		*dst = append(*dst, from)
	}
	for _, m := range msgs {
		switch {
		case m.Kind == bus.KindUrgent:
			add(&must, mustSeen, m.From)
		case m.Kind == bus.KindRequest && m.To != "all":
			add(&must, mustSeen, m.From)
		case m.Kind == bus.KindRequest:
			add(&maybe, maybeSeen, m.From)
		}
	}
	return must, maybe
}

func replyCommand(from string) string {
	return fmt.Sprintf("agentbus reply --to %s \"...\"", from)
}

func MsgCompact(m bus.Msg) string {
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

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func needs(n int) string {
	if n == 1 {
		return "s"
	}
	return ""
}

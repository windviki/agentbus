// Package mcp implements a minimal stdio MCP server exposing agentbus tools.
// This is the deep-integration path for harnesses that support MCP tools but
// have no hook system (kimi, dsh, ...). Session identity is derived from the
// owning agent process via /proc ancestry, so every tool call from the same
// agent addresses the same mailbox.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"agentbus/internal/client"
	"agentbus/internal/procinfo"
	"agentbus/internal/proto"
	"agentbus/internal/server"
)

const protocolVersion = "2025-06-18"

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var tools = []toolDef{
	{
		Name: "agentbus_who",
		Description: "List live AI agent sessions on this machine (name, agent CLI, what they are working on, worktree). " +
			"Run this before starting non-trivial work to spot overlapping parallel sessions.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	},
	{
		Name:        "agentbus_announce",
		Description: "Publish/update your session status so other parallel agents can see what you are doing, e.g. \"fixing auth flow in worktree wt-auth\".",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "description": "one line: verb + what + where"},
			},
			"required": []string{"status"},
		},
	},
	{
		Name: "agentbus_send",
		Description: "Send a message to another agent session by name, or broadcast with to=\"all\". " +
			"kind: chat (default) | request (direct request wakes an idle peer and expects a reply) | reply | notice (no reply needed) | urgent (real interruption; also wakes).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to":   map[string]any{"type": "string", "description": "session name, or \"all\""},
				"body": map[string]any{"type": "string"},
				"kind": map[string]any{"type": "string", "enum": []string{"chat", "request", "reply", "notice", "urgent"}},
			},
			"required": []string{"to", "body"},
		},
	},
	{
		Name: "agentbus_ask",
		Description: "Ask another agent session a question and wait for its reply (request-reply). " +
			"The directed request wakes an idle peer, so use it for a decision that blocks you; the reply is returned directly.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to":           map[string]any{"type": "string"},
				"body":         map[string]any{"type": "string"},
				"wait_seconds": map[string]any{"type": "number", "description": "max seconds to wait (default 120)"},
			},
			"required": []string{"to", "body"},
		},
	},
	{
		Name:        "agentbus_inbox",
		Description: "Read messages addressed to this session. Pass wait_seconds to long-poll for new ones.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"peek":         map[string]any{"type": "boolean", "description": "read without marking delivered"},
				"wait_seconds": map[string]any{"type": "number"},
			},
		},
	},
}

// Run reads JSON-RPC requests (newline-delimited) on stdin until EOF.
func Run() error {
	home := os.Getenv("AGENTBUS_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			h = "."
		}
		home = h + "/.agentbus"
	}
	out := json.NewEncoder(os.Stdout)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			if req.ID != nil {
				_ = out.Encode(errResp(req.ID, -32700, "parse error"))
			}
			continue
		}
		if req.ID == nil || len(req.ID) == 0 {
			continue // notification (initialized/cancelled) — nothing to answer
		}
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			pv := protocolVersion
			if p.ProtocolVersion != "" {
				pv = p.ProtocolVersion
			}
			_ = out.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"protocolVersion": pv,
					"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
					"serverInfo":      map[string]any{"name": "agentbus", "version": server.Version},
				},
			})
		case "ping":
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		case "tools/list":
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": tools}})
		case "tools/call":
			result, isErr := callTool(home, req.Params)
			resp := map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": result}},
					"isError": isErr,
				},
			}
			_ = out.Encode(resp)
		default:
			_ = out.Encode(errResp(req.ID, -32601, "method not found: "+req.Method))
		}
	}
	return sc.Err()
}

func errResp(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

// sessionID resolves (and lazily registers) this MCP server's agent session.
func sessionID(home string) (string, error) {
	a := procinfo.Detect()
	if a.Pid <= 0 {
		return "", fmt.Errorf("no owning agent process detected (run agentbus MCP server as a child of your agent CLI)")
	}
	key := fmt.Sprintf("pid:%d:%s", a.Pid, a.Start)
	label := a.Label
	if label == "" {
		label = "unknown"
	}
	resp, err := client.Call(home, true, proto.Request{
		Op: "register", ID: key, Agent: label, Pid: a.Pid, PidStart: a.Start,
	})
	if err != nil {
		return "", err
	}
	if !resp.Ok {
		return "", fmt.Errorf("%s", resp.Error)
	}
	return key, nil
}

func callTool(home string, params json.RawMessage) (string, bool) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "bad params: " + err.Error(), true
	}
	var args map[string]any
	_ = json.Unmarshal(p.Arguments, &args)
	str := func(k string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return ""
	}
	num := func(k string, def float64) float64 {
		if v, ok := args[k].(float64); ok {
			return v
		}
		return def
	}
	home = strings.TrimSpace(home)

	switch p.Name {
	case "agentbus_who":
		resp, err := client.Call(home, true, proto.Request{Op: "who"})
		if err != nil {
			return err.Error(), true
		}
		if !resp.Ok {
			return resp.Error, true
		}
		if len(resp.Sessions) == 0 {
			return "no other live agent sessions", false
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d live agent session(s):\n", len(resp.Sessions))
		for _, s := range resp.Sessions {
			fmt.Fprintf(&b, "- %s · %s · %s · %s ago", s.Name, s.Agent, s.Cwd, agoStr(s.LastSeen))
			if s.Branch != "" && s.Branch != "HEAD" {
				fmt.Fprintf(&b, "@%s", s.Branch)
			}
			if s.Status != "" {
				fmt.Fprintf(&b, " · \"%s\"", s.Status)
			}
			b.WriteByte('\n')
		}
		b.WriteString("message: agentbus_send(to=<name>, body=...) or agentbus_ask(to=<name>, body=...)")
		return b.String(), false

	case "agentbus_announce":
		key, err := sessionID(home)
		if err != nil {
			return err.Error(), true
		}
		resp, err := client.Call(home, true, proto.Request{Op: "announce", ID: key, Status: str("status")})
		if err != nil {
			return err.Error(), true
		}
		if !resp.Ok {
			return resp.Error, true
		}
		return "status of '" + resp.Name + "' published", false

	case "agentbus_send":
		key, err := sessionID(home)
		if err != nil {
			return err.Error(), true
		}
		resp, err := client.Call(home, true, proto.Request{
			Op: "send", ID: key, To: str("to"), Body: str("body"), Kind: str("kind"),
		})
		if err != nil {
			return err.Error(), true
		}
		if !resp.Ok {
			return resp.Error, true
		}
		if resp.Dup {
			return "duplicate suppressed (identical message sent moments ago)", false
		}
		return fmt.Sprintf("delivered to '%s' (%d recipient(s))", str("to"), resp.Recipients), false

	case "agentbus_ask":
		key, err := sessionID(home)
		if err != nil {
			return err.Error(), true
		}
		resp, err := client.Call(home, true, proto.Request{
			Op: "ask", ID: key, To: str("to"), Body: str("body"),
			WaitMs: int(num("wait_seconds", 120) * 1000),
		})
		if err != nil {
			return err.Error(), true
		}
		if !resp.Ok {
			return resp.Error, true
		}
		if len(resp.Messages) == 0 {
			return fmt.Sprintf("no reply from '%s' before timeout — proceed and re-check later", str("to")), false
		}
		m := resp.Messages[0]
		return "reply from " + m.From + ": " + m.Body, false

	case "agentbus_inbox":
		key, err := sessionID(home)
		if err != nil {
			return err.Error(), true
		}
		peek := false
		if v, ok := args["peek"].(bool); ok {
			peek = v
		}
		ack := !peek
		resp, err := client.Call(home, true, proto.Request{
			Op: "poll", ID: key, Ack: &ack, WaitMs: int(num("wait_seconds", 0) * 1000),
		})
		if err != nil {
			return err.Error(), true
		}
		if !resp.Ok {
			return resp.Error, true
		}
		if len(resp.Messages) == 0 {
			return "no new messages", false
		}
		var b strings.Builder
		for _, m := range resp.Messages {
			fmt.Fprintf(&b, "[%s] %s→%s: %s\n", kindTag(m.Kind), m.From, m.To, m.Body)
		}
		return b.String(), false
	}
	return "unknown tool " + p.Name, true
}

func kindTag(k string) string {
	if k == "" {
		return "chat"
	}
	return k
}

func agoStr(ts int64) string {
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

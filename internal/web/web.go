// Package web serves the agentbus dashboard and the A2A-compatible HTTP
// boundary on loopback. Everything under the page uses relative URLs so it
// works behind code-server's /proxy/<port>/ path unchanged.
package web

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"agentbus/internal/bus"
)

//go:embed index.html
var indexHTML []byte

type server struct {
	b       *bus.Bus
	version string
	started time.Time
	token   string
	baseURL string
}

// Start launches the dashboard/A2A listener on 127.0.0.1. It never blocks and
// never fails the daemon: on bind error it logs and returns "". Disable with
// AGENTBUS_WEB=0; port via AGENTBUS_WEB_PORT (default 8787); bearer token via
// AGENTBUS_WEB_TOKEN.
func Start(b *bus.Bus, version string, started time.Time) string {
	if os.Getenv("AGENTBUS_WEB") == "0" {
		return ""
	}
	port := os.Getenv("AGENTBUS_WEB_PORT")
	if port == "" {
		port = "8787"
	}
	addr := "127.0.0.1:" + port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("[agentbus] web disabled: %v", err)
		return ""
	}
	s := &server{b: b, version: version, started: started, token: os.Getenv("AGENTBUS_WEB_TOKEN"), baseURL: "http://" + addr}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/state", s.guard(s.handleState))
	mux.HandleFunc("/api/send", s.guard(s.handleSend))
	mux.HandleFunc("/a2a/", s.guard(s.handleA2A))
	mux.HandleFunc("/.well-known/agent-card.json", s.guard(s.handleRootCard))
	go func() { _ = http.Serve(ln, mux) }()
	return addr
}

// Addr returns the loopback address of the web listener ("" when disabled).
func Addr() string {
	if os.Getenv("AGENTBUS_WEB") == "0" {
		return ""
	}
	port := os.Getenv("AGENTBUS_WEB_PORT")
	if port == "" {
		port = "8787"
	}
	return "127.0.0.1:" + port
}

func (s *server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" &&
			r.Header.Get("X-Agentbus-Token") != s.token &&
			r.URL.Query().Get("token") != s.token &&
			r.Header.Get("Authorization") != "Bearer "+s.token {
			http.Error(w, `{"ok":false,"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

type stateDTO struct {
	OK       bool          `json:"ok"`
	Version  string        `json:"version"`
	Uptime   int64         `json:"uptime"`
	Now      int64         `json:"now"`
	WebIdent string        `json:"web_identity"`
	Sessions []bus.Session `json:"sessions"`
	Messages []bus.Msg     `json:"messages"`
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, stateDTO{
		OK:       true,
		Version:  s.version,
		Uptime:   int64(time.Since(s.started) / time.Second),
		Now:      time.Now().Unix(),
		WebIdent: "user-web",
		Sessions: s.b.Who(),
		Messages: s.b.Log(100),
	})
}

type sendDTO struct {
	To   string `json:"to"`
	Body string `json:"body"`
	Kind string `json:"kind"`
	From string `json:"from"`
}

func (s *server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in sendDTO
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad json: " + err.Error()})
		return
	}
	from := in.From
	if bus.NormName(from) == "" {
		from = "user-web"
	}
	m, n, dup, err := s.b.Send(from, in.To, in.Body, in.Kind, "")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "messageId": m.ID, "recipients": n, "dup": dup,
	})
}

// --- A2A boundary ------------------------------------------------------------
//
// agentbus speaks a pragmatic subset of A2A (agentprotocol spec): JSON-RPC 2.0
// over HTTP, `message/send` with text parts, one agent URL per session at
// /a2a/{name}, and an Agent Card at /.well-known/agent-card.json relative to
// that URL. Streaming/tasks/artifacts are not implemented (v1); see DESIGN.md
// for the envelope mapping.

func (s *server) handleRootCard(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":               "agentbus",
		"description":        "Local message bus for concurrent AI coding-agent sessions; per-agent endpoints live at /a2a/{name}",
		"url":                s.baseURL + "/",
		"version":            s.version,
		"protocolVersion":    "0.3.0",
		"preferredTransport": "JSONRPC",
		"capabilities":       map[string]any{"streaming": false, "pushNotifications": false, "stateTransitionHistory": false},
		"defaultInputModes":  []string{"text/plain"},
		"defaultOutputModes": []string{"text/plain"},
		"skills":             []map[string]any{{"id": "agentbus-router", "name": "Route messages to local agent sessions"}},
	})
}

func (s *server) sessionByName(name string) (bus.Session, bool) {
	for _, sess := range s.b.Who() {
		if sess.Name == name {
			return sess, true
		}
	}
	return bus.Session{}, false
}

func (s *server) handleA2A(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/a2a/")
	parts := strings.Split(rest, "/")
	name := parts[0]
	sess, ok := s.sessionByName(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "no live session " + name})
		return
	}
	// Agent Card: GET /a2a/{name}/.well-known/agent-card.json
	if len(parts) > 1 && strings.Join(parts[1:], "/") == ".well-known/agent-card.json" {
		desc := fmt.Sprintf("agentbus session '%s' (%s)", name, sess.Agent)
		if sess.Status != "" {
			desc += " — " + sess.Status
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name":               name,
			"description":        desc,
			"url":                s.baseURL + "/a2a/" + name,
			"version":            s.version,
			"protocolVersion":    "0.3.0",
			"preferredTransport": "JSONRPC",
			"capabilities":       map[string]any{"streaming": false, "pushNotifications": false, "stateTransitionHistory": false},
			"defaultInputModes":  []string{"text/plain"},
			"defaultOutputModes": []string{"text/plain"},
			"skills":             []map[string]any{{"id": "agentbus-session", "name": "Local parallel coding session"}},
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST JSON-RPC or GET agent card", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  struct {
			Message struct {
				MessageID string `json:"messageId"`
				Role      string `json:"role"`
				Parts     []struct {
					Kind string `json:"kind"`
					Text string `json:"text"`
				} `json:"parts"`
				Metadata struct {
					Agentbus struct {
						From    string `json:"from"`
						Kind    string `json:"kind"`
						ReplyTo string `json:"replyTo"`
					} `json:"agentbus"`
				} `json:"metadata"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error: " + err.Error()}})
		return
	}
	rpcErr := func(code int, msg string) {
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": code, "message": msg}})
	}
	if req.Method != "message/send" {
		rpcErr(-32601, "method not supported: agentbus implements the A2A subset 'message/send' (text parts) only")
		return
	}
	var text strings.Builder
	for _, p := range req.Params.Message.Parts {
		if p.Kind == "text" || p.Kind == "" {
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(p.Text)
		}
	}
	md := req.Params.Message.Metadata.Agentbus
	from := md.From
	if bus.NormName(from) == "" {
		from = "a2a-client"
	}
	m, _, _, err := s.b.Send(from, name, text.String(), md.Kind, md.ReplyTo)
	if err != nil {
		rpcErr(-32000, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result": map[string]any{
			"jsonrpc":   "2.0",
			"id":        m.ID,
			"kind":      "message",
			"role":      "agent",
			"parts":     []map[string]any{{"kind": "text", "text": "queued for delivery to '" + name + "'"}},
			"contextId": name,
			"metadata":  map[string]any{"agentbus": map[string]any{"queued": true}},
		},
	})
}

// ServeHTTPCheck pings the dashboard (used by doctor).
func ServeHTTPCheck() bool {
	addr := Addr()
	if addr == "" {
		return false
	}
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + addr + "/api/state")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

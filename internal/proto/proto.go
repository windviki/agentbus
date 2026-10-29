// Package proto defines the JSON-lines request/response protocol spoken over
// the agentbus unix socket.
package proto

import "agentbus/internal/bus"

// Request is one client -> daemon call. Session-scoped ops identify the
// caller by ID (the stable session key); senders may also use From directly
// (human "user" case).
type Request struct {
	Op       string `json:"op"`
	ID       string `json:"id,omitempty"`
	Want     string `json:"want,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Pid      int    `json:"pid,omitempty"`
	PidStart string `json:"pid_start,omitempty"`
	Status   string `json:"status,omitempty"`
	To       string `json:"to,omitempty"`
	From     string `json:"from,omitempty"`
	Body     string `json:"body,omitempty"`
	Type     string `json:"type,omitempty"`
	WaitMs   int    `json:"wait_ms,omitempty"`
	Ack      *bool  `json:"ack,omitempty"`
	N        int    `json:"n,omitempty"`
}

// Response is the daemon's answer. Ok=false means Error carries the reason.
type Response struct {
	Ok          bool          `json:"ok"`
	Error       string        `json:"error,omitempty"`
	Version     string        `json:"version,omitempty"`
	Home        string        `json:"home,omitempty"`
	Uptime      int64         `json:"uptime,omitempty"`
	Name        string        `json:"name,omitempty"`
	RenamedFrom string        `json:"renamed_from,omitempty"`
	Agent       string        `json:"agent,omitempty"`
	MsgID       string        `json:"msg_id,omitempty"`
	Dup         bool          `json:"dup,omitempty"`
	Recipients  int           `json:"recipients,omitempty"`
	Sessions    []bus.Session `json:"sessions,omitempty"`
	Messages    []bus.Msg     `json:"messages,omitempty"`
	Mine        []bus.Msg     `json:"mine,omitempty"`
	Recent      []bus.Msg     `json:"recent,omitempty"`
}

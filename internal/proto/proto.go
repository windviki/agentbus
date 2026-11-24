// Package proto defines the JSON-lines request/response protocol spoken over
// the agentbus unix socket.
package proto

import "github.com/windviki/agentbus/internal/bus"

// Delivery is the wire form of a native endpoint. Unlike bus.Delivery, Token
// is intentionally serialized so a hook can pass it to the daemon once; the
// bus session and persisted state always omit it.
type Delivery struct {
	Driver   string `json:"driver"`
	Address  string `json:"address,omitempty"`
	NativeID string `json:"native_id,omitempty"`
	Wake     string `json:"wake,omitempty"`
	Token    string `json:"token,omitempty"`
}

func (d Delivery) Bus() bus.Delivery {
	return bus.Delivery{Driver: d.Driver, Address: d.Address, NativeID: d.NativeID, Wake: d.Wake, Token: d.Token}
}

// Request is one client -> daemon call. Session-scoped ops identify the
// caller by ID (the stable session key); senders may also use From directly
// (human "user" case).
type Request struct {
	Op       string `json:"op"`
	ID       string `json:"id,omitempty"`
	Want     string `json:"want,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Pid      int    `json:"pid,omitempty"`
	PidStart string `json:"pid_start,omitempty"`
	Status   string `json:"status,omitempty"`
	// State is the activity state a hook reports with a register, poll or
	// state op: "working" inside a turn, "idle" once at the prompt.
	State      string    `json:"state,omitempty"`
	Delivery   *Delivery `json:"delivery,omitempty"`
	To         string    `json:"to,omitempty"`
	From       string    `json:"from,omitempty"`
	Body       string    `json:"body,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	ReplyTo    string    `json:"reply_to,omitempty"`
	WaitMs     int       `json:"wait_ms,omitempty"`
	Ack        *bool     `json:"ack,omitempty"`
	WakeOnly   bool      `json:"wake_only,omitempty"`
	WakePolicy string    `json:"wake_policy,omitempty"`
	ActiveOnly bool      `json:"active_only,omitempty"`
	Claim      string    `json:"claim,omitempty"`
	Driver     string    `json:"driver,omitempty"`
	Error      string    `json:"error,omitempty"`
	MsgID      string    `json:"msg_id,omitempty"`
	N          int       `json:"n,omitempty"`
}

// Response is the daemon's answer. Ok=false means Error carries the reason.
type Response struct {
	Ok          bool                   `json:"ok"`
	Error       string                 `json:"error,omitempty"`
	Version     string                 `json:"version,omitempty"`
	Home        string                 `json:"home,omitempty"`
	Uptime      int64                  `json:"uptime,omitempty"`
	Name        string                 `json:"name,omitempty"`
	Peer        string                 `json:"peer,omitempty"`
	RenamedFrom string                 `json:"renamed_from,omitempty"`
	Agent       string                 `json:"agent,omitempty"`
	MsgID       string                 `json:"msg_id,omitempty"`
	Dup         bool                   `json:"dup,omitempty"`
	Recipients  int                    `json:"recipients,omitempty"`
	Sessions    []bus.Session          `json:"sessions,omitempty"`
	Messages    []bus.Msg              `json:"messages,omitempty"`
	Mine        []bus.Msg              `json:"mine,omitempty"`
	Recent      []bus.Msg              `json:"recent,omitempty"`
	Receipts    map[string]bus.Receipt `json:"receipts,omitempty"`
	WebAddr     string                 `json:"web_addr,omitempty"`
}

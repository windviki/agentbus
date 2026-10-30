// Package client dials the agentbus daemon. When the daemon is not running,
// user-facing commands transparently spawn it (ssh-agent style) via
// EnsureRunning, so nobody has to manage a service by hand.
package client

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/windviki/agentbus/internal/proto"
)

func SocketPath(home string) string { return filepath.Join(home, "bus.sock") }

func dial(home string) (net.Conn, error) {
	return net.DialTimeout("unix", SocketPath(home), 2*time.Second)
}

// Alive reports whether the daemon is reachable.
func Alive(home string) bool {
	c, err := dial(home)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// Call performs one request/response round-trip. With autoStart the daemon is
// spawned on demand; otherwise a missing daemon is an error.
func Call(home string, autoStart bool, req proto.Request) (proto.Response, error) {
	c, err := dial(home)
	if err != nil && autoStart {
		if err2 := EnsureRunning(home); err2 != nil {
			return proto.Response{}, fmt.Errorf("daemon unreachable at %s: %w", SocketPath(home), err)
		}
		c, err = dial(home)
	}
	if err != nil {
		return proto.Response{}, fmt.Errorf("daemon not running (%s): %w", SocketPath(home), err)
	}
	defer c.Close()
	// Generous deadline: covers long-poll waits (agentbus inbox --wait 120).
	c.SetDeadline(time.Now().Add(3 * time.Minute))
	if err := json.NewEncoder(c).Encode(&req); err != nil {
		return proto.Response{}, fmt.Errorf("write: %w", err)
	}
	var resp proto.Response
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		return proto.Response{}, fmt.Errorf("read: %w", err)
	}
	return resp, nil
}

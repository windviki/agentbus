package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/windviki/agentbus/internal/bus"
	"github.com/windviki/agentbus/internal/proto"
)

// newTestServer builds the dispatch surface without a socket or state file
// reader; statePath lives in a temp dir so the debounced save is harmless.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	return &Server{
		home:      dir,
		started:   time.Now(),
		statePath: filepath.Join(dir, "state.json"),
		b:         bus.New(bus.DefaultOptions()),
	}
}

func TestDispatchCarriesTurnState(t *testing.T) {
	s := newTestServer(t)

	// SessionStart: registered as idle, not "in progress".
	resp, shutdown := s.dispatch(&proto.Request{
		Op: "register", ID: "sid:test", Agent: "dsh", Cwd: "/w", Repo: "/w/.git", State: bus.StateIdle,
	})
	if !resp.Ok || shutdown {
		t.Fatalf("register: ok=%v shutdown=%v err=%s", resp.Ok, shutdown, resp.Error)
	}
	who, _ := s.dispatch(&proto.Request{Op: "who"})
	if len(who.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(who.Sessions))
	}
	if who.Sessions[0].Working || who.Sessions[0].State != bus.StateIdle {
		t.Fatalf("a fresh session must start idle: %+v", who.Sessions[0])
	}

	// A prompt/tool hook piggybacks working on the poll.
	if resp, _ = s.dispatch(&proto.Request{Op: "poll", ID: "sid:test", State: bus.StateWorking}); !resp.Ok {
		t.Fatalf("poll working: %s", resp.Error)
	}
	who, _ = s.dispatch(&proto.Request{Op: "who"})
	if !who.Sessions[0].Working {
		t.Fatal("working state set through poll must be reflected by who")
	}

	// The Stop hook piggybacks idle on its peek.
	ack := false
	if resp, _ = s.dispatch(&proto.Request{Op: "poll", ID: "sid:test", Ack: &ack, State: bus.StateIdle}); !resp.Ok {
		t.Fatalf("poll idle: %s", resp.Error)
	}
	who, _ = s.dispatch(&proto.Request{Op: "who"})
	if who.Sessions[0].Working {
		t.Fatal("idle state set through poll must be reflected by who")
	}

	// A harness without hooks can report explicitly.
	if resp, _ = s.dispatch(&proto.Request{Op: "state", ID: "sid:test", State: bus.StateWorking}); !resp.Ok {
		t.Fatalf("state op: %s", resp.Error)
	}
	who, _ = s.dispatch(&proto.Request{Op: "who"})
	if !who.Sessions[0].Working {
		t.Fatal("state op must mark the session working")
	}

	// Invalid state and unknown session fail loudly.
	if resp, _ = s.dispatch(&proto.Request{Op: "state", ID: "sid:test", State: "busy"}); resp.Ok {
		t.Fatal("invalid state must be rejected")
	}
	if resp, _ = s.dispatch(&proto.Request{Op: "state", ID: "sid:missing", State: bus.StateIdle}); resp.Ok {
		t.Fatal("unknown session must be rejected")
	}
}

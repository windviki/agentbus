package server

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/windviki/agentbus/internal/bus"
	"github.com/windviki/agentbus/internal/proto"
)

func TestNativePushAcceptedAndRecorded(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "native.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	frames := make(chan map[string]any, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		dec := json.NewDecoder(conn)
		var auth map[string]any
		if dec.Decode(&auth) != nil {
			return
		}
		var frame map[string]any
		if dec.Decode(&frame) == nil {
			frames <- frame
		}
	}()

	s := newTestServer(t)
	s.dispatch(&proto.Request{Op: "register", ID: "sid:a", Want: "alice", State: bus.StateWorking})
	resp, _ := s.dispatch(&proto.Request{
		Op: "register", ID: "sid:b", Want: "bob", State: bus.StateWorking,
		Delivery: &proto.Delivery{Driver: "claude", Address: socket, NativeID: "sid:b", Token: "token"},
	})
	if !resp.Ok {
		t.Fatal(resp.Error)
	}
	resp, _ = s.dispatch(&proto.Request{Op: "send", ID: "sid:a", To: "bob", Body: "push end to end", Kind: bus.KindRequest})
	if !resp.Ok {
		t.Fatal(resp.Error)
	}
	s.flushNative()
	select {
	case frame := <-frames:
		content := frame["message"].(map[string]any)["content"].(string)
		if !strings.Contains(content, "push end to end") {
			t.Fatalf("native content = %q", content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native push did not arrive")
	}
	receipt := s.b.Receipts(resp.MsgID)["bob"]
	if receipt.State != bus.DeliveryAccepted || receipt.Driver != "claude" {
		t.Fatalf("receipt = %+v", receipt)
	}
	ack := true
	polled, _ := s.dispatch(&proto.Request{Op: "poll", ID: "sid:b", Ack: &ack})
	if len(polled.Messages) != 0 {
		t.Fatalf("native accepted message was also delivered by hook: %+v", polled.Messages)
	}
}

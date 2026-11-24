package delivery

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/windviki/agentbus/internal/bus"
)

func TestIncomingTextCarriesWorkContextAndAdjustmentGuidance(t *testing.T) {
	text := IncomingText([]bus.Msg{{
		ID: "m-context", From: "alice", To: "bob", Kind: bus.KindNotice, Body: "protocol changed", Ts: 1730000000,
		Context: &bus.WorkContext{Cwd: "/work/api", Branch: "main", Status: "editing protocol"},
	}})
	for _, want := range []string{"protocol changed", "/work/api@main", "editing protocol", "m-context", "adjust the existing plan", "do not restart"} {
		if !strings.Contains(text, want) {
			t.Errorf("incoming text missing %q:\n%s", want, text)
		}
	}
}

func TestClaudePushWritesAuthenticatedNativeFrame(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "claude.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	type result struct {
		Auth  map[string]any
		Frame map[string]any
		Err   error
	}
	got := make(chan result, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- result{Err: err}
			return
		}
		defer conn.Close()
		dec := json.NewDecoder(conn)
		var auth, frame map[string]any
		if err := dec.Decode(&auth); err != nil {
			got <- result{Err: err}
			return
		}
		if err := dec.Decode(&frame); err != nil {
			got <- result{Err: err}
			return
		}
		got <- result{Auth: auth, Frame: frame}
	}()
	s := bus.Session{
		Name: "bob", Working: true,
		Delivery: &bus.Delivery{Driver: "claude", Address: socket, NativeID: "sid:claude-1", Token: "test-token"},
	}
	msgs := []bus.Msg{{ID: "m1", From: "alice", To: "bob", Body: "native hello", Ts: time.Now().Unix()}}
	if err := Push(context.Background(), s, msgs); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-got:
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		if r.Auth["token"] != "test-token" {
			t.Fatalf("missing auth frame: %+v", r.Auth)
		}
		if r.Frame["session_id"] != "claude-1" || r.Frame["priority"] != "next" || r.Frame["msgV"] != float64(1) {
			t.Fatalf("bad native frame: %+v", r.Frame)
		}
		content := r.Frame["message"].(map[string]any)["content"].(string)
		if !strings.Contains(content, "native hello") {
			t.Fatalf("message body missing: %q", content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native frame was not received")
	}
}

func TestKimiPushSteersWorkingAndPromptsIdle(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-kimi-token" {
			t.Errorf("missing bearer token")
		}
		paths = append(paths, r.URL.Path)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "success", "data": map[string]any{}})
	}))
	defer server.Close()
	t.Setenv("AGENTBUS_KIMI_URL", server.URL)
	t.Setenv("AGENTBUS_KIMI_TOKEN", "test-kimi-token")
	msgs := []bus.Msg{{ID: "m-kimi", From: "alice", To: "bob", Kind: bus.KindRequest, Body: "adjust now", Ts: time.Now().Unix()}}
	for _, tc := range []struct {
		working bool
		action  string
	}{{true, "steer"}, {false, "prompts"}} {
		s := bus.Session{Name: "bob", Working: tc.working, Delivery: &bus.Delivery{Driver: "kimi", NativeID: "sid:kimi-1"}}
		if err := Push(context.Background(), s, msgs); err != nil {
			t.Fatal(err)
		}
		want := "/api/v1/sessions/kimi-1/" + tc.action
		if paths[len(paths)-1] != want {
			t.Fatalf("path = %q, want %q", paths[len(paths)-1], want)
		}
	}
	if bodies[0]["prompt_id"] != bodies[1]["prompt_id"] {
		t.Fatalf("retry prompt id is not stable: %+v", bodies)
	}
	content := bodies[0]["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(content, "adjust now") || !strings.Contains(content, "agentbus reply") {
		t.Fatalf("Kimi content missing request and reply path: %q", content)
	}
}

func TestKimiDiscoversLiveServerInstance(t *testing.T) {
	var authorized int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer registry-token" {
			authorized++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	t.Setenv("AGENTBUS_KIMI_URL", "")
	t.Setenv("AGENTBUS_KIMI_TOKEN", "")
	if err := os.MkdirAll(filepath.Join(home, "server", "instances"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "server.token"), []byte("registry-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	instance := map[string]any{"product": "kimi-code-desktop", "host": "0.0.0.0", "port": port, "started_at": 20}
	data, _ := json.Marshal(instance)
	if err := os.WriteFile(filepath.Join(home, "server", "instances", "one.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	base, token, err := kimiServer(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if base != server.URL || token != "registry-token" || authorized == 0 {
		t.Fatalf("discovery = %q, token %q, authorized %d", base, token, authorized)
	}
}

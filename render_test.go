package main

import (
	"strings"
	"testing"
	"time"

	"github.com/windviki/agentbus/internal/bus"
	"github.com/windviki/agentbus/internal/proto"
)

func TestJoinSummaryListsOnlyWorkingSessions(t *testing.T) {
	now := time.Now().Unix()
	repo := "/home/coder/proj/.git"
	resp := proto.Response{
		Name: "me", Agent: "dsh",
		Sessions: []bus.Session{
			{Name: "busy-one", Agent: "zcode", Cwd: "/home/coder/proj", Repo: repo,
				Working: true, State: bus.StateWorking, StateAt: now, Status: "editing parser"},
			{Name: "finished-one", Agent: "kimi", Cwd: "/home/coder/proj", Repo: repo,
				Working: false, State: bus.StateIdle, StateAt: now - 7200},
		},
	}
	out := joinSummary(resp, "/home/coder/proj", repo)
	if !strings.Contains(out, "busy-one") {
		t.Fatalf("working session missing from the roster:\n%s", out)
	}
	if strings.Contains(out, "finished-one") {
		t.Fatalf("idle session leaked into the roster:\n%s", out)
	}
	if !strings.Contains(out, "1 idle session(s) not listed") {
		t.Fatalf("idle count line missing:\n%s", out)
	}
}

func TestRosterActivityLabel(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name string
		s    bus.Session
		want string
	}{
		{"working", bus.Session{Name: "a", Working: true, State: bus.StateWorking, StateAt: now - 120}, "working "},
		{"idle", bus.Session{Name: "b", Working: false, State: bus.StateIdle, StateAt: now - 7200}, "idle "},
		{"no-state", bus.Session{Name: "c", LastSeen: now - 60}, "idle "},
	}
	for _, tc := range cases {
		if got := sessionActivity(tc.s); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: activity %q, want prefix %q", tc.name, got, tc.want)
		}
	}
}

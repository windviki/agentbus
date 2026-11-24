package bus

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNativeClaimCompletesWithoutDuplicateHookDelivery(t *testing.T) {
	b, _ := newTestBus()
	b.Register("sid:a", "alice", "claude-code", "/work", "main", 0, "")
	b.Register("sid:b", "bob", "claude-code", "/other", "topic", 0, "")
	b.SetRepo("sid:a", "/work/.git")
	b.Announce("sid:a", "refactoring delivery")
	m, _, _, err := b.Send("alice", "bob", "use the new protocol", KindRequest, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Context == nil || m.Context.Cwd != "/work" || m.Context.Branch != "main" || m.Context.Status != "refactoring delivery" {
		t.Fatalf("sender context missing: %+v", m.Context)
	}

	claimed, _, err := b.PollWithOptions("sid:b", PollOptions{Claim: "native-1", Driver: "claude"})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v, %d messages", err, len(claimed))
	}
	if msgs, _, _ := b.Poll("sid:b", true, 0); len(msgs) != 0 {
		t.Fatalf("hook consumed a claimed message: %+v", msgs)
	}
	if got := b.Receipts(m.ID)["bob"].State; got != DeliveryPushing {
		t.Fatalf("receipt = %q, want pushing", got)
	}
	if err := b.CompleteClaim("sid:b", "native-1", "claude", nil); err != nil {
		t.Fatal(err)
	}
	if msgs, _, _ := b.Poll("sid:b", true, 0); len(msgs) != 0 {
		t.Fatalf("accepted message returned again: %+v", msgs)
	}
	mine, _, _ := b.Mine("sid:b")
	if len(mine) != 1 || mine[0].ID != m.ID {
		t.Fatalf("accepted history missing: %+v", mine)
	}
	if got := b.Receipts(m.ID)["bob"]; got.State != DeliveryAccepted || got.Driver != "claude" {
		t.Fatalf("accepted receipt = %+v", got)
	}

	st := b.Snapshot()
	restored := New(DefaultOptions())
	restored.Restore(st)
	if got := restored.Receipts(m.ID)["bob"].State; got != DeliveryAccepted {
		t.Fatalf("restored receipt = %q", got)
	}
}

func TestNativeClaimFailureReleasesAndExpires(t *testing.T) {
	b, clock := newTestBus()
	b.Register("sid:a", "alice", "", "", "", 0, "")
	b.Register("sid:b", "bob", "", "", "", 0, "")
	m, _, _, _ := b.Send("alice", "bob", "fallback", KindChat, "")
	if _, _, _ = b.PollWithOptions("sid:b", PollOptions{Claim: "native-1", Driver: "kimi"}); len(b.inbox["bob"]) != 1 {
		t.Fatal("claim removed the inbox message")
	}
	if err := b.CompleteClaim("sid:b", "native-1", "kimi", errors.New("server offline")); err != nil {
		t.Fatal(err)
	}
	r := b.Receipts(m.ID)["bob"]
	if r.State != DeliveryQueued || !strings.Contains(r.Error, "offline") {
		t.Fatalf("release receipt = %+v", r)
	}
	if msgs, _, _ := b.Poll("sid:b", false, 0); len(msgs) != 1 {
		t.Fatalf("released message not visible to hooks: %+v", msgs)
	}
	b.PollWithOptions("sid:b", PollOptions{Claim: "native-2"})
	clock.advance(31 * time.Second)
	if msgs, _, _ := b.Poll("sid:b", false, 0); len(msgs) != 1 {
		t.Fatalf("expired claim still hides the message: %+v", msgs)
	}
}

func TestPollWakeAndActivePolicies(t *testing.T) {
	b, _ := newTestBus()
	b.Register("sid:a", "alice", "", "", "", 0, "")
	b.Register("sid:b", "bob", "", "", "", 0, "")
	b.Send("alice", "bob", "ordinary", KindChat, "")
	if msgs, _, _ := b.PollWithOptions("sid:b", PollOptions{WakeOnly: true}); len(msgs) != 0 {
		t.Fatalf("ordinary chat woke an idle peer: %+v", msgs)
	}
	b.Send("alice", "bob", "question", KindRequest, "")
	if msgs, _, _ := b.PollWithOptions("sid:b", PollOptions{WakeOnly: true}); len(msgs) != 2 {
		t.Fatalf("direct request did not flush the queued batch: %+v", msgs)
	}
	b.SetState("sid:b", StateIdle)
	if msgs, _, _ := b.PollWithOptions("sid:b", PollOptions{ActiveOnly: true}); len(msgs) != 0 {
		t.Fatalf("idle watcher returned messages: %+v", msgs)
	}
	b.SetState("sid:b", StateWorking)
	if msgs, _, _ := b.PollWithOptions("sid:b", PollOptions{ActiveOnly: true, Ack: true}); len(msgs) != 2 {
		t.Fatalf("active watcher did not receive its batch: %+v", msgs)
	}
}

func TestActiveWatchStopsWhenTurnEnds(t *testing.T) {
	b := New(DefaultOptions())
	b.Register("sid:a", "alice", "", "", "", 0, "")
	b.Register("sid:b", "bob", "", "", "", 0, "")
	b.SetState("sid:b", StateWorking)
	done := make(chan []Msg, 1)
	go func() {
		msgs, _, _ := b.PollWithOptions("sid:b", PollOptions{ActiveOnly: true, Ack: true, Wait: time.Second})
		done <- msgs
	}()
	time.Sleep(50 * time.Millisecond)
	b.SetState("sid:b", StateIdle)
	select {
	case msgs := <-done:
		if len(msgs) != 0 {
			t.Fatalf("stopped watcher returned messages: %+v", msgs)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("watcher did not stop after the turn became idle")
	}
	b.Send("alice", "bob", "still queued", KindChat, "")
	msgs, _, _ := b.Poll("sid:b", false, 0)
	if len(msgs) != 1 {
		t.Fatalf("idle message was consumed by the old watcher: %+v", msgs)
	}
}

func TestAskReplyCorrelationDoesNotConsumeNotice(t *testing.T) {
	b := New(DefaultOptions())
	b.Register("sid:a", "alice", "", "", "", 0, "")
	b.Register("sid:b", "bob", "", "", "", 0, "")
	b.Send("alice", "bob", "question", KindRequest, "")
	request, _, _ := b.Poll("sid:b", true, 0)
	b.Send("bob", "alice", "unrelated progress", KindNotice, "")
	if _, got, _ := b.TakeReply("sid:a", "bob", request[0].ID, 20*time.Millisecond); got {
		t.Fatal("notice was mistaken for the request reply")
	}
	b.Send("bob", "alice", "answer", KindReply, request[0].ID)
	if reply, got, _ := b.TakeReply("sid:a", "bob", request[0].ID, time.Second); !got || reply.Body != "answer" {
		t.Fatalf("correlated reply missing: %+v, %v", reply, got)
	}
	msgs, _, _ := b.Poll("sid:a", true, 0)
	if len(msgs) != 1 || msgs[0].Kind != KindNotice {
		t.Fatalf("unrelated notice was consumed: %+v", msgs)
	}
}

func TestDeliveryTokenNeverSerialized(t *testing.T) {
	s := Session{Delivery: &Delivery{Driver: "claude", Address: "/tmp/socket", Token: "secret-token"}}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-token") {
		t.Fatalf("native token leaked into session JSON: %s", data)
	}
}

package bus

import (
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	t   time.Time
	pid map[int]bool // pid -> alive
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1730000000, 0), pid: map[int]bool{}}
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
func (c *fakeClock) alive(pid int, _ string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pid == 1 {
		return true
	}
	return c.pid[pid]
}

func newTestBus() (*Bus, *fakeClock) {
	c := newFakeClock()
	b := New(Options{
		Now:            c.Now,
		PidAlive:       c.alive,
		TTLSeconds:     3600,
		MsgTTLSeconds:  3600,
		TombTTLSeconds: 600,
		RatePerMinute:  5,
	})
	return b, c
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

func TestRegisterAutoNames(t *testing.T) {
	b, _ := newTestBus()
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		s, _, err := b.Register(idOf(i), "", "zcode", "/w", "", 0, "")
		if err != nil {
			t.Fatalf("register %d: %v", i, err)
		}
		if !nameRe.MatchString(s.Name) {
			t.Fatalf("bad auto name %q", s.Name)
		}
		if seen[s.Name] {
			t.Fatalf("duplicate auto name %q", s.Name)
		}
		seen[s.Name] = true
	}
}

func idOf(i int) string { return []string{"a", "b", "c", "d", "e", "f", "g", "h"}[i] }

func TestRegisterExplicitCollisionRenames(t *testing.T) {
	b, _ := newTestBus()
	s1, _, err := b.Register("sid:1", "bob", "", "", "", 0, "")
	if err != nil || s1.Name != "bob" {
		t.Fatalf("first bob: %v %q", err, s1.Name)
	}
	s2, renamed, err := b.Register("sid:2", "bob", "", "", "", 0, "")
	if err != nil {
		t.Fatalf("second bob: %v", err)
	}
	if s2.Name != "bob-2" || renamed != "bob" {
		t.Fatalf("expected bob-2/bob, got %q/%q", s2.Name, renamed)
	}
	s3, renamed, err := b.Register("sid:3", "Bob The Builder!", "", "", "", 0, "")
	if err != nil || s3.Name != "bob-the-builder" || renamed != "" {
		t.Fatalf("normalization: %v %q %q", err, s3.Name, renamed)
	}
}

func TestRegisterIdempotent(t *testing.T) {
	b, _ := newTestBus()
	s1, _, _ := b.Register("sid:1", "bob", "claude-code", "/x", "", 0, "")
	s2, _, err := b.Register("sid:1", "bob", "", "", "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if s1.Name != s2.Name || len(b.Who()) != 1 {
		t.Fatalf("re-register not idempotent: %q vs %q", s1.Name, s2.Name)
	}
}

func TestSendDirectPollAck(t *testing.T) {
	b, _ := newTestBus()
	b.Register("sid:a", "alice", "", "", "", 0, "")
	b.Register("sid:b", "bob", "", "", "", 0, "")

	if _, _, _, err := b.Send("alice", "bob", "you there?", "", ""); err != nil {
		t.Fatal(err)
	}
	// peek keeps the message
	msgs, _, err := b.Poll("sid:b", false, 0)
	if err != nil || len(msgs) != 1 || msgs[0].Body != "you there?" {
		t.Fatalf("peek: %v %d", err, len(msgs))
	}
	msgs, _, _ = b.Poll("sid:b", false, 0)
	if len(msgs) != 1 {
		t.Fatalf("peek should not ack, got %d", len(msgs))
	}
	// ack removes it
	msgs, _, _ = b.Poll("sid:b", true, 0)
	if len(msgs) != 1 {
		t.Fatal("ack poll should return the message")
	}
	msgs, _, _ = b.Poll("sid:b", true, 0)
	if len(msgs) != 0 {
		t.Fatalf("inbox should be empty after ack, got %d", len(msgs))
	}
	// recent-mine remembers it
	mine, _, _ := b.Mine("sid:b")
	if len(mine) != 1 {
		t.Fatalf("recent-mine should keep 1, got %d", len(mine))
	}
}

func TestBroadcastExcludesSender(t *testing.T) {
	b, _ := newTestBus()
	b.Register("sid:a", "alice", "", "", "", 0, "")
	b.Register("sid:b", "bob", "", "", "", 0, "")
	b.Register("sid:c", "carol", "", "", "", 0, "")
	_, n, _, err := b.Send("alice", "all", "rebuilding main", "", "")
	if err != nil || n != 2 {
		t.Fatalf("broadcast: %v recipients=%d", err, n)
	}
	for _, id := range []string{"sid:a", "sid:b", "sid:c"} {
		msgs, _, _ := b.Poll(id, true, 0)
		want := 0
		if id != "sid:a" {
			want = 1
		}
		if len(msgs) != want {
			t.Fatalf("%s: got %d msgs, want %d", id, len(msgs), want)
		}
	}
}

func TestSendUnknownSuggests(t *testing.T) {
	b, _ := newTestBus()
	b.Register("sid:a", "bob", "", "", "", 0, "")
	_, _, _, err := b.Send("user", "bo", "hi", "", "")
	if err == nil || !strings.Contains(err.Error(), "bob") {
		t.Fatalf("expected suggestion containing bob, got %v", err)
	}
}

func TestRateLimitAndDedupe(t *testing.T) {
	b, _ := newTestBus()
	b.Register("sid:a", "alice", "", "", "", 0, "")
	b.Register("sid:b", "bob", "", "", "", 0, "")
	for i := 0; i < 5; i++ {
		if _, _, _, err := b.Send("alice", "bob", strings.Repeat("m", i+1), "", ""); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if _, _, _, err := b.Send("alice", "bob", "sixth", "", ""); err == nil || !strings.Contains(err.Error(), "rate") {
		t.Fatalf("expected rate limit, got %v", err)
	}
	// identical resend is deduped even under the limit
	b2, _ := newTestBus()
	b2.Register("x", "alice", "", "", "", 0, "")
	b2.Register("y", "bob", "", "", "", 0, "")
	m1, _, _, _ := b2.Send("alice", "bob", "same", "", "")
	m2, _, dup2, _ := b2.Send("alice", "bob", "same", "", "")
	if !dup2 || m1.ID != m2.ID {
		t.Fatalf("dedupe failed: dup=%v %s vs %s", dup2, m1.ID, m2.ID)
	}
	msgs, _, _ := b2.Poll("y", true, 0)
	if len(msgs) != 1 {
		t.Fatalf("dedupe should queue once, got %d", len(msgs))
	}
}

func TestPollWaitsForMessage(t *testing.T) {
	b, _ := newTestBus()
	b.Register("sid:a", "alice", "", "", "", 0, "")
	b.Register("sid:b", "bob", "", "", "", 0, "")
	go func() {
		time.Sleep(80 * time.Millisecond)
		b.Send("alice", "bob", "late", "", "")
	}()
	start := time.Now()
	msgs, _, err := b.Poll("sid:b", true, 3*time.Second)
	if err != nil || len(msgs) != 1 || msgs[0].Body != "late" {
		t.Fatalf("long poll: %v %d", err, len(msgs))
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("poll returned before message arrived")
	}
}

func TestReapByPid(t *testing.T) {
	b, c := newTestBus()
	c.pid[4242] = true
	b.Register("sid:a", "bob", "", "", "", 4242, "s1")
	if len(b.Who()) != 1 {
		t.Fatal("bob should be live")
	}
	c.pid[4242] = false // process died
	b.Reap()
	if len(b.Who()) != 0 {
		t.Fatal("bob should be reaped")
	}
	// name tombstoned briefly
	s, renamed, err := b.Register("sid:b", "bob", "", "", "", 0, "")
	if err != nil || s.Name != "bob-2" || renamed != "bob" {
		t.Fatalf("tombstone: %v %q %q", err, s.Name, renamed)
	}
}

func TestReapByTTL(t *testing.T) {
	b, c := newTestBus()
	b.Register("sid:a", "bob", "", "", "", 0, "")
	c.advance(2 * time.Hour)
	b.Reap()
	if len(b.Who()) != 0 {
		t.Fatal("idle session should expire by TTL")
	}
}

func TestTombstoneExpiry(t *testing.T) {
	b, c := newTestBus()
	b.Register("sid:a", "bob", "", "", "", 0, "")
	b.Leave("sid:a")
	c.advance(20 * time.Minute) // > TombTTLSeconds=600
	s, _, err := b.Register("sid:b", "bob", "", "", "", 0, "")
	if err != nil || s.Name != "bob" {
		t.Fatalf("tomb should expire: %v %q", err, s.Name)
	}
}

func TestSnapshotRestore(t *testing.T) {
	b, _ := newTestBus()
	b.Register("sid:a", "alice", "zcode", "/w", "main", 0, "")
	b.Register("sid:b", "bob", "claude-code", "/w2", "", 0, "")
	b.Send("alice", "bob", "ping", "", "")
	b.Send("alice", "all", "broadcast", "", "")
	st := b.Snapshot()

	c2 := newFakeClock()
	b2 := New(Options{Now: c2.Now, PidAlive: c2.alive, TTLSeconds: 3600, MsgTTLSeconds: 3600, TombTTLSeconds: 600, RatePerMinute: 5})
	b2.Restore(st)
	if len(b2.Who()) != 2 {
		t.Fatalf("sessions lost: %d", len(b2.Who()))
	}
	msgs, name, err := b2.Poll("sid:b", true, 0)
	if err != nil || name != "bob" || len(msgs) != 2 {
		t.Fatalf("inbox lost: %v %q %d", err, name, len(msgs))
	}
}

func TestNormName(t *testing.T) {
	cases := map[string]string{
		"  Bob The Builder ": "bob-the-builder",
		"__weird__name__":    "weird-name",
		"A/B\\C":             "a-b-c",
		"--lead--and--trail": "lead-and-trail",
		"UPPER_case 123":     "upper-case-123",
		"!!!":                "",
		"":                   "",
	}
	for in, want := range cases {
		if got := NormName(in); got != want {
			t.Errorf("NormName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKinds(t *testing.T) {
	b, _ := newTestBus()
	b.Register("sid:a", "alice", "", "", "", 0, "")
	b.Register("sid:b", "bob", "", "", "", 0, "")
	if _, _, _, err := b.Send("alice", "bob", "bad", "yell", ""); err == nil {
		t.Fatal("invalid kind should be rejected")
	}
	m, _, _, err := b.Send("alice", "bob", "heads up", KindNotice, "")
	if err != nil || m.Kind != KindNotice {
		t.Fatalf("notice: %v %v", err, m.Kind)
	}
	m, _, _, err = b.Send("alice", "bob", "stop now", KindUrgent, "")
	if err != nil || !HasUrgent([]Msg{m}) {
		t.Fatal("HasUrgent should detect urgent")
	}
	if HasUrgent([]Msg{{Kind: KindChat}}) {
		t.Fatal("chat must not count as urgent")
	}
}

func TestAskTakeFrom(t *testing.T) {
	b, c := newTestBus()
	b.Register("sid:a", "alice", "", "", "", 0, "")
	b.Register("sid:b", "bob", "", "", "", 0, "")
	// alice asks bob; a broadcast from carol must not be taken
	b.Register("sid:c", "carol", "", "", "", 0, "")
	req, _, _, err := b.Send("alice", "bob", "split files or tests?", KindRequest, "")
	if err != nil {
		t.Fatal(err)
	}
	b.Send("carol", "all", "noise broadcast", KindNotice, "")

	go func() {
		time.Sleep(60 * time.Millisecond)
		b.Send("bob", "alice", "you take files", KindReply, req.ID)
	}()
	m, got, name := b.TakeFrom("sid:a", "bob", 3*time.Second)
	if !got || name != "alice" || m.From != "bob" || m.ReplyTo != req.ID {
		t.Fatalf("TakeFrom: got=%v name=%q m=%+v", got, name, m)
	}
	// the broadcast noise stayed queued for alice
	msgs, _, _ := b.Poll("sid:a", true, 0)
	if len(msgs) != 1 || msgs[0].From != "carol" {
		t.Fatalf("broadcast should remain queued, got %+v", msgs)
	}
	// timeout path (advance the fake clock so the deadline actually passes)
	go func() {
		time.Sleep(80 * time.Millisecond)
		c.advance(2 * time.Second)
	}()
	if _, got, _ := b.TakeFrom("sid:a", "bob", 50*time.Millisecond); got {
		t.Fatal("TakeFrom should time out with no reply pending")
	}
}

func TestLastPeerReplyTarget(t *testing.T) {
	b, _ := newTestBus()
	b.Register("sid:a", "alice", "", "", "", 0, "")
	b.Register("sid:b", "bob", "", "", "", 0, "")
	b.Register("sid:c", "carol", "", "", "", 0, "")
	if _, ok := b.LastPeer("sid:a", ""); ok {
		t.Fatal("no message delivered yet")
	}
	// a human message and two peer messages, all delivered in one poll
	b.Send("user", "alice", "human note", KindChat, "")
	first, _, _, _ := b.Send("bob", "alice", "first", KindRequest, "")
	b.Send("carol", "alice", "second", KindChat, "")
	if _, _, err := b.Poll("sid:a", true, 0); err != nil {
		t.Fatal(err)
	}
	last, ok := b.LastPeer("sid:a", "")
	if !ok || last.From != "carol" {
		t.Fatalf("newest peer should be carol, got %+v ok=%v", last, ok)
	}
	last, ok = b.LastPeer("sid:a", "bob")
	if !ok || last.ID != first.ID {
		t.Fatalf("filtered peer should be bob's request, got %+v", last)
	}
	// a peer that left is not a valid reply target anymore
	b.Leave("sid:b")
	if _, ok := b.LastPeer("sid:a", "bob"); ok {
		t.Fatal("departed peer must not be a reply target")
	}
	// ... but the remaining live peer still is
	if last, ok := b.LastPeer("sid:a", ""); !ok || last.From != "carol" {
		t.Fatalf("carol should still be reachable, got %+v", last)
	}
}

func TestHumanRecipientAndReplyTarget(t *testing.T) {
	b, _ := newTestBus()
	b.Register("sid:a", "alice", "", "", "", 0, "")
	m, n, dup, err := b.Send("alice", "user", "ack for the human", KindReply, "")
	if err != nil || n != 1 || dup || m.To != "user" {
		t.Fatalf("send to human: %v n=%d dup=%v m=%+v", err, n, dup, m)
	}
	if len(b.Who()) != 1 {
		t.Fatalf("human target must not create a session")
	}
	if log := b.Log(1); len(log) != 1 || log[0].ID != m.ID {
		t.Fatalf("human message must be in the global log: %+v", log)
	}
	// a human message delivered to alice is a valid reply target
	if _, _, _, err := b.Send("user", "alice", "please stop", KindUrgent, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Poll("sid:a", true, 0); err != nil {
		t.Fatal(err)
	}
	last, ok := b.LastPeer("sid:a", "user")
	if !ok || last.Body != "please stop" {
		t.Fatalf("human message should be addressable, got %+v ok=%v", last, ok)
	}
	// the dashboard identity is addressable too
	if _, _, _, err := b.Send("user-web", "alice", "status?", KindChat, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Poll("sid:a", true, 0); err != nil {
		t.Fatal(err)
	}
	if last, ok := b.LastPeer("sid:a", "user-web"); !ok || last.From != "user-web" {
		t.Fatalf("user-web should be addressable, got %+v ok=%v", last, ok)
	}
	if _, n, _, err := b.Send("alice", "user-web", "running", KindReply, ""); err != nil || n != 1 {
		t.Fatalf("reply to user-web: %v n=%d", err, n)
	}
}

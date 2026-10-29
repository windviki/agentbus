// Package bus implements the in-memory session registry and message-routing
// core of agentbus: unique naming, per-session inboxes, presence (pid or TTL
// liveness), rate limiting, dedupe, and a persistable snapshot.
package bus

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"agentbus/internal/procinfo"
)

var (
	ErrUnknownSession = errors.New("unknown session")
	ErrNameTaken      = errors.New("name taken")
	ErrRateLimited    = errors.New("rate limited")
	ErrBadInput       = errors.New("bad input")
)

const (
	MaxBody    = 8192
	MaxStatus  = 200
	InboxCap   = 200
	MineCap    = 5
	RecentCap  = 300
	NameMax    = 32
	dupWindowS = 60
	pollTick   = 50 * time.Millisecond
)

// Msg is one message on the bus. The JSON field names follow the A2A message
// envelope where sensible (messageId, replyTo, kind) so the HTTP/A2A boundary
// can map losslessly.
type Msg struct {
	ID      string `json:"messageId"`
	ReplyTo string `json:"replyTo,omitempty"`
	From    string `json:"from"`
	To      string `json:"to"` // session name or "all"
	Kind    string `json:"kind,omitempty"`
	Body    string `json:"body"`
	Ts      int64  `json:"ts"`
}

// Message kinds — they drive delivery behaviour (see docs/design.md "delivery
// ladder"): only Urgent may wake an idle session via the Stop hook; the rest
// are injected at the next natural boundary (next tool call / next prompt).
const (
	KindChat    = "chat"    // default conversation message
	KindRequest = "request" // expects an answer (agentbus ask)
	KindReply   = "reply"   // answer to a request (carries ReplyTo)
	KindNotice  = "notice"  // one-way FYI, no reply expected
	KindUrgent  = "urgent"  // may wake an idle session (human interrupt)
)

func ValidKind(k string) bool {
	switch k {
	case KindChat, KindRequest, KindReply, KindNotice, KindUrgent:
		return true
	}
	return false
}

// HasUrgent reports whether any message in the list is urgent.
func HasUrgent(msgs []Msg) bool {
	for _, m := range msgs {
		if m.Kind == KindUrgent {
			return true
		}
	}
	return false
}

// Session is one agent session on the bus. ID is a stable client-side key
// ("sid:<uuid>" from a hook session id, or "pid:<pid>:<start>"); Name is the
// short human/agent-facing handle messages are addressed to.
type Session struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Agent     string `json:"agent,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Pid       int    `json:"pid,omitempty"`
	PidStart  string `json:"pid_start,omitempty"`
	Status    string `json:"status,omitempty"`
	CreatedAt int64  `json:"created_at"`
	LastSeen  int64  `json:"last_seen"`
}

// Options tunes bus behaviour (injected by tests).
type Options struct {
	Now            func() time.Time
	PidAlive       func(pid int, start string) bool
	TTLSeconds     int64 // sessions without a pid expire after this idle time
	MsgTTLSeconds  int64
	TombTTLSeconds int64 // freed names stay reserved this long
	RatePerMinute  int
}

func DefaultOptions() Options {
	return Options{
		Now:            time.Now,
		PidAlive:       procinfo.Alive,
		TTLSeconds:     6 * 3600,
		MsgTTLSeconds:  6 * 3600,
		TombTTLSeconds: 3600,
		RatePerMinute:  20,
	}
}

// State is the persistable snapshot of the bus.
type State struct {
	Version  int              `json:"version"`
	Seq      int64            `json:"seq"`
	Sessions []*Session       `json:"sessions"`
	Inboxes  map[string][]Msg `json:"inboxes"`
	Tomb     map[string]int64 `json:"tomb"`
}

type dupEntry struct {
	m  Msg
	ts int64
}

type Bus struct {
	mu     sync.Mutex
	opt    Options
	sess   map[string]*Session // by ID
	byName map[string]*Session
	inbox  map[string][]Msg // by name
	mine   map[string][]Msg // recently delivered, by name
	tomb   map[string]int64 // name -> free-at unix
	recent []Msg
	rates  map[string][]int64
	dup    map[string]dupEntry
	seq    int64
}

func New(o Options) *Bus {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PidAlive == nil {
		o.PidAlive = func(int, string) bool { return true }
	}
	if o.TTLSeconds <= 0 {
		o.TTLSeconds = 6 * 3600
	}
	if o.MsgTTLSeconds <= 0 {
		o.MsgTTLSeconds = 6 * 3600
	}
	if o.TombTTLSeconds <= 0 {
		o.TombTTLSeconds = 3600
	}
	if o.RatePerMinute <= 0 {
		o.RatePerMinute = 20
	}
	return &Bus{
		opt:    o,
		sess:   map[string]*Session{},
		byName: map[string]*Session{},
		inbox:  map[string][]Msg{},
		mine:   map[string][]Msg{},
		tomb:   map[string]int64{},
		rates:  map[string][]int64{},
		dup:    map[string]dupEntry{},
	}
}

func (b *Bus) now() int64 { return b.opt.Now().Unix() }

// Register creates a session or refreshes an existing one (idempotent by ID).
// want may be empty (auto name). The second return value is the originally
// requested name when a collision forced a rename.
func (b *Bus) Register(id, want, agent, cwd, branch string, pid int, pidStart string) (*Session, string, error) {
	if id == "" {
		return nil, "", fmt.Errorf("%w: empty session id", ErrBadInput)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reapLocked()
	if s := b.sess[id]; s != nil {
		s.LastSeen = b.now()
		if agent != "" {
			s.Agent = agent
		}
		if cwd != "" {
			s.Cwd = cwd
		}
		if branch != "" {
			s.Branch = branch
		}
		if pid != 0 {
			s.Pid, s.PidStart = pid, pidStart
		}
		return s, "", nil
	}
	name, renamed, err := b.pickNameLocked(want)
	if err != nil {
		return nil, "", err
	}
	n := b.now()
	s := &Session{
		ID: id, Name: name, Agent: agent, Cwd: cwd, Branch: branch,
		Pid: pid, PidStart: pidStart, CreatedAt: n, LastSeen: n,
	}
	b.sess[id] = s
	b.byName[name] = s
	return s, renamed, nil
}

// NameOf resolves a session id to its display name.
func (b *Bus) NameOf(id string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sess[id]
	if s == nil {
		return "", false
	}
	return s.Name, true
}

// Announce publishes/updates the session's status ("doing X in worktree Y").
func (b *Bus) Announce(id, status string) (*Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reapLocked()
	s := b.sess[id]
	if s == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	s.Status = Trunc(strings.TrimSpace(status), MaxStatus)
	s.LastSeen = b.now()
	return s, nil
}

// Send routes one message. to = "all" broadcasts to every live session except
// the sender. kind selects the delivery class (chat/request/reply/notice/
// urgent); replyTo correlates a reply with the message it answers. Returns
// the message, recipient count, and whether it was dropped as a duplicate of
// a recent identical send.
func (b *Bus) Send(from, to, body, kind, replyTo string) (Msg, int, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reapLocked()

	body = strings.TrimSpace(body)
	if body == "" || len(body) > MaxBody {
		return Msg{}, 0, false, fmt.Errorf("%w: body must be 1..%d bytes", ErrBadInput, MaxBody)
	}
	from = NormName(from)
	if from == "" {
		return Msg{}, 0, false, fmt.Errorf("%w: empty sender", ErrBadInput)
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = KindChat
	}
	if !ValidKind(kind) {
		return Msg{}, 0, false, fmt.Errorf("%w: kind must be chat|request|reply|notice|urgent", ErrBadInput)
	}
	now := b.now()
	if !b.allowRateLocked(from, now) {
		return Msg{}, 0, false, fmt.Errorf("%w: over %d msgs/min from %q — pause and summarize", ErrRateLimited, b.opt.RatePerMinute, from)
	}
	to = strings.ToLower(strings.TrimSpace(to))
	broadcast := to == "" || to == "all" || to == "#all"
	dest := to
	if broadcast {
		dest = "all"
	}
	// "user" is the human side of the bus: there is no session process to
	// deliver to, but the message belongs in the global log/dashboard so a
	// human can read it. This makes `agentbus reply` work for human messages
	// (for example an urgent interrupt sent from the CLI/dashboard).
	human := dest == "user" || dest == "user-web"
	m := Msg{ID: b.nextIDLocked(), ReplyTo: replyTo, From: from, To: dest, Kind: kind, Body: body, Ts: now}

	key := from + "\x00" + dest + "\x00" + body
	if d, ok := b.dup[key]; ok && now-d.ts < dupWindowS {
		return d.m, 0, true, nil
	}

	var targets []*Session
	if human {
		// no session inbox; the message is delivered to the log below
	} else if broadcast {
		for _, s := range b.sess {
			if s.Name != from {
				targets = append(targets, s)
			}
		}
	} else {
		t := b.byName[dest]
		if t == nil {
			return Msg{}, 0, false, fmt.Errorf("%w: no live session %q%s", ErrUnknownSession, dest, b.suggestLocked(dest))
		}
		targets = []*Session{t}
	}
	for _, t := range targets {
		q := append(b.inbox[t.Name], m)
		if len(q) > InboxCap {
			q = q[len(q)-InboxCap:]
		}
		b.inbox[t.Name] = q
	}
	b.recent = append(b.recent, m)
	if len(b.recent) > RecentCap {
		b.recent = b.recent[len(b.recent)-RecentCap:]
	}
	b.dup[key] = dupEntry{m, now}
	b.bumpRateLocked(from, now)
	if human {
		return m, 1, false, nil
	}
	return m, len(targets), false, nil
}

// Poll fetches pending messages for a session. With ack=true they are removed
// from the inbox (and remembered in "recently delivered"). wait > 0 long-polls.
func (b *Bus) Poll(id string, ack bool, wait time.Duration) ([]Msg, string, error) {
	deadline := b.opt.Now().Add(wait)
	for {
		b.mu.Lock()
		b.reapLocked()
		s := b.sess[id]
		if s == nil {
			b.mu.Unlock()
			return nil, "", fmt.Errorf("%w: %s", ErrUnknownSession, id)
		}
		name := s.Name
		s.LastSeen = b.now()
		q := b.inbox[name]
		if len(q) > 0 {
			msgs := append([]Msg(nil), q...)
			if ack {
				b.inbox[name] = nil
				b.mine[name] = append(b.mine[name], q...)
				if len(b.mine[name]) > MineCap {
					b.mine[name] = b.mine[name][len(b.mine[name])-MineCap:]
				}
			}
			b.mu.Unlock()
			return msgs, name, nil
		}
		b.mu.Unlock()
		rem := deadline.Sub(b.opt.Now())
		if rem <= 0 {
			return nil, name, nil
		}
		time.Sleep(min(pollTick, rem))
	}
}

// TakeFrom waits for and removes the first message sent by peer (direct or
// broadcast) from the calling session's inbox — the reply half of `ask`.
// Other messages stay queued for normal hook delivery.
func (b *Bus) TakeFrom(id, peer string, wait time.Duration) (Msg, bool, string) {
	peer = strings.ToLower(strings.TrimSpace(peer))
	deadline := b.opt.Now().Add(wait)
	for {
		b.mu.Lock()
		b.reapLocked()
		s := b.sess[id]
		if s == nil {
			b.mu.Unlock()
			return Msg{}, false, ""
		}
		name := s.Name
		s.LastSeen = b.now()
		q := b.inbox[name]
		for i, m := range q {
			if m.From == peer && (m.To == name || m.To == "all") {
				b.inbox[name] = append(q[:i:i], q[i+1:]...)
				b.mu.Unlock()
				return m, true, name
			}
		}
		b.mu.Unlock()
		rem := deadline.Sub(b.opt.Now())
		if rem <= 0 {
			return Msg{}, false, name
		}
		time.Sleep(min(pollTick, rem))
	}
}

// Who returns live sessions sorted by name.
func (b *Bus) Who() []Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reapLocked()
	out := make([]Session, 0, len(b.sess))
	for _, s := range b.sess {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Leave removes a session and reserves its name briefly against reuse.
func (b *Bus) Leave(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sess[id]
	if s == nil {
		return false
	}
	b.removeLocked(s)
	return true
}

// Mine returns recently-delivered messages (post-ack, for re-reading).
func (b *Bus) Mine(id string) ([]Msg, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sess[id]
	if s == nil {
		return nil, "", fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	return append([]Msg(nil), b.mine[s.Name]...), s.Name, nil
}

// LastPeer returns the most recently delivered message that came from a live
// peer session (optionally only from `peer`) — the message `agentbus reply`
// answers. Messages from the human ("user") and from sessions that have
// already left are skipped so a reply always has a real recipient.
func (b *Bus) LastPeer(id, peer string) (Msg, bool) {
	peer = strings.ToLower(strings.TrimSpace(peer))
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sess[id]
	if s == nil {
		return Msg{}, false
	}
	q := b.mine[s.Name]
	for i := len(q) - 1; i >= 0; i-- {
		m := q[i]
		if m.From == "" {
			continue
		}
		if peer != "" && strings.ToLower(m.From) != peer {
			continue
		}
		// "user" is the addressable human side; other senders must still be
		// live for a reply to reach them.
		if m.From != "user" && m.From != "user-web" && b.byName[m.From] == nil {
			continue
		}
		return m, true
	}
	return Msg{}, false
}

// Log returns the tail of the global message history (all deliveries).
func (b *Bus) Log(n int) []Msg {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.recent) {
		n = len(b.recent)
	}
	out := append([]Msg(nil), b.recent[len(b.recent)-n:]...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Reap forces a liveness pass (also run lazily inside most ops).
func (b *Bus) Reap() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reapLocked()
}

func (b *Bus) reapLocked() {
	now := b.now()
	for _, s := range b.sess {
		alive := false
		if s.Pid > 0 {
			alive = b.opt.PidAlive(s.Pid, s.PidStart)
		} else {
			alive = now-s.LastSeen < b.opt.TTLSeconds
		}
		if !alive {
			b.removeLocked(s)
		}
	}
	for n, exp := range b.tomb {
		if exp <= now {
			delete(b.tomb, n)
		}
	}
	for name, q := range b.inbox {
		kept := q[:0]
		for _, m := range q {
			if now-m.Ts < b.opt.MsgTTLSeconds {
				kept = append(kept, m)
			}
		}
		if len(kept) == 0 && len(q) > 0 {
			delete(b.inbox, name)
		} else {
			b.inbox[name] = kept
		}
	}
	if len(b.recent) > RecentCap {
		b.recent = b.recent[len(b.recent)-RecentCap:]
	}
	for from, ts := range b.rates {
		kept := ts[:0]
		for _, t := range ts {
			if now-t < 60 {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(b.rates, from)
		} else {
			b.rates[from] = kept
		}
	}
	for k, d := range b.dup {
		if now-d.ts >= dupWindowS {
			delete(b.dup, k)
		}
	}
}

func (b *Bus) removeLocked(s *Session) {
	delete(b.sess, s.ID)
	if cur := b.byName[s.Name]; cur == s {
		delete(b.byName, s.Name)
	}
	delete(b.inbox, s.Name)
	delete(b.mine, s.Name)
	delete(b.rates, s.Name)
	b.tomb[s.Name] = b.now() + b.opt.TombTTLSeconds
}

func (b *Bus) pickNameLocked(want string) (string, string, error) {
	now := b.now()
	if want == "" {
		for i := 0; i < 64; i++ {
			n := adjectives[rand.Intn(len(adjectives))] + "-" + nouns[rand.Intn(len(nouns))]
			if i >= 48 {
				n += "-" + fmt.Sprint(rand.Intn(90)+10)
			}
			if b.freeLocked(n, now) {
				return n, "", nil
			}
		}
		return "", "", fmt.Errorf("%w: name generator exhausted", ErrNameTaken)
	}
	want = NormName(want)
	if want == "" {
		return b.pickNameLocked("")
	}
	if b.freeLocked(want, now) {
		return want, "", nil
	}
	for i := 2; i <= 9; i++ {
		c := fmt.Sprintf("%s-%d", want, i)
		if b.freeLocked(c, now) {
			return c, want, nil
		}
	}
	return "", "", fmt.Errorf("%w: %q and variants 2..9 in use (agentbus who)", ErrNameTaken, want)
}

func (b *Bus) freeLocked(n string, now int64) bool {
	if b.byName[n] != nil {
		return false
	}
	if exp, ok := b.tomb[n]; ok && exp > now {
		return false
	}
	return true
}

func (b *Bus) suggestLocked(want string) string {
	var hits []string
	for name := range b.byName {
		if strings.HasPrefix(name, want) || strings.Contains(name, want) || strings.Contains(want, name) {
			hits = append(hits, name)
		}
	}
	sort.Strings(hits)
	if len(hits) > 3 {
		hits = hits[:3]
	}
	if len(hits) == 0 {
		return " (agentbus who)"
	}
	return " (did you mean: " + strings.Join(hits, ", ") + "?)"
}

func (b *Bus) allowRateLocked(from string, now int64) bool {
	ts := b.rates[from]
	kept := ts[:0]
	for _, t := range ts {
		if now-t < 60 {
			kept = append(kept, t)
		}
	}
	b.rates[from] = kept
	return len(kept) < b.opt.RatePerMinute
}

func (b *Bus) bumpRateLocked(from string, now int64) {
	b.rates[from] = append(b.rates[from], now)
}

func (b *Bus) nextIDLocked() string {
	b.seq++
	return fmt.Sprintf("m%x-%x", b.opt.Now().UnixNano(), b.seq)
}

// Snapshot returns a persistable copy of the bus state.
func (b *Bus) Snapshot() *State {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := &State{
		Version: 1, Seq: b.seq,
		Sessions: make([]*Session, 0, len(b.sess)),
		Inboxes:  map[string][]Msg{},
		Tomb:     map[string]int64{},
	}
	for _, s := range b.sess {
		cp := *s
		st.Sessions = append(st.Sessions, &cp)
	}
	for n, q := range b.inbox {
		st.Inboxes[n] = append([]Msg(nil), q...)
	}
	for n, e := range b.tomb {
		st.Tomb[n] = e
	}
	return st
}

// Restore resets the bus from a snapshot.
func (b *Bus) Restore(st *State) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sess = map[string]*Session{}
	b.byName = map[string]*Session{}
	b.inbox = map[string][]Msg{}
	b.mine = map[string][]Msg{}
	b.tomb = map[string]int64{}
	b.rates = map[string][]int64{}
	b.dup = map[string]dupEntry{}
	b.recent = nil
	b.seq = st.Seq
	for _, s := range st.Sessions {
		cp := *s
		b.sess[cp.ID] = &cp
		b.byName[cp.Name] = &cp
	}
	for n, q := range st.Inboxes {
		if len(q) > 0 {
			b.inbox[n] = append([]Msg(nil), q...)
		}
	}
	for n, e := range st.Tomb {
		b.tomb[n] = e
	}
}

// NormName normalizes a requested name: lowercase, [a-z0-9-], no leading/
// trailing dash, max NameMax bytes. Whitespace, underscores, path separators
// and dots act as word separators. Returns "" when nothing usable remains.
func NormName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, sep := range []string{" ", "\t", "_", "/", "\\", ".", ",", ":"} {
		s = strings.ReplaceAll(s, sep, "-")
	}
	var bd strings.Builder
	lastDash := true // suppress leading dash
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if r == '-' {
			if lastDash {
				continue
			}
			lastDash = true
			bd.WriteRune('-')
			continue
		}
		if !ok {
			continue
		}
		lastDash = false
		bd.WriteRune(r)
		if bd.Len() >= NameMax {
			break
		}
	}
	out := strings.TrimRight(bd.String(), "-")
	if len(out) > NameMax {
		out = out[:NameMax]
	}
	return out
}

// Trunc shortens s to at most n bytes (UTF-8 safe-ish: falls back to bytes).
func Trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

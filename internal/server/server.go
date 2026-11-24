// Package server is the agentbus daemon: a tiny unix-socket JSON-lines
// server wrapping the bus, with atomic state persistence to state.json.
package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/windviki/agentbus/internal/bus"
	"github.com/windviki/agentbus/internal/client"
	"github.com/windviki/agentbus/internal/delivery"
	"github.com/windviki/agentbus/internal/proto"
	"github.com/windviki/agentbus/internal/web"
)

var Version = "0.1.0"

type Server struct {
	home      string
	b         *bus.Bus
	started   time.Time
	statePath string
	saveMu    sync.Mutex
	saving    bool
	webAddr   string
	flushMu   sync.Mutex
	retryMu   sync.Mutex
	retryAt   map[string]time.Time
}

// Run serves in the foreground until SIGTERM/SIGINT or a shutdown op.
func Run(home string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	if client.Alive(home) {
		return fmt.Errorf("agentbus daemon already running (%s)", client.SocketPath(home))
	}
	s := &Server{home: home, started: time.Now(), statePath: filepath.Join(home, "state.json"), retryAt: map[string]time.Time{}}
	s.b = bus.New(bus.DefaultOptions())
	s.loadState()

	sock := client.SocketPath(home)
	_ = os.Remove(sock) // safe: Alive() above proved nothing is listening
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	_ = os.Chmod(sock, 0o600)
	pidFile := filepath.Join(home, "daemon.pid")
	_ = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)

	// Web dashboard + A2A boundary on loopback (disable: AGENTBUS_WEB=0).
	s.webAddr = web.Start(s.b, Version, s.started, func() {
		s.save()
		s.triggerFlush()
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		s.saveNow()
		_ = ln.Close()
		_ = os.Remove(sock)
		_ = os.Remove(pidFile)
		os.Exit(0)
	}()
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for range t.C {
			s.b.Reap()
			s.saveNow()
		}
	}()
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			s.flushNative()
		}
	}()

	defer func() {
		_ = ln.Close()
		_ = os.Remove(sock)
		_ = os.Remove(pidFile)
		s.saveNow()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for sc.Scan() {
		line := sc.Text()
		if len(line) == 0 {
			continue
		}
		var req proto.Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.write(conn, proto.Response{Error: "bad request: " + err.Error()})
			continue
		}
		resp, shutdown := s.dispatch(&req)
		s.write(conn, resp)
		if shutdown {
			go func() {
				time.Sleep(100 * time.Millisecond) // let the reply flush
				s.saveNow()
				p, _ := os.FindProcess(os.Getpid())
				_ = p.Signal(syscall.SIGTERM)
			}()
		}
	}
}

func (s *Server) write(conn net.Conn, resp proto.Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = conn.Write(append(data, '\n'))
}

func errResp(err error) proto.Response {
	return proto.Response{Error: err.Error()}
}

func (s *Server) dispatch(r *proto.Request) (proto.Response, bool) {
	resp := proto.Response{Ok: true}
	switch r.Op {
	case "ping":
		resp.Version, resp.Home, resp.Uptime = Version, s.home, int64(time.Since(s.started)/time.Second)
	case "register":
		sess, renamed, err := s.b.Register(r.ID, r.Want, r.Agent, r.Cwd, r.Branch, r.Pid, r.PidStart)
		if err != nil {
			return errResp(err), false
		}
		s.b.SetRepo(r.ID, r.Repo)
		if r.Delivery != nil {
			if err := s.b.SetDelivery(r.ID, r.Delivery.Bus()); err != nil {
				return errResp(err), false
			}
		}
		// A hook may report the turn state with the same round-trip; a
		// SessionStart registers the session as idle until its first prompt.
		if r.State != "" {
			if _, err := s.b.SetState(r.ID, r.State); err != nil {
				return errResp(err), false
			}
		}
		resp.Name, resp.RenamedFrom, resp.Agent = sess.Name, renamed, sess.Agent
		resp.Sessions = others(s.b.Who(), sess.Name)
		s.save()
		s.triggerFlush()
	case "announce":
		sess, err := s.b.Announce(r.ID, r.Status)
		if err != nil {
			return errResp(err), false
		}
		resp.Name = sess.Name
		s.save()
	case "send":
		from := r.From
		if from == "" {
			from = "user"
		}
		if r.ID != "" {
			if name, ok := s.b.NameOf(r.ID); ok {
				from = name
			}
		}
		m, n, dup, err := s.b.Send(from, r.To, r.Body, r.Kind, r.ReplyTo)
		if err != nil {
			return errResp(err), false
		}
		resp.MsgID, resp.Recipients, resp.Dup = m.ID, n, dup
		s.save()
		s.triggerFlush()
	case "ask":
		// request + wait-for-reply: send kind=request, then long-wait for the
		// first message the peer sends back to us (taken from the inbox so it
		// is not double-delivered by hooks).
		if r.ID == "" {
			return errResp(fmt.Errorf("ask requires a session identity")), false
		}
		name, ok := s.b.NameOf(r.ID)
		if !ok {
			return errResp(fmt.Errorf("unknown session %s", r.ID)), false
		}
		m, _, _, err := s.b.Send(name, r.To, r.Body, bus.KindRequest, "")
		if err != nil {
			return errResp(err), false
		}
		resp.MsgID = m.ID
		s.save()
		s.triggerFlush()
		if r.WaitMs > 0 {
			reply, got, _ := s.b.TakeReply(r.ID, m.To, m.ID, time.Duration(r.WaitMs)*time.Millisecond)
			if got {
				resp.Messages = []bus.Msg{reply}
			}
			s.save()
		}
	case "reply":
		// Answer the most recent delivered peer message (kind=reply with
		// replyTo pointing at it), so agents can reply without bookkeeping
		// message ids themselves.
		if r.ID == "" {
			return errResp(fmt.Errorf("reply requires a session identity")), false
		}
		name, ok := s.b.NameOf(r.ID)
		if !ok {
			return errResp(fmt.Errorf("unknown session %s", r.ID)), false
		}
		last, ok := s.b.LastPeer(r.ID, r.To)
		if !ok {
			return errResp(fmt.Errorf("no recent peer message to reply to (use: agentbus send <name> \"...\")")), false
		}
		m, _, _, err := s.b.Send(name, last.From, r.Body, bus.KindReply, last.ID)
		if err != nil {
			return errResp(err), false
		}
		resp.MsgID, resp.Peer = m.ID, last.From
		s.save()
		s.triggerFlush()
	case "poll":
		ack := true
		if r.Ack != nil {
			ack = *r.Ack
		}
		if r.Delivery != nil {
			if err := s.b.SetDelivery(r.ID, r.Delivery.Bus()); err != nil {
				return errResp(err), false
			}
			s.triggerFlush()
		}
		// Hooks piggyback the turn state on every poll (the Stop hook says
		// "idle", the prompt/tool hooks say "working"), so presence and
		// activity stay one round-trip.
		if r.State != "" {
			if _, err := s.b.SetState(r.ID, r.State); err != nil {
				return errResp(err), false
			}
		}
		msgs, name, err := s.b.PollWithOptions(r.ID, bus.PollOptions{
			Ack: ack, Wait: time.Duration(r.WaitMs) * time.Millisecond,
			WakeOnly: r.WakeOnly, WakePolicy: r.WakePolicy, ActiveOnly: r.ActiveOnly, Claim: r.Claim, Driver: r.Driver,
		})
		if err != nil {
			return errResp(err), false
		}
		resp.Messages, resp.Name = msgs, name
		if (ack || r.Claim != "") && len(msgs) > 0 {
			s.save()
		}
	case "complete":
		var deliveryErr error
		if r.Error != "" {
			deliveryErr = fmt.Errorf("%s", r.Error)
		}
		if err := s.b.CompleteClaim(r.ID, r.Claim, r.Driver, deliveryErr); err != nil {
			return errResp(err), false
		}
		s.save()
	case "receipt":
		resp.Receipts = s.b.Receipts(r.MsgID)
	case "state":
		// Explicit state report for harnesses that do not run `agentbus hook`
		// (MCP-only clients, integration scripts).
		sess, err := s.b.SetState(r.ID, r.State)
		if err != nil {
			return errResp(err), false
		}
		resp.Name = sess.Name
		s.save()
	case "mine":
		mine, name, err := s.b.Mine(r.ID)
		if err != nil {
			return errResp(err), false
		}
		resp.Mine, resp.Name = mine, name
	case "who":
		resp.Sessions = s.b.Who()
	case "leave":
		if !s.b.Leave(r.ID) {
			return errResp(fmt.Errorf("unknown session %s", r.ID)), false
		}
		s.save()
	case "log":
		resp.Recent = s.b.Log(r.N)
	case "status":
		resp.Version, resp.Home, resp.WebAddr = Version, s.home, s.webAddr
		resp.Uptime = int64(time.Since(s.started) / time.Second)
		resp.Sessions = s.b.Who()
	case "shutdown":
		return proto.Response{Ok: true, Version: Version}, true
	default:
		return errResp(fmt.Errorf("unknown op %q", r.Op)), false
	}
	return resp, false
}

func (s *Server) triggerFlush() {
	go s.flushNative()
}

func (s *Server) flushNative() {
	if !s.flushMu.TryLock() {
		return
	}
	defer s.flushMu.Unlock()
	for _, sess := range s.b.Who() {
		if !delivery.CanPush(sess) || s.nativeRetryBlocked(sess.ID) {
			continue
		}
		owner := fmt.Sprintf("native-%d", time.Now().UnixNano())
		msgs, _, err := s.b.PollWithOptions(sess.ID, bus.PollOptions{
			Claim: owner, Driver: sess.Delivery.Driver, WakeOnly: !sess.Working, WakePolicy: sess.Delivery.Wake,
		})
		if err != nil || len(msgs) == 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		pushErr := delivery.Push(ctx, sess, msgs)
		cancel()
		if completeErr := s.b.CompleteClaim(sess.ID, owner, sess.Delivery.Driver, pushErr); completeErr != nil {
			continue
		}
		s.setNativeRetry(sess.ID, pushErr)
		s.save()
	}
}

func (s *Server) nativeRetryBlocked(id string) bool {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	return s.retryAt[id].After(time.Now())
}

func (s *Server) setNativeRetry(id string, err error) {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	if s.retryAt == nil {
		s.retryAt = map[string]time.Time{}
	}
	if err == nil {
		delete(s.retryAt, id)
		return
	}
	s.retryAt[id] = time.Now().Add(2 * time.Second)
}

func others(all []bus.Session, exclude string) []bus.Session {
	out := make([]bus.Session, 0, len(all))
	for _, s := range all {
		if s.Name != exclude {
			out = append(out, s)
		}
	}
	return out
}

// save schedules a debounced snapshot (coalesces bursts of messages).
func (s *Server) save() {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if s.saving {
		return
	}
	s.saving = true
	time.AfterFunc(300*time.Millisecond, func() {
		s.saveNow()
		s.saveMu.Lock()
		s.saving = false
		s.saveMu.Unlock()
	})
}

// saveNow writes state.json atomically (tmp + rename).
func (s *Server) saveNow() {
	st := s.b.Snapshot()
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, s.statePath)
}

func (s *Server) loadState() {
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return
	}
	var st bus.State
	if json.Unmarshal(data, &st) == nil {
		s.b.Restore(&st)
	}
}

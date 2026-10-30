// Package server is the agentbus daemon: a tiny unix-socket JSON-lines
// server wrapping the bus, with atomic state persistence to state.json.
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"agentbus/internal/bus"
	"agentbus/internal/client"
	"agentbus/internal/proto"
	"agentbus/internal/web"
)

const Version = "0.1.0"

type Server struct {
	home      string
	b         *bus.Bus
	started   time.Time
	statePath string
	saveMu    sync.Mutex
	saving    bool
	webAddr   string
}

// Run serves in the foreground until SIGTERM/SIGINT or a shutdown op.
func Run(home string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	if client.Alive(home) {
		return fmt.Errorf("agentbus daemon already running (%s)", client.SocketPath(home))
	}
	s := &Server{home: home, started: time.Now(), statePath: filepath.Join(home, "state.json")}
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
	s.webAddr = web.Start(s.b, Version, s.started, s.save)

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
		resp.Name, resp.RenamedFrom, resp.Agent = sess.Name, renamed, sess.Agent
		resp.Sessions = others(s.b.Who(), sess.Name)
		s.save()
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
		reply, got, _ := s.b.TakeFrom(r.ID, m.To, time.Duration(r.WaitMs)*time.Millisecond)
		if got {
			resp.Messages = []bus.Msg{reply}
		}
		s.save()
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
	case "poll":
		ack := true
		if r.Ack != nil {
			ack = *r.Ack
		}
		msgs, name, err := s.b.Poll(r.ID, ack, time.Duration(r.WaitMs)*time.Millisecond)
		if err != nil {
			return errResp(err), false
		}
		resp.Messages, resp.Name = msgs, name
		if ack && len(msgs) > 0 {
			s.save()
		}
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

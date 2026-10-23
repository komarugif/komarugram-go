// SPDX-License-Identifier: Unlicense OR MIT

package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/mt"
	"github.com/gotd/td/proto"
)

// fakeSOCKS is a SOCKS5 server that connects nowhere: whatever is asked to be
// connected to is answered as Telegram's server would, to the first request of
// the key exchange, in the intermediate transport.
type fakeSOCKS struct {
	ln       net.Listener
	user     string
	password string

	mu      sync.Mutex
	targets []string
	// silent makes the "Telegram" at the other end say nothing.
	silent bool
	// connectDelay is how long the proxy takes to connect, as one with a
	// handshake of its own would, and replyDelay how long Telegram takes to
	// answer a request.
	connectDelay, replyDelay time.Duration
	requests                 int
}

func newFakeSOCKS(t *testing.T, user, password string) *fakeSOCKS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSOCKS{ln: ln, user: user, password: password}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *fakeSOCKS) proxy() Proxy {
	host, port, _ := net.SplitHostPort(s.ln.Addr().String())
	n, _ := strconv.Atoi(port)
	return Proxy{Kind: SOCKS5, Host: host, Port: n}
}

func (s *fakeSOCKS) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.targets...)
}

func (s *fakeSOCKS) serve(c net.Conn) {
	defer c.Close()
	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	if s.user == "" {
		c.Write([]byte{5, 0})
	} else {
		c.Write([]byte{5, 2})
		var v [2]byte
		if _, err := io.ReadFull(c, v[:]); err != nil {
			return
		}
		user := make([]byte, v[1])
		io.ReadFull(c, user)
		var n [1]byte
		io.ReadFull(c, n[:])
		pass := make([]byte, n[0])
		io.ReadFull(c, pass)
		if string(user) != s.user || string(pass) != s.password {
			c.Write([]byte{1, 1})
			return
		}
		c.Write([]byte{1, 0})
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		io.ReadFull(c, ip)
		host = net.IP(ip).String()
	case 3:
		var n [1]byte
		io.ReadFull(c, n[:])
		name := make([]byte, n[0])
		io.ReadFull(c, name)
		host = string(name)
	default:
		return
	}
	var port [2]byte
	io.ReadFull(c, port[:])
	s.mu.Lock()
	s.targets = append(s.targets, net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:])))))
	s.mu.Unlock()
	time.Sleep(s.connectDelay)
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	s.telegram(c)
}

func (s *fakeSOCKS) telegram(c net.Conn) {
	var tag [4]byte
	if _, err := io.ReadFull(c, tag[:]); err != nil {
		return
	}
	for s.answer(c) {
	}
}

// answer reads one packet and answers it, false at the end of the connection.
func (s *fakeSOCKS) answer(c net.Conn) bool {
	var size [4]byte
	if _, err := io.ReadFull(c, size[:]); err != nil {
		return false
	}
	packet := make([]byte, binary.LittleEndian.Uint32(size[:]))
	if _, err := io.ReadFull(c, packet); err != nil {
		return false
	}
	if s.silent {
		io.Copy(io.Discard, c)
		return false
	}
	var msg proto.UnencryptedMessage
	if err := msg.Decode(&bin.Buffer{Buf: packet}); err != nil {
		return false
	}
	var req mt.ReqPqMultiRequest
	if err := req.Decode(&bin.Buffer{Buf: msg.MessageData}); err != nil {
		return false
	}
	var data bin.Buffer
	res := mt.ResPQ{Nonce: req.Nonce, Pq: []byte{1, 2, 3}, ServerPublicKeyFingerprints: []int64{1}}
	if err := res.Encode(&data); err != nil {
		return false
	}
	var out bin.Buffer
	if err := (proto.UnencryptedMessage{MessageID: msg.MessageID + 1, MessageData: data.Buf}).Encode(&out); err != nil {
		return false
	}
	var frame [4]byte
	binary.LittleEndian.PutUint32(frame[:], uint32(out.Len()))
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()
	time.Sleep(s.replyDelay)
	_, err := c.Write(append(frame[:], out.Buf...))
	return err == nil
}

// TestPingThroughSOCKS5 checks the whole path of a SOCKS5 proxy: the client
// connects to Telegram's address through it, by IP address and not by name,
// and its first request is answered.
func TestPingThroughSOCKS5(t *testing.T) {
	s := newFakeSOCKS(t, "", "")
	p := s.proxy()
	took, err := Ping(context.Background(), &p)
	if err != nil {
		t.Fatal(err)
	}
	if took < 0 || took > PingTimeout {
		t.Errorf("took %v", took)
	}
	if got := s.asked(); len(got) != 1 || got[0] != "149.154.167.51:443" {
		t.Errorf("the proxy was asked for %v, want Telegram's second server by its address", got)
	}
}

func TestPingWithAPassword(t *testing.T) {
	s := newFakeSOCKS(t, "bob", "hunter2")
	p := s.proxy()
	p.User, p.Password = "bob", "hunter2"
	if _, err := Ping(context.Background(), &p); err != nil {
		t.Errorf("with the password: %v", err)
	}
	p.Password = "wrong"
	_, err := Ping(context.Background(), &p)
	if err == nil || Why(err) != FailAuth {
		t.Errorf("with a wrong password: %v (%v), want a refusal of it", err, Why(err))
	}
}

func TestPingSaysWhyNot(t *testing.T) {
	// Nothing listens where a listener was.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	host, port, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	if _, err := Ping(context.Background(), &Proxy{Kind: SOCKS5, Host: host, Port: n}); Why(err) != FailRefused {
		t.Errorf("a proxy that is not there: %v (%v)", err, Why(err))
	}

	// A proxy that is there and a Telegram that is silent.
	s := newFakeSOCKS(t, "", "")
	s.silent = true
	p := s.proxy()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Ping(ctx, &p)
	if Why(err) != FailTimeout {
		t.Errorf("a silent server: %v (%v)", err, Why(err))
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("the ping waited %v for a context of 200ms", time.Since(start))
	}
}

func TestResolverRefusesWhatIsNotAProxy(t *testing.T) {
	for _, p := range []Proxy{
		{},
		{Kind: SOCKS5, Host: "127.0.0.1"},
		{Kind: MTProto, Host: "127.0.0.1", Port: 443},
		{Kind: MTProto, Host: "127.0.0.1", Port: 443, Secret: "zz!!"},
		{Kind: "http", Host: "127.0.0.1", Port: 443},
	} {
		if _, err := p.Resolver(); err == nil {
			t.Errorf("%+v made a resolver", p)
		}
	}
	if _, err := Ping(context.Background(), &Proxy{}); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("an empty proxy was pinged: %v", err)
	}
}

// TestPingIsTheRoundTripAndNotTheConnecting checks that a proxy that takes
// long to connect, as one with handshakes of its own does, is not made out
// slow for it: what is timed is a request on a connection that is made.
func TestPingIsTheRoundTripAndNotTheConnecting(t *testing.T) {
	s := newFakeSOCKS(t, "", "")
	s.connectDelay, s.replyDelay = 400*time.Millisecond, 60*time.Millisecond
	p := s.proxy()
	took, err := Ping(context.Background(), &p)
	if err != nil {
		t.Fatal(err)
	}
	if took < 60*time.Millisecond || took >= 300*time.Millisecond {
		t.Errorf("took %v: the reply takes 60ms and the connecting 400ms", took)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requests != 2 {
		t.Errorf("%d requests, want one to prove the connection and one timed", s.requests)
	}
}

// SPDX-License-Identifier: Unlicense OR MIT

package proxy

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/mt"
	"github.com/gotd/td/proto"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/transport"
)

// PingTimeout is how long Ping waits for Telegram's answer.
const PingTimeout = 8 * time.Second

// pingDC is the server asked: the second data center, which every account can
// reach. Its address is Telegram's well-known one.
var pingDC = tg.DCOption{ID: 2, IPAddress: "149.154.167.51", Port: 443}

// Ping measures how long Telegram takes to answer through p: the round trip
// of a request on a connection that is made, as the time of a proxy is meant,
// and not the making of it, with the handshakes of the proxy, of TLS and of
// the WebSocket, which is several round trips more for some of them. The
// request asks for nothing and is Telegram's own, answered by it alone; the
// first one only proves the connection, and the second is timed. A nil p is
// the direct connection, to compare the proxies with.
func Ping(ctx context.Context, p *Proxy) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, PingTimeout)
	defer cancel()
	var r dcs.Resolver
	if p == nil {
		r = dcs.Plain(dcs.PlainOptions{})
	} else {
		var err error
		if r, err = p.Resolver(); err != nil {
			return 0, err
		}
	}
	conn, err := r.Primary(ctx, pingDC.ID, dcs.List{Options: []tg.DCOption{pingDC}})
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	// A handshake or a read that the context does not reach is ended by closing.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	ids := proto.NewMessageIDGen(time.Now)
	if err := requestPQ(ctx, conn, ids); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return 0, err
	}
	start := time.Now()
	if err := requestPQ(ctx, conn, ids); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return 0, err
	}
	return time.Since(start), nil
}

// requestPQ sends the first request of Telegram's key exchange, which
// takes no key, and reads the answer to it.
func requestPQ(ctx context.Context, conn transport.Conn, ids *proto.MessageIDGen) error {
	var nonce bin.Int128
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	var data bin.Buffer
	if err := (&mt.ReqPqMultiRequest{Nonce: nonce}).Encode(&data); err != nil {
		return err
	}
	var out bin.Buffer
	msg := proto.UnencryptedMessage{
		MessageID:   ids.New(proto.MessageFromClient),
		MessageData: data.Buf,
	}
	if err := msg.Encode(&out); err != nil {
		return err
	}
	if err := conn.Send(ctx, &out); err != nil {
		return err
	}
	var in bin.Buffer
	if err := conn.Recv(ctx, &in); err != nil {
		return err
	}
	var reply proto.UnencryptedMessage
	if err := reply.Decode(&in); err != nil {
		return err
	}
	var res mt.ResPQ
	body := bin.Buffer{Buf: reply.MessageData}
	if err := res.Decode(&body); err != nil {
		return err
	}
	if res.Nonce != nonce {
		return errors.New("proxy: the reply is not to the request")
	}
	return nil
}

// Failure is why a proxy could not be reached, as far as is told.
type Failure int

const (
	// FailOther is any failure that is not one of the others.
	FailOther Failure = iota
	// FailTimeout is no answer in time.
	FailTimeout
	// FailAuth is a user and a password the proxy does not accept.
	FailAuth
	// FailRefused is a proxy that is not there: nothing listens at its
	// address, or it cannot be found.
	FailRefused
)

// Why says what is wrong with a failed proxy.
func Why(err error) Failure {
	var ne net.Error
	switch {
	case err == nil:
		return FailOther
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return FailTimeout
	case strings.Contains(err.Error(), "authentication failed"):
		return FailAuth
	case strings.Contains(err.Error(), "refused"), strings.Contains(err.Error(), "no such host"),
		strings.Contains(err.Error(), "unreachable"):
		return FailRefused
	}
	return FailOther
}

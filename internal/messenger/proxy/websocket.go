// SPDX-License-Identifier: Unlicense OR MIT

package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/coder/websocket"
	"github.com/go-faster/errors"
	"github.com/gotd/td/crypto"
	"github.com/gotd/td/mtproxy"
	"github.com/gotd/td/mtproxy/obfuscator"
	"github.com/gotd/td/proto/codec"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/transport"
	"github.com/gotd/td/wsutil"
)

const (
	websocketPort = 443
	// telegramDomain is the domain of Telegram's web clients, whose servers
	// take the WebSocket.
	telegramDomain = "web.telegram.org"
)

// Telegram is the WebSocket of Telegram's own web clients, which needs no
// server of anyone's: where Telegram's addresses are slowed down or cut and
// its web client is not, this gets through as the web client does.
func Telegram() Proxy {
	return Proxy{Kind: WebSocket, Host: telegramDomain, Port: websocketPort}
}

// parseWebSocket reads what follows "wss://": the domain whose kws{n}
// subdomains reach Telegram's servers, and a port, 443 where there is none.
func parseWebSocket(rest string) (Proxy, error) {
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	p := Proxy{Kind: WebSocket, Host: rest, Port: websocketPort}
	if strings.Contains(rest, "@") {
		return Proxy{}, ErrNoAddress
	}
	if host, port, err := net.SplitHostPort(rest); err == nil {
		n, err := strconv.Atoi(port)
		if err != nil {
			return Proxy{}, ErrBadPort
		}
		p.Host, p.Port = host, n
	} else {
		p.Host = strings.Trim(rest, "[]")
	}
	return p, p.Validate()
}

// hostLiteral writes host as a URL does: an IPv6 address in brackets.
func hostLiteral(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

// websocketResolver connects to Telegram's servers over WebSocket, to the domains
// kws{n}.<domain> (and kws{n}-1.<domain> for media), as Telegram's web
// clients do and gotd's own resolver does, which has no media servers.
type websocketResolver struct {
	domain string
	port   int
	tag    [4]byte
	cdc    codec.Intermediate
	rand   io.Reader
	// client dials; nil for the default one, which tests replace.
	client *http.Client
	// premium, if set, tells whether the files are to be asked for at the
	// address of Premium accounts, as it is each time a connection is made.
	premium func() bool
}

var _ dcs.Resolver = websocketResolver{}

func newWebSocketResolver(p Proxy, o Options) websocketResolver {
	cdc := codec.Intermediate{}
	return websocketResolver{domain: p.Host, port: p.Port, tag: cdc.ObfuscatedTag(), cdc: cdc, rand: crypto.DefaultRand(), premium: o.PremiumMedia}
}

// urls are the addresses to try for a server, the first that answers is used:
// the media servers of Telegram's own domain have a name of their own.
func (r websocketResolver) urls(dc int, media bool) []string {
	// The special server of the test of the web clients is the second one.
	if dc == 203 {
		dc = 2
	}
	host := r.domain
	if r.port != websocketPort {
		host = net.JoinHostPort(hostOrBare(r.domain), strconv.Itoa(r.port))
	} else {
		host = hostLiteral(host)
	}
	var urls []string
	if media && strings.EqualFold(r.domain, telegramDomain) {
		// The web clients take the files of a Premium account to a path of its
		// own; where that is not taken, the usual one is.
		if r.premium != nil && r.premium() {
			urls = append(urls, fmt.Sprintf("wss://kws%d-1.%s/apiws_premium", dc, host))
		}
		urls = append(urls, fmt.Sprintf("wss://kws%d-1.%s/apiws", dc, host))
	}
	return append(urls, fmt.Sprintf("wss://kws%d.%s/apiws", dc, host))
}

func hostOrBare(h string) string { return strings.Trim(h, "[]") }

func (r websocketResolver) connect(ctx context.Context, dc int, media bool) (transport.Conn, error) {
	if dc < 1 {
		dc = -dc
	}
	var failure error
	for _, url := range r.urls(dc, media) {
		conn, err := r.dial(ctx, url, dc, media)
		if err == nil {
			return conn, nil
		}
		failure = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, failure
}

func (r websocketResolver) dial(ctx context.Context, url string, dc int, media bool) (transport.Conn, error) {
	ws, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"binary"}, HTTPClient: r.client})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return nil, errors.Wrap(err, "dial websocket")
	}
	// The media servers are told of by the sign of the number, as an MTProxy is.
	id := dc
	if media {
		id = -dc
	}
	conn := obfuscator.Obfuscated2(r.rand, wsutil.NetConn(ws))
	if err := conn.Handshake(r.tag, id, mtproxy.Secret{Type: mtproxy.Simple}); err != nil {
		ws.CloseNow()
		return nil, errors.Wrap(err, "handshake")
	}
	proto := transport.NewProtocol(func() transport.Codec { return codec.NoHeader{Codec: r.cdc} })
	t, err := proto.Handshake(conn)
	if err != nil {
		ws.CloseNow()
		return nil, errors.Wrap(err, "transport handshake")
	}
	return t, nil
}

func (r websocketResolver) Primary(ctx context.Context, dc int, _ dcs.List) (transport.Conn, error) {
	return r.connect(ctx, dc, false)
}

func (r websocketResolver) MediaOnly(ctx context.Context, dc int, _ dcs.List) (transport.Conn, error) {
	return r.connect(ctx, dc, true)
}

func (r websocketResolver) CDN(context.Context, int, dcs.List) (transport.Conn, error) {
	return nil, errors.New("CDN servers are not reached over WebSocket")
}

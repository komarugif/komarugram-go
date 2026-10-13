// SPDX-License-Identifier: Unlicense OR MIT

// Package proxy is how the client reaches Telegram when it cannot do it
// directly: the proxies a user keeps, the links they are written as, the
// connections made through them and the time those take.
package proxy

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Kind is the protocol of a proxy.
type Kind string

const (
	// MTProto is a proxy for Telegram alone, as Telegram's own links name it
	// (tg://proxy); its secret is what the connection is encrypted with.
	MTProto Kind = "mtproto"
	// SOCKS5 is a general proxy, as sing-box, Xray or an SSH tunnel offers on
	// the local machine, with or without a user and a password.
	SOCKS5 Kind = "socks5"
	// WebSocket carries Telegram's own protocol in the WebSocket of its web
	// clients (https://core.telegram.org/mtproto/transports#websocket), to
	// kws{n}.<Host>: web.telegram.org, which looks like any visit to the web
	// client, or a domain of the user's behind Cloudflare. It has no
	// credentials, as there is no proxy of the user's to give them to.
	WebSocket Kind = "websocket"
)

// Proxy is one proxy of the list.
type Proxy struct {
	Kind Kind   `json:"kind"`
	Host string `json:"host"`
	Port int    `json:"port"`
	// User and Password are those of a SOCKS5 proxy that asks for them.
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	// Secret is that of an MTProto proxy, as it was written: in hex, or in
	// base64 as the links of some lists have it.
	Secret string `json:"secret,omitempty"`
}

// The errors of Parse and Validate, which are what a user is told.
var (
	ErrEmpty      = errors.New("proxy: no link")
	ErrUnknown    = errors.New("proxy: not a proxy link")
	ErrNoAddress  = errors.New("proxy: no address")
	ErrBadHost    = errors.New("proxy: not an address")
	ErrBadPort    = errors.New("proxy: the port is not between 1 and 65535")
	ErrNoSecret   = errors.New("proxy: an MTProto proxy needs a secret")
	ErrBadSecret  = errors.New("proxy: the secret is neither hex nor base64")
	ErrNoPassword = errors.New("proxy: a user needs a password")
)

// ID tells a proxy among the others in the list: two links to the same
// server, with the same user, are one proxy, whose secret or password the
// later link changes.
func (p Proxy) ID() string {
	return string(p.Kind) + "|" + strings.ToLower(p.Host) + "|" + strconv.Itoa(p.Port) + "|" + p.User
}

// Address is the host and the port, as net.Dial takes them.
func (p Proxy) Address() string {
	return net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
}

// String is what the list calls the proxy: its address, with the user where
// there is one, as there may be several of a server.
func (p Proxy) String() string {
	if p.Kind == WebSocket && p.Port == websocketPort {
		return p.Host
	}
	if p.User != "" {
		return p.User + "@" + p.Address()
	}
	return p.Address()
}

// Link writes the proxy as a link that Parse reads back: Telegram's own for
// an MTProto proxy, and the usual URL for a SOCKS5 one.
func (p Proxy) Link() string {
	switch p.Kind {
	case MTProto:
		q := url.Values{}
		q.Set("server", p.Host)
		q.Set("port", strconv.Itoa(p.Port))
		q.Set("secret", p.Secret)
		return "tg://proxy?" + q.Encode()
	case WebSocket:
		if p.Port == websocketPort {
			return "wss://" + hostLiteral(p.Host)
		}
		return "wss://" + p.Address()
	case SOCKS5:
		u := url.URL{Scheme: "socks5", Host: p.Address()}
		if p.User != "" || p.Password != "" {
			u.User = url.UserPassword(p.User, p.Password)
		}
		return u.String()
	}
	return ""
}

// Validate reports why the proxy cannot be used, nil where it can.
func (p Proxy) Validate() error {
	if p.Host == "" {
		return ErrNoAddress
	}
	if !validHost(p.Host) {
		return ErrBadHost
	}
	if p.Port < 1 || p.Port > 65535 {
		return ErrBadPort
	}
	switch p.Kind {
	case MTProto:
		if p.Secret == "" {
			return ErrNoSecret
		}
		if _, err := DecodeSecret(p.Secret); err != nil {
			return err
		}
	case SOCKS5:
		if p.User != "" && p.Password == "" {
			return ErrNoPassword
		}
	case WebSocket:
	default:
		return fmt.Errorf("proxy: unknown kind %q", p.Kind)
	}
	return nil
}

// DecodeSecret reads a proxy secret written in hex or in base64.
func DecodeSecret(s string) ([]byte, error) {
	if key, err := hex.DecodeString(s); err == nil {
		return key, nil
	}
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if key, err := enc.DecodeString(s); err == nil {
			return key, nil
		}
	}
	return nil, ErrBadSecret
}

// Lookup tells whether host is a name that exists, for ParseLookup to tell
// which side of an "@" is the address where their look does not.
type Lookup func(host string) bool

// Parse reads the proxy a link names:
//
//   - tg://proxy?server=…&port=…&secret=…, also on t.me, for MTProto;
//   - tg://socks?server=…&port=…&user=…&pass=…, also on t.me;
//   - socks5://user:pass@host:port, as everywhere, and socks5://host:port@user:pass,
//     as it is often written; a link without credentials is SOCKS5 as well,
//     and so is a bare host:port.
//
// Which side of the "@" is the address is told by their look: an IP address
// or localhost is one, a name with a dot is more of one than a name without,
// and a port is a number up to 65535. Where both sides could be, the first is
// the user, as in a URL.
func Parse(link string) (Proxy, error) {
	return ParseLookup(link, nil)
}

// ParseLookup is Parse with lookup to settle the order where the look of the
// two sides cannot: of two names that look alike, the one that exists is the
// address. It is asked of nothing else.
func ParseLookup(link string, lookup Lookup) (Proxy, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return Proxy{}, ErrEmpty
	}
	if p, ok, err := parseTelegram(link); ok {
		return p, err
	}
	rest := link
	if i := strings.Index(link, "://"); i >= 0 {
		switch strings.ToLower(link[:i]) {
		case "wss":
			return parseWebSocket(link[i+3:])
		case "socks5", "socks5h", "socks":
			rest = link[i+3:]
		default:
			return Proxy{}, ErrUnknown
		}
	} else if strings.ContainsAny(link, "?=") {
		return Proxy{}, ErrUnknown
	}
	// What follows the authority is not a part of the proxy.
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	p, err := parseAuthority(rest, lookup)
	if err != nil {
		return Proxy{}, err
	}
	p.Kind = SOCKS5
	return p, p.Validate()
}

// parseTelegram reads Telegram's links of proxies; ok is false for any other.
func parseTelegram(link string) (p Proxy, ok bool, err error) {
	u, perr := url.Parse(link)
	if perr != nil {
		return Proxy{}, false, nil
	}
	var kind string
	switch {
	case u.Scheme == "tg" && (u.Host == "proxy" || u.Host == "socks"):
		kind = u.Host
	case (u.Scheme == "https" || u.Scheme == "http") && (u.Host == "t.me" || u.Host == "telegram.me") &&
		(u.Path == "/proxy" || u.Path == "/socks"):
		kind = strings.TrimPrefix(u.Path, "/")
	default:
		return Proxy{}, false, nil
	}
	q := u.Query()
	port, perr := strconv.Atoi(strings.TrimSpace(q.Get("port")))
	if perr != nil {
		port = 0
	}
	p = Proxy{Host: strings.TrimSpace(q.Get("server")), Port: port}
	if kind == "proxy" {
		p.Kind, p.Secret = MTProto, strings.TrimSpace(q.Get("secret"))
	} else {
		p.Kind, p.User, p.Password = SOCKS5, q.Get("user"), q.Get("pass")
	}
	return p, true, p.Validate()
}

// reading is one way of reading the authority of a link.
type reading struct {
	p Proxy
	// score is how much the host looks like an address, and the first order
	// of the two is worth a little more, to win where all else is the same.
	score int
}

// parseAuthority reads what follows the scheme of a SOCKS5 link: host:port,
// user:pass@host:port or host:port@user:pass. The "@" may be in the
// password, and either side may be the address, so each place of it and each
// order is tried, and what looks most like an address is taken.
func parseAuthority(s string, lookup Lookup) (Proxy, error) {
	if !strings.Contains(s, "@") {
		return addressOf(s)
	}
	var readings []reading
	noPassword := false
	for i := range len(s) {
		if s[i] != '@' {
			continue
		}
		for _, swapped := range []bool{false, true} {
			addr, cred := s[i+1:], s[:i]
			if swapped {
				addr, cred = cred, addr
			}
			p, err := addressOf(addr)
			if err != nil {
				continue
			}
			user, pass, ok := strings.Cut(cred, ":")
			if !ok || user == "" {
				noPassword = noPassword || cred != ""
				continue
			}
			p.User, p.Password = unescape(user), unescape(pass)
			score := 2 * lookScore(p.Host)
			if !swapped {
				score++
			}
			readings = append(readings, reading{p, score})
		}
	}
	if len(readings) == 0 {
		if noPassword {
			return Proxy{}, ErrNoPassword
		}
		return Proxy{}, ErrNoAddress
	}
	best := readings[0]
	for _, r := range readings[1:] {
		if r.score > best.score {
			best = r
		}
	}
	if lookup != nil {
		// The orders that look alike: the one whose host exists is the address.
		var alike []reading
		for _, r := range readings {
			if lookScore(r.p.Host) == lookScore(best.p.Host) && lookup(r.p.Host) {
				alike = append(alike, r)
			}
		}
		if len(alike) == 1 {
			best = alike[0]
		}
	}
	return best.p, nil
}

// addressOf reads host:port, with [] around an IPv6 address.
func addressOf(s string) (Proxy, error) {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return Proxy{}, ErrNoAddress
	}
	if !validHost(host) {
		return Proxy{}, ErrBadHost
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return Proxy{}, ErrBadPort
	}
	return Proxy{Host: host, Port: n}, nil
}

// unescape reads the %XX escapes of a user or a password, as a URL has them;
// text that has none, or has them wrong, stays as it is.
func unescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// lookScore says how much host looks like an address, as against a user's
// name: 3 for an IP address or localhost, 2 for a name with a dot, 1 for any
// other.
func lookScore(host string) int {
	switch {
	case net.ParseIP(host) != nil, strings.EqualFold(host, "localhost"):
		return 3
	case strings.Contains(host, "."):
		return 2
	}
	return 1
}

// validHost reports whether host is an IP address or a name of labels made of
// letters, digits, hyphens and underscores.
func validHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127) {
				return false
			}
		}
	}
	return true
}

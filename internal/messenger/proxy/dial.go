// SPDX-License-Identifier: Unlicense OR MIT

package proxy

import (
	"errors"
	"net"

	"github.com/gotd/td/telegram/dcs"
	xproxy "golang.org/x/net/proxy"
)

// Options are what a connection is made by besides the proxy.
type Options struct {
	// PremiumMedia, if set, tells at each connection whether the files over
	// WebSocket are asked for at the address of Premium accounts, as Telegram's
	// web clients ask for them.
	PremiumMedia func() bool
}

// Resolver makes the resolver that connects to Telegram through the proxy.
// The proxy is not asked for anything until a connection is made.
func (p Proxy) Resolver() (dcs.Resolver, error) { return p.ResolverWith(Options{}) }

// ResolverWith is Resolver with o.
func (p Proxy) ResolverWith(o Options) (dcs.Resolver, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	switch p.Kind {
	case MTProto:
		key, err := DecodeSecret(p.Secret)
		if err != nil {
			return nil, err
		}
		return dcs.MTProxy(p.Address(), key, dcs.MTProxyOptions{})
	case WebSocket:
		return newWebSocketResolver(p, o), nil
	case SOCKS5:
		dial, err := p.socksDialer()
		if err != nil {
			return nil, err
		}
		return dcs.Plain(dcs.PlainOptions{Dial: dial}), nil
	}
	return nil, errors.New("proxy: unknown kind")
}

// socksDialer dials the addresses of Telegram's servers through the SOCKS5
// proxy: they are IP addresses, so no name is asked of the proxy, or of the
// system's resolver, which would tell of what the connection is for.
func (p Proxy) socksDialer() (dcs.DialFunc, error) {
	var auth *xproxy.Auth
	if p.User != "" {
		auth = &xproxy.Auth{User: p.User, Password: p.Password}
	}
	d, err := xproxy.SOCKS5("tcp", p.Address(), auth, &net.Dialer{})
	if err != nil {
		return nil, err
	}
	cd, ok := d.(xproxy.ContextDialer)
	if !ok {
		return nil, errors.New("proxy: the SOCKS5 dialer does not take a context")
	}
	return cd.DialContext, nil
}

package dcpool

import (
	"context"
	"io"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

// DownloadClient keeps origin requests on the file's DC and CDN requests on
// separate, unauthenticated CDN transports managed by gotd (including RSA keys).
func DownloadClient(p Pool, ctx context.Context, dc int) downloader.Client {
	api := p.Client(ctx, dc)
	if provider, ok := p.(downloader.CDNProvider); ok {
		return downloadClient{api, provider}
	}
	return api
}

type downloadClient struct {
	*tg.Client
	downloader.CDNProvider
}

// CDN is the connection to CDN DC dc, opened once and kept for every file
// it serves, until the pool closes: a connection of its own for each file
// made each one wait for a new auth key, whose Diffie-Hellman exchange
// costs round trips and a check of a 2048-bit prime. The closer it gives
// leaves the connection open.
func (p *pool) CDN(ctx context.Context, dc int, max int64) (downloader.CDN, io.Closer, error) {
	p.mu.Lock()
	invoker, ok := p.cdns[dc]
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return nil, nil, io.ErrClosedPipe
	}
	if !ok {
		// Opened without the lock: the first one fetches the CDN's keys.
		opened, err := p.openCDN(ctx, dc, max)
		if err != nil {
			return nil, nil, err
		}
		p.mu.Lock()
		switch kept, ok := p.cdns[dc]; {
		case p.closed:
			p.mu.Unlock()
			_ = opened.Close()
			return nil, nil, io.ErrClosedPipe
		case ok:
			// Another download opened it meanwhile.
			invoker = kept
			p.mu.Unlock()
			_ = opened.Close()
		default:
			p.cdns[dc] = opened
			invoker = opened
			p.mu.Unlock()
		}
	}
	var api tg.Invoker = invoker
	if p.observer != nil {
		api = chainMiddlewares(invoker, p.observer(dc)...)
	}
	return tg.NewClient(api), keptOpen{}, nil
}

// keptOpen is the closer of a connection the pool keeps.
type keptOpen struct{}

func (keptOpen) Close() error { return nil }

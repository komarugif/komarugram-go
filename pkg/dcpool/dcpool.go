package dcpool

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"komarugram/pkg/logctx"
	"komarugram/pkg/middlewares/takeout"
)

// Pool hands out connections for file transfers: those to the account's own
// DC go to its media-only addresses (see homePool), which serve nothing else.
type Pool interface {
	Client(ctx context.Context, dc int) *tg.Client
	Takeout(ctx context.Context, dc int) *tg.Client
	Default(ctx context.Context) *tg.Client
	Close() error
}

type pool struct {
	api         *telegram.Client
	size        int64
	mu          *sync.Mutex
	middlewares []telegram.Middleware
	observer    func(int) []telegram.Middleware

	invokers map[int]tg.Invoker
	closes   map[int]func() error
	takeout  int64
	closed   bool
	// cdns are the connections to CDN DCs, by DC, kept for every file they
	// serve (CDN); openCDN opens one, the client's own but in tests.
	cdns    map[int]telegram.CloseInvoker
	openCDN func(ctx context.Context, dc int, max int64) (telegram.CloseInvoker, error)
}

func NewPool(c *telegram.Client, size int64, middlewares ...telegram.Middleware) Pool {
	return &pool{
		api:         c,
		size:        max(size, 1),
		mu:          &sync.Mutex{},
		middlewares: middlewares,
		invokers:    make(map[int]tg.Invoker),
		closes:      make(map[int]func() error),
		takeout:     0,
		cdns:        make(map[int]telegram.CloseInvoker),
		openCDN:     c.CDN,
	}
}

func (p *pool) current() int {
	return p.api.Config().ThisDC
}

func (p *pool) Client(ctx context.Context, dc int) *tg.Client {
	return tg.NewClient(p.invoker(ctx, dc))
}

func (p *pool) invoker(ctx context.Context, dc int) tg.Invoker {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { return errors.New("dcpool: closed") })
	}
	if i, ok := p.invokers[dc]; ok {
		return i
	}

	// lazy init
	var (
		invoker telegram.CloseInvoker
		err     error
	)
	if dc == p.current() {
		invoker = p.homePool(ctx, dc)
	} else {
		invoker, err = p.api.DC(ctx, dc, p.size)
	}

	if err != nil {
		logctx.From(ctx).Error("create invoker", zap.Error(err))
		return telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { return err })
	}

	p.closes[dc] = invoker.Close
	chain := append([]telegram.Middleware(nil), p.middlewares...)
	if p.observer != nil {
		chain = append(chain, p.observer(dc)...)
	}
	p.invokers[dc] = chainMiddlewares(invoker, chain...)

	return p.invokers[dc]
}

func (p *pool) homePool(ctx context.Context, dc int) telegram.CloseInvoker {
	if !hasMediaOnly(p.api.Config().DCOptions, dc) {
		return sharedInvoker{p.api}
	}
	invoker, err := p.api.MediaOnly(context.WithValue(ctx, ownAuthorization{}, true), dc, p.size)
	if err != nil {
		logctx.From(ctx).Warn("media-only connections to own DC", zap.Int("dc", dc), zap.Error(err))
		return sharedInvoker{p.api}
	}
	return invoker
}

// ownAuthorization marks the context of a pool to the client's own DC.
type ownAuthorization struct{}

// OnTransfer is the telegram.Options.OnTransfer that lets a Pool open
// media-only connections to the account's own DC: they are authorized
// already, and there is nothing to transfer.
func OnTransfer(ctx context.Context, _ *telegram.Client, transfer func(context.Context) error) error {
	if ctx.Value(ownAuthorization{}) != nil {
		return nil
	}
	return transfer(ctx)
}

func hasMediaOnly(options []tg.DCOption, dc int) bool {
	for _, o := range options {
		if o.ID == dc && o.MediaOnly && !o.CDN {
			return true
		}
	}
	return false
}

// sharedInvoker is the client's own connection, which the pool must not
// close.
type sharedInvoker struct{ tg.Invoker }

func (sharedInvoker) Close() error { return nil }

func (p *pool) Default(ctx context.Context) *tg.Client {
	return p.Client(ctx, p.current())
}

func (p *pool) Close() (err error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	id, closes, cdns := p.takeout, p.closes, p.cdns
	p.mu.Unlock()
	for _, cdn := range cdns {
		err = multierr.Append(err, cdn.Close())
	}
	if id != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = takeout.UnTakeout(ctx, chainMiddlewares(p.api, takeout.Middleware(id)))
		cancel()
	}
	for _, close := range closes {
		err = multierr.Append(err, close())
	}
	return err
}

func (p *pool) Takeout(ctx context.Context, dc int) *tg.Client {
	p.mu.Lock()
	if !p.closed && p.takeout == 0 {
		id, err := takeout.Takeout(ctx, p.api)
		if err != nil {
			logctx.From(ctx).Warn("takeout error", zap.Error(err))
		} else {
			p.takeout = id
		}
	}
	id := p.takeout
	p.mu.Unlock()
	invoker := p.invoker(ctx, dc)
	if id != 0 {
		invoker = chainMiddlewares(invoker, takeout.Middleware(id))
	}
	return tg.NewClient(invoker)
}

// NewObservedPool additionally wraps each application invoker with DC-specific
// middleware. Internal auth transfer/transport messages are not observed here.
func NewObservedPool(c *telegram.Client, size int64, observer func(int) []telegram.Middleware) Pool {
	p := NewPool(c, size).(*pool)
	p.observer = observer
	return p
}

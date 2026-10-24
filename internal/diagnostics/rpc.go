package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// Middleware observes application RPCs, not encrypted packets, transport acks,
// unsolicited updates or internal MTProto retries. Pools must install it too.
func Middleware(account string, dc int) []telegram.Middleware {
	r := Current()
	if r == nil {
		return nil
	}
	scope := r.Scope(account)
	return []telegram.Middleware{telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
			if Current() != r {
				return next.Invoke(ctx, in, out)
			}
			method := reflect.TypeOf(in).String()
			if named, ok := in.(interface{ TypeName() string }); ok {
				method = named.TypeName()
			}
			r.mu.Lock()
			r.seq++
			r.inFlight++
			rpc := RPC{ID: r.seq, At: time.Now(), Scope: scope, Method: method, DC: dc, ResponseBytes: -1, Pending: true}
			if len(r.pending) < RPCLimit {
				r.pending[rpc.ID] = rpc
			}
			r.counters[scope+"/rpc.started"]++
			r.mu.Unlock()
			enc := &measuredEncoder{Encoder: in}
			dec := &measuredDecoder{Decoder: out}
			dec.bytes.Store(-1)
			err := next.Invoke(ctx, enc, dec)
			rpc.Duration = time.Since(rpc.At)
			rpc.RequestBytes = enc.bytes.Load()
			rpc.Encodes = enc.calls.Load()
			rpc.ResponseBytes = dec.bytes.Load()
			rpc.Error = ErrorClass(err)
			rpc.Pending = false
			r.mu.Lock()
			delete(r.pending, rpc.ID)
			r.inFlight--
			r.rpcs.add(rpc, RPCLimit)
			r.counters[scope+"/rpc.completed"]++
			if err != nil {
				r.counters[scope+"/rpc.errors"]++
			}
			r.mu.Unlock()
			return err
		}
	})}
}

type measuredEncoder struct {
	bin.Encoder
	bytes, calls atomic.Int64
}

func (m *measuredEncoder) Encode(b *bin.Buffer) error {
	n := len(b.Buf)
	err := m.Encoder.Encode(b)
	m.bytes.Add(int64(len(b.Buf) - n))
	m.calls.Add(1)
	return err
}

type measuredDecoder struct {
	bin.Decoder
	bytes atomic.Int64
}

func (m *measuredDecoder) Decode(b *bin.Buffer) error {
	n := len(b.Buf)
	err := m.Decoder.Decode(b)
	m.bytes.Store(int64(n - len(b.Buf)))
	return err
}
func ErrorClass(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, io.EOF) {
		return "EOF"
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "network timeout"
		}
		return "network error"
	}
	if rpc, ok := tgerr.As(err); ok {
		// Avoid storing raw Error()/Message, which may include server/user data.
		kind := strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' || r == '_' {
				return r
			}
			return -1
		}, rpc.Type)
		if len(kind) > 64 {
			kind = kind[:64]
		}
		if rpc.Argument != 0 {
			// The number of FLOOD_WAIT_X and its kind: how long to wait.
			return fmt.Sprintf("RPC %d %s %d", rpc.Code, kind, rpc.Argument)
		}
		return fmt.Sprintf("RPC %d %s", rpc.Code, kind)
	}
	return fmt.Sprintf("%T", err)
}

// Preserve gotd's optional request-type metadata for its own tracing.
func (m *measuredEncoder) TypeID() uint32 {
	if v, ok := m.Encoder.(interface{ TypeID() uint32 }); ok {
		return v.TypeID()
	}
	return 0
}

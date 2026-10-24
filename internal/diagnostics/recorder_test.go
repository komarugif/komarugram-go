package diagnostics

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tgerr"
)

type word struct{ value byte }

func (w *word) Encode(b *bin.Buffer) error { b.Buf = append(b.Buf, w.value, 0, 0, 0); return nil }
func (w *word) Decode(b *bin.Buffer) error { w.value = b.Buf[0]; b.Skip(4); return nil }
func TestRecorderAndRPC(t *testing.T) {
	r := Enable()
	defer r.Close()
	for i := 0; i < EventLimit+5; i++ {
		r.Event("demo", "scroll", "no content", time.Microsecond, i)
	}
	snapshot := r.Snapshot()
	if len(snapshot.Events) != EventLimit || snapshot.DroppedEvents != 5 || snapshot.Events[0].Count != 5 {
		t.Fatal("unbounded or out-of-order ring")
	}
	// Observe actual encode/decode calls; preserve the original decoded output.
	next := telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		b := bin.Buffer{}
		if err := in.Encode(&b); err != nil {
			return err
		}
		return out.Decode(&b)
	})
	middleware := Middleware("private-account-id", 2)[0]
	out := &word{}
	if err := middleware.Handle(next)(context.Background(), &word{7}, out); err != nil || out.value != 7 {
		t.Fatal("middleware changed RPC semantics", err)
	}
	row := r.Snapshot().RPCs[0]
	if row.RequestBytes != 4 || row.ResponseBytes != 4 || row.Encodes != 1 || row.DC != 2 || row.Error != "" || len(r.Snapshot().Pending) != 0 {
		t.Fatalf("bad measurement: %+v", row)
	}
	want := tgerr.New(420, "FLOOD_WAIT_10")
	fail := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { return want })
	if got := middleware.Handle(fail)(context.Background(), &word{}, &word{}); !errors.Is(got, want) {
		t.Fatal("error replaced")
	}
	row = r.Snapshot().RPCs[1]
	if row.Error != "RPC 420 FLOOD_WAIT 10" || row.ResponseBytes != -1 {
		t.Fatal(row)
	}
	path, err := r.Capture(t.TempDir(), "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-account-id") {
		t.Fatal("account identity leaked")
	}
	r.Clear()
	if len(r.Snapshot().RPCs) != 0 || len(r.Snapshot().Events) != 0 {
		t.Fatal("clear failed")
	}
}

// SPDX-License-Identifier: Unlicense OR MIT

package sandbox

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
)

// manyTypesModule is addModule with types more than a thousand, unused but
// the first, for each of which wazero makes an entry into compiled code: a
// module taken from the disk cache is shared before they are made.
func manyTypesModule() []byte {
	valueTypes := []byte{0x7f, 0x7e, 0x7d, 0x7c} // i32, i64, f32, f64
	var types []byte
	n := 0
	types = append(types, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f) // (i32, i32) -> i32
	n++
	for i := 0; n < 1100; i++ {
		// The parameters of type i: its digits in base 4, as many as
		// five, and a result of i32: all of them different.
		var params []byte
		for v, digits := i, 0; digits < 5; v, digits = v/4, digits+1 {
			params = append(params, valueTypes[v%4])
		}
		params = params[:1+i%5]
		types = append(types, 0x60, byte(len(params)))
		types = append(types, params...)
		types = append(types, 0x01, 0x7e)
		n++
	}
	section := func(id byte, body []byte) []byte {
		var size []byte
		for v := uint32(len(body)); ; {
			b := byte(v & 0x7f)
			v >>= 7
			if v != 0 {
				size = append(size, b|0x80)
				continue
			}
			size = append(size, b)
			break
		}
		return append(append([]byte{id}, size...), body...)
	}
	count := []byte{byte(n&0x7f | 0x80), byte(n >> 7)}
	m := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	m = append(m, section(0x01, append(count, types...))...)
	m = append(m, 0x03, 0x02, 0x01, 0x00)                                           // one function of type 0
	m = append(m, 0x07, 0x07, 0x01, 0x03, 'a', 'd', 'd', 0x00, 0x00)                // export "add"
	m = append(m, 0x0a, 0x09, 0x01, 0x07, 0x00, 0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b) // i32.add
	return m
}

// Runtimes that compile one module at once, taken from the disk cache,
// each have it whole: wazero shares a module read from its file cache with
// the runtimes of its cache before it made its entries into the code, and
// one that took it then, and called it, ran into none (index out of range
// in wazevo's moduleEngine.NewFunction), as a sticker's sandbox did.
func TestConcurrentCompileFromDiskCache(t *testing.T) {
	if !compiles() {
		t.Skip("wazero interprets here")
	}
	module := manyTypesModule()
	p := newPaths(t)
	run(t, p, module)
	ctx := context.Background()
	for round := range 20 {
		c, err := NewDiskCache(p.dir, p.key)
		if err != nil {
			t.Fatal(err)
		}
		SetCache(c)
		var (
			start  = make(chan struct{})
			wg     sync.WaitGroup
			mu     sync.Mutex
			failed []string
			rts    []*Runtime
		)
		for range 8 {
			rt, err := NewRuntime(ctx, Limits{Memory: 1 << 20, Slow: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			rts = append(rts, rt)
			wg.Go(func() {
				defer func() {
					if v := recover(); v != nil {
						mu.Lock()
						failed = append(failed, fmt.Sprint(v))
						mu.Unlock()
					}
				}()
				<-start
				compiled, err := rt.CompileModule(ctx, module)
				if err != nil {
					panic(err)
				}
				mod, err := rt.Instantiate(ctx, compiled, wazero.NewModuleConfig().WithName(""))
				if err != nil {
					panic(err)
				}
				if res, err := mod.ExportedFunction("add").Call(ctx, 2, 3); err != nil || res[0] != 5 {
					panic(fmt.Sprintf("add(2, 3) = %v, %v", res, err))
				}
			})
		}
		close(start)
		wg.Wait()
		for _, rt := range rts {
			_ = rt.Close(ctx)
		}
		SetCache(nil)
		_ = c.Close(ctx)
		if len(failed) > 0 {
			t.Fatalf("round %d: %d of 8 failed: %s", round, len(failed), failed[0])
		}
	}
}

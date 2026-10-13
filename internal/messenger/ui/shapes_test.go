// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"reflect"
	"testing"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// opsBytes is how many bytes of operations o holds.
func opsBytes(o *op.Ops) int {
	return reflect.ValueOf(&o.Internal).Elem().FieldByName("data").Len()
}

// The badges of names record their paths once: a frame then adds a call of
// them, which Gio draws from the vertices it keeps, instead of paths built,
// and the check mark stroked on the CPU, again.
func TestBadgesRecordTheirPathsOnce(t *testing.T) {
	for name, draw := range map[string]func(gtx layout.Context){
		"verified": func(gtx layout.Context) { layoutVerified(gtx, 16) },
		"premium":  func(gtx layout.Context) { premiumStarIcon(gtx, token.MatColor{R: 1, A: 255}, 16) },
	} {
		var o op.Ops
		gtx := layout.Context{Ops: &o, Metric: unit.Metric{PxPerDp: 1.37, PxPerSp: 1.37}}
		draw(gtx)
		o.Reset()
		draw(gtx)
		if n := opsBytes(&o); n > 64 {
			t.Errorf("%s: %d bytes of operations in a frame after the first, want a call of those recorded", name, n)
		}
	}
}

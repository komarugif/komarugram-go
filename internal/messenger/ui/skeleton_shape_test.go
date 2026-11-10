package ui

import (
	"image"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// skeletonOf is the skeleton of message m's row, laid out at 500 px, with
// its profile, as the reserve has it.
func skeletonOf(t *testing.T, h *menuHarness, m model.Message) skeletonRow {
	t.Helper()
	metric := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	theme := defaults.NewTheme(layout.Context{Ops: new(op.Ops), Metric: metric, Values: map[string]any{}}, schemes.SchemeBaselineLight())
	var height int
	for range 2 {
		gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Constraints: layout.Constraints{Max: image.Pt(500, 1<<20)}, Metric: metric, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, theme)
		height = h.page.row(gtx, m, false, 0, localization.For("en"), false).Size.Y
	}
	data := h.page.takeProfile(h.page.rows[m.Key.MessageID], m, 500, height)
	if data == nil {
		t.Fatal("the row took no profile")
	}
	env := model.RenderEnvironment{WidthPx: 500, ScaleMilli: 1000, TextScaleMilli: 1000}
	rows := reserveRows([]model.ReserveRow{{ID: m.Key.MessageID, Revision: m.ContentRevision, Height: height, HeightRevision: m.ContentRevision, Profile: data, ProfileRevision: m.ContentRevision}}, env)
	if len(rows) != 1 || rows[0].profile == nil {
		t.Fatalf("the reserve has %+v", rows)
	}
	h.page.env = env
	return rows[0]
}

// A skeleton draws a bar for each line of its text, as long as the line,
// within its bubble.
func TestSkeletonBarsAreTheLines(t *testing.T) {
	h := newMenuHarness(t, nil)
	m := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 900}, Date: time.Unix(1000, 0), Text: "One\nA longer line, two\nThree", Outgoing: true, ContentRevision: 7}
	s := skeletonOf(t, h, m)
	shape, _ := h.page.shapeOf(s, true)
	var lines []image.Rectangle
	for _, b := range h.page.skeletonBars(s) {
		if b.kind == barLine {
			lines = append(lines, b.rect)
		}
		if !b.rect.In(image.Rectangle{Max: shape.bubble}) {
			t.Errorf("bar %v is out of the bubble %v", b.rect, shape.bubble)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("%d bars for 3 lines: %v", len(lines), lines)
	}
	if !(lines[1].Dx() > lines[0].Dx() && lines[1].Dx() > lines[2].Dx()) || lines[0].Min.Y >= lines[1].Min.Y || lines[1].Min.Y >= lines[2].Min.Y {
		t.Fatalf("the bars are not the lines: %v", lines)
	}
}

// With animations off a skeleton is plain: its bubble is worked out from
// its profile, but not what the bubble shows.
func TestSkeletonBarsNotWithAnimationsOff(t *testing.T) {
	h := newMenuHarness(t, nil)
	m := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 901}, Date: time.Unix(1000, 0), Text: "Some text", ContentRevision: 7}
	s := skeletonOf(t, h, m)
	s.height = 60
	gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Exact(image.Pt(500, 60)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	wdk.SetAnimationsEnabled(gtx, false)
	h.page.shapeCache = nil
	h.page.skeleton(gtx, h.page.resolveShape(gtx, s))
	if c := h.page.shapeCache[s.id]; c.bubble.X <= 0 || c.barred {
		t.Fatalf("with animations off, the skeleton is %+v", c)
	}
	h.page.shapeCache = nil
	wdk.SetAnimationsEnabled(gtx, true)
	h.page.skeleton(gtx, h.page.resolveShape(gtx, s))
	if c := h.page.shapeCache[s.id]; !c.barred || len(c.bars) == 0 {
		t.Fatal("with animations on, the skeleton has no bars")
	}
}

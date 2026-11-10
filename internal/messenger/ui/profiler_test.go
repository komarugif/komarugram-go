package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"komarugram/internal/diagnostics"
	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/localization"

	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// profilerWith is the profiler of a recorder with a frame, a history, an
// event and a line of the journal.
func profilerWith(t *testing.T) *profiler {
	t.Helper()
	r := diagnostics.Enable()
	t.Cleanup(r.Close)
	r.RecordFrame(diagnostics.Frame{At: time.Now(), Window: "demo", Total: 20 * time.Millisecond, Layout: 18 * time.Millisecond, Phases: []diagnostics.Phase{{Name: "history.rich-text", Duration: 10 * time.Millisecond, Calls: 8}}})
	r.History(diagnostics.History{Window: "demo", Chat: 1, Messages: 300, Visible: 8, Restore: "linear-id-anchor", RestoreScanned: 290, Above: 120, Below: 4000, Reserve: 4100, ReserveTaken: true, Leaps: 3})
	r.Event("demo", "history.restore", "anchor:290", time.Millisecond, 290)
	p := ProfilerSpec(r, t.TempDir(), localization.For("ru")).Build(nil).(*profiler)
	t.Cleanup(p.Close)
	historylog.Printf("leap chat=1 to 500: 600 messages, 200..800")
	return p
}

// profilerFrame draws a frame of the profiler at 1150 by 860.
func profilerFrame(p *profiler, gtx layout.Context) {
	wdk.InitMaterialThemeInContext(gtx, p.Theme(gtx))
	p.next = time.Time{}
	p.Update(gtx)
	p.Layout(gtx)
}

// Every tab of the profiler shows what the recorder has, the journal the
// lines of the history's journal, which it keeps while it is open.
func TestProfilerTabs(t *testing.T) {
	p := profilerWith(t)
	if !historylog.On() {
		t.Fatal("the profiler open, the journal is not kept")
	}
	want := map[profilerTab]string{profilerFrames: "FPS", profilerMemory: "", profilerRPC: "in flight", profilerHistory: "above 120", profilerJournal: "leap chat=1 to 500"}
	for tab := profilerFrames; tab < profilerTabs; tab++ {
		p.tab = tab
		gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(1150, 860)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		profilerFrame(p, gtx)
		if len(p.rows) == 0 {
			t.Fatalf("tab %d is empty", tab)
		}
		var all strings.Builder
		for _, r := range p.rows {
			all.WriteString(r.text + "\n")
		}
		if w := want[tab]; w != "" && !strings.Contains(all.String(), w) {
			t.Fatalf("tab %d lacks %q:\n%s", tab, w, all.String())
		}
	}
	p.Close()
	if historylog.On() {
		t.Fatal("the profiler closed, the journal is still kept")
	}
}

// TestRenderProfiler saves each tab of the profiler, for looking at it:
// PROFILER_PNG_DIR=/tmp/profiler.
func TestRenderProfiler(t *testing.T) {
	dir := os.Getenv("PROFILER_PNG_DIR")
	if dir == "" {
		t.Skip("set PROFILER_PNG_DIR to a directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := profilerWith(t)
	for i := range 40 {
		historylog.Printf("reserve: chat 1 after %d: 120 rows of 600 kept (120 heights, 118 profiles), up to message %d", 1000+i*20, 1120+i*20)
	}
	for tab := profilerFrames; tab < profilerTabs; tab++ {
		p.tab = tab
		renderFrames(t, image.Pt(1150, 860), filepath.Join(dir, fmt.Sprintf("tab-%d.png", tab)), func(gtx layout.Context) {
			gtx.Metric = unit.Metric{PxPerDp: 1, PxPerSp: 1}
			profilerFrame(p, gtx)
		})
	}
}

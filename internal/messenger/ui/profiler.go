// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"komarugram/internal/appwindow"
	"komarugram/internal/crash"
	"komarugram/internal/diagnostics"
	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/localization"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/token"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// The profiler is the window -profile opens beside the messenger's
// (docs/PROFILING.md): its frames, memory, Telegram's RPC, the histories
// and the journal of how they load, as the diagnostics recorder has them,
// in the client's components and theme. It reads the recorder twice a
// second, unless frozen; the journal's lines are kept while it is open.

// profilerTab is a tab of the profiler.
type profilerTab int

const (
	profilerFrames profilerTab = iota
	profilerMemory
	profilerRPC
	profilerHistory
	profilerJournal
	profilerTabs
)

// profiler is the profiler's window.
type profiler struct {
	window    *appwindow.Window
	recorder  *diagnostics.Recorder
	directory string
	l         localization.Catalog
	theme     *token.Theme
	snapshot  diagnostics.Snapshot
	journal   []historylog.Line
	written   uint64
	next      time.Time
	tab       profilerTab
	tabs      tabRow
	freeze    surface
	clear     surface
	errors    surface
	save      surface
	captures  [6]surface
	filter    widget.Editor
	list      scroll.List
	paused    bool
	onlyErrs  bool
	status    string
	results   chan string
	// rows are the rows of the tab shown, made again with each snapshot.
	rows []profilerRow
}

// profilerRow is a row of the profiler's list: a title of a part of it, a
// note on what it shows, or a line of what it measured.
type profilerRow struct {
	kind profilerRowKind
	text string
}

type profilerRowKind uint8

const (
	rowLine profilerRowKind = iota
	rowTitle
	rowNote
)

// ProfilerSpec is the profiler's window, of recorder r, saving what it
// captures in directory, in the language of l.
func ProfilerSpec(r *diagnostics.Recorder, directory string, l localization.Catalog) appwindow.Spec {
	return appwindow.Spec{Options: appwindow.Options{Title: l.T("profiler.title"), Width: 1150, Height: 860}, Build: func(w *appwindow.Window) appwindow.Content {
		p := &profiler{window: w, recorder: r, directory: directory, l: l, results: make(chan string, 1)}
		p.filter.SingleLine = true
		p.list.Axis = layout.Vertical
		historylog.Keep(true)
		return p
	}}
}

func (p *profiler) Close() {
	historylog.Keep(false)
	if !p.recorder.AutoExporting() {
		p.recorder.Close()
	}
}

func (p *profiler) Theme(gtx layout.Context) *token.Theme {
	if p.theme == nil {
		p.theme = defaults.NewTheme(gtx, schemes.SchemeBaselineDark())
	}
	return p.theme
}

// captureKinds are what the capture buttons record, the first the export
// of the recorder's data.
var captureKinds = [...]string{"snapshot", "cpu", "heap", "allocs", "trace", "goroutine"}

func (p *profiler) Update(gtx layout.Context) {
	if i, ok := p.tabs.Clicked(gtx, int(p.tab), int(profilerTabs)); ok {
		p.tab = profilerTab(i)
		p.list.Position = layout.Position{}
		p.next = time.Time{}
	}
	if p.freeze.Clicked(gtx) {
		p.paused = !p.paused
		p.next = time.Time{}
	}
	if p.clear.Clicked(gtx) {
		p.recorder.Clear()
		p.snapshot = p.recorder.Snapshot()
		p.next = time.Time{}
	}
	if p.errors.Clicked(gtx) {
		p.onlyErrs = !p.onlyErrs
		p.next = time.Time{}
	}
	if p.save.Clicked(gtx) {
		p.saveJournal()
	}
	for i, kind := range captureKinds {
		if p.captures[i].Clicked(gtx) {
			p.capture(kind)
		}
	}
	select {
	case p.status = <-p.results:
	default:
	}
	for {
		e, ok := p.filter.Update(gtx)
		if !ok {
			break
		}
		if _, changed := e.(widget.ChangeEvent); changed {
			p.next = time.Time{}
		}
	}
	if p.snapshot.At.IsZero() || !p.paused && !gtx.Now.Before(p.next) {
		p.snapshot = p.recorder.Snapshot()
		p.journal, p.written = historylog.Lines()
		p.rows = p.filtered(p.makeRows())
		p.next = gtx.Now.Add(500 * time.Millisecond)
	}
}

// capture records kind off the frame, and tells where it went, or why not.
func (p *profiler) capture(kind string) {
	p.status = p.l.Format("profiler.capturing", map[string]string{"kind": kind})
	go func() {
		defer crash.Recover("profile capture", func(e *crash.Panic) {
			p.tell(p.l.Format("profiler.capture_failed", map[string]string{"error": e.Error()}))
		})
		path, err := p.recorder.Capture(p.directory, kind)
		if err != nil {
			p.tell(p.l.Format("profiler.capture_failed", map[string]string{"error": err.Error()}))
			return
		}
		p.tell(path)
	}()
}

// tell shows message in the status line, from any goroutine.
func (p *profiler) tell(message string) {
	select {
	case p.results <- message:
	default:
	}
	p.window.Invalidate()
}

// saveJournal writes the journal's lines kept to a new file in the
// profiler's directory, as -history-log writes them.
func (p *profiler) saveJournal() {
	lines := p.journal
	directory := p.directory
	go func() {
		defer crash.Recover("history journal save", func(e *crash.Panic) {
			p.tell(p.l.Format("profiler.capture_failed", map[string]string{"error": e.Error()}))
		})
		if err := os.MkdirAll(directory, 0o700); err != nil {
			p.tell(p.l.Format("profiler.capture_failed", map[string]string{"error": err.Error()}))
			return
		}
		path := filepath.Join(directory, time.Now().Format("20060102-150405")+"-history.log")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			p.tell(p.l.Format("profiler.capture_failed", map[string]string{"error": err.Error()}))
			return
		}
		var b strings.Builder
		for _, line := range lines {
			fmt.Fprintf(&b, "%s g%d %s\n", line.At.Format("15:04:05.000"), line.Goroutines, line.Text)
		}
		_, err = f.WriteString(b.String())
		if e := f.Close(); err == nil {
			err = e
		}
		if err != nil {
			p.tell(p.l.Format("profiler.capture_failed", map[string]string{"error": err.Error()}))
			return
		}
		p.tell(path)
	}()
}

func (p *profiler) Layout(gtx layout.Context) {
	sc := scheme(gtx)
	fillRect(gtx, sc.Surface.Color, gtx.Constraints.Max)
	if !p.paused {
		gtx.Execute(op.InvalidateCmd{At: p.next})
	}
	l := p.l
	layout.UniformInset(16).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("profiler.heading"), token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("profiler.subtitle"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 2)
			}),
			vspace(10),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.actions(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if p.status == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, p.status, token.TypestyleBodySmall, sc.Primary.Color, 2)
				})
			}),
			vspace(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Dp(48)))
				titles := []string{l.T("profiler.frames"), l.T("profiler.memory"), l.T("profiler.rpc"), l.T("profiler.history"), l.T("profiler.journal")}
				return p.tabs.Layout(gtx, titles, int(p.tab))
			}),
			vspace(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.filterBar(gtx) }),
			vspace(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if p.tab != profilerFrames {
					return layout.Dimensions{}
				}
				return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return p.graph(gtx) })
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return p.layoutRows(gtx) }),
		)
	})
}

// actions are the profiler's buttons: freezing the view, clearing the
// metrics and exporting them, and the runtime's profiles.
func (p *profiler) actions(gtx layout.Context) layout.Dimensions {
	l := p.l
	freeze := l.T("profiler.freeze")
	if p.paused {
		freeze = l.T("profiler.resume")
	}
	buttons := []struct {
		s     *surface
		title string
		tonal bool
	}{
		{&p.freeze, freeze, true},
		{&p.clear, l.T("profiler.clear"), true},
		{&p.captures[0], l.T("profiler.export"), true},
		{&p.captures[1], l.T("profiler.cpu"), false},
		{&p.captures[2], l.T("profiler.heap"), false},
		{&p.captures[3], l.T("profiler.allocs"), false},
		{&p.captures[4], l.T("profiler.trace"), false},
		{&p.captures[5], l.T("profiler.goroutines"), false},
	}
	var children []layout.FlexChild
	for _, b := range buttons {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				if b.tonal {
					return tonalButton(gtx, b.s, b.title, 0)
				}
				return textButton(gtx, b.s, b.title)
			})
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// filterBar is the filter of the list, and what the tab shown has beside
// it: the RPC's errors alone, or saving the journal.
func (p *profiler) filterBar(gtx layout.Context) layout.Dimensions {
	sc := scheme(gtx)
	l := p.l
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return plate(gtx, 12, func(gtx layout.Context) layout.Dimensions {
				return flatEditor(gtx, &p.filter, l.T("profiler.filter"))
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			switch p.tab {
			case profilerRPC:
				title := l.T("profiler.errors_off")
				if p.onlyErrs {
					title = l.T("profiler.errors_on")
				}
				return layout.Inset{Left: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return tonalButton(gtx, &p.errors, title, 0) })
			case profilerJournal:
				return layout.Inset{Left: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							count := l.Format("profiler.journal_count", map[string]string{"kept": fmt.Sprint(len(p.journal)), "written": fmt.Sprint(p.written)})
							return layout.Inset{Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return label(gtx, count, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return tonalButton(gtx, &p.save, l.T("profiler.journal_save"), 0)
						}),
					)
				})
			}
			return layout.Dimensions{}
		}),
	)
}

// layoutRows draws the rows of the tab shown; the journal follows its end
// while the list is at it.
func (p *profiler) layoutRows(gtx layout.Context) layout.Dimensions {
	sc := scheme(gtx)
	rows := p.rows
	if p.tab == profilerJournal {
		p.list.ScrollToEnd = true
	} else {
		p.list.ScrollToEnd = false
	}
	return layout.UniformInset(4).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return p.list.Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
			r := rows[i]
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			switch r.kind {
			case rowTitle:
				return layout.Inset{Top: 10, Bottom: 4, Left: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, r.text, token.TypestyleTitleSmall, sc.Primary.Color, 0)
				})
			case rowNote:
				return layout.Inset{Bottom: 2, Left: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, r.text, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
				})
			}
			macro := op.Record(gtx.Ops)
			dims := layout.Inset{Top: 3, Bottom: 3, Left: 4, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, r.text, token.TypestylePreformatted, sc.Surface.OnColor, 0)
			})
			call := macro.Stop()
			if i%2 == 0 {
				paint.FillShape(gtx.Ops, sc.Surface.OnColor.SetOpacity(.03).AsNRGBA(), clip.Rect{Max: dims.Size}.Op())
			}
			call.Add(gtx.Ops)
			return dims
		})
	})
}

// filtered is rows with the lines the filter asks for, the titles and the
// notes too while it asks for none.
func (p *profiler) filtered(rows []profilerRow) []profilerRow {
	query := strings.ToLower(strings.TrimSpace(p.filter.Text()))
	if query == "" {
		return rows
	}
	out := rows[:0]
	for _, r := range rows {
		if r.kind == rowLine && strings.Contains(strings.ToLower(r.text), query) {
			out = append(out, r)
		}
	}
	return out
}

// graph draws the time of the windows' last frames, the budget of 60 Hz
// across it, in the theme's colors.
func (p *profiler) graph(gtx layout.Context) layout.Dimensions {
	sc := scheme(gtx)
	var frames []diagnostics.Frame
	for _, name := range p.windowNames() {
		frames = append(frames, p.snapshot.Frames[name]...)
	}
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(96))
	return plate(gtx, 8, func(gtx layout.Context) layout.Dimensions {
		size.X = gtx.Constraints.Max.X
		sort.Slice(frames, func(i, j int) bool { return frames[i].At.Before(frames[j].At) })
		if len(frames) > 180 {
			frames = frames[len(frames)-180:]
		}
		for i, f := range frames {
			x := i * size.X / max(1, len(frames))
			next := (i + 1) * size.X / max(1, len(frames))
			height := min(size.Y, int(ms(f.Total)/50*float64(size.Y)))
			col := sc.Primary.Color
			if f.Total > time.Second/60 {
				col = sc.Error.Color
			}
			paint.FillShape(gtx.Ops, col.AsNRGBA(), clip.Rect(image.Rect(x, size.Y-height, max(x+1, next-1), size.Y)).Op())
		}
		y := size.Y - int(16.667/50*float64(size.Y))
		paint.FillShape(gtx.Ops, sc.Surface.OnColor.SetOpacity(.4).AsNRGBA(), clip.Rect(image.Rect(0, y, size.X, y+1)).Op())
		return layout.Dimensions{Size: size}
	})
}

// plate draws w on a rounded plate of the container's color, pad in from
// its edges, as wide as it may be.
func plate(gtx layout.Context, pad unit.Dp, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := layout.UniformInset(pad).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return w(gtx)
	})
	call := macro.Stop()
	fillRounded(gtx, scheme(gtx).SurfaceContainerHigh, dims.Size, gtx.Dp(12))
	call.Add(gtx.Ops)
	return dims
}

func (p *profiler) windowNames() []string {
	var names []string
	for name := range p.snapshot.Frames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func mib(n uint64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f KiB", float64(n)/1024)
}

// makeRows makes the rows of the tab shown from the snapshot.
func (p *profiler) makeRows() []profilerRow {
	var rows []profilerRow
	l := p.l
	title := func(key string) { rows = append(rows, profilerRow{kind: rowTitle, text: l.T(key)}) }
	note := func(key string) { rows = append(rows, profilerRow{kind: rowNote, text: l.T(key)}) }
	line := func(format string, args ...any) {
		rows = append(rows, profilerRow{text: fmt.Sprintf(format, args...)})
	}
	s := &p.snapshot
	switch p.tab {
	case profilerFrames:
		note("profiler.frames_note")
		for _, name := range p.windowNames() {
			frames := s.Frames[name]
			if len(frames) == 0 {
				continue
			}
			rows = append(rows, profilerRow{kind: rowTitle, text: name})
			last := frames[len(frames)-1]
			durations := make([]time.Duration, len(frames))
			fps, slow := 0, 0
			var setup, update, lay, submit time.Duration
			phases := map[string]diagnostics.Phase{}
			for i, f := range frames {
				durations[i] = f.Total
				if s.At.Sub(f.At) < 2*time.Second {
					fps++
				}
				if f.Total > time.Second/60 {
					slow++
				}
				setup += f.Setup
				update += f.Update
				lay += f.Layout
				submit += f.Submit
				for _, ph := range f.Phases {
					a := phases[ph.Name]
					a.Name = ph.Name
					a.Calls += ph.Calls
					a.Duration += ph.Duration
					phases[ph.Name] = a
				}
			}
			sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
			quant := func(q float64) float64 { return ms(durations[int(float64(len(durations)-1)*q)]) }
			n := time.Duration(len(frames))
			line("%5.1f FPS   last %6.2f ms   p50 %6.2f   p95 %6.2f   p99 %6.2f ms   slow %d of %d", float64(fps)/2, ms(last.Total), quant(.5), quant(.95), quant(.99), slow, len(frames))
			line("average     setup %5.2f   update %5.2f   layout %5.2f   submit/wait %5.2f ms", ms(setup/n), ms(update/n), ms(lay/n), ms(submit/n))
			line("viewport    %dx%d   px/dp %.2f   px/sp %.2f", last.Width, last.Height, last.PxPerDp, last.PxPerSp)
			var ordered []diagnostics.Phase
			for _, ph := range phases {
				ordered = append(ordered, ph)
			}
			sort.Slice(ordered, func(i, j int) bool { return ordered[i].Duration > ordered[j].Duration })
			for _, ph := range ordered {
				line("  %-30s %7.3f ms/frame   %6d calls", ph.Name, ms(ph.Duration/n), ph.Calls)
			}
		}
	case profilerMemory:
		note("profiler.memory_note")
		if n := len(s.Memory); n > 0 {
			title("profiler.memory_runtime")
			m := s.Memory[n-1]
			line("heap alloc %-11s in use %-11s objects %-9d goroutines %d", mib(m.HeapAlloc), mib(m.HeapInuse), m.HeapObjects, m.Goroutines)
			line("sys        %-11s stack  %-11s idle    %-9s released   %s", mib(m.Sys), mib(m.StackInuse), mib(m.HeapIdle), mib(m.HeapReleased))
			line("GC cycles  %-11d next   %-11s pauses  %.2f ms", m.GC, mib(m.NextGC), float64(m.PauseTotal)/1e6)
			if n > 1 {
				prev := s.Memory[n-2]
				if seconds := m.At.Sub(prev.At).Seconds(); seconds > 0 {
					line("allocating %.2f MiB/s   GC pause in the last interval %.3f ms", float64(m.TotalAlloc-prev.TotalAlloc)/seconds/(1<<20), float64(m.PauseTotal-prev.PauseTotal)/1e6)
				}
			}
		}
		if len(s.Gauges) > 0 {
			title("profiler.memory_caches")
		}
		for _, g := range s.Gauges {
			size := "n/a"
			if g.Bytes > 0 {
				size = mib(uint64(g.Bytes))
			}
			line("%-14s %-30s %9d   %-11s %s ago", g.Scope, g.Name, g.Entries, size, s.At.Sub(g.At).Round(time.Second))
		}
	case profilerRPC:
		note("profiler.rpc_note")
		line("in flight %d   done %d of %d kept   overwritten %d", s.InFlight, len(s.RPCs), diagnostics.RPCLimit, s.DroppedRPCs)
		if !p.onlyErrs && len(s.Pending) > 0 {
			title("profiler.rpc_pending")
			for _, r := range s.Pending {
				line("#%-6d %-12s DC%d %-38s running %.0f ms", r.ID, r.Scope, r.DC, r.Method, ms(s.At.Sub(r.At)))
			}
		}
		title("profiler.rpc_done")
		for i := len(s.RPCs) - 1; i >= 0; i-- {
			r := s.RPCs[i]
			if p.onlyErrs && r.Error == "" {
				continue
			}
			status := r.Error
			if status == "" {
				status = "OK"
			}
			response := "?"
			if r.ResponseBytes >= 0 {
				response = fmt.Sprint(r.ResponseBytes)
			}
			line("%s #%-6d %-12s DC%d %-36s %8.2f ms  ↑%d ↓%s B  encodes %d  %s", r.At.Format("15:04:05.000"), r.ID, r.Scope, r.DC, r.Method, ms(r.Duration), r.RequestBytes, response, r.Encodes, status)
		}
	case profilerHistory:
		note("profiler.history_note")
		for _, h := range s.Histories {
			rows = append(rows, profilerRow{kind: rowTitle, text: fmt.Sprintf("%s · chat %d", h.Window, h.Chat)})
			line("loaded %-6d visible %-4d first %-6d offset %-6d high %d px   revision %d", h.Messages, h.Visible, h.First, h.Offset, h.TotalHeight, h.Revision)
			line("room   above %-6d below %-6d reserve %-6d taken %-5t stopped %t", h.Above, h.Below, h.Reserve, h.ReserveTaken, h.ReserveStopped)
			line("rows   laid out %-5d heights changed %-5d off screen %-6d leaps %d", h.RowsLaidOut, h.MeasurementsChanged, h.OffScreen, h.Leaps)
			line("cache  rows %-6d heights %-7d unsaved %-5d dirty %-5d shapes %-6d to read %d", h.RowsCached, h.MeasurementsCached, h.Unsaved, h.Dirty, h.Shapes, h.ShapesWanted)
			line("build  rebuilt %-5t %s %.2f ms hits %d   restore %s %.2f ms scanned %d   fresh %t", h.Rebuilt, h.Reason, ms(h.RebuildTime), h.LayoutHits, h.Restore, ms(h.RestoreTime), h.RestoreScanned, h.SnapshotFresh)
			line("env    %s", h.Environment)
		}
		var names []string
		for name := range s.Counters {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) > 0 {
			title("profiler.history_counters")
		}
		for _, name := range names {
			line("%-52s %d", name, s.Counters[name])
		}
		rows = append(rows, profilerRow{kind: rowTitle, text: l.Format("profiler.history_events", map[string]string{"kept": fmt.Sprint(len(s.Events)), "dropped": fmt.Sprint(s.DroppedEvents)})})
		for i := len(s.Events) - 1; i >= 0; i-- {
			e := s.Events[i]
			line("%s %-12s %-26s %8.2f ms n %-5d %s", e.At.Format("15:04:05.000"), e.Scope, e.Name, ms(e.Duration), e.Count, e.Detail)
		}
	case profilerJournal:
		note("profiler.journal_note")
		for _, j := range p.journal {
			for i, text := range strings.Split(j.Text, "\n") {
				if i == 0 {
					line("%s g%-4d %s", j.At.Format("15:04:05.000"), j.Goroutines, text)
				} else {
					line("%22s %s", "", text)
				}
			}
		}
	}
	if len(rows) < 3 {
		note("profiler.empty")
	}
	return rows
}

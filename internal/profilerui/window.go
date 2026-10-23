// Package profilerui provides the separate opt-in developer window.
package profilerui

import (
	"fmt"
	"image"
	"image/color"
	"sort"
	"strings"
	"time"

	"komarugram/internal/appwindow"
	"komarugram/internal/crash"
	"komarugram/internal/diagnostics"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/token"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type View struct {
	window          *appwindow.Window
	recorder        *diagnostics.Recorder
	directory       string
	theme           *token.Theme
	material        *material.Theme
	snapshot        diagnostics.Snapshot
	next            time.Time
	tab             int
	tabs            [4]widget.Clickable
	pause, clear    widget.Clickable
	captures        [6]widget.Clickable
	filter          widget.Editor
	errors          widget.Bool
	list            widget.List
	paused          bool
	status          string
	statusSelection widget.Selectable
	results         chan string
}

func Spec(r *diagnostics.Recorder, directory string) appwindow.Spec {
	return appwindow.Spec{Options: appwindow.Options{Title: "komarugram-go · Профилировщик", Width: 1150, Height: 820}, Build: func(w *appwindow.Window) appwindow.Content {
		m := material.NewTheme()
		m.Palette = material.Palette{Bg: color.NRGBA{R: 19, G: 24, B: 32, A: 255}, Fg: color.NRGBA{R: 222, G: 231, B: 241, A: 255}, ContrastBg: color.NRGBA{R: 74, G: 131, B: 205, A: 255}, ContrastFg: color.NRGBA{R: 255, G: 255, B: 255, A: 255}}
		v := &View{window: w, recorder: r, directory: directory, material: m, results: make(chan string, 1)}
		v.filter.SingleLine = true
		v.list.Axis = layout.Vertical
		return v
	}}
}
func (v *View) Close() {
	if !v.recorder.AutoExporting() {
		v.recorder.Close()
	}
}
func (v *View) Theme(gtx layout.Context) *token.Theme {
	if v.theme == nil {
		v.theme = defaults.NewTheme(gtx, schemes.SchemeBaselineDark())
	}
	return v.theme
}
func (v *View) Update(gtx layout.Context) {
	for i := range v.tabs {
		if v.tabs[i].Clicked(gtx) {
			v.tab = i
			v.list.Position = layout.Position{}
		}
	}
	if v.pause.Clicked(gtx) {
		v.paused = !v.paused
		v.next = time.Time{}
	}
	if v.clear.Clicked(gtx) {
		v.recorder.Clear()
		v.snapshot = v.recorder.Snapshot()
		v.next = time.Time{}
	}
	kinds := []string{"snapshot", "cpu", "heap", "allocs", "trace", "goroutine"}
	for i, kind := range kinds {
		if v.captures[i].Clicked(gtx) {
			v.status = "Запись " + kind + "…"
			go func() {
				defer crash.Recover("profile capture", func(e *crash.Panic) {
					select {
					case v.results <- "Ошибка записи: " + e.Error():
					default:
					}
					v.window.Invalidate()
				})
				path, err := v.recorder.Capture(v.directory, kind)
				message := path
				if err != nil {
					message = "Ошибка записи: " + err.Error()
				}
				select {
				case v.results <- message:
				default:
				}
				v.window.Invalidate()
			}()
		}
	}
	select {
	case v.status = <-v.results:
	default:
	}
	if v.snapshot.At.IsZero() || !v.paused && !gtx.Now.Before(v.next) {
		v.snapshot = v.recorder.Snapshot()
		v.next = gtx.Now.Add(500 * time.Millisecond)
	}
}
func (v *View) text(gtx layout.Context, text string, size unit.Sp, mono bool) layout.Dimensions {
	l := material.Label(v.material, size, text)
	if mono {
		l.Font = font.Font{Typeface: "monospace"}
	}
	return l.Layout(gtx)
}
func (v *View) button(gtx layout.Context, c *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(v.material, c, text)
	b.TextSize = 12
	b.Inset = layout.Inset{Top: 7, Bottom: 7, Left: 10, Right: 10}
	return layout.UniformInset(3).Layout(gtx, b.Layout)
}
func (v *View) Layout(gtx layout.Context) {
	paint.Fill(gtx.Ops, v.material.Bg)
	if !v.paused {
		gtx.Execute(op.InvalidateCmd{At: v.next})
	}
	layout.UniformInset(14).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.text(gtx, "komarugram-go / Performance lab", 24, false)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.text(gtx, "Окно профилировщика не входит в FPS. Память и runtime-профили относятся ко всему процессу.", 12, false)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				label := "Заморозить вид"
				if v.paused {
					label = "Продолжить"
				}
				return layout.Flex{}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.button(gtx, &v.pause, label) }), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return v.button(gtx, &v.clear, "Сбросить метрики")
				}), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return v.button(gtx, &v.captures[0], "Экспорт JSON")
				}))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				labels := []string{"CPU · 15 с", "Heap", "Allocations", "Trace · 5 с", "Goroutines"}
				var children []layout.FlexChild
				for i, text := range labels {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.button(gtx, &v.captures[i+1], text) }))
				}
				return layout.Flex{}.Layout(gtx, children...)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if v.status == "" {
					return layout.Dimensions{}
				}
				l := material.Label(v.material, 12, v.status)
				l.State = &v.statusSelection
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				var tabs []layout.FlexChild
				for i, title := range []string{"Кадры", "Память", "Telegram RPC", "История"} {
					if i == v.tab {
						title = "● " + title
					}
					tabs = append(tabs, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.button(gtx, &v.tabs[i], title) }))
				}
				return layout.Flex{}.Layout(gtx, tabs...)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					e := material.Editor(v.material, &v.filter, "Фильтр: окно, аккаунт, RPC, событие…")
					e.TextSize = 14
					return layout.UniformInset(8).Layout(gtx, e.Layout)
				}), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.CheckBox(v.material, &v.errors, "Только ошибки RPC").Layout(gtx)
				}))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if v.tab != 0 {
					return layout.Dimensions{}
				}
				var frames []diagnostics.Frame
				names := v.windowNames()
				for _, name := range names {
					frames = append(frames, v.snapshot.Frames[name]...)
				}
				return graph(gtx, frames)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				rows := v.rows()
				query := strings.ToLower(strings.TrimSpace(v.filter.Text()))
				if query != "" {
					out := rows[:0]
					for _, row := range rows {
						if strings.Contains(strings.ToLower(row), query) {
							out = append(out, row)
						}
					}
					rows = out
				}
				return material.List(v.material, &v.list).Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
					return layout.Inset{Top: 5, Bottom: 5, Left: 6, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						if i%2 == 0 {
							paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 7}, clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, gtx.Sp(24))}.Op())
						}
						return v.text(gtx, rows[i], 12, true)
					})
				})
			}),
		)
	})
}
func (v *View) windowNames() []string {
	var names []string
	for name := range v.snapshot.Frames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
func bytes(n uint64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f KiB", float64(n)/1024)
}
func (v *View) rows() []string {
	var rows []string
	switch v.tab {
	case 0:
		rows = append(rows, "FPS = фактические кадры за 2 с (в покое 0). CPU/frame включает setup + update + layout + submit/wait.", "p50/p95/p99 — последние ≤600 кадров; >16.7 ms — бюджет 60 Hz. GPU отдельно не измеряется.")
		for _, name := range v.windowNames() {
			frames := v.snapshot.Frames[name]
			if len(frames) == 0 {
				continue
			}
			last := frames[len(frames)-1]
			durations := make([]time.Duration, len(frames))
			fps, slow := 0, 0
			var setup, update, lay, submit time.Duration
			phases := map[string]diagnostics.Phase{}
			for i, f := range frames {
				durations[i] = f.Total
				if v.snapshot.At.Sub(f.At) < 2*time.Second {
					fps++
				}
				if f.Total > time.Second/60 {
					slow++
				}
				setup += f.Setup
				update += f.Update
				lay += f.Layout
				submit += f.Submit
				for _, p := range f.Phases {
					a := phases[p.Name]
					a.Name = p.Name
					a.Calls += p.Calls
					a.Duration += p.Duration
					phases[p.Name] = a
				}
			}
			sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
			quant := func(q float64) float64 { return ms(durations[int(float64(len(durations)-1)*q)]) }
			n := time.Duration(len(frames))
			rows = append(rows, fmt.Sprintf("%s | %.1f FPS | last %.2f ms | p50 %.2f / p95 %.2f / p99 %.2f ms | slow %d/%d", name, float64(fps)/2, ms(last.Total), quant(.5), quant(.95), quant(.99), slow, len(frames)), fmt.Sprintf("  avg setup %.2f · update %.2f · layout %.2f · submit/wait %.2f ms", ms(setup/n), ms(update/n), ms(lay/n), ms(submit/n)), fmt.Sprintf("  viewport %dx%d · px/dp %.2f · px/sp %.2f", last.Width, last.Height, last.PxPerDp, last.PxPerSp))
			var ordered []diagnostics.Phase
			for _, p := range phases {
				ordered = append(ordered, p)
			}
			sort.Slice(ordered, func(i, j int) bool { return ordered[i].Duration > ordered[j].Duration })
			for _, p := range ordered {
				rows = append(rows, fmt.Sprintf("  %-27s %.3f ms/frame · %d calls (nested spans, не суммировать)", p.Name, ms(p.Duration/n), p.Calls))
			}
		}
	case 1:
		rows = append(rows, "Go heap: живые объекты по последней GC; Heap/Allocations выгружают стеки аллокаций для go tool pprof.", "Sys — память runtime, не RSS. Оценки кешей могут разделять изображения; не складывать их с heap.")
		if n := len(v.snapshot.Memory); n > 0 {
			m := v.snapshot.Memory[n-1]
			rows = append(rows, fmt.Sprintf("Heap alloc %s | in-use %s | objects %d | goroutines %d", bytes(m.HeapAlloc), bytes(m.HeapInuse), m.HeapObjects, m.Goroutines), fmt.Sprintf("Runtime sys %s | stack %s | idle %s | released %s", bytes(m.Sys), bytes(m.StackInuse), bytes(m.HeapIdle), bytes(m.HeapReleased)), fmt.Sprintf("GC cycles %d | next GC %s | pause total %.2f ms", m.GC, bytes(m.NextGC), float64(m.PauseTotal)/1e6))
			if n > 1 {
				prev := v.snapshot.Memory[n-2]
				seconds := m.At.Sub(prev.At).Seconds()
				if seconds > 0 {
					rows = append(rows, fmt.Sprintf("Allocation rate %.2f MiB/s | last interval GC pause %.3f ms", float64(m.TotalAlloc-prev.TotalAlloc)/seconds/(1<<20), float64(m.PauseTotal-prev.PauseTotal)/1e6))
				}
			}
		}
		for _, g := range v.snapshot.Gauges {
			size := "n/a"
			if g.Bytes > 0 {
				size = bytes(uint64(g.Bytes))
			}
			rows = append(rows, fmt.Sprintf("%-14s %-28s %8d entries  %s (estimate; %s ago)", g.Scope, g.Name, g.Entries, size, v.snapshot.At.Sub(g.At).Round(time.Second)))
		}
	case 2:
		rows = append(rows, "TL payload bytes: без MTProto framing/encryption, gzip и transport retries; response ? = decoder не вызывался.", fmt.Sprintf("In flight %d · completed ring %d/%d · overwritten %d; main + media DC + CDN application calls", v.snapshot.InFlight, len(v.snapshot.RPCs), diagnostics.RPCLimit, v.snapshot.DroppedRPCs))
		for _, r := range v.snapshot.Pending {
			if !v.errors.Value {
				rows = append(rows, fmt.Sprintf("#%d %s DC%d %-38s RUNNING %.0f ms", r.ID, r.Scope, r.DC, r.Method, ms(v.snapshot.At.Sub(r.At))))
			}
		}
		for i := len(v.snapshot.RPCs) - 1; i >= 0; i-- {
			r := v.snapshot.RPCs[i]
			if v.errors.Value && r.Error == "" {
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
			rows = append(rows, fmt.Sprintf("%s #%d %-12s DC%d %-36s %8.2f ms  ↑%d ↓%s B  encodes:%d  %s", r.At.Format("15:04:05.000"), r.ID, r.Scope, r.DC, r.Method, ms(r.Duration), r.RequestBytes, response, r.Encodes, status))
		}
	case 3:
		rows = append(rows, "Живая лента: layout calls включают измерительные проходы Gio; они могут быть больше видимых строк.", "Rebuild O(N), restore: линейный поиск ID; HeightIndex: Fenwick O(log N). Инструмент показывает текущую реализацию.")
		for _, h := range v.snapshot.Histories {
			rows = append(rows, fmt.Sprintf("%s chat:%d rev:%d | messages:%d visible:%d first:%d offset:%d | total:%d px", h.Window, h.Chat, h.Revision, h.Messages, h.Visible, h.First, h.Offset, h.TotalHeight), fmt.Sprintf("  rows laid out:%d heights changed:%d row cache:%d measurements:%d dirty:%d snapshot fresh:%t", h.RowsLaidOut, h.MeasurementsChanged, h.RowsCached, h.MeasurementsCached, h.Dirty, h.SnapshotFresh), fmt.Sprintf("  rebuild:%t %s %.2f ms hits:%d | restore:%s %.2f ms scanned:%d", h.Rebuilt, h.Reason, ms(h.RebuildTime), h.LayoutHits, h.Restore, ms(h.RestoreTime), h.RestoreScanned), "  "+h.Environment)
		}
		var counterNames []string
		for name := range v.snapshot.Counters {
			counterNames = append(counterNames, name)
		}
		sort.Strings(counterNames)
		for _, name := range counterNames {
			rows = append(rows, fmt.Sprintf("counter %-48s %d", name, v.snapshot.Counters[name]))
		}
		rows = append(rows, fmt.Sprintf("История событий (последние %d, вытеснено %d):", len(v.snapshot.Events), v.snapshot.DroppedEvents))
		for i := len(v.snapshot.Events) - 1; i >= 0; i-- {
			e := v.snapshot.Events[i]
			rows = append(rows, fmt.Sprintf("%s %-12s %-25s %8.2f ms n:%d %s", e.At.Format("15:04:05.000"), e.Scope, e.Name, ms(e.Duration), e.Count, e.Detail))
		}
	}
	if len(rows) < 3 {
		rows = append(rows, "Откройте чат и прокрутите историю в основном окне. В -demo сетевых RPC нет.")
	}
	return rows
}
func graph(gtx layout.Context, frames []diagnostics.Frame) layout.Dimensions {
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(80))
	paint.FillShape(gtx.Ops, color.NRGBA{R: 12, G: 17, B: 24, A: 255}, clip.Rect{Max: size}.Op())
	sort.Slice(frames, func(i, j int) bool { return frames[i].At.Before(frames[j].At) })
	if len(frames) > 180 {
		frames = frames[len(frames)-180:]
	}
	for i, f := range frames {
		x := i * size.X / max(1, len(frames))
		next := (i + 1) * size.X / max(1, len(frames))
		height := min(size.Y, int(ms(f.Total)/50*float64(size.Y)))
		col := color.NRGBA{R: 70, G: 178, B: 173, A: 255}
		if f.Total > time.Second/60 {
			col = color.NRGBA{R: 232, G: 137, B: 90, A: 255}
		}
		paint.FillShape(gtx.Ops, col, clip.Rect(image.Rect(x, size.Y-height, max(x+1, next-1), size.Y)).Op())
	}
	y := size.Y - int(16.667/50*float64(size.Y))
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 100}, clip.Rect(image.Rect(0, y, size.X, y+1)).Op())
	return layout.Dimensions{Size: size}
}

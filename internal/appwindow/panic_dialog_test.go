package appwindow

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/crash"
)

// TestPanicDialogRendersWithLongReport draws the dialog of a long panic
// with a stack longer than its box, in the theme the host was told, and
// checks its background is the theme's; PANIC_PNG_DIR saves both themes to
// look at, and a panic the client had.
func TestPanicDialogRendersWithLongReport(t *testing.T) {
	size := image.Pt(640, 440)
	window, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer window.Release()
	stack := "goroutine 1 [running]:\n"
	for i := range 40 {
		stack += fmt.Sprintf("komarugram/internal/messenger/ui.(*historyView).layoutBubble(0xc000%04x, {0x1, 0x2})\n\tC:/Users/dev/komarugram-go/internal/messenger/ui/history_bubble.go:%d +0x1a4\n", i, 100+i)
	}
	long := &crash.Panic{Where: "window account", Value: strings.Repeat("unexpected state ", 30), Stack: []byte(stack)}
	for _, c := range []struct {
		name   string
		theme  Theme
		p      *crash.Panic
		status string
	}{
		{"light", ThemeLight, long, ""},
		{"dark", ThemeDark, long, ""},
		// A panic the client had, with the longest status.
		{"checkbox", ThemeLight, checkboxPanic(t), "window.issue_paste"},
	} {
		name, theme := c.name, c.theme
		host := new(Host)
		host.SetTheme(theme)
		dialog := newPanicDialog(&Window{Window: new(app.Window), host: host}, c.p)
		if c.status != "" {
			dialog.status = texts().T(c.status)
		}
		ops := new(op.Ops)
		gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		th := dialog.Theme(gtx)
		wdk.InitMaterialThemeInContext(gtx, th)
		dialog.Update(gtx)
		dialog.Layout(gtx)
		if err := window.Frame(ops); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rectangle{Max: size})
		if err := window.Screenshot(img); err != nil {
			t.Fatal(err)
		}
		if got, want := color.NRGBAModel.Convert(img.At(4, 4)), th.Scheme.SurfaceContainerLowest.AsNRGBA(); got != want {
			t.Errorf("%s: the background is %v, want %v", name, got, want)
		}
		if dark := th.Scheme.SurfaceContainerLowest.AsNRGBA().R < 0x80; dark != (theme == ThemeDark) {
			t.Errorf("%s: the dialog took the other theme", name)
		}
		if dir := os.Getenv("PANIC_PNG_DIR"); dir != "" {
			out, err := os.Create(filepath.Join(dir, "panic-"+name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(out, img); err != nil {
				t.Fatal(err)
			}
			out.Close()
		}
	}
}

func TestPanicDialogDoesNotRepeatAfterIgnore(t *testing.T) {
	h := new(Host)
	if !h.beginPanicNotice("window") || h.beginPanicNotice("window") {
		t.Fatal("duplicate panic dialog opened")
	}
	h.ignorePanicNotice("window")
	if h.beginPanicNotice("window") {
		t.Fatal("ignored panic dialog reopened")
	}
	if !h.beginPanicNotice("another") {
		t.Fatal("different panic was ignored")
	}
}

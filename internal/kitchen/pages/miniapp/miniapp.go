// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"komarugram/internal/miniappprefs"
	"komarugram/pkg/miniapp"

	"gio-mw/exp"
	"gio-mw/exp/examples"
	"gio-mw/exp/router"
	"gio-mw/token"
	"gio-mw/wdk/block"
	"gio-mw/widget/button"

	"gioui.org/layout"
	"gioui.org/op"
)

// shownEvents is how many of the most recent events the log keeps on screen.
const shownEvents = 10

// demoApp and demoAccount are the identities the demo launches under. A real
// client would pass the bot and the logged-in account.
const (
	demoApp     = "kitchen_demo_bot"
	demoAccount = "kitchen"
)

type Page struct {
	demo   *miniapp.Demo
	bridge *miniapp.Bridge
	err    error
	dark   bool

	// The privacy settings are shared with the messenger's settings; the
	// running app keeps the profile it was opened with, the choice is what
	// the next launch uses.
	privacy     *miniappprefs.Settings
	privacyView *miniappprefs.View

	open      *button.Button
	theme     *button.Button
	mainClick *button.Button
	backClick *button.Button
	viewport  *button.Button
	closeApp  *button.Button
}

func NewPage() router.PageWidget {
	page := &Page{
		open:      button.Filled(),
		theme:     button.Text(),
		mainClick: button.Text(),
		backClick: button.Text(),
		viewport:  button.Text(),
		closeApp:  button.Text(),
	}
	page.privacy = miniappprefs.New(miniapp.Ephemeral)
	strings := miniappprefs.English
	strings.Title = "Storage" // The page is titled Mini Apps already.
	page.privacyView = miniappprefs.NewView(page.privacy, strings)
	page.privacyView.TitleStyle = token.TypestyleTitleMedium
	return page
}

func (p *Page) IsWide() bool {
	return true
}

func (p *Page) Update(gtx layout.Context) {
	ctx := context.Background()

	p.privacyView.Update(gtx)

	if p.open.Clicked(gtx) {
		p.launch(ctx)
	}
	if p.bridge == nil {
		return
	}
	if p.theme.Clicked(gtx) {
		p.dark = !p.dark
		theme := miniapp.LightTheme
		if p.dark {
			theme = miniapp.DarkTheme
		}
		p.fail(p.bridge.Send(ctx, "theme_changed", theme))
	}
	if p.mainClick.Clicked(gtx) {
		p.fail(p.bridge.Send(ctx, "main_button_pressed", "null"))
	}
	if p.backClick.Clicked(gtx) {
		p.fail(p.bridge.Send(ctx, "back_button_pressed", "null"))
	}
	if p.viewport.Clicked(gtx) {
		p.fail(p.bridge.Send(ctx, "viewport_changed",
			`{height:720,is_expanded:true,is_state_stable:true}`))
	}
	if p.closeApp.Clicked(gtx) {
		p.shutdown()
	}
}

// launch serves the bundled Mini App and opens it in the user's browser.
func (p *Page) launch(ctx context.Context) {
	p.shutdown()
	p.err = nil

	demo, err := miniapp.ServeDemo()
	if err != nil {
		p.err = err
		return
	}
	bridge, err := miniapp.Open(ctx, demo.URL, demo.Params, miniapp.Profile{
		Storage: p.privacy.Storage(),
		App:     demoApp,
		Account: demoAccount,
	})
	if err != nil {
		p.err = err
		_ = demo.Close()
		return
	}
	p.demo, p.bridge, p.dark = demo, bridge, false
}

func (p *Page) shutdown() {
	if p.bridge != nil {
		p.bridge.Close()
		p.bridge = nil
	}
	if p.demo != nil {
		_ = p.demo.Close()
		p.demo = nil
	}
}

func (p *Page) fail(err error) {
	if err != nil {
		p.err = err
	}
}

func (p *Page) View(gtx layout.Context) layout.Dimensions {
	return block.UniformPadding(examples.SpacingMedium).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return block.Line{
			Axis:     block.AxisVertical,
			Overflow: block.OverflowClip,
		}.Layout(gtx,
			block.NewSegment(func(gtx layout.Context) layout.Dimensions {
				txt := "Mini Apps"
				return exp.HeadlineL(gtx, txt)
			}),
			block.NewSegment(func(gtx layout.Context) layout.Dimensions {
				txt := "Telegram's own SDK, running in the user's browser, bridged over CDP or WebDriver BiDi"
				return exp.BodyL(gtx, txt)
			}),
			block.NewVerticalSpacer(examples.SpacingSmall),
			block.NewSegment(p.sectionStatus),
			block.NewVerticalSpacer(examples.SpacingSmall),
			block.NewSegment(p.sectionStorage),
			block.NewVerticalSpacer(examples.SpacingSmall),
			block.NewSegment(p.sectionControls),
			block.NewVerticalSpacer(examples.SpacingSmall),
			block.NewSegment(p.sectionLog),
		)
	})
}

func (p *Page) sectionStatus(gtx layout.Context) layout.Dimensions {
	return exp.BodyL(gtx, p.statusText(gtx))
}

func (p *Page) statusText(gtx layout.Context) string {
	switch {
	case !miniapp.Available():
		if choice := os.Getenv(miniapp.BrowserEnv); choice != "" {
			return miniapp.BrowserEnv + " names " + choice + ", which was not found"
		}
		return "no Chromium-based browser or Firefox found"
	case p.err != nil:
		return "error: " + p.err.Error()
	case p.bridge == nil:
		browser := filepath.Base(miniapp.Browser())
		if version := miniapp.BrowserVersion(); version != "" {
			browser += " (" + version + ")"
		}
		return "no Mini App running · will use " + browser
	case !p.bridge.Running():
		p.shutdown()
		return "the Mini App window was closed"
	}
	if err := p.bridge.Err(); err != nil {
		p.shutdown()
		return "bridge lost: " + err.Error()
	}
	// Events arrive on the bridge's own goroutine; keep repainting to show them.
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(150 * time.Millisecond)})
	return fmt.Sprintf("running · %d events received from the app", len(p.bridge.Events()))
}

// sectionStorage picks what the next Mini App is allowed to keep between
// launches. Official clients have no such choice — they store per account —
// but a client written from scratch can also give each app a profile of its
// own, or none at all.
func (p *Page) sectionStorage(gtx layout.Context) layout.Dimensions {
	return p.privacyView.Layout(gtx)
}

func (p *Page) sectionControls(gtx layout.Context) layout.Dimensions {
	running := p.bridge != nil
	return block.Line{
		Axis:     block.AxisHorizontal,
		Overflow: block.OverflowWrap,
		Expand:   true,
	}.Layout(gtx,
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			if !miniapp.Available() {
				gtx = gtx.Disabled()
			}
			return p.open.Layout(gtx, "Open Mini App")
		}),
		block.NewHorizontalSpacer(examples.SpacingSmall),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			if !running {
				gtx = gtx.Disabled()
			}
			return p.theme.Layout(gtx, "Switch theme")
		}),
		block.NewHorizontalSpacer(examples.SpacingSmall),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			if !running {
				gtx = gtx.Disabled()
			}
			return p.mainClick.Layout(gtx, "Press MainButton")
		}),
		block.NewHorizontalSpacer(examples.SpacingSmall),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			if !running {
				gtx = gtx.Disabled()
			}
			return p.backClick.Layout(gtx, "Press BackButton")
		}),
		block.NewHorizontalSpacer(examples.SpacingSmall),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			if !running {
				gtx = gtx.Disabled()
			}
			return p.viewport.Layout(gtx, "Resize viewport")
		}),
		block.NewHorizontalSpacer(examples.SpacingSmall),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			if !running {
				gtx = gtx.Disabled()
			}
			return p.closeApp.Layout(gtx, "Close")
		}),
	)
}

// sectionLog shows what the Mini App asked the client to do.
func (p *Page) sectionLog(gtx layout.Context) layout.Dimensions {
	if p.bridge == nil {
		return layout.Dimensions{}
	}
	events := p.bridge.Events()
	if len(events) > shownEvents {
		events = events[len(events)-shownEvents:]
	}

	segments := make([]block.Segment, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		segments = append(segments, block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			data := event.Data
			if len(data) > 64 {
				data = data[:64] + "…"
			}
			line := event.Type
			if data != "" && data != `""` {
				line += "  " + data
			}
			return exp.BodyS(gtx, line)
		}))
	}
	return block.Line{Axis: block.AxisVertical, Overflow: block.OverflowClip}.Layout(gtx, segments...)
}

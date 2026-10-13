// SPDX-License-Identifier: Unlicense OR MIT

package motion

import (
	"fmt"
	"slices"

	"gio-mw/exp"
	"gio-mw/exp/powersave"
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"gio-mw/widget/radio"
	"gio-mw/widget/slider"

	"gioui.org/layout"
	"gioui.org/unit"
)

// Strings are the texts of View; the application words them.
type Strings struct {
	Title      string
	Modes      map[powersave.Mode]string
	LowBattery string // Format with the threshold in percent.
	Never      string // Shown instead of the threshold when it is off.
	// Status formats the detected state: power saver, battery, system
	// animations, outcome.
	Status    string
	On, Off   string
	NoBattery string
	Battery   string // Format with the percent and Plugged or Unplugged.
	Plugged   string
	Unplugged string
}

// lowBatteryOptions are the thresholds offered, in percent.
var lowBatteryOptions = []int{0, 5, 10, 15, 20, 25, 30, 40, 50}

// View is the animation settings UI for a Settings.
type View struct {
	// TitleStyle is the style of the title, TypestyleTitleLarge by default.
	TitleStyle token.Typestyle

	settings   *Settings
	strings    Strings
	mode       *radio.Radios[powersave.Mode]
	lowBattery *slider.Slider
}

func NewView(settings *Settings, strings Strings) *View {
	v := &View{settings: settings, strings: strings}
	modes := []powersave.Mode{powersave.ModeAuto, powersave.ModeOn, powersave.ModeOff}
	v.mode = radio.NewRadios(modes, settings.Mode(), settings.SetMode)
	threshold := settings.LowBattery()
	if !slices.Contains(lowBatteryOptions, threshold) {
		threshold = powersave.DefaultLowBattery
	}
	v.lowBattery = slider.StandardSlider(lowBatteryOptions, threshold, settings.SetLowBattery)
	return v
}

// SetStrings switches the labels without replacing widget state.
func (v *View) SetStrings(strings Strings) { v.strings = strings }

func (v *View) Update(gtx layout.Context) {
	// Another window may have changed the shared preference.
	v.mode.SetValue(v.settings.Mode())
	v.lowBattery.SetValue(v.settings.LowBattery())
	v.mode.Update(gtx)
}

func (v *View) Layout(gtx layout.Context) layout.Dimensions {
	return block.Line{
		Axis:     block.AxisVertical,
		Overflow: block.OverflowClip,
	}.Layout(gtx,
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			style := v.TitleStyle
			if style == token.TypestyleDefault {
				style = token.TypestyleTitleLarge
			}
			return wdk.LayoutLabel(gtx, wdk.LabelStyle{Typestyle: style, Color: exp.GetSurfaceTheme(gtx).OnColor}, v.strings.Title)
		}),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return v.mode.Layout(gtx, radio.LeadingKind, v.strings.Modes)
		}),
		block.NewVerticalSpacer(unit.Dp(8)),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			txt := v.strings.Never
			if threshold := v.lowBattery.GetValue(); threshold > 0 {
				txt = fmt.Sprintf(v.strings.LowBattery, threshold)
			}
			return exp.BodyL(gtx, txt)
		}),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(360))
			return v.lowBattery.Layout(gtx)
		}),
		block.NewVerticalSpacer(unit.Dp(8)),
		block.NewSegment(v.layoutStatus),
	)
}

// layoutStatus describes the detected system state and the outcome.
func (v *View) layoutStatus(gtx layout.Context) layout.Dimensions {
	s := v.strings
	state := v.settings.SystemState()
	onOff := func(b bool) string {
		if b {
			return s.On
		}
		return s.Off
	}
	battery := s.NoBattery
	if state.BatteryPercent >= 0 {
		source := s.Plugged
		if state.OnBattery {
			source = s.Unplugged
		}
		battery = fmt.Sprintf(s.Battery, state.BatteryPercent, source)
	}
	txt := fmt.Sprintf(s.Status, onOff(state.PowerSaver), battery, onOff(!state.ReduceMotion), onOff(v.settings.AnimationsEnabled()))
	return exp.BodyM(gtx, txt)
}

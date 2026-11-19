// SPDX-License-Identifier: Unlicense OR MIT

// Package motion decides whether the application animates: it combines the
// user's preference with the system power state watched by
// gio-mw/exp/powersave, and provides the settings UI for it.
package motion

import (
	"sync"

	"gio-mw/exp/powersave"
	"gio-mw/wdk"

	"gioui.org/layout"
)

// Settings holds the user's animation preference and the watched system
// power state. It is safe for concurrent use.
type Settings struct {
	mu         sync.Mutex
	policy     powersave.Policy
	disabled   wdk.AnimationKind
	monitor    *powersave.Monitor
	invalidate func()
	changed    func(powersave.Mode, int)
}

// ApplyPreferences synchronizes saved preferences without writing them back.
func (m *Settings) ApplyPreferences(mode powersave.Mode, lowBattery int, disabled wdk.AnimationKind) {
	policy := powersave.Policy{Mode: mode, LowBattery: float64(lowBattery)}
	m.mu.Lock()
	changed := m.policy != policy || m.disabled != disabled
	m.policy, m.disabled = policy, disabled
	m.mu.Unlock()
	if changed {
		m.invalidate()
	}
}

// Frame publishes the window's animation policy to the widgets in this frame.
func (m *Settings) Frame(gtx layout.Context) {
	m.mu.Lock()
	policy, disabled := m.policy, m.disabled
	m.mu.Unlock()
	wdk.SetAnimationsEnabled(gtx, policy.AnimationsEnabled(m.monitor.State()))
	wdk.SetDisabledAnimations(gtx, disabled)
}

// New starts watching the system; invalidate is called when the decision may
// have changed, so the window can redraw.
func New(invalidate func()) *Settings {
	m := &Settings{
		policy:     powersave.Policy{Mode: powersave.ModeAuto, LowBattery: powersave.DefaultLowBattery},
		invalidate: invalidate,
	}
	m.monitor = powersave.Start(func(powersave.State) { invalidate() })
	return m
}

func (m *Settings) Mode() powersave.Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policy.Mode
}

func (m *Settings) SetMode(mode powersave.Mode) {
	m.mu.Lock()
	if m.policy.Mode == mode {
		m.mu.Unlock()
		return
	}
	m.policy.Mode = mode
	changed := m.changed
	lowBattery := int(m.policy.LowBattery)
	m.mu.Unlock()
	m.invalidate()
	if changed != nil {
		changed(mode, lowBattery)
	}
}

// LowBattery returns the battery charge in percent at or below which Auto
// mode turns animations off; 0 disables that check.
func (m *Settings) LowBattery() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int(m.policy.LowBattery)
}

func (m *Settings) SetLowBattery(percent int) {
	m.mu.Lock()
	if int(m.policy.LowBattery) == percent {
		m.mu.Unlock()
		return
	}
	m.policy.LowBattery = float64(percent)
	changed := m.changed
	mode := m.policy.Mode
	m.mu.Unlock()
	m.invalidate()
	if changed != nil {
		changed(mode, percent)
	}
}

// SetPreferenceChanged installs the callback used to persist user changes.
// Applying the same value is a no-op, which makes synchronization between
// several window-local Settings safe.
func (m *Settings) SetPreferenceChanged(changed func(powersave.Mode, int)) {
	m.mu.Lock()
	m.changed = changed
	m.mu.Unlock()
}

// SystemState returns the watched system power state.
func (m *Settings) SystemState() powersave.State {
	return m.monitor.State()
}

// AnimationsEnabled reports whether widgets should animate now.
func (m *Settings) AnimationsEnabled() bool {
	return m.AnimationEnabled(0)
}

func (m *Settings) AnimationEnabled(kind wdk.AnimationKind) bool {
	m.mu.Lock()
	policy, disabled := m.policy, m.disabled
	m.mu.Unlock()
	return disabled&kind == 0 && policy.AnimationsEnabled(m.monitor.State())
}

func (m *Settings) Close() {
	m.monitor.Close()
}

# Local gio-mw changes

Base: [gio-mw](https://git.sr.ht/~schnwalter/gio-mw) at `be0445f`
("add update instructions", 2026-02-21), the pseudo-version
`v0.0.0-20260221053317-be0445f63b48` that the root go.mod still names; the
root go.mod selects this copy with a `replace`.

The fork came into the repository with its changes already made, and they
were not written down then. This list was made on 2026-10-13 by comparing
the copy with that commit; later changes are added as they are made.

To compare again: clone the repository, check out `be0445f`, replace
`git.sr.ht/~schnwalter/gio-mw` with `gio-mw` in its Go files, and
`diff -r` it with this directory.

## Module

- The module is `gio-mw`, not `git.sr.ht/~schnwalter/gio-mw`: every import,
  `go.mod` and the README's update command. Most files that differ from the
  base differ only in this (`defaults/schemes`, `cmd/genscheme`, the themes
  of the widgets, most examples).
- `examples/kitchen/go.mod` carves the kitchen out of the module, so that
  `gio-mw/examples/kitchen` is not ambiguous. The messenger's kitchen is
  `internal/kitchen`, built on it.
- `internal/examples` and `internal/router` moved to `exp/examples` and
  `exp/router`, for the kitchen outside the module to import them.
- `github.com/godbus/dbus/v5` is required, by `exp/appearance` and
  `exp/powersave`.

## Motion

Upstream animated only an indicator's progress (with the easing functions
of `wdk/easing.go`, still there); widgets here
animate as Material 3 describes, and every animation can be turned off.

- `token/motion.go`: `Easing`, a cubic Bézier like CSS `cubic-bezier()`,
  with `Ease`, and the Material 3 easing tokens (`EasingStandard`,
  `EasingEmphasizedDecelerate`…). `token/motion_test.go`.
- `token/color.go`: `LerpColor` mixes two colors with premultiplied alpha,
  so a fade to or from transparent keeps the visible color's hue.
- `wdk/animation.go`: `FloatTween` and `ColorTween` animate a value towards
  a target given every frame, from wherever it is when the target changes.
  The zero value starts at the first target; without a clock (`gtx.Now`
  zero, as in tests) or with animations off they jump to the target.
  `wdk/animation_test.go`.
- `wdk/motion.go`: `SetAnimationsEnabled` / `AnimationsEnabled` turn
  animations off for a frame through `gtx.Values`. The messenger sets it
  every frame from `internal/motion` (the user's choice, power saving).
- `wdk/ripple.go`: `Ripple`, the press ripple of Compose's Material 3
  (expands in 225 ms, fades in 75 ms and out 150 ms after the release),
  drawn from the presses of a `widget.Clickable`'s `History`. It replaces
  the pressed state layer, which widgets draw only when `RipplesEnabled` is
  false. `Clip` is a function, called only while a ripple is visible: a
  shape made for every widget in every frame made most of the operations of
  a list's row (330 of 382 bytes; 14% of a frame of the chat list).
  `wdk/ripple_test.go`. The messenger's `surface` draws it too.
- `wdk/shaped.go`: `CornerShapes.Resolve` turns corners adapted to the size
  into sizes, and `LerpCornerShapes` interpolates two shapes, for shapes
  that morph (a pressed button).
- `wdk/elevation.go`: `DrawShadow` and `ShadowSize` take the shadow apart
  from `Elevation`, for a shadow between two levels.
- Widgets: each keeps its tweens in an `animation` struct, one per widget,
  or per option for `checkbox`, `radio` and `toggle` (a map by value, kept
  as long as the clickables are).
  - `button`: the container's shape morphs to the pressed one; shadow,
    container, outline, state layer, label and icon colors animate; ripple.
  - `checkbox`: the check mark is drawn along its length and morphs into the
    dash of the indeterminate state; the outline fades as the container
    fills; ripple. The whole row, label included, is clickable.
  - `radio`: the dot grows from the center; ripple; the whole row is
    clickable.
  - `toggle`: the handle slides and changes size, the check icon fades, the
    state layer follows the handle; ripple. The graphic is drawn outside the
    clickable, whose clip cut the state layer.
  - `rail`: the colors of the indicator, the label and the state layer
    animate; ripple. `rail_theme.go` adds
    the hover and press opacities of an item's state layer.
  - `tab`: the active indicator slides between tabs in one row and grows
    from the center when they wrap; label colors animate; ripple. The widths
    of the tabs are kept in the `Group`, cleared every frame, not a map made
    every frame.
  - `input`: the label floats up and scales between its resting and its
    floating place; the caret appears once the label is out of its way
    (`wdk.EditorStyle.HideCaret` hides the caret of an empty editor).
  - `overlay`: items animate in and out (side items slide from their edge,
    centered ones grow and fade, top and bottom ones slide a little and
    fade); `ClearItem` closes an item, which is removed once it has animated
    out (at once with animations off). `CloseOnScrim` and `Closed` are new.
    The top item is the last one not closing.
  - `indicator`: progress animates with a `FloatTween`;
    `CircularIndeterminate` (`indeterminate.go`) is the rotating spinner,
    where upstream had a TODO. `indeterminate_test.go`.
  - `slider`: the handle slides between the stops (a `FloatTween` of the
    stop's index, fractional on the way, which the tracks, the stops and the
    handle are drawn from) instead of jumping, whether a drag, a click or
    `SetValue` moved it; the handle narrows and widens as it is pressed and
    released. `slider_test.go`.

## Surfaces

- `exp/surface_theme.go`, `exp/surface_variants.go`: the stack of surface
  themes lives in `gtx.Values` (`SurfaceNamespace`), one per window and
  frame; upstream it was one for the whole program, which windows drawn
  from their own goroutines pushed and popped at once. `GetSurfaceTheme`
  and `NewSurfaceTheme` take `gtx`, the background surface resets the stack
  if a frame draws it twice, and with an empty stack `GetSurfaceTheme`
  returns the material theme's background instead of panicking.
  `exp/surface_theme_test.go`.

## Text and icons

- `defaults/fonts.go`, `defaults/theme.go`: `SetFonts` gives every theme
  made after it the program's own fonts (the user's font files, the emoji
  font, emoji drawn as pictures by `text.EmojiImages`, a Gio fork change),
  tried before the system's families. With an emoji font of its own the
  families no longer name Noto Color Emoji, whose digits would otherwise be
  drawn for all text on a system without the text fonts named before it.
- `wdk/require.go`: `RequireIconWidget` keeps the four colors and sizes an
  icon was drawn in last, each in its own `widget.Icon`, which keeps only
  the picture of the last one: an icon drawn in two colors in a frame (the
  kind of a chat, in the selected row and the others) was rasterized again
  twice a frame. Safe for several windows. `wdk/require_test.go`.
- `widget/search`: the hint stays until something is typed, focused or
  not, and the editor is centered on the hint's line.

## New packages

- `exp/appearance`: watches the system's light or dark scheme (Linux: the
  XDG desktop portal, GNOME, XFCE's and GNOME's theme names over D-Bus;
  Windows and macOS: polled). `appearance_test.go`.
- `exp/powersave`: watches power saving, the battery and the system's
  animation setting (Linux: power-profiles-daemon, UPower, the portal's
  GNOME and KDE settings over D-Bus; Windows: polled), with the `Policy`
  of the Auto, On and Off modes. `powersave_test.go`.
- `widget/scroll`: a list with smooth wheel scrolling, kinetic scrolling
  after a touchpad and an overlay scrollbar, as Chromium and Telegram
  Desktop have; horizontal lists scroll with the vertical wheel. How far
  each device scrolls is in `docs/SCROLLING.md`. `scroll_test.go`,
  `drag_test.go`.

## Known

- `wdk/block`'s `TestPadding_layout_LTR` fails, as it does at the base
  commit: `wdk/block` is not changed here.

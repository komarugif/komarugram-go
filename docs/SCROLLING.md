# Scrolling: how far a touchpad and a trackpoint go

Notes for improving scrolling, after a trackpoint user found it slower than
in GTK and Qt programs even with `scroll.ContinuousScale` at 5 (commit
`975cd31`). What other toolkits do was read from their sources, not
measured; nothing here has been changed in the code yet.

## Where it stands

- `third_party/gio-mw/widget/scroll/scroll.go`: `scroll.Pixels` turns a
  `pointer.Event` into pixels. A wheel's notch is multiplied by
  `WheelScale`; a touchpad's scrolling, and the kinetic scrolling after it,
  by `TouchpadScale` (2.5 on Wayland); a trackpoint's
  (`pointer.Event.Continuous`, Wayland only) by `ContinuousScale`, twice
  `TouchpadScale`, a factor chosen, not measured.
- `komarugif/gio/app/os_wayland.go`: `gio_onPointerAxis` passes the axis
  values of `wl_pointer.axis` on as they are, in surface units.
  `gio_onPointerAxisSource` sets `Continuous` for the `continuous` source,
  which libinput reports for a trackpoint's scrolling and a mouse's with a
  button held. After `axis_stop` Gio flings, for both a touchpad and a
  trackpoint.
- `-scroll-log` writes every scroll event the lists receive, with the
  scales, `wheel` and `continuous`.

## What other programs do on Wayland

Pixels for each unit of a `wl_pointer.axis` value, in logical pixels:

| Program | Touchpad (`finger`) | Trackpoint (`continuous`) | Kinetic scrolling after a trackpoint |
|---|---|---|---|
| GTK 4 | ×2.5 | ×2.5, the same | yes |
| GTK 3 | ×min(page^(2/3), page/2) / 10: ×7.1 for a list 600 px high, ×8.6 at 800, ×10 at 1000 | the same | yes |
| Qt Widgets | ×0.3 × the step of a line: ×6 at a 20 px step | the same | no |
| Telegram Desktop | ×2.5 × its interface scale | the same | no |
| KomaruGram | ×2.5 | ×5 | yes (Gio's fling) |

None of them scrolls a trackpoint differently from a touchpad.

### GTK 4

- `gdk/wayland/gdkseat-wayland.c`: the axis values become a smooth scroll
  event as they are, in `GDK_SCROLL_UNIT_SURFACE`
  (`flush_smooth_scroll_event`). `get_scroll_device` gives the `continuous`
  source a device of its own, `GDK_SOURCE_TRACKPOINT`.
- `gtk/gtkscrolledwindow.c`: a delta in `GDK_SCROLL_UNIT_SURFACE` is
  multiplied by `MAGIC_SCROLL_FACTOR`, 2.5, whatever the source; a wheel's
  by `pow(page_size, 2.0 / 3.0)`. The source matters only for the
  scrollbars' indicators.
- `gtk/gtkeventcontrollerscroll.c`: with `GTK_EVENT_CONTROLLER_SCROLL_KINETIC`
  a smooth scroll that ends with a stop event emits `decelerate`, with the
  velocity of the last events, for a trackpoint as for a touchpad.

Sources: [gdkseat-wayland.c](https://gitlab.gnome.org/GNOME/gtk/-/blob/main/gdk/wayland/gdkseat-wayland.c),
[gtkscrolledwindow.c](https://gitlab.gnome.org/GNOME/gtk/-/blob/main/gtk/gtkscrolledwindow.c),
[gtkeventcontrollerscroll.c](https://gitlab.gnome.org/GNOME/gtk/-/blob/main/gtk/gtkeventcontrollerscroll.c).

### GTK 3

- `gdk/wayland/gdkdevice-wayland.c`: the axis values are divided by 10
  (`pointer_handle_axis`); `continuous` is `GDK_SOURCE_TRACKPOINT` here too.
- `gtk/gtkscrolledwindow.c`: every smooth delta, a touchpad's or a
  trackpoint's, is multiplied by `get_scroll_unit`,
  `MIN(pow(page_size, 2.0 / 3.0), page_size / 2.0)`: the higher the list,
  the faster it scrolls. A stop event starts kinetic scrolling.
- Firefox and the programs of XFCE and MATE are GTK 3: these are likely
  the "GTK programs" a user compares with.

Sources: [gdkdevice-wayland.c](https://gitlab.gnome.org/GNOME/gtk/-/blob/gtk-3-24/gdk/wayland/gdkdevice-wayland.c),
[gtkscrolledwindow.c](https://gitlab.gnome.org/GNOME/gtk/-/blob/gtk-3-24/gtk/gtkscrolledwindow.c).

### Qt

- `src/plugins/platforms/wayland/qwaylandinputdevice.cpp` (in qtbase now):
  a wheel event carries `pixelDelta`, the axis value as it is, and
  `angleDelta`, the value × 12 when there are no discrete steps (the code
  says `//TODO: why multiply by 12?`). `finger` and `continuous` alike are
  `Qt::MouseEventSynthesizedBySystem`.
- `src/widgets/widgets/qabstractslider.cpp`: Qt Widgets read `angleDelta`
  only: `angleDelta / 120 × wheelScrollLines() (3) × singleStep`, without
  kinetic scrolling. The step of a line depends on the widget.
- When the compositor has pointer gestures (KWin, Mutter and wlroots do),
  the plugin registers a `touchpad` device with `PixelScroll`; with no
  mouse device registered, `QPointingDevice::primaryPointingDevice` gives
  that one, and wheel events carry it. Read from `qwaylandinputdevice.cpp`,
  `qwaylandwindow.cpp` and `qpointingdevice.cpp`, not tried.

Sources: [qwaylandinputdevice.cpp](https://github.com/qt/qtbase/blob/dev/src/plugins/platforms/wayland/qwaylandinputdevice.cpp),
[qwaylandwindow.cpp](https://github.com/qt/qtbase/blob/dev/src/plugins/platforms/wayland/qwaylandwindow.cpp),
[qpointingdevice.cpp](https://github.com/qt/qtbase/blob/dev/src/gui/kernel/qpointingdevice.cpp),
[qabstractslider.cpp](https://github.com/qt/qtbase/blob/dev/src/widgets/widgets/qabstractslider.cpp).

### Telegram Desktop

- `lib_ui/ui/ui_utility.cpp`, `ScrollDeltaF`: on Wayland, when the device
  has `PixelScroll`, `pixelDelta × kMagicScrollMultiplier` (2.5), through
  `style::ConvertScaleExact`, the interface scale set in Telegram Desktop;
  otherwise `angleDelta × wheelScrollLines() / (kPixelToAngleDelta ×
  kDefaultWheelScrollLines)`, ×6 of the axis value. Which one applies
  depends on the device Qt gives (see Qt above).
- No kinetic scrolling for a touchpad or a trackpoint.

Source: [ui_utility.cpp](https://github.com/desktop-app/lib_ui/blob/master/ui/ui_utility.cpp);
also `tdesktop/Telegram/lib_ui` when cloned.

## Gio does not scale scrolling by the display's scale on Wayland

`gio_onPointerAxis` takes `fromFixed(value)` as it is, in surface units,
while the pointer's position is multiplied by the buffer scale
(`fromFixed(x) * float32(w.scale)`). Lists work in physical pixels: with a
scale of 2, a touchpad, a trackpoint and a wheel all scroll half as far as
in GTK and Qt, which work in logical pixels. Gio on macOS multiplies by
the scale (`os_macos.go`, `gio_onMouse`); upstream Gio's Wayland backend
does not, as the fork
([os_wayland.go](https://git.sr.ht/~eliasnaur/gio/blob/main/app/os_wayland.go)).

If the user's display is scaled, this, not the factors, may be what is
left of the difference: `ContinuousScale` of 5 at a scale of 2 is 2.5
logical pixels, GTK 4's factor.

## Plan

1. **Ask the user**: the display's scale, the compositor, and the programs
   compared (GTK 3 or GTK 4, Qt Widgets or QML), with a `-scroll-log` of a
   trackpoint's scrolling.
2. **Write the window's scale into the `-scroll-log` header**: it is not
   there, and a log cannot tell it now.
3. **Scale the axis values by the buffer scale on Wayland**, in the fork
   (`gio_onPointerAxis`, and the discrete steps' distance in `flushScroll`),
   as Gio does on macOS; record it in `komarugif/gio/LOCAL_CHANGES.md`.
   This changes the wheel and the touchpad too on scaled displays: check
   live on Wayland at scales 1 and 2, the wheel's notch against
   `scroll.NotchPixels` and the touchpad against GTK 4.
4. **Then decide on `ContinuousScale`**: Telegram Desktop, the project's
   reference, and GTK 4 scroll a trackpoint as a touchpad, ×2.5 in logical
   pixels; GTK 3 and Qt Widgets go 6 to 10. Choose one reference and say
   which, instead of a factor of our own.
5. **X11**: since `ed90122` a trackpoint's smooth scrolling through XInput
   2.1 is taken for a wheel (`Wheel: !d.touchpad` in `os_x11_xi2.go`), and
   glides by notches. libinput's `Scroll Method Enabled` property, with
   button scrolling on, would tell a trackpoint, as the touchpad is told by
   its properties.

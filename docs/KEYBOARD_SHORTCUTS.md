# Keyboard shortcuts

Settings → Keyboard shortcuts lists the application's commands, their context,
defaults and user overrides. Assign records a chord or accepts typed notation
such as `Ctrl+Alt+T`; the latter also works when the desktop intercepts the
combination before the application receives a key event. Escape cancels the
recording. Save replaces the command's bindings, remove disables them, and reset
restores that command's defaults. Changes are saved atomically with preferences.
An absent override means defaults; a present empty list means disabled.

The registry is `internal/messenger/shortcuts`. Application, history, composer,
media-viewer and article commands use the same routing and stable IDs. Contexts
allow the viewer and message selection to share Copy, while preventing an
application-wide shortcut from shadowing a focused command. Ordinary text-editor
operations, dialog dismissal and Enter submission remain standard input rather
than assignable application commands. Remapping the composer's enhanced paste
changes the attachment-aware route; the text widget retains ordinary text paste.
At least one settings menu entry or settings shortcut must remain accessible.

## Physical positions and text

`key.Event.Physical` and `key.Filter.Physical` in the local Gio fork identify the
unshifted US position of a hardware key, separately from its logical `Name`.
Linux X11/Wayland use XKB key names, Windows uses the scan code plus extended bit,
macOS uses `NSEvent.keyCode`, and the web backend uses `KeyboardEvent.code`.
QWERTY, Dvorak, Cyrillic and shifted punctuation therefore use the same command
position. These labels are not claims about the characters printed on a user's
keycaps. Unknown positions remain empty; synthetic events and backends without
physical information fall back to logical names, with no layout independence
promised for that fallback. Not every exotic/keypad key is assignable yet.

Text composition, editor input and IME events retain their original path. The
physical chord is used only by command filters. Shortcut capture takes focus
before recording, and the application command route is suspended while that
dialog is open so recording a shortcut cannot also execute it.

## Wayland, Plasma and system conflicts

Application shortcuts here operate in a focused window. The compositor can
consume a system-wide combination before delivering it to Gio. Registering a
local filter does not request or override a global shortcut.

The standards-based global registration path is the
[XDG GlobalShortcuts portal](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.GlobalShortcuts.html):
`CreateSession`, a user-mediated `BindShortcuts`, `Activated`/`Deactivated` and
`ShortcutsChanged`. `ConfigureShortcuts` requires interface version 2. Binding
may be attempted only once per session. `ListShortcuts` is scoped to the
application's session (or its previous assignments), not a cross-application
inventory. It cannot prove that a local shortcut is free system-wide.

Preferred triggers use the
[XDG Shortcuts specification](https://specifications.freedesktop.org/shortcuts/latest/),
which names XKB symbols and modifiers. These are not the same representation as
our physical positions. A future global registration service must negotiate with
the portal, display the compositor's returned trigger description, track session
lifetime and user changes, and use activation tokens. No global registration or
global keyboard hook is implemented in this slice.

For Plasma, the already-running
[KGlobalAccel D-Bus API](https://invent.kde.org/frameworks/kglobalaccel/-/blob/master/src/org.kde.KGlobalAccel.xml)
allows a read-only `isGlobalShortcutAvailable` query. The checker uses Qt's
[key and modifier identifiers](https://github.com/qt/qtbase/blob/dev/src/corelib/global/qnamespace.h)
for a single stroke and an empty component name. It checks ownership before
calling either KDE or the portal and uses `FlagNoAutoStart`: opening our settings
must not start kglobalacceld on XFCE or another desktop. Calls run outside Layout
with cancellation and a bounded deadline. No registration, steal, unregister,
write to `kglobalshortcutsrc` or changes to another application's bindings occur.

A positive KDE collision is shown before saving and beside the assigned command.
It remains advisory: the user can keep it and change Plasma's assignment later.
A negative result is not a guarantee: these queries use logical US labels, while
our assignments use positions; other layouts, multi-stroke sequences, other
services and changes after a check may differ. On other desktops or if the service
cannot answer, the UI reports that automatic checks are unavailable. Portal
availability is shown separately from conflict coverage.

## Validation and manual Plasma checks

Linux/X11 is the development environment. Tests load actual US, Dvorak, Russian
and German XKB maps, check modifier/Shift handling, verify physical routing,
recording focus, manual entry, persistence and overlapping contexts. A private D-Bus daemon tests a missing KDE service and an occupied/free
combination against a fake KGlobalAccel interface; it does not run KWin.

On KDE Plasma Wayland, manually check:

1. Assign Open settings to a modified letter or punctuation key. Switch layouts,
   including a non-Latin layout, and verify that the same physical key works.
   Verify ordinary typing, composition and standard clipboard operations too.
2. In Plasma's system shortcut settings, reserve a test chord for another action.
   Type that chord manually in our assignment dialog (the compositor may prevent
   recording it), wait for the check, and verify the collision warning. Confirm
   that our checker did not reassign the system action.
3. Clear the Plasma binding and refresh our check. Verify the local command now
   receives the chord. Try a second layout; a negative logical-label query must
   not be treated as a universal physical-key guarantee.
4. Verify that commands do not fire in another application's focused window and
   that Enter/Escape retain their dialog/editor meanings.

Native Windows/macOS interaction and a real KDE/Wayland session still require
platform testing. The new registry/checker adds no module or native dependency.

// SPDX-License-Identifier: Unlicense OR MIT

//go:build (linux && !android) || freebsd

package tray

import (
	"fmt"
	"image"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"

	"komarugram/internal/appicon"
)

// The icon is a StatusNotifierItem, the protocol of KDE Plasma, LXQt, Xfce's
// and Cinnamon's panels and of GNOME's AppIndicator extension, and its menu a
// com.canonical.dbusmenu.
const (
	itemPath    = dbus.ObjectPath("/StatusNotifierItem")
	itemIface   = "org.kde.StatusNotifierItem"
	menuPath    = dbus.ObjectPath("/MenuBar")
	menuIface   = "com.canonical.dbusmenu"
	watcherName = "org.kde.StatusNotifierWatcher"
	watcherPath = dbus.ObjectPath("/StatusNotifierWatcher")
)

// Tray is the application's icon.
type Tray struct {
	conn *dbus.Conn
	name string
	opts Options

	mu         sync.Mutex
	registered bool
	// token is the activation token the host provided for the next click.
	token string
}

// Start shows the icon. It fails without a session bus; where no panel shows
// status notifier items, it succeeds but Available reports false.
func Start(opts Options) (*Tray, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("tray: %w", err)
	}
	t := &Tray{conn: conn, opts: opts, name: fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())}
	if err := t.export(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("tray: %w", err)
	}
	if _, err := conn.RequestName(t.name, dbus.NameFlagDoNotQueue); err != nil {
		conn.Close()
		return nil, fmt.Errorf("tray: %w", err)
	}
	// A panel that restarts brings a new watcher, which knows no items.
	if err := conn.AddMatchSignal(
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, watcherName),
	); err != nil {
		conn.Close()
		return nil, fmt.Errorf("tray: %w", err)
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	go t.watch(signals)
	t.register()
	return t, nil
}

// Available reports whether a panel shows the icon now, so that closing the
// last window may leave the application running in the tray.
func (t *Tray) Available() bool {
	t.mu.Lock()
	registered := t.registered
	t.mu.Unlock()
	if !registered && !t.register() {
		return false
	}
	v, err := t.conn.Object(watcherName, watcherPath).GetProperty(watcherName + ".IsStatusNotifierHostRegistered")
	if err != nil {
		return false
	}
	host, _ := v.Value().(bool)
	return host
}

// Close removes the icon.
func (t *Tray) Close() {
	t.conn.Close()
}

// SetBadge is a no-op on StatusNotifierItem desktops.
func (*Tray) SetBadge(Badge) {}

func (t *Tray) register() bool {
	err := t.conn.Object(watcherName, watcherPath).Call(watcherName+".RegisterStatusNotifierItem", 0, t.name).Err
	t.mu.Lock()
	t.registered = err == nil
	t.mu.Unlock()
	return err == nil
}

func (t *Tray) watch(signals <-chan *dbus.Signal) {
	for sig := range signals {
		if sig.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(sig.Body) != 3 {
			continue
		}
		if owner, _ := sig.Body[2].(string); owner != "" {
			t.register()
		} else {
			t.mu.Lock()
			t.registered = false
			t.mu.Unlock()
		}
	}
}

func (t *Tray) takeToken() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	token := t.token
	t.token = ""
	return token
}

// pixmap is an icon image of the protocol: ARGB32 in network byte order.
type pixmap struct {
	W, H int32
	Data []byte
}

type tooltip struct {
	IconName   string
	IconPixmap []pixmap
	Title      string
	Text       string
}

func pixmaps() []pixmap {
	var out []pixmap
	for _, im := range appicon.TrayImages(16, 22, 24, 32, 48, 64) {
		size := im.Rect.Dx()
		data := make([]byte, 0, size*size*4)
		for i := 0; i < len(im.Pix); i += 4 {
			data = append(data, im.Pix[i+3], im.Pix[i], im.Pix[i+1], im.Pix[i+2])
		}
		out = append(out, pixmap{int32(size), int32(size), data})
	}
	return out
}

// export publishes the item and its menu. Their methods are given as tables
// of method values: godbus's Export, and prop and introspect built on it,
// look methods up by reflection, which keeps the linker from dropping any
// exported method in the program and doubled the binary with all of
// gotd's API.
func (t *Tray) export() error {
	item := &statusItem{t}
	if err := t.conn.ExportMethodTable(map[string]any{
		"Activate":                  item.Activate,
		"SecondaryActivate":         item.SecondaryActivate,
		"ContextMenu":               item.ContextMenu,
		"Scroll":                    item.Scroll,
		"ProvideXdgActivationToken": item.ProvideXdgActivationToken,
	}, itemPath, itemIface); err != nil {
		return err
	}
	if err := exportProperties(t.conn, itemPath, itemIface, map[string]dbus.Variant{
		"Category":            dbus.MakeVariant("Communications"),
		"Id":                  dbus.MakeVariant(t.opts.ID),
		"Title":               dbus.MakeVariant(t.opts.Title),
		"Status":              dbus.MakeVariant("Active"),
		"WindowId":            dbus.MakeVariant(int32(0)),
		"IconName":            dbus.MakeVariant(""),
		"IconThemePath":       dbus.MakeVariant(""),
		"IconPixmap":          dbus.MakeVariant(pixmaps()),
		"OverlayIconName":     dbus.MakeVariant(""),
		"OverlayIconPixmap":   dbus.MakeVariant([]pixmap{}),
		"AttentionIconName":   dbus.MakeVariant(""),
		"AttentionIconPixmap": dbus.MakeVariant([]pixmap{}),
		"AttentionMovieName":  dbus.MakeVariant(""),
		"ToolTip":             dbus.MakeVariant(tooltip{IconPixmap: []pixmap{}, Title: t.opts.Title}),
		"ItemIsMenu":          dbus.MakeVariant(false),
		"Menu":                dbus.MakeVariant(menuPath),
	}, itemIntrospection); err != nil {
		return err
	}
	m := &menu{t}
	if err := t.conn.ExportMethodTable(map[string]any{
		"GetLayout":          m.GetLayout,
		"GetGroupProperties": m.GetGroupProperties,
		"GetProperty":        m.GetProperty,
		"Event":              m.Event,
		"EventGroup":         m.EventGroup,
		"AboutToShow":        m.AboutToShow,
		"AboutToShowGroup":   m.AboutToShowGroup,
	}, menuPath, menuIface); err != nil {
		return err
	}
	return exportProperties(t.conn, menuPath, menuIface, map[string]dbus.Variant{
		"Version":       dbus.MakeVariant(uint32(3)),
		"TextDirection": dbus.MakeVariant("ltr"),
		"Status":        dbus.MakeVariant("normal"),
		"IconThemePath": dbus.MakeVariant([]string{}),
	}, menuIntrospection)
}

// exportProperties publishes the read-only properties of iface at path, and
// the introspection data of the object.
func exportProperties(conn *dbus.Conn, path dbus.ObjectPath, iface string, props map[string]dbus.Variant, introspection string) error {
	unknown := func(name string) *dbus.Error {
		return dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", []any{"no property " + name})
	}
	if err := conn.ExportMethodTable(map[string]any{
		"Get": func(in, name string) (dbus.Variant, *dbus.Error) {
			v, ok := props[name]
			if in != iface || !ok {
				return dbus.Variant{}, unknown(name)
			}
			return v, nil
		},
		"GetAll": func(in string) (map[string]dbus.Variant, *dbus.Error) {
			if in != iface {
				return map[string]dbus.Variant{}, nil
			}
			return props, nil
		},
		"Set": func(in, name string, v dbus.Variant) *dbus.Error {
			return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", []any{name + " is read-only"})
		},
	}, path, "org.freedesktop.DBus.Properties"); err != nil {
		return err
	}
	return conn.ExportMethodTable(map[string]any{
		"Introspect": func() (string, *dbus.Error) { return introspection, nil },
	}, path, "org.freedesktop.DBus.Introspectable")
}

// statusItem is the org.kde.StatusNotifierItem object.
type statusItem struct{ t *Tray }

func (i *statusItem) Activate(x, y int32) *dbus.Error {
	if i.t.opts.Activate != nil {
		go i.t.opts.Activate(i.t.takeToken())
	}
	return nil
}

func (i *statusItem) SecondaryActivate(x, y int32) *dbus.Error { return nil }

// ContextMenu is called only by hosts that do not show Menu themselves.
func (i *statusItem) ContextMenu(x, y int32) *dbus.Error { return nil }

func (i *statusItem) Scroll(delta int32, orientation string) *dbus.Error { return nil }

// ProvideXdgActivationToken is how a Wayland host lets the next Activate
// raise a window.
func (i *statusItem) ProvideXdgActivationToken(token string) *dbus.Error {
	i.t.mu.Lock()
	i.t.token = token
	i.t.mu.Unlock()
	return nil
}

// menu is the com.canonical.dbusmenu object. Its root, ID 0, holds the items
// of Options, the item i with ID i+1. The menu never changes, so its
// revision stays 1.
type menu struct{ t *Tray }

type menuLayout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}

type menuProperties struct {
	ID         int32
	Properties map[string]dbus.Variant
}

type menuEvent struct {
	ID        int32
	EventID   string
	Data      dbus.Variant
	Timestamp uint32
}

func (m *menu) item(id int32) (Item, bool) {
	if id < 1 || int(id) > len(m.t.opts.Items) {
		return Item{}, false
	}
	return m.t.opts.Items[id-1], true
}

func (m *menu) properties(id int32) (map[string]dbus.Variant, bool) {
	if id == 0 {
		return map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")}, true
	}
	it, ok := m.item(id)
	if !ok {
		return nil, false
	}
	if it.Separator {
		return map[string]dbus.Variant{"type": dbus.MakeVariant("separator")}, true
	}
	return map[string]dbus.Variant{
		"label":   dbus.MakeVariant(it.Label),
		"enabled": dbus.MakeVariant(true),
		"visible": dbus.MakeVariant(true),
	}, true
}

func unknownItem(id int32) *dbus.Error {
	return dbus.NewError(menuIface+".Error", []any{fmt.Sprintf("no menu item %d", id)})
}

func (m *menu) GetLayout(parentID, depth int32, names []string) (uint32, menuLayout, *dbus.Error) {
	props, ok := m.properties(parentID)
	if !ok {
		return 0, menuLayout{}, unknownItem(parentID)
	}
	layout := menuLayout{ID: parentID, Properties: props, Children: []dbus.Variant{}}
	if parentID == 0 && depth != 0 {
		for i := range m.t.opts.Items {
			id := int32(i + 1)
			props, _ := m.properties(id)
			layout.Children = append(layout.Children, dbus.MakeVariant(menuLayout{ID: id, Properties: props, Children: []dbus.Variant{}}))
		}
	}
	return 1, layout, nil
}

func (m *menu) GetGroupProperties(ids []int32, names []string) ([]menuProperties, *dbus.Error) {
	if len(ids) == 0 {
		for i := 0; i <= len(m.t.opts.Items); i++ {
			ids = append(ids, int32(i))
		}
	}
	out := []menuProperties{}
	for _, id := range ids {
		if props, ok := m.properties(id); ok {
			out = append(out, menuProperties{id, props})
		}
	}
	return out, nil
}

func (m *menu) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	props, ok := m.properties(id)
	if !ok {
		return dbus.Variant{}, unknownItem(id)
	}
	v, ok := props[name]
	if !ok {
		return dbus.Variant{}, dbus.NewError(menuIface+".Error", []any{"no property " + name})
	}
	return v, nil
}

func (m *menu) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	it, ok := m.item(id)
	if !ok {
		return unknownItem(id)
	}
	if eventID == "clicked" && it.Action != nil {
		go it.Action(m.t.takeToken())
	}
	return nil
}

func (m *menu) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	errors := []int32{}
	for _, e := range events {
		if m.Event(e.ID, e.EventID, e.Data, e.Timestamp) != nil {
			errors = append(errors, e.ID)
		}
	}
	return errors, nil
}

func (m *menu) AboutToShow(id int32) (bool, *dbus.Error) { return false, nil }

func (m *menu) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	return []int32{}, []int32{}, nil
}

const itemIntrospection = `<node>
 <interface name="org.kde.StatusNotifierItem">
  <property name="Category" type="s" access="read"/>
  <property name="Id" type="s" access="read"/>
  <property name="Title" type="s" access="read"/>
  <property name="Status" type="s" access="read"/>
  <property name="WindowId" type="i" access="read"/>
  <property name="IconName" type="s" access="read"/>
  <property name="IconThemePath" type="s" access="read"/>
  <property name="IconPixmap" type="a(iiay)" access="read"/>
  <property name="OverlayIconName" type="s" access="read"/>
  <property name="OverlayIconPixmap" type="a(iiay)" access="read"/>
  <property name="AttentionIconName" type="s" access="read"/>
  <property name="AttentionIconPixmap" type="a(iiay)" access="read"/>
  <property name="AttentionMovieName" type="s" access="read"/>
  <property name="ToolTip" type="(sa(iiay)ss)" access="read"/>
  <property name="ItemIsMenu" type="b" access="read"/>
  <property name="Menu" type="o" access="read"/>
  <method name="ContextMenu"><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/></method>
  <method name="Activate"><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/></method>
  <method name="SecondaryActivate"><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/></method>
  <method name="Scroll"><arg name="delta" type="i" direction="in"/><arg name="orientation" type="s" direction="in"/></method>
  <method name="ProvideXdgActivationToken"><arg name="token" type="s" direction="in"/></method>
  <signal name="NewTitle"/>
  <signal name="NewIcon"/>
  <signal name="NewAttentionIcon"/>
  <signal name="NewOverlayIcon"/>
  <signal name="NewToolTip"/>
  <signal name="NewStatus"><arg name="status" type="s"/></signal>
 </interface>
 <interface name="org.freedesktop.DBus.Introspectable">
  <method name="Introspect"><arg name="out" direction="out" type="s"/></method>
 </interface>
 <interface name="org.freedesktop.DBus.Properties">
  <method name="Get"><arg name="interface" direction="in" type="s"/><arg name="property" direction="in" type="s"/><arg name="value" direction="out" type="v"/></method>
  <method name="GetAll"><arg name="interface" direction="in" type="s"/><arg name="props" direction="out" type="a{sv}"/></method>
  <method name="Set"><arg name="interface" direction="in" type="s"/><arg name="property" direction="in" type="s"/><arg name="value" direction="in" type="v"/></method>
  <signal name="PropertiesChanged"><arg name="interface" type="s"/><arg name="changed_properties" type="a{sv}"/><arg name="invalidated_properties" type="as"/></signal>
 </interface>
</node>`

const menuIntrospection = `<node>
 <interface name="com.canonical.dbusmenu">
  <property name="Version" type="u" access="read"/>
  <property name="TextDirection" type="s" access="read"/>
  <property name="Status" type="s" access="read"/>
  <property name="IconThemePath" type="as" access="read"/>
  <method name="GetLayout">
   <arg type="i" name="parentId" direction="in"/>
   <arg type="i" name="recursionDepth" direction="in"/>
   <arg type="as" name="propertyNames" direction="in"/>
   <arg type="u" name="revision" direction="out"/>
   <arg type="(ia{sv}av)" name="layout" direction="out"/>
  </method>
  <method name="GetGroupProperties">
   <arg type="ai" name="ids" direction="in"/>
   <arg type="as" name="propertyNames" direction="in"/>
   <arg type="a(ia{sv})" name="properties" direction="out"/>
  </method>
  <method name="GetProperty">
   <arg type="i" name="id" direction="in"/>
   <arg type="s" name="name" direction="in"/>
   <arg type="v" name="value" direction="out"/>
  </method>
  <method name="Event">
   <arg type="i" name="id" direction="in"/>
   <arg type="s" name="eventId" direction="in"/>
   <arg type="v" name="data" direction="in"/>
   <arg type="u" name="timestamp" direction="in"/>
  </method>
  <method name="EventGroup">
   <arg type="a(isvu)" name="events" direction="in"/>
   <arg type="ai" name="idErrors" direction="out"/>
  </method>
  <method name="AboutToShow">
   <arg type="i" name="id" direction="in"/>
   <arg type="b" name="needUpdate" direction="out"/>
  </method>
  <method name="AboutToShowGroup">
   <arg type="ai" name="ids" direction="in"/>
   <arg type="ai" name="updatesNeeded" direction="out"/>
   <arg type="ai" name="idErrors" direction="out"/>
  </method>
  <signal name="ItemsPropertiesUpdated">
   <arg type="a(ia{sv})" name="updatedProps"/>
   <arg type="a(ias)" name="removedProps"/>
  </signal>
  <signal name="LayoutUpdated">
   <arg type="u" name="revision"/>
   <arg type="i" name="parent"/>
  </signal>
  <signal name="ItemActivationRequested">
   <arg type="i" name="id"/>
   <arg type="u" name="timestamp"/>
  </signal>
 </interface>
 <interface name="org.freedesktop.DBus.Introspectable">
  <method name="Introspect"><arg name="out" direction="out" type="s"/></method>
 </interface>
 <interface name="org.freedesktop.DBus.Properties">
  <method name="Get"><arg name="interface" direction="in" type="s"/><arg name="property" direction="in" type="s"/><arg name="value" direction="out" type="v"/></method>
  <method name="GetAll"><arg name="interface" direction="in" type="s"/><arg name="props" direction="out" type="a{sv}"/></method>
  <method name="Set"><arg name="interface" direction="in" type="s"/><arg name="property" direction="in" type="s"/><arg name="value" direction="in" type="v"/></method>
  <signal name="PropertiesChanged"><arg name="interface" type="s"/><arg name="changed_properties" type="a{sv}"/><arg name="invalidated_properties" type="as"/></signal>
 </interface>
</node>`

// Notify is not the tray's on these desktops: notifications have their own
// D-Bus service (internal/notify).
func (*Tray) Notify(string, string, bool, image.Image) error { return ErrUnsupported }

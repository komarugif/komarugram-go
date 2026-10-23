// SPDX-License-Identifier: Unlicense OR MIT

//go:build (linux && !android) || freebsd

package notify

import (
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"komarugram/internal/crash"
)

const (
	busName  = "org.freedesktop.Notifications"
	busPath  = dbus.ObjectPath("/org/freedesktop/Notifications")
	busIface = "org.freedesktop.Notifications"
)

// New returns the notifier of this system: the desktop's Notifications
// service (https://specifications.freedesktop.org/notification-spec/).
func New(app string, tray Balloon) Notifier {
	return &dbusNotifier{app: app, queue: make(chan Notification, 64), byTag: map[string]uint32{}, shown: map[uint32]shown{}}
}

type shown struct {
	tag  string
	open func(string)
	// token is the activation token the click comes with.
	token string
}

type dbusNotifier struct {
	app string
	// queue keeps notifications in order for the one goroutine that sends
	// them; mu guards the rest.
	queue  chan Notification
	start  sync.Once
	mu     sync.Mutex
	conn   *dbus.Conn
	failed bool
	markup bool
	byTag  map[string]uint32
	shown  map[uint32]shown
}

func (d *dbusNotifier) connect() *dbus.Conn {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil || d.failed {
		return d.conn
	}
	conn, err := dbus.ConnectSessionBus()
	if err == nil {
		err = conn.AddMatchSignal(dbus.WithMatchObjectPath(busPath), dbus.WithMatchInterface(busIface))
	}
	if err != nil {
		log.Printf("notify: %v", err)
		d.failed = true
		if conn != nil {
			conn.Close()
		}
		return nil
	}
	var caps []string
	if err := conn.Object(busName, busPath).Call(busIface+".GetCapabilities", 0).Store(&caps); err != nil {
		log.Printf("notify: no notification service: %v", err)
	}
	d.markup = slices.Contains(caps, "body-markup")
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	go d.watch(signals)
	d.conn = conn
	return conn
}

func (d *dbusNotifier) Show(n Notification) {
	d.start.Do(func() {
		go func() {
			for n := range d.queue {
				func() {
					// A notification that panics is not shown; the next are.
					defer crash.Recover("notification", nil)
					d.show(n)
				}()
			}
		}()
	})
	select {
	case d.queue <- n:
	default:
		// The service is not answering; what it has not shown is old.
	}
}

var markupEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func (d *dbusNotifier) show(n Notification) {
	conn := d.connect()
	if conn == nil {
		return
	}
	d.mu.Lock()
	replaces := d.byTag[n.Tag]
	body := n.Body
	if d.markup {
		body = markupEscaper.Replace(body)
	}
	d.mu.Unlock()
	hints := map[string]dbus.Variant{
		"category": dbus.MakeVariant("im.received"),
		"urgency":  dbus.MakeVariant(byte(1)),
	}
	if n.Sound {
		hints["sound-name"] = dbus.MakeVariant("message-new-instant")
	} else {
		hints["suppress-sound"] = dbus.MakeVariant(true)
	}
	var actions []string
	if n.Open != nil {
		actions = []string{"default", ""}
	}
	var id uint32
	err := conn.Object(busName, busPath).Call(busIface+".Notify", 0,
		d.app, replaces, "", clip(n.Title, 200), clip(body, 1000), actions, hints, int32(-1)).Store(&id)
	if err != nil {
		log.Printf("notify: %v", err)
		return
	}
	d.mu.Lock()
	if replaces != 0 && replaces != id {
		delete(d.shown, replaces)
	}
	if n.Tag != "" {
		d.byTag[n.Tag] = id
	}
	d.shown[id] = shown{tag: n.Tag, open: n.Open}
	d.mu.Unlock()
}

func (d *dbusNotifier) watch(signals <-chan *dbus.Signal) {
	for sig := range signals {
		if len(sig.Body) < 2 {
			continue
		}
		id, ok := sig.Body[0].(uint32)
		if !ok {
			continue
		}
		switch sig.Name {
		case busIface + ".ActivationToken":
			// Comes before ActionInvoked, for raising the window on Wayland.
			if token, ok := sig.Body[1].(string); ok {
				d.mu.Lock()
				if s, ok := d.shown[id]; ok {
					s.token = token
					d.shown[id] = s
				}
				d.mu.Unlock()
			}
		case busIface + ".ActionInvoked":
			d.mu.Lock()
			s, ok := d.shown[id]
			d.mu.Unlock()
			if ok && s.open != nil {
				go s.open(s.token)
			}
		case busIface + ".NotificationClosed":
			d.mu.Lock()
			if s, ok := d.shown[id]; ok && d.byTag[s.tag] == id {
				delete(d.byTag, s.tag)
			}
			delete(d.shown, id)
			d.mu.Unlock()
		}
	}
}

func (d *dbusNotifier) Clicked(string) {}

func (d *dbusNotifier) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		d.conn.Close()
		d.conn = nil
	}
	d.failed = true
}

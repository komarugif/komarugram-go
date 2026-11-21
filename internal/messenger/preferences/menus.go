// SPDX-License-Identifier: Unlicense OR MIT

package preferences

import (
	"slices"
	"strings"
)

// FolderMenuItem is the navigation slot expanded into the active account's ordered folders.
const FolderMenuItem = "folders"

// Menus stores user choices by stable menu and action IDs. Unknown IDs remain
// saved so a temporarily unavailable account keeps its place.
type Menus map[string]MenuLayout

type MenuLayout struct {
	Order  []string `json:"order,omitempty"`
	Hidden []string `json:"hidden,omitempty"`
}

func (m Menus) Clone() Menus {
	if m == nil {
		return nil
	}
	out := make(Menus, len(m))
	for id, v := range m {
		out[id] = MenuLayout{Order: slices.Clone(v.Order), Hidden: slices.Clone(v.Hidden)}
	}
	return out
}

// Arrange intersects saved choices with currently available actions. New actions
// follow the saved ones in their default order; preferences never add authority.
func (m MenuLayout) Arrange(available []string, includeHidden bool) []string {
	present := make(map[string]bool, len(available))
	for _, id := range available {
		present[id] = true
	}
	hidden := make(map[string]bool, len(m.Hidden))
	if !includeHidden {
		for _, id := range m.Hidden {
			hidden[id] = true
		}
	}
	out := make([]string, 0, len(available))
	add := func(id string) {
		if present[id] && !hidden[id] {
			out = append(out, id)
		}
		delete(present, id)
	}
	for _, id := range m.Order {
		add(id)
	}
	for _, id := range available {
		add(id)
	}
	return out
}

// WithOrder merges a reordered catalog into the saved order, preserving unknown actions.
func (m MenuLayout) WithOrder(order []string) MenuLayout {
	known := make(map[string]bool, len(order))
	for _, id := range order {
		known[id] = true
	}
	next := slices.Clone(m.Order)
	i := 0
	for j, id := range next {
		if known[id] && i < len(order) {
			next[j] = order[i]
			i++
		}
	}
	// Newly discovered actions may have had no slot yet.
	next = append(next, order[i:]...)
	return MenuLayout{Order: next, Hidden: slices.Clone(m.Hidden)}
}

func legacyFolderItem(id string) bool {
	return id == "all" || strings.HasPrefix(id, "folder:")
}

// Earlier versions placed All chats and every account's folders separately.
// Collapse them at the first old slot. A partially hidden group stays visible.
func (m Menus) migrateFolderItems() {
	for _, menu := range []string{"sidebar", "drawer"} {
		layout := m[menu]
		if !slices.ContainsFunc(layout.Order, legacyFolderItem) && !slices.ContainsFunc(layout.Hidden, legacyFolderItem) {
			continue
		}
		placed := slices.Contains(layout.Order, FolderMenuItem)
		hideGroup := !placed && slices.Contains(layout.Order, "all") && slices.Contains(layout.Hidden, "all")
		order := make([]string, 0, len(layout.Order))
		for _, id := range layout.Order {
			if !legacyFolderItem(id) {
				order = append(order, id)
				continue
			}
			if !slices.Contains(layout.Hidden, id) {
				hideGroup = false
			}
			if !placed {
				order = append(order, FolderMenuItem)
				placed = true
			}
		}
		hidden := slices.DeleteFunc(layout.Hidden, legacyFolderItem)
		if hideGroup && !slices.Contains(hidden, FolderMenuItem) {
			hidden = append(hidden, FolderMenuItem)
		}
		m[menu] = MenuLayout{Order: order, Hidden: hidden}
	}
}

func (s *Store) SetMenus(m Menus) error {
	menus := m.Clone()
	menus.migrateFolderItems()
	return s.change(func(g *Global) { g.Menus = menus })
}

// SPDX-License-Identifier: Unlicense OR MIT

package preferences

import "slices"

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

// WithOrder merges a reordered visible catalog into the saved order without
// discarding actions belonging to other accounts.
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

func (s *Store) SetMenus(m Menus) error {
	return s.change(func(g *Global) { g.Menus = m.Clone() })
}

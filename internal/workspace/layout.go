// Package workspace is how a window is divided: panes side by side or one
// above the other, each holding tabs. It is a binary tree of splits whose
// leaves are the panes, as a tiling window manager's, and knows nothing of
// what a tab shows or of the interface that draws it.
package workspace

// TabID names a tab, whatever pane holds it.
type TabID int

// PaneID names a pane.
type PaneID int

// Zone is a part of a pane something is dropped on: its middle, or one of
// its edges, which splits it.
type Zone uint8

const (
	ZoneNone Zone = iota
	ZoneCenter
	ZoneLeft
	ZoneRight
	ZoneTop
	ZoneBottom
)

// ZoneAt is the zone of a pane under a point, x and y from 0 to 1 across
// it: the edges are the outer quarter on each side, the nearest one where
// they meet in the corners.
func ZoneAt(x, y float32) Zone {
	const edge = 0.25
	l, r, t, b := x, 1-x, y, 1-y
	m := min(l, r, t, b)
	if m > edge {
		return ZoneCenter
	}
	switch m {
	case l:
		return ZoneLeft
	case r:
		return ZoneRight
	case t:
		return ZoneTop
	}
	return ZoneBottom
}

// Pane is a group of tabs, of which one shows.
type Pane struct {
	ID   PaneID  `json:"id"`
	Tabs []TabID `json:"tabs"`
	// Active is the index of the tab that shows.
	Active int `json:"active"`
}

// ActiveTab is the tab that shows, and false for a pane without tabs.
func (p *Pane) ActiveTab() (TabID, bool) {
	if p == nil || len(p.Tabs) == 0 {
		return 0, false
	}
	return p.Tabs[min(max(p.Active, 0), len(p.Tabs)-1)], true
}

// Index is the place of a tab among the pane's, -1 when it is not there.
func (p *Pane) Index(tab TabID) int {
	for i, t := range p.Tabs {
		if t == tab {
			return i
		}
	}
	return -1
}

// Node is a split of two nodes, or a pane.
type Node struct {
	// Pane is set for a leaf, and the rest for a split.
	Pane *Pane `json:"pane,omitempty"`
	// Stacked puts A above B; otherwise A is on the left of B.
	Stacked bool `json:"stacked,omitempty"`
	// Ratio is the share of the room A takes, from 0 to 1.
	Ratio float32 `json:"ratio,omitempty"`
	A     *Node   `json:"a,omitempty"`
	B     *Node   `json:"b,omitempty"`
}

// Layout is the panes of a window.
type Layout struct {
	Root *Node `json:"root"`
	// Focus is the pane the keyboard acts on.
	Focus    PaneID `json:"focus"`
	NextPane PaneID `json:"nextPane"`
}

// New is a layout of one pane holding the tabs.
func New(tabs ...TabID) *Layout {
	l := &Layout{NextPane: 1}
	p := l.newPane(tabs...)
	l.Root = &Node{Pane: p}
	l.Focus = p.ID
	return l
}

func (l *Layout) newPane(tabs ...TabID) *Pane {
	p := &Pane{ID: l.NextPane, Tabs: append([]TabID(nil), tabs...)}
	l.NextPane++
	return p
}

// Panes lists the panes from left to right and top to bottom, as the tree
// holds them.
func (l *Layout) Panes() []*Pane {
	var out []*Pane
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Pane != nil {
			out = append(out, n.Pane)
			return
		}
		walk(n.A)
		walk(n.B)
	}
	walk(l.Root)
	return out
}

// Pane finds a pane by its ID.
func (l *Layout) Pane(id PaneID) *Pane {
	for _, p := range l.Panes() {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// Focused is the pane with the focus, the first one when the focus names
// none.
func (l *Layout) Focused() *Pane {
	if p := l.Pane(l.Focus); p != nil {
		return p
	}
	panes := l.Panes()
	if len(panes) == 0 {
		return nil
	}
	l.Focus = panes[0].ID
	return panes[0]
}

// PaneOf finds the pane holding a tab.
func (l *Layout) PaneOf(tab TabID) *Pane {
	for _, p := range l.Panes() {
		if p.Index(tab) >= 0 {
			return p
		}
	}
	return nil
}

// Tabs lists every tab, pane by pane.
func (l *Layout) Tabs() []TabID {
	var out []TabID
	for _, p := range l.Panes() {
		out = append(out, p.Tabs...)
	}
	return out
}

// AddTab puts a new tab in a pane at an index, the end for -1, and shows
// it.
func (l *Layout) AddTab(pane PaneID, tab TabID, at int) {
	p := l.Pane(pane)
	if p == nil {
		p = l.Focused()
	}
	insert(p, tab, at)
}

func insert(p *Pane, tab TabID, at int) {
	if at < 0 || at > len(p.Tabs) {
		at = len(p.Tabs)
	}
	p.Tabs = append(p.Tabs, 0)
	copy(p.Tabs[at+1:], p.Tabs[at:])
	p.Tabs[at] = tab
	p.Active = at
}

// take removes a tab from its pane, which shows the tab that was after it,
// and returns the pane.
func (l *Layout) take(tab TabID) *Pane {
	p := l.PaneOf(tab)
	if p == nil {
		return nil
	}
	i := p.Index(tab)
	p.Tabs = append(p.Tabs[:i], p.Tabs[i+1:]...)
	switch {
	case p.Active > i:
		p.Active--
	case p.Active >= len(p.Tabs):
		p.Active = max(len(p.Tabs)-1, 0)
	}
	return p
}

// RemoveTab closes a tab. A pane left without tabs goes, its room given to
// the pane beside it, unless it is the last one, which stays empty: it
// reports whether the layout still has tabs.
func (l *Layout) RemoveTab(tab TabID) bool {
	if p := l.take(tab); p != nil {
		l.collapse(p)
	}
	return len(l.Tabs()) > 0
}

// collapse removes a pane without tabs from the tree, unless it is the
// root.
func (l *Layout) collapse(p *Pane) {
	if len(p.Tabs) > 0 || l.Root.Pane == p {
		return
	}
	var walk func(n *Node) bool
	walk = func(n *Node) bool {
		if n == nil || n.Pane != nil {
			return false
		}
		for _, side := range [2]struct{ gone, kept *Node }{{n.A, n.B}, {n.B, n.A}} {
			if side.gone.Pane == p {
				*n = *side.kept
				return true
			}
		}
		return walk(n.A) || walk(n.B)
	}
	walk(l.Root)
	if l.Focus == p.ID {
		l.Focus = 0
		l.Focused()
	}
}

// MoveTab moves a tab to a pane, at an index among the tabs it has then
// (-1 for the end), and shows it there with the focus. Moving it within
// its pane reorders the tabs: the index is where it goes among the others.
func (l *Layout) MoveTab(tab TabID, to PaneID, at int) {
	dst := l.Pane(to)
	src := l.PaneOf(tab)
	if dst == nil || src == nil {
		return
	}
	if src == dst {
		i := src.Index(tab)
		if at < 0 {
			at = len(src.Tabs)
		}
		if at > i {
			at--
		}
		src.Tabs = append(src.Tabs[:i], src.Tabs[i+1:]...)
		insert(src, tab, min(at, len(src.Tabs)))
		l.Focus = dst.ID
		return
	}
	l.take(tab)
	insert(dst, tab, at)
	l.Focus = dst.ID
	l.collapse(src)
}

// Split moves a tab into a new pane on a side of the target pane, which
// gives it half its room, and focuses it. Splitting a pane with its only
// tab changes nothing, and ZoneCenter moves the tab into the target. It
// reports whether the layout changed.
func (l *Layout) Split(target PaneID, tab TabID, zone Zone) bool {
	dst := l.Pane(target)
	src := l.PaneOf(tab)
	if dst == nil || src == nil || zone == ZoneNone {
		return false
	}
	if zone == ZoneCenter {
		if src == dst {
			return false
		}
		l.MoveTab(tab, target, -1)
		return true
	}
	if src == dst && len(src.Tabs) == 1 {
		return false
	}
	l.take(tab)
	fresh := l.newPane(tab)
	node := l.nodeOf(dst)
	old := &Node{Pane: dst}
	added := &Node{Pane: fresh}
	split := Node{Ratio: 0.5, Stacked: zone == ZoneTop || zone == ZoneBottom}
	if zone == ZoneLeft || zone == ZoneTop {
		split.A, split.B = added, old
	} else {
		split.A, split.B = old, added
	}
	*node = split
	l.Focus = fresh.ID
	l.collapse(src)
	return true
}

func (l *Layout) nodeOf(p *Pane) *Node {
	var found *Node
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil || found != nil {
			return
		}
		if n.Pane == p {
			found = n
			return
		}
		walk(n.A)
		walk(n.B)
	}
	walk(l.Root)
	return found
}

// Neighbor is the pane after the focused one, or before it, around the
// layout, for a key that goes from pane to pane.
func (l *Layout) Neighbor(forward bool) *Pane {
	panes := l.Panes()
	if len(panes) == 0 {
		return nil
	}
	at := 0
	for i, p := range panes {
		if p.ID == l.Focus {
			at = i
		}
	}
	if forward {
		return panes[(at+1)%len(panes)]
	}
	return panes[(at+len(panes)-1)%len(panes)]
}

// Valid reports whether a layout read from a file holds together: every
// node a pane or a split of two, pane IDs unique, and no tab twice.
func (l *Layout) Valid() bool {
	if l == nil || l.Root == nil {
		return false
	}
	panes, tabs := map[PaneID]bool{}, map[TabID]bool{}
	var walk func(n *Node) bool
	walk = func(n *Node) bool {
		if n == nil {
			return false
		}
		if n.Pane != nil {
			if n.A != nil || n.B != nil || panes[n.Pane.ID] || n.Pane.ID >= l.NextPane {
				return false
			}
			panes[n.Pane.ID] = true
			for _, t := range n.Pane.Tabs {
				if tabs[t] {
					return false
				}
				tabs[t] = true
			}
			return true
		}
		return n.Ratio > 0 && n.Ratio < 1 && walk(n.A) && walk(n.B)
	}
	return walk(l.Root)
}

// first and last are the first and the last pane of a part of the layout,
// in the order Panes lists them.
func first(n *Node) *Pane {
	for n != nil && n.Pane == nil {
		n = n.A
	}
	if n == nil {
		return nil
	}
	return n.Pane
}

func last(n *Node) *Pane {
	for n != nil && n.Pane == nil {
		n = n.B
	}
	if n == nil {
		return nil
	}
	return n.Pane
}

// stackedAt is the split of an upper and a lower part of which upper, or
// lower, tells that it is the one sought.
func (l *Layout) stackedAt(is func(n *Node) bool) *Node {
	var found *Node
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil || n.Pane != nil || found != nil {
			return
		}
		if n.Stacked && is(n) {
			found = n
			return
		}
		walk(n.A)
		walk(n.B)
	}
	walk(l.Root)
	return found
}

// Under is the pane right under a pane: the first of the lower part of
// the split whose upper part ends with it. nil when nothing is under it.
func (l *Layout) Under(id PaneID) *Pane {
	n := l.stackedAt(func(n *Node) bool { p := last(n.A); return p != nil && p.ID == id })
	if n == nil {
		return nil
	}
	return first(n.B)
}

// Over is the pane a pane is right under, nil when it is under none.
func (l *Layout) Over(id PaneID) *Pane {
	n := l.stackedAt(func(n *Node) bool { p := first(n.B); return p != nil && p.ID == id })
	if n == nil {
		return nil
	}
	return last(n.A)
}

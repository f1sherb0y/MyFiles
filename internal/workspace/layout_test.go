package workspace

import (
	"encoding/json"
	"reflect"
	"testing"
)

func tabsOf(l *Layout) [][]TabID {
	var out [][]TabID
	for _, p := range l.Panes() {
		out = append(out, p.Tabs)
	}
	return out
}

func TestSplitAndCollapse(t *testing.T) {
	l := New(1, 2, 3)
	first := l.Focus
	if l.Split(first, 2, ZoneRight) != true {
		t.Fatal("split refused")
	}
	if got := tabsOf(l); !reflect.DeepEqual(got, [][]TabID{{1, 3}, {2}}) {
		t.Fatalf("after split right: %v", got)
	}
	right := l.Focus
	if right == first {
		t.Fatal("the new pane has no focus")
	}
	// Splitting a pane with its only tab changes nothing.
	if l.Split(right, 2, ZoneBottom) {
		t.Fatal("split a pane with its only tab")
	}
	// A tab dropped on the top of the other pane goes above it.
	l.Split(right, 3, ZoneTop)
	if got := tabsOf(l); !reflect.DeepEqual(got, [][]TabID{{1}, {3}, {2}}) {
		t.Fatalf("after split top: %v", got)
	}
	if !l.Root.B.Stacked || l.Root.Stacked {
		t.Fatalf("directions: root %v, right %v", l.Root.Stacked, l.Root.B.Stacked)
	}
	// Closing the only tab of a pane gives its room to its neighbor.
	if !l.RemoveTab(3) {
		t.Fatal("no tabs left")
	}
	if got := tabsOf(l); !reflect.DeepEqual(got, [][]TabID{{1}, {2}}) {
		t.Fatalf("after closing: %v", got)
	}
	if l.Focused() == nil || !l.Valid() {
		t.Fatal("focus lost")
	}
	// Moving the last tab of a pane into another leaves one pane.
	l.Split(l.Pane(first).ID, 2, ZoneCenter)
	if got := tabsOf(l); !reflect.DeepEqual(got, [][]TabID{{1, 2}}) {
		t.Fatalf("after merge: %v", got)
	}
	if l.Root.Pane == nil {
		t.Fatal("root is still a split")
	}
	l.RemoveTab(1)
	if l.RemoveTab(2) {
		t.Fatal("tabs left after closing all")
	}
	if len(l.Panes()) != 1 {
		t.Fatal("the last pane went")
	}
}

func TestMoveWithinPane(t *testing.T) {
	l := New(1, 2, 3, 4)
	p := l.Focus
	l.MoveTab(1, p, 3) // before the tab at index 3
	if got := l.Pane(p).Tabs; !reflect.DeepEqual(got, []TabID{2, 3, 1, 4}) {
		t.Fatalf("moved right: %v", got)
	}
	l.MoveTab(4, p, 0)
	if got := l.Pane(p).Tabs; !reflect.DeepEqual(got, []TabID{4, 2, 3, 1}) {
		t.Fatalf("moved left: %v", got)
	}
	l.MoveTab(4, p, -1)
	if got := l.Pane(p).Tabs; !reflect.DeepEqual(got, []TabID{2, 3, 1, 4}) {
		t.Fatalf("moved to the end: %v", got)
	}
	if tab, _ := l.Pane(p).ActiveTab(); tab != 4 {
		t.Fatalf("active is %d", tab)
	}
}

func TestZoneAt(t *testing.T) {
	for _, tc := range []struct {
		x, y float32
		want Zone
	}{
		{0.5, 0.5, ZoneCenter}, {0.1, 0.5, ZoneLeft}, {0.9, 0.5, ZoneRight},
		{0.5, 0.1, ZoneTop}, {0.5, 0.95, ZoneBottom}, {0.05, 0.1, ZoneLeft},
	} {
		if got := ZoneAt(tc.x, tc.y); got != tc.want {
			t.Errorf("ZoneAt(%v, %v) = %v, want %v", tc.x, tc.y, got, tc.want)
		}
	}
}

func TestJSON(t *testing.T) {
	l := New(1, 2, 3)
	l.Split(l.Focus, 3, ZoneBottom)
	data, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	var back Layout
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Valid() || !reflect.DeepEqual(tabsOf(&back), tabsOf(l)) {
		t.Fatalf("read back %s", data)
	}
}

// Under and Over go between a pane and the one right under it.
func TestUnderOver(t *testing.T) {
	l := New(1, 2, 3, 4)
	top := l.Focused().ID
	if l.Under(top) != nil || l.Over(top) != nil {
		t.Fatal("a pane alone has one under or over it")
	}
	l.Split(top, 2, ZoneBottom)
	low := l.PaneOf(2).ID
	if p := l.Under(top); p == nil || p.ID != low {
		t.Fatalf("under the upper pane: %v", p)
	}
	if p := l.Over(low); p == nil || p.ID != top {
		t.Fatalf("over the lower pane: %v", p)
	}
	if l.Under(low) != nil || l.Over(top) != nil {
		t.Fatal("something under the lower pane, or over the upper")
	}
	// A pane beside the upper one has nothing under it; one beside the
	// lower one is under nothing.
	l.Split(top, 3, ZoneLeft)
	left := l.PaneOf(3).ID
	if l.Under(left) != nil {
		t.Fatal("something under the pane beside the upper one")
	}
	if p := l.Under(top); p == nil || p.ID != low {
		t.Fatalf("under the upper pane, once it has one beside it: %v", p)
	}
	l.Split(low, 4, ZoneRight)
	if l.Over(l.PaneOf(4).ID) != nil {
		t.Fatal("the pane beside the lower one is under one")
	}
}

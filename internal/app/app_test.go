package app

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"myfiles/internal/fsys"
	"myfiles/internal/shell"
)

func TestNavigate(t *testing.T) {
	h := newHarness(t, "alpha/inner.txt", "beta/", "one.txt", "two.txt")
	tab := h.w.current()
	if got, want := h.rows(tab), []string{"alpha", "beta", "one.txt", "two.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	if !h.tt.HasText("4 items") {
		t.Errorf("status: %q", h.tt.Texts())
	}

	h.doubleClick("alpha")
	if tab.Path != h.path("alpha") || !reflect.DeepEqual(h.rows(tab), []string{"inner.txt"}) {
		t.Fatalf("after opening alpha: %s %v", tab.Path, h.rows(tab))
	}
	// Back, with the folder left chosen; forward; and up with Backspace.
	h.key(ui.Alt, ui.KeyLeft)
	if tab.Path != h.root || !tab.sel.Has("alpha") {
		t.Fatalf("after back: %s, chosen alpha %v", tab.Path, tab.sel.Has("alpha"))
	}
	h.key(ui.Alt, ui.KeyRight)
	if tab.Path != h.path("alpha") {
		t.Fatalf("after forward: %s", tab.Path)
	}
	h.key(0, ui.KeyBackspace)
	if tab.Path != h.root || !tab.sel.Has("alpha") {
		t.Fatalf("after up: %s", tab.Path)
	}

	// Enter opens what is chosen.
	h.key(0, ui.KeyEnter)
	if tab.Path != h.path("alpha") {
		t.Fatalf("after Enter: %s", tab.Path)
	}
	// A file opens with its program, started in its folder.
	h.doubleClick("inner.txt")
	h.wait("the file to open", func() bool {
		l := h.desk.launches()
		return len(l) == 1 && l[0] == "open "+h.path("alpha/inner.txt")
	})
	// A component of the address goes to its folder.
	h.click(filepath.Base(h.root))
	if tab.Path != h.root {
		t.Fatalf("after the crumb: %s", tab.Path)
	}
	if !tab.sel.Has("alpha") {
		t.Error("the folder left is not chosen in the one above it")
	}
	if len(tab.back) == 0 {
		t.Error("no history")
	}
}

func TestAddressAsText(t *testing.T) {
	h := newHarness(t, "alpha/inner.txt", "one.txt")
	tab := h.w.current()

	h.key(ui.Cmd, ui.KeyL)
	if !tab.editing || tab.addr != h.root {
		t.Fatalf("editing %v, text %q", tab.editing, tab.addr)
	}
	// The path is selected whole: typing replaces it, and it copies.
	h.tt.Command("copy")
	if got := h.tt.Clipboard(); got != h.root {
		t.Errorf("copied %q, want %q", got, h.root)
	}
	h.tt.Type(h.path("alpha"))
	h.key(0, ui.KeyEnter)
	if tab.editing || tab.Path != h.path("alpha") {
		t.Fatalf("after Enter: editing %v, path %s", tab.editing, tab.Path)
	}

	// A file's path shows its folder with the file chosen.
	h.key(ui.Cmd, ui.KeyL)
	h.tt.Type(h.path("one.txt"))
	h.key(0, ui.KeyEnter)
	if tab.Path != h.root || !tab.sel.Has("one.txt") {
		t.Fatalf("a file's path: %s, chosen %v", tab.Path, tab.sel.Has("one.txt"))
	}

	// Escape leaves the address as it was; so does a path that is not
	// there, which says so.
	h.key(ui.Cmd, ui.KeyL)
	h.tt.Type("nowhere")
	h.key(0, ui.KeyEscape)
	if tab.editing || tab.Path != h.root {
		t.Fatalf("after Escape: editing %v, path %s", tab.editing, tab.Path)
	}
	h.key(ui.Cmd, ui.KeyL)
	h.tt.Type(h.path("missing"))
	h.key(0, ui.KeyEnter)
	if tab.Path != h.root || h.w.status == "" {
		t.Fatalf("a missing path: %s, status %q", tab.Path, h.w.status)
	}

	// A click beside the components edits too.
	h.click("Edit address")
	if !tab.editing {
		t.Fatal("a click beside the address does not edit it")
	}
}

func TestFilterAndSort(t *testing.T) {
	h := newHarness(t, "alpha/", "big.bin", "one.txt", "two.txt")
	tab := h.w.current()
	if err := os.WriteFile(h.path("big.bin"), make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	h.w.refresh(tab)
	h.settle()

	h.key(ui.Cmd, ui.KeyF)
	h.tt.Type("T")
	h.settle()
	if got, want := h.rows(tab), []string{"one.txt", "two.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered rows %v, want %v", got, want)
	}
	h.key(0, ui.KeyEscape)
	if len(tab.view) != 4 {
		t.Fatalf("after clearing the filter: %v", h.rows(tab))
	}

	// A click on a header sorts by it, directories still first; another
	// reverses.
	h.click("Size")
	if got, want := h.rows(tab), []string{"alpha", "one.txt", "two.txt", "big.bin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("by size: %v, want %v", got, want)
	}
	h.click("Size")
	if got, want := h.rows(tab), []string{"alpha", "big.bin", "two.txt", "one.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("by size, descending: %v, want %v", got, want)
	}
}

// tabAt is the middle of a tab's header.
func (h *harness) tabAt(name string) [2]float32 {
	h.t.Helper()
	info, ok := h.tt.AccessByRole(ui.RoleTab, name)
	if !ok {
		h.t.Fatalf("no tab %q", name)
	}
	return [2]float32{info.Bounds.X + info.Bounds.W/2, info.Bounds.Y + info.Bounds.H/2}
}

func TestDragTabsToSplit(t *testing.T) {
	h := newHarness(t, "alpha/a.txt", "beta/b.txt", "gamma/")
	root := filepath.Base(h.root)
	h.open("alpha")
	h.open("beta")
	if got := h.panes(); !reflect.DeepEqual(got, [][]string{{root, "alpha", "beta"}}) {
		t.Fatalf("tabs %v", got)
	}

	// A tab let go on the right of the pane splits it side by side.
	from := h.tabAt("beta")
	h.drag(from, [2]float32{from[0] + 30, from[1] + 30}, [2]float32{860, 300})
	if got := h.panes(); !reflect.DeepEqual(got, [][]string{{root, "alpha"}, {"beta"}}) {
		t.Fatalf("after a drop on the right: %v", got)
	}
	if h.w.layout.Root.Pane != nil || h.w.layout.Root.Stacked {
		t.Fatalf("the root is not split side by side: %+v", h.w.layout.Root)
	}
	if cur := h.w.current(); cur.title() != "beta" {
		t.Errorf("the focus is on %s", cur.title())
	}

	// One let go at the bottom of the left pane goes under it.
	from = h.tabAt("alpha")
	h.drag(from, [2]float32{from[0] + 30, from[1] + 30}, [2]float32{200, 560})
	if got := h.panes(); !reflect.DeepEqual(got, [][]string{{root}, {"alpha"}, {"beta"}}) {
		t.Fatalf("after a drop at the bottom: %v", got)
	}
	if left := h.w.layout.Root.A; left.Pane != nil || !left.Stacked {
		t.Fatalf("the left side is not split one above the other: %+v", left)
	}

	// On the top of a pane, above it; on its left, before it.
	from = h.tabAt("alpha")
	to := h.tabAt("beta")
	h.drag(from, [2]float32{from[0] + 30, from[1] + 30}, [2]float32{to[0], 80})
	if got := h.panes(); !reflect.DeepEqual(got, [][]string{{root}, {"alpha"}, {"beta"}}) || !h.w.layout.Root.B.Stacked {
		t.Fatalf("after a drop at the top: %v, right side %+v", got, h.w.layout.Root.B)
	}

	// On another pane's tab, it joins that pane, before the tab; a pane
	// left without tabs gives its room back.
	from = h.tabAt("alpha")
	to = h.tabAt("beta")
	h.drag(from, [2]float32{from[0] + 30, from[1] + 30}, [2]float32{to[0] - 20, to[1]})
	if got := h.panes(); !reflect.DeepEqual(got, [][]string{{root}, {"alpha", "beta"}}) {
		t.Fatalf("after a drop on a tab: %v", got)
	}
	// And in the middle of a pane, it joins it at the end.
	from = h.tabAt("alpha")
	h.drag(from, [2]float32{from[0] + 30, from[1] + 30}, [2]float32{200, 300})
	if got := h.panes(); !reflect.DeepEqual(got, [][]string{{root, "alpha"}, {"beta"}}) {
		t.Fatalf("after a drop in the middle: %v", got)
	}

	// Within a pane, tabs reorder.
	from = h.tabAt("alpha")
	to = h.tabAt(root)
	h.drag(from, [2]float32{from[0] - 30, from[1]}, [2]float32{to[0] - 20, to[1]})
	if got := h.panes(); !reflect.DeepEqual(got, [][]string{{"alpha", root}, {"beta"}}) {
		t.Fatalf("after reordering: %v", got)
	}
	if !h.w.layout.Valid() {
		t.Fatal("the layout no longer holds together")
	}
}

func TestSplitKeysAndTabs(t *testing.T) {
	h := newHarness(t, "alpha/")
	root := filepath.Base(h.root)
	h.key(ui.Cmd, ui.KeyBackslash)
	if got := h.panes(); !reflect.DeepEqual(got, [][]string{{root}, {root}}) {
		t.Fatalf("after Ctrl+\\: %v", got)
	}
	h.key(ui.Cmd|ui.Shift, ui.KeyBackslash)
	if len(h.w.layout.Panes()) != 3 {
		t.Fatalf("after Ctrl+Shift+\\: %v", h.panes())
	}
	h.key(ui.Cmd, ui.KeyT)
	h.key(ui.Cmd, ui.KeyW)
	h.key(ui.Cmd, ui.KeyW)
	if len(h.w.layout.Panes()) != 2 {
		t.Fatalf("after closing a pane's last tab: %v", h.panes())
	}
	h.key(0, ui.KeyF6)
	h.key(ui.Cmd, ui.KeyW)
	h.key(ui.Cmd, ui.KeyW)
	if !h.f.closed {
		t.Fatal("closing the last tab does not close the window")
	}
}

func TestCopyCutPasteBetweenTabs(t *testing.T) {
	h := newHarness(t, "src/a.txt", "src/b.txt", "dst/")
	src := h.w.tabs[h.open("src")]
	dst := h.w.tabs[h.open("dst")]

	h.w.show(src.ID)
	h.settle()
	h.click("a.txt")
	h.key(ui.Cmd, ui.KeyC)
	files, _ := h.desk.data.Files()
	mark, _ := h.desk.data.Read(shell.CutFormat)
	if !reflect.DeepEqual(files, []string{h.path("src/a.txt")}) || shell.IsCut(mark) || len(mark) == 0 {
		t.Fatalf("clipboard after copy: %v, cut mark %v", files, mark)
	}
	h.w.show(dst.ID)
	h.settle()
	h.key(ui.Cmd, ui.KeyV)
	// The file is there before the tab shows it: what is waited for is
	// the tab, chosen file and all.
	h.wait("the copy, chosen in its tab", func() bool { return h.exists("dst/a.txt") && dst.sel.Has("a.txt") })
	if !h.exists("src/a.txt") {
		t.Fatal("a copy took the original away")
	}

	// Cut dims the file until it is pasted, which moves it.
	h.w.show(src.ID)
	h.settle()
	h.click("b.txt")
	h.key(ui.Cmd, ui.KeyX)
	mark, _ = h.desk.data.Read(shell.CutFormat)
	if !shell.IsCut(mark) || !h.a.cut[h.path("src/b.txt")] {
		t.Fatalf("after cut: mark %v, cut %v", mark, h.a.cut)
	}
	h.w.show(dst.ID)
	h.settle()
	h.key(ui.Cmd, ui.KeyV)
	h.wait("the move, and the source without the file", func() bool {
		return h.exists("dst/b.txt") && !h.exists("src/b.txt") && reflect.DeepEqual(h.rows(src), []string{"a.txt"})
	})
	if h.a.cut != nil {
		t.Error("the file is still cut after its paste")
	}

	// What the system's file manager cut pastes as a move here too.
	other, _ := transfer.FileData(h.path("src/a.txt"))
	_ = h.desk.write(transfer.New(append(other.Items(), transfer.NewItem(transfer.Bytes(shell.CutFormat, shell.CutData(true))))...), nil)
	h.w.show(src.ID)
	h.settle()
	h.w.newFolder(src)
	h.wait("the new folder", func() bool { return h.exists("src/New folder") })
	h.key(0, ui.KeyEscape)
	h.w.paste(h.path("src/New folder"))
	h.wait("the move into the new folder", func() bool { return h.exists("src/New folder/a.txt") && !h.exists("src/a.txt") })
}

func TestDragFilesBetweenPanes(t *testing.T) {
	h := newHarness(t, "src/a.txt", "src/b.txt", "src/c.txt", "dst/sub/")
	src := h.w.tabs[h.open("src")]
	dst := h.w.tabs[h.open("dst")]
	h.w.layout.Split(h.w.layout.Focus, dst.ID, 4) // to the right
	h.settle()
	if len(h.w.layout.Panes()) != 2 {
		t.Fatalf("panes %v", h.panes())
	}

	// Two rows chosen drag together, as files any program takes.
	h.w.show(src.ID)
	h.settle()
	h.click("a.txt")
	x, y := h.center("b.txt")
	h.tt.ClickAtWith(ui.Cmd, x, y)
	h.settle()
	// Later than a double click would come.
	h.tt.Advance(time.Second)
	h.tt.Press(x, y)
	h.tt.Move(x+20, y+12)
	data, ops, dragging := h.tt.DragData()
	if !dragging {
		t.Fatal("pressing a row and moving starts no drag")
	}
	files, _ := data.Files()
	if want := []string{h.path("src/a.txt"), h.path("src/b.txt")}; !reflect.DeepEqual(files, want) {
		t.Fatalf("dragging %v, want %v", files, want)
	}
	if ops != transfer.Copy|transfer.Move {
		t.Fatalf("the drag allows %v", ops)
	}

	// Let go over the other pane, they move into its folder: within a
	// volume, a drop moves.
	px, py := h.center("sub")
	if op := h.tt.DragDataOver(px, py+120, data, ops, transfer.None); op == transfer.None {
		t.Fatal("the other pane takes no files")
	}
	if op := h.tt.DropData(px, py+120, data, ops, transfer.None); op == transfer.None {
		t.Fatal("the drop was refused")
	}
	h.tt.Release(px, py+120)
	h.wait("the move, shown by both panes", func() bool {
		return h.exists("dst/a.txt") && h.exists("dst/b.txt") && !h.exists("src/a.txt") && !h.exists("src/b.txt") &&
			reflect.DeepEqual(h.rows(src), []string{"c.txt"}) && reflect.DeepEqual(h.rows(dst), []string{"sub", "a.txt", "b.txt"})
	})

	// On a directory's row, files go into the directory; with Ctrl, as
	// a copy.
	one, _ := transfer.FileData(h.path("src/c.txt"))
	px, py = h.center("sub")
	if op := h.tt.DropData(px, py, one, transfer.Copy|transfer.Move, transfer.Copy); op != transfer.Copy {
		t.Fatalf("a drop with Ctrl took %v", op)
	}
	h.wait("the copy", func() bool { return h.exists("dst/sub/c.txt") })
	if !h.exists("src/c.txt") {
		t.Fatal("a copy took the original away")
	}

	// On a tab's header, into its folder; on a component of the
	// address, into that folder.
	two, _ := transfer.FileData(h.path("dst/a.txt"))
	at := h.tabAt("src")
	if op := h.tt.DropData(at[0], at[1], two, transfer.Copy|transfer.Move, transfer.None); op == transfer.None {
		t.Fatal("a tab takes no files")
	}
	h.wait("the move to the tab", func() bool { return h.exists("src/a.txt") && !h.exists("dst/a.txt") })

	// Files of another program, dropped: the same.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	three, _ := transfer.FileData(outside)
	px, py = h.center("sub")
	h.tt.DropData(px, py+120, three, transfer.Copy|transfer.Move, transfer.None)
	h.wait("the drop from outside", func() bool { return h.exists("dst/outside.txt") })
}

func TestDeleteRenameCreate(t *testing.T) {
	h := newHarness(t, "one.txt", "two.txt", "keep.txt")
	tab := h.w.current()

	// Deleting for good asks first.
	h.click("one.txt")
	h.key(ui.Shift, ui.KeyDelete)
	if h.w.ask == nil || !h.exists("one.txt") {
		t.Fatal("deleting for good does not ask")
	}
	h.click("Cancel")
	if h.w.ask != nil || !h.exists("one.txt") {
		t.Fatal("Cancel deleted, or left the question open")
	}
	h.click("one.txt")
	h.key(ui.Shift, ui.KeyDelete)
	h.click("Delete")
	h.wait("the deletion", func() bool { return !h.exists("one.txt") && !slices.Contains(h.rows(tab), "one.txt") })

	// F2 renames in place, the name before its extension selected.
	h.click("two.txt")
	h.key(0, ui.KeyF2)
	h.tt.Type("renamed")
	h.key(0, ui.KeyEnter)
	h.wait("the rename, chosen in its tab", func() bool {
		return h.exists("renamed.txt") && !h.exists("two.txt") && tab.sel.Has("renamed.txt")
	})
	if l := h.desk.launches(); len(l) > 0 {
		t.Fatalf("the Enter that ends a rename also opened: %q", l)
	}

	// A new folder is made, chosen, and ready to be named.
	h.key(ui.Cmd|ui.Shift, ui.KeyN)
	h.wait("the new folder", func() bool { return h.exists("New folder") && slices.Contains(h.rows(tab), "New folder") })
	h.tt.Type("made here")
	h.key(0, ui.KeyEnter)
	h.wait("its name", func() bool {
		return h.exists("made here") && !h.exists("New folder") && slices.Contains(h.rows(tab), "made here")
	})

	// A file made by another program shows up by itself.
	if err := os.WriteFile(h.path("appeared.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.wait("the file another program made", func() bool { return slices.Contains(h.rows(tab), "appeared.txt") })
}

func TestContextMenus(t *testing.T) {
	h := newHarness(t, "alpha/", "one.txt")
	if err := h.tt.RightClick("one.txt"); err != nil {
		t.Fatal(err)
	}
	menu := h.tt.Menu()
	for _, want := range []string{"Open", "Cut", "Copy", "Copy Path", "Rename", "Delete", "Properties"} {
		if !slices.ContainsFunc(menu, func(s string) bool { return strings.HasPrefix(s, want) }) {
			t.Errorf("the menu of a file has no %q: %q", want, menu)
		}
	}
	if err := h.tt.ChooseMenuItem("Copy Path"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if h.desk.text != h.path("one.txt") {
		t.Errorf("Copy Path copied %q", h.desk.text)
	}

	// A file opens with one of the programs for its type, listed in a
	// submenu, or with one chosen in the system's dialog.
	if err := h.tt.RightClick("one.txt"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if err := h.tt.ChooseMenuItem("Open With", "Editor"); err != nil {
		t.Fatalf("%v: %q", err, h.tt.Menu())
	}
	h.wait("the file to open", func() bool {
		l := h.desk.launches()
		return slices.Contains(l, "open "+h.path("one.txt")+" in editor.exe")
	})
	if err := h.tt.RightClick("one.txt"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if err := h.tt.ChooseMenuItem("Open With", "Choose Another App…"); err != nil {
		t.Fatalf("%v: %q", err, h.tt.Menu())
	}
	h.wait("the dialog", func() bool {
		return slices.Contains(h.desk.launches(), "open with "+h.path("one.txt"))
	})

	// What the system and other programs have for the file is under
	// More, drawn here: without what the menu has itself, with its
	// submenus, and run when chosen. The system's menu is read as the
	// menu opens, not before.
	h.desk.mu.Lock()
	h.desk.menus = 0
	h.desk.mu.Unlock()
	h.settle()
	if h.desk.menus != 0 {
		t.Fatalf("the system's menu was read %d times with no menu open", h.desk.menus)
	}
	if err := h.tt.RightClick("one.txt"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if err := h.tt.ChooseMenuItem("More", "Compress", "Add to Archive"); err != nil {
		t.Fatalf("%v: %q", err, h.tt.Menu())
	}
	h.wait("the command", func() bool {
		return slices.Contains(h.desk.launches(), "command 10 of "+h.path("one.txt"))
	})
	// The one read moments ago for the same file may still serve.
	if h.desk.menus > 1 {
		t.Errorf("the system's menu was read %d times for one menu", h.desk.menus)
	}
	if got := tidy(h.a.sysIcons((&fakeMenu{}).Items())()); len(got) != 2 || got[0].Text != "Compress" || got[1].Text != "Scan for Viruses" {
		t.Errorf("the system's items under More are %+v", got)
	}

	// A directory opens in a tab of its own, or in a pane beside.
	if err := h.tt.RightClick("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("Open in Split Pane"); err != nil {
		t.Fatalf("%v: %q", err, h.tt.Menu())
	}
	h.settle()
	if got := h.panes(); !reflect.DeepEqual(got, [][]string{{filepath.Base(h.root)}, {"alpha"}}) {
		t.Fatalf("after Open in Split Pane: %v", got)
	}
}

// TestEmptyRoom checks what a press on the room beside the entries does:
// it lets go of what was chosen, and dragged it chooses the entries its
// rectangle touches, added to those chosen when Ctrl is held. A press on
// an entry stays the entry's.
func TestEmptyRoom(t *testing.T) {
	for _, mode := range []viewMode{viewDetails, viewList, viewIcons} {
		t.Run(viewSpecs[mode].id, func(t *testing.T) {
			h := newHarness(t, "alpha/", "beta/", "one.txt", "two.txt")
			tab := h.w.current()
			h.w.setView(tab, mode)
			h.settle()
			chosen := func() []string {
				var names []string
				for name := range tab.sel.All() {
					names = append(names, name)
				}
				slices.Sort(names)
				return names
			}
			h.click("beta")
			if got := chosen(); !slices.Equal(got, []string{"beta"}) {
				t.Fatalf("a click on an entry chose %v", got)
			}
			// The room below the entries, well inside the view.
			const ex, ey = 700, 480
			h.tt.ClickAt(ex, ey)
			h.settle()
			if got := chosen(); len(got) != 0 {
				t.Errorf("a click on empty room left %v chosen", got)
			}

			// A rectangle from the empty room up over the first entries.
			ax, ay := h.center("alpha")
			bx, by := h.center("beta")
			h.drag([2]float32{ex, ey}, [2]float32{ex - 40, ey - 40}, [2]float32{min(ax, bx) - 2, min(ay, by) - 2})
			if got := chosen(); !slices.Contains(got, "alpha") || !slices.Contains(got, "beta") {
				t.Errorf("a rectangle over alpha and beta chose %v", got)
			}
			// One that shrinks lets go of what it touches no more.
			h.tt.Press(ex, ey)
			h.tt.Move(ex-40, ey-40)
			h.tt.Move(min(ax, bx)-2, min(ay, by)-2)
			h.settle()
			h.tt.Move(ex-20, ey-20)
			h.settle()
			if got := chosen(); len(got) != 0 {
				t.Errorf("the rectangle drawn back still has %v chosen", got)
			}
			h.tt.Release(ex-20, ey-20)
			h.settle()

			// With Ctrl, what was chosen stays.
			h.click("two.txt")
			h.tt.ClickAtWith(ui.Ctrl, ex, ey)
			h.settle()
			if got := chosen(); !slices.Equal(got, []string{"two.txt"}) {
				t.Errorf("Ctrl and a click on empty room left %v chosen", got)
			}
		})
	}
}

// TestSettingsTab checks that the settings show in a tab of their own,
// one for the window, which the keys of folders leave alone.
func TestSettingsTab(t *testing.T) {
	h := newHarness(t, "alpha/", "one.txt")
	h.key(ui.Cmd, ui.KeyComma)
	tab := h.w.current()
	if tab == nil || !tab.settings || len(h.w.tabs) != 2 {
		t.Fatalf("Ctrl+, shows %+v among %d tabs", tab, len(h.w.tabs))
	}
	for _, want := range []string{"Settings", "Default file manager", "Show hidden files", "Theme"} {
		if _, ok := h.tt.Find(want); !ok {
			t.Errorf("the settings tab shows no %q; texts %q", want, h.tt.Texts())
		}
	}
	if got := h.panes(); !reflect.DeepEqual(got, [][]string{{filepath.Base(h.root), "Settings"}}) {
		t.Errorf("the tabs are %v", got)
	}
	// A folder's keys do nothing to it, and what they would open opens
	// nowhere.
	h.key(ui.Alt, ui.KeyUp)
	h.key(0, ui.KeyF5)
	h.key(ui.Cmd|ui.Shift, ui.KeyN)
	h.key(ui.Cmd, ui.KeyBackslash)
	if cur := h.w.current(); cur != tab || len(h.w.tabs) != 2 || h.exists("New folder") {
		t.Errorf("a folder's keys changed the settings tab: %v", h.panes())
	}
	// Asked for again, from the folder's tab, the same tab shows.
	h.key(ui.Cmd, ui.Key1)
	if h.w.current() == tab {
		t.Fatal("Ctrl+1 did not show the folder's tab")
	}
	h.key(ui.Cmd, ui.KeyComma)
	if h.w.current() != tab || len(h.w.tabs) != 2 {
		t.Errorf("Ctrl+, again shows %v", h.panes())
	}
	// A setting changed there holds.
	h.click("Show hidden files")
	if !h.a.set.ShowHidden {
		t.Error("the check box of the settings tab did not change the setting")
	}
	h.key(ui.Cmd, ui.KeyW)
	if len(h.w.tabs) != 1 || h.w.current().settings {
		t.Errorf("Ctrl+W left %v", h.panes())
	}
}

func TestDrives(t *testing.T) {
	h := newHarness(t, "one.txt")
	tab := h.w.current()
	h.w.navigate(tab, fsys.Computer, navPush, "")
	h.settle()
	if tab.Path != fsys.Computer || len(tab.view) == 0 {
		t.Fatalf("This PC shows %v", h.rows(tab))
	}
	// A drive opens as a folder, and Up comes back with it chosen.
	first := tab.entry(0).Name
	h.key(0, ui.KeyDown)
	h.key(0, ui.KeyEnter)
	if tab.Path != first {
		t.Fatalf("opening %s went to %s", first, tab.Path)
	}
	h.key(ui.Alt, ui.KeyUp)
	if tab.Path != fsys.Computer || !tab.sel.Has(first) {
		t.Fatalf("up from a drive: %s, chosen %v", tab.Path, tab.sel.Has(first))
	}
}

func TestViews(t *testing.T) {
	h := newHarness(t, "alpha/inner.txt", "beta/", "one.txt", "two.txt")
	tab := h.w.current()
	keys := []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6}
	for mode := viewList; mode < viewModes; mode++ {
		h.key(ui.Cmd|ui.Shift, keys[mode])
		if tab.mode != mode {
			t.Fatalf("Ctrl+Shift+%d shows view %d", mode+1, tab.mode)
		}
		for _, name := range []string{"alpha", "beta", "one.txt", "two.txt"} {
			if !h.tt.HasText(name) {
				t.Fatalf("%s: no %q on screen: %q", viewSpecs[mode].id, name, h.tt.Texts())
			}
		}
		// A click chooses, the arrows move, and Enter opens.
		h.click("one.txt")
		if !tab.sel.Has("one.txt") || tab.sel.Len() != 1 {
			t.Fatalf("%s: a click chose %d items", viewSpecs[mode].id, tab.sel.Len())
		}
		h.doubleClick("alpha")
		if tab.Path != h.path("alpha") {
			t.Fatalf("%s: a double click on a folder went to %s", viewSpecs[mode].id, tab.Path)
		}
		if tab.mode != mode {
			t.Fatalf("%s: the view changed with the folder", viewSpecs[mode].id)
		}
		h.key(0, ui.KeyBackspace)
		if tab.Path != h.root || !tab.sel.Has("alpha") {
			t.Fatalf("%s: Backspace went to %s", viewSpecs[mode].id, tab.Path)
		}
		h.tt.Advance(time.Second)
	}

	// In a grid: copy, a menu on an item not chosen, renaming, a drag.
	h.key(ui.Cmd|ui.Shift, ui.Key4)
	h.click("two.txt")
	h.key(ui.Cmd, ui.KeyC)
	if files, _ := h.desk.data.Files(); !reflect.DeepEqual(files, []string{h.path("two.txt")}) {
		t.Fatalf("copied %v", files)
	}
	if err := h.tt.RightClick("one.txt"); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("Copy Path"); err != nil {
		t.Fatalf("%v: %q", err, h.tt.Menu())
	}
	h.settle()
	if h.desk.text != h.path("one.txt") || !tab.sel.Has("one.txt") || tab.sel.Len() != 1 {
		t.Fatalf("the menu acted on %q, chosen %d", h.desk.text, tab.sel.Len())
	}
	h.tt.Advance(time.Second)
	h.click("two.txt")
	h.key(0, ui.KeyF2)
	if tab.renaming != "two.txt" {
		t.Fatalf("F2 renames %q", tab.renaming)
	}
	h.tt.Type("second")
	h.key(0, ui.KeyEnter)
	h.wait("the rename", func() bool { return h.exists("second.txt") && !h.exists("two.txt") })
	if l := h.desk.launches(); len(l) > 0 {
		t.Fatalf("the Enter that ends a rename also opened: %q", l)
	}
	// Escape gives a rename up, and Backspace in it goes nowhere.
	h.tt.Advance(time.Second)
	h.click("one.txt")
	h.key(0, ui.KeyF2)
	h.key(0, ui.KeyBackspace)
	h.key(0, ui.KeyEscape)
	if tab.renaming != "" || tab.Path != h.root || !h.exists("one.txt") {
		t.Fatalf("after Escape: renaming %q, at %s", tab.renaming, tab.Path)
	}

	h.tt.Advance(time.Second)
	x, y := h.center("one.txt")
	h.tt.Press(x, y)
	h.tt.Move(x+20, y+12)
	data, ops, dragging := h.tt.DragData()
	files, _ := data.Files()
	if !dragging || ops != transfer.Copy|transfer.Move || !reflect.DeepEqual(files, []string{h.path("one.txt")}) {
		t.Fatalf("dragging %v: %v, %v", dragging, files, ops)
	}
	// On a folder's item, it goes into the folder.
	bx, by := h.center("beta")
	if op := h.tt.DropData(bx, by, data, ops, transfer.None); op == transfer.None {
		t.Fatal("a folder's item takes no files")
	}
	h.tt.Release(bx, by)
	h.wait("the move", func() bool { return h.exists("beta/one.txt") && !h.exists("one.txt") })

	// The view is a tab's own, and new tabs take the last one chosen.
	id := h.open("alpha")
	if h.w.tabs[id].mode != viewIcons {
		t.Fatalf("a new tab shows view %d", h.w.tabs[id].mode)
	}
	h.key(ui.Cmd|ui.Shift, ui.Key1)
	if h.w.tabs[id].mode != viewDetails || tab.mode != viewIcons {
		t.Fatalf("views after Ctrl+Shift+1: %d and %d", h.w.tabs[id].mode, tab.mode)
	}
}

func TestUnreadableFolder(t *testing.T) {
	h := newHarness(t, "alpha/")
	tab := h.w.current()
	h.w.navigate(tab, h.path("gone"), navPush, "")
	h.settle()
	if tab.err == nil || !h.tt.HasText("This folder can't be shown") {
		t.Fatalf("a folder that is not there shows %q", h.tt.Texts())
	}
	// Back returns from it, and a folder deleted while shown gives way
	// to the one above it.
	h.key(ui.Alt, ui.KeyLeft)
	if tab.Path != h.root || tab.err != nil {
		t.Fatalf("back went to %s (%v)", tab.Path, tab.err)
	}
	h.doubleClick("alpha")
	if err := os.Remove(h.path("alpha")); err != nil {
		t.Fatal(err)
	}
	h.wait("the folder above", func() bool { return tab.Path == h.root && !slices.Contains(h.rows(tab), "alpha") })
}

func TestWheelChangesView(t *testing.T) {
	h := newHarness(t, "alpha/", "one.txt")
	tab := h.w.current()
	x, y := h.center("one.txt")
	h.tt.WheelWith(ui.Ctrl, x, y+100, 0, -120)
	h.settle()
	if tab.mode != viewList {
		t.Fatalf("Ctrl and the wheel up show view %d", tab.mode)
	}
	h.tt.WheelWith(ui.Ctrl, x, y+100, 0, -120)
	h.tt.WheelWith(ui.Ctrl, x, y+100, 0, 120)
	h.tt.WheelWith(ui.Ctrl, x, y+100, 0, 120)
	h.settle()
	if tab.mode != viewDetails {
		t.Fatalf("and back down, view %d", tab.mode)
	}
	// Without Ctrl the wheel scrolls, and the view stays.
	h.tt.Wheel(x, y+100, 0, -120)
	h.settle()
	if tab.mode != viewDetails {
		t.Fatalf("the wheel alone changed the view to %d", tab.mode)
	}
}

// TestUpdateButton checks that a newer version is downloaded unasked and
// then offered, at the bottom of the sidebar or, without it, of the
// window: the button starts the program again. Nothing shows before, nor
// when the settings say not to look.
func TestUpdateButton(t *testing.T) {
	h := newHarness(t, "one.txt")
	h.a.set.AutoUpdate = false
	h.desk.newer = "9.9.9"
	h.a.checkUpdate()
	h.settle()
	if _, ok := h.tt.Find("Restart to Update"); ok || h.a.updateReady != "" {
		t.Fatal("an update was looked for with the setting off")
	}
	h.a.set.AutoUpdate = true
	h.a.checkUpdate()
	h.wait("the update", func() bool { return h.a.updateReady == "9.9.9" })
	if l := h.desk.launches(); !slices.Equal(l, []string{"install 9.9.9"}) {
		t.Fatalf("the update did %q", l)
	}
	for _, sidebar := range []bool{false, true} {
		h.a.set.Sidebar = sidebar
		h.tt.Frame()
		h.settle()
		r, ok := h.tt.Find("Restart to Update")
		if !ok || r.Y < 500 || r.X > 200 {
			t.Fatalf("with the sidebar %v, the button is at %v (found %v)", sidebar, r, ok)
		}
	}
	// Looked for again, the version in place is not downloaded twice.
	h.a.checkUpdate()
	h.settle()
	if l := h.desk.launches(); len(l) != 0 {
		t.Errorf("a second check did %q", l)
	}
	h.click("Restart to Update")
	if l := h.desk.launches(); !slices.Equal(l, []string{"relaunch"}) {
		t.Errorf("the button did %q", l)
	}
}

// TestBandEndsWithThePress checks that the rectangle a press on empty room
// draws is gone once the press is: a click, on an entry or beside them,
// leaves none following the pointer.
func TestBandEndsWithThePress(t *testing.T) {
	for _, mode := range []viewMode{viewDetails, viewList, viewIcons} {
		t.Run(viewSpecs[mode].id, func(t *testing.T) {
			h := newHarness(t, "alpha/", "beta/", "one.txt", "two.txt")
			tab := h.w.current()
			h.w.setView(tab, mode)
			h.settle()
			x, y := h.center("beta")
			for _, at := range [][2]float32{{700, 480}, {x, y}, {x + 300, y}, {880, y}} {
				h.tt.ClickAt(at[0], at[1])
				h.settle()
				h.tt.Move(at[0]-120, at[1]+60)
				h.tt.Move(at[0]-200, at[1]+90)
				h.settle()
				// A press whose release never came, as one the system took
				// for a drag: the pointer moving free ends it.
				tab.band = band{on: true, active: true, x0: 10, y0: 10, base: map[string]bool{}, hit: map[string]bool{}}
				h.tt.Move(at[0]-100, at[1]+40)
				h.settle()
				if tab.band.on || tab.band.active {
					t.Errorf("after a click at %v the rectangle follows the pointer (on %v, active %v)", at, tab.band.on, tab.band.active)
					tab.band = band{}
				}
			}
		})
	}
}

// TestSidebar checks the sidebar: the folders pinned, taken once from the
// system's file manager and then the user's to change; the folders used
// lately; and the tree, whose nodes read what they hold as they open and
// follow the folders made in them.
func TestSidebar(t *testing.T) {
	h := newHarness(t, "alpha/deep/", "alpha/other/", "beta/", "gamma/", "one.txt")
	a := h.a
	a.set.Sidebar = true
	h.desk.quick = []shell.QuickFolder{
		{Path: h.path("beta"), Pinned: true}, {Path: shell.RecycleBin, Pinned: true}, {Path: h.path("gamma")},
	}
	h.desk.linux = []string{"Arch"}
	a.importPins()
	h.wait("the pins of the system's file manager", func() bool {
		return slices.Equal(a.set.Pinned, []string{h.path("beta"), shell.RecycleBin})
	})
	// What it lists besides is where the recent folders start.
	if got := a.recentFolders(); !slices.Equal(got, []string{h.path("gamma")}) {
		t.Errorf("the recent folders start as %v", got)
	}
	// Asked again, as at the next start, the pins are the user's.
	h.desk.quick = nil
	a.unpin(shell.RecycleBin)
	a.importPins()
	h.settle()
	if !slices.Equal(a.set.Pinned, []string{h.path("beta")}) {
		t.Fatalf("a second import changed the pins: %v", a.set.Pinned)
	}

	// Pinned from a folder's menu, and moved before the other.
	if err := h.tt.RightClick("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("Pin to Sidebar"); err != nil {
		t.Fatalf("%v: %q", err, h.tt.Menu())
	}
	h.settle()
	a.movePin(h.path("alpha"), h.path("beta"), false)
	if !slices.Equal(a.set.Pinned, []string{h.path("alpha"), h.path("beta")}) {
		t.Fatalf("the pins are %v", a.set.Pinned)
	}

	// A folder shown counts as recent, unless it is pinned.
	tab := h.w.current()
	h.w.navigate(tab, h.path("alpha"), navPush, "")
	h.settle()
	h.w.navigate(tab, h.path("alpha/deep"), navPush, "")
	h.settle()
	recent := a.recentFolders()
	if !slices.Contains(recent, h.path("alpha/deep")) || slices.Contains(recent, h.path("alpha")) {
		t.Errorf("after showing alpha and alpha/deep, the recent folders are %v", recent)
	}
	a.forgetRecent(h.path("alpha/deep"))
	if slices.Contains(a.recentFolders(), h.path("alpha/deep")) {
		t.Error("a folder taken out of the recent ones is still there")
	}

	// A node of the tree reads its folders as it opens, and not before.
	alpha := h.path("alpha")
	if a.side.kids[alpha] != nil && a.side.kids[alpha].loaded {
		t.Fatal("the tree read a folder no node of it shows")
	}
	a.toggle(alpha)
	h.wait("the folders of the node", func() bool {
		k := a.side.kids[alpha]
		return k != nil && k.loaded && slices.Equal(k.paths, []string{h.path("alpha/deep"), h.path("alpha/other")})
	})
	// A folder made in it shows in the tree once a tab read the folder.
	if err := os.Mkdir(h.path("alpha/new"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.w.navigate(tab, alpha, navPush, "")
	h.wait("the new folder in the tree", func() bool { return slices.Contains(a.side.kids[alpha].paths, h.path("alpha/new")) })
	a.toggle(alpha)
	if a.side.open[alpha] || len(a.set.Expanded) != 0 {
		t.Error("the node closed is still kept open")
	}

	// The network's computers are no folders: their shares are.
	if !isComputer(`\\NAS`) || isComputer(`\\NAS\public\`) || holdsFolders(`\\NAS`) || !holdsFolders(`\\NAS\public\`) || holdsFolders(shell.RecycleBin) {
		t.Error("computers, shares and places are told apart wrongly")
	}
	shares, err := a.kids(`\\NAS`, false)
	if err != nil || !slices.Equal(shares, []string{`\\NAS\public\`}) {
		t.Errorf("a computer holds %v, %v", shares, err)
	}
	if got := treeName(`\\NAS\public\`); got != "public" {
		t.Errorf("a share is called %q", got)
	}
	if got := treeName(`\\wsl.localhost\Arch\`); got != "Arch" {
		t.Errorf("a distribution is called %q", got)
	}
	// The Recycle Bin, pinned, shows in the tab as a folder does.
	a.pin(shell.RecycleBin)
	h.settle()
	h.w.navigate(tab, shell.RecycleBin, navPush, "")
	h.wait("the Recycle Bin in the tab", func() bool { return tab.bin() && !tab.loading })
	if slices.Contains(a.recentFolders(), shell.RecycleBin) {
		t.Error("the Recycle Bin counts as a recent folder")
	}
}

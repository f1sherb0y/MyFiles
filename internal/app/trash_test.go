package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"myfiles/internal/shell"
)

// binOf fills the Recycle Bin of a test and shows it in the tab.
func binOf(h *harness, items ...shell.TrashItem) *Tab {
	h.t.Helper()
	h.desk.mu.Lock()
	h.desk.bin = items
	h.desk.mu.Unlock()
	tab := h.w.current()
	h.w.navigate(tab, shell.RecycleBin, navPush, "")
	h.wait("the Recycle Bin", func() bool { return tab.bin() && !tab.loading && len(tab.view) == len(items) })
	return tab
}

// shown lists the names the Recycle Bin shows, in order.
func shown(tab *Tab) []string {
	names := make([]string, len(tab.view))
	for i, at := range tab.view {
		names[i] = tab.trash[at].Name
	}
	return names
}

func TestRecycleBin(t *testing.T) {
	h := newHarness(t, "kept.txt")
	const kept = `C:\$Recycle.Bin\S-1\`
	day := int64(24 * time.Hour)
	tab := binOf(h,
		shell.TrashItem{Name: "notes.txt", From: `C:\work`, Path: kept + "$RAAA.txt", Deleted: 3 * day, Size: 300},
		shell.TrashItem{Name: "notes.txt", From: `D:\other`, Path: kept + "$RBBB.txt", Deleted: 1 * day, Size: 100},
		shell.TrashItem{Name: "album", From: `C:\photos`, Path: kept + "$RCCC", Deleted: 2 * day, Size: 9000, Dir: true},
		shell.TrashItem{Name: "zebra.png", From: `C:\photos`, Path: kept + "$RDDD.png", Deleted: 4 * day, Size: 50},
	)

	// It shows as a folder does: what was deleted by the names it had,
	// where it was, and the tab named after the bin.
	if got := shown(tab); !slices.Equal(got, []string{"album", "notes.txt", "notes.txt", "zebra.png"}) {
		t.Fatalf("the Recycle Bin shows %v", got)
	}
	if tab.title() != "Recycle Bin" {
		t.Errorf("the tab is called %q", tab.title())
	}
	texts := h.tt.Texts()
	for _, want := range []string{"zebra.png", `D:\other`, "Date deleted", "Original location"} {
		if !slices.Contains(texts, want) {
			t.Errorf("no %q on screen: %q", want, texts)
		}
	}

	// Ordered by when it was deleted, and narrowed by the names it had,
	// not those it is kept under.
	tab.sort = ui.SortOrder{Column: colModified, Descending: true}
	tab.review(false, true)
	if got := shown(tab); !slices.Equal(got, []string{"zebra.png", "notes.txt", "album", "notes.txt"}) {
		t.Errorf("by date deleted, newest first: %v", got)
	}
	tab.sort = ui.SortOrder{}
	tab.filter = "$R"
	tab.review(false, true)
	if len(tab.view) != 0 {
		t.Errorf("the filter matched the names things are kept under: %v", shown(tab))
	}
	tab.filter = "NOTES"
	tab.review(false, true)
	if got := shown(tab); !slices.Equal(got, []string{"notes.txt", "notes.txt"}) {
		t.Errorf("filtered by notes: %v", got)
	}
	tab.filter = ""
	tab.review(false, true)
	h.settle()

	// Nothing there opens, is copied, or takes what is pasted.
	h.doubleClick("zebra.png")
	h.key(0, ui.KeyEnter)
	h.key(ui.Cmd, ui.KeyC)
	h.key(ui.Cmd, ui.KeyV)
	h.key(ui.Cmd|ui.Shift, ui.KeyN)
	h.key(0, ui.KeyF2)
	if l := h.desk.launches(); len(l) > 0 || tab.renaming != "" || !tab.bin() {
		t.Fatalf("in the Recycle Bin something opened, or was renamed: %q", l)
	}

	// Its menu puts back, deletes, and copies where the thing was.
	if err := h.tt.RightClick("zebra.png"); err != nil {
		t.Fatal(err)
	}
	menu := h.tt.Menu()
	for _, no := range []string{"Open", "Cut", "Copy", "Rename", "Properties", "More"} {
		if slices.ContainsFunc(menu, func(s string) bool { return s == no || strings.HasPrefix(s, no+"\t") }) {
			t.Errorf("the menu of something deleted has %q: %q", no, menu)
		}
	}
	if err := h.tt.ChooseMenuItem("Copy Path"); err != nil {
		t.Fatalf("%v: %q", err, menu)
	}
	h.settle()
	if h.desk.text != `C:\photos\zebra.png` {
		t.Errorf("Copy Path copied %q", h.desk.text)
	}
	if err := h.tt.RightClick("zebra.png"); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("Restore"); err != nil {
		t.Fatal(err)
	}
	h.wait("the restore", func() bool { return len(tab.view) == 3 && !slices.Contains(shown(tab), "zebra.png") })
	if l := h.desk.launches(); !slices.Equal(l, []string{"restore " + kept + "$RDDD.png"}) {
		t.Errorf("Restore did %q", l)
	}

	// Deleting there is for good, so it asks, and Cancel leaves it.
	h.click("album")
	h.key(0, ui.KeyDelete)
	if h.w.ask == nil {
		t.Fatal("deleting from the Recycle Bin does not ask")
	}
	if !strings.Contains(h.w.ask.title, "album") {
		t.Errorf("the question names the thing as kept: %q", h.w.ask.title)
	}
	h.click("Cancel")
	h.settle()
	if l := h.desk.launches(); len(l) > 0 || len(tab.view) != 3 {
		t.Fatalf("Cancel deleted: %q", l)
	}
	h.click("album")
	h.key(0, ui.KeyDelete)
	h.click("Delete")
	h.wait("the deletion", func() bool { return len(tab.view) == 2 })
	if l := h.desk.launches(); !slices.Equal(l, []string{"delete for good " + kept + "$RCCC"}) {
		t.Errorf("Delete did %q", l)
	}

	// Emptied from the menu of the room beside the items, once confirmed.
	h.w.emptyTrash(tab)
	h.settle()
	if h.w.ask == nil || !strings.Contains(h.w.ask.title, "2") {
		t.Fatalf("emptying does not ask about the 2 items: %+v", h.w.ask)
	}
	h.click("Empty Recycle Bin")
	h.wait("the emptying", func() bool { return len(tab.entries) == 0 })
	if l := h.desk.launches(); !slices.Equal(l, []string{"empty the bin"}) {
		t.Errorf("emptying did %q", l)
	}
	if !slices.Contains(h.tt.Texts(), "The Recycle Bin is empty") {
		t.Errorf("an empty bin does not say so: %q", h.tt.Texts())
	}

	// Up, and back: the bin is a place in the tab's history.
	h.w.up(tab)
	h.settle()
	if tab.bin() {
		t.Error("up from the Recycle Bin stays there")
	}
	h.w.goBack(tab)
	h.wait("back in the Recycle Bin", func() bool { return tab.bin() && !tab.loading })
}

// What goes to the Recycle Bin shows in a tab that has it open.
func TestRecycleBinFollowsDeletes(t *testing.T) {
	h := newHarness(t, "one.txt")
	id := h.w.newTab(h.w.layout.Focus, shell.RecycleBin, "")
	h.settle()
	bin := h.w.tabs[id]
	if !bin.bin() || len(bin.view) != 0 {
		t.Fatalf("a new tab on the Recycle Bin shows %v", shown(bin))
	}
	h.desk.mu.Lock()
	h.desk.bin = []shell.TrashItem{{Name: "one.txt", From: h.root, Path: `C:\$Recycle.Bin\S-1\$RONE.txt`}}
	h.desk.mu.Unlock()
	// As after a delete in the other tab.
	h.a.refreshTrash()
	h.wait("the deleted file in the bin's tab", func() bool { return slices.Equal(shown(bin), []string{"one.txt"}) })
	h.desk.launches()
}

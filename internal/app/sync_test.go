package app

import (
	"os"
	"slices"
	"testing"

	"myfiles/internal/shell"
)

// Tabs do not hold the folders they show: a folder with a tab inside it
// can be renamed, and the tabs go where their folders went.
func TestTabsFollowRenames(t *testing.T) {
	h := newHarness(t, "alpha/deep/x.txt", "alpha/deep/y.txt", "alpha/other/", "beta/")
	first := h.w.current()
	alpha := h.w.tabs[h.open("alpha")]
	deep := h.w.tabs[h.open("alpha/deep")]
	h.w.navigate(deep, h.path("alpha/deep"), navPush, "")
	h.settle()
	// Somewhere to come back from, and a choice to keep.
	h.w.navigate(alpha, h.path("alpha/other"), navPush, "")
	h.settle()
	h.w.navigate(alpha, h.path("alpha"), navPush, "")
	h.settle()
	deep.sel.Add("y.txt")

	// With a tab in alpha and one in alpha\deep, alpha is renamed.
	if err := os.Rename(h.path("alpha"), h.path("Renamed")); err != nil {
		t.Fatalf("a folder with tabs in it cannot be renamed: %v", err)
	}
	h.wait("the tabs in the folder renamed", func() bool {
		return alpha.Path == h.path("Renamed") && deep.Path == h.path("Renamed/deep") && !alpha.loading && !deep.loading
	})
	if alpha.err != nil || deep.err != nil {
		t.Fatalf("after the rename: %v, %v", alpha.err, deep.err)
	}
	if !slices.Equal(h.rows(deep), []string{"x.txt", "y.txt"}) || !deep.sel.Has("y.txt") {
		t.Errorf("the tab that followed shows %v, with y.txt chosen: %v", h.rows(deep), deep.sel.Has("y.txt"))
	}
	if alpha.title() != "Renamed" {
		t.Errorf("the tab is called %q", alpha.title())
	}
	h.wait("the tab of the folder above", func() bool { return slices.Contains(h.rows(first), "Renamed") })
	// The way back leads where the folder is now.
	if len(alpha.back) == 0 || alpha.back[len(alpha.back)-1].path != h.path("Renamed/other") {
		t.Errorf("the way back leads to %+v", alpha.back)
	}
	// And the folder is watched under its new name.
	if err := os.WriteFile(h.path("Renamed/deep/z.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h.wait("a file made under the new name", func() bool { return slices.Contains(h.rows(deep), "z.txt") })

	// The folder shown itself, renamed.
	if err := os.Rename(h.path("Renamed/deep"), h.path("Renamed/deeper")); err != nil {
		t.Fatal(err)
	}
	h.wait("the tab of the folder renamed", func() bool {
		return deep.Path == h.path("Renamed/deeper") && slices.Contains(h.rows(alpha), "deeper") && !slices.Contains(h.rows(alpha), "deep")
	})

	// Renamed twice with nothing between, above and itself: followed
	// both times.
	if err := os.Rename(h.path("Renamed"), h.path("Again")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(h.path("Again/deeper"), h.path("Again/deepest")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(h.path("Again"), h.path("Renamed")); err != nil {
		t.Fatal(err)
	}
	h.wait("the tabs of folders renamed in a row", func() bool {
		return alpha.Path == h.path("Renamed") && deep.Path == h.path("Renamed/deepest") && !alpha.loading && !deep.loading &&
			deep.err == nil && slices.Contains(h.rows(deep), "z.txt") && slices.Contains(h.rows(alpha), "deepest")
	})
	if err := os.WriteFile(h.path("Renamed/deepest/last.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h.wait("a file made after the renames", func() bool { return slices.Contains(h.rows(deep), "last.txt") })

	// Deleted from above: both tabs come back to what is left.
	if err := os.RemoveAll(h.path("Renamed")); err != nil {
		t.Fatal(err)
	}
	h.wait("the tabs of folders deleted", func() bool {
		return alpha.Path == h.root && deep.Path == h.root && !slices.Contains(h.rows(first), "Renamed")
	})
}

// Two tabs on one folder show the same, whichever the change came from.
func TestTabsOnOneFolder(t *testing.T) {
	h := newHarness(t, "shared/one.txt", "shared/two.txt")
	a := h.w.tabs[h.open("shared")]
	b := h.w.tabs[h.open("shared")]
	b.sel.Add("one.txt")
	b.sel.Add("two.txt")
	if err := os.WriteFile(h.path("shared/three.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.path("shared/one.txt")); err != nil {
		t.Fatal(err)
	}
	want := []string{"three.txt", "two.txt"}
	h.wait("both tabs", func() bool { return slices.Equal(h.rows(a), want) && slices.Equal(h.rows(b), want) })
	if b.sel.Has("one.txt") || !b.sel.Has("two.txt") {
		t.Error("the choice of the other tab kept what is gone, or lost what is there")
	}
	// The Recycle Bin is no folder to watch, and is none the worse.
	bin := h.w.tabs[h.w.newTab(h.w.layout.Focus, shell.RecycleBin, "")]
	h.settle()
	if !bin.bin() || bin.err != nil {
		t.Fatalf("the Recycle Bin: %v", bin.err)
	}
}

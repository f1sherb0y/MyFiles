package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"myfiles/internal/fsys"
)

// candidateNames are the candidates a tab's address shows, in order.
func candidateNames(t *Tab) []string {
	if t.cands == nil {
		return nil
	}
	var names []string
	for _, c := range t.cands.shown {
		names = append(names, c.name)
	}
	return names
}

// typeAddress types a text in place of the address.
func (h *harness) typeAddress(text string) {
	h.t.Helper()
	h.key(ui.Cmd, ui.KeyL)
	h.tt.Type(text)
	h.settle()
}

func TestAddressCandidates(t *testing.T) {
	h := newHarness(t, "alpha/index/", "alpha/inner/deep/", "alpha/other/", "alpha/in.txt", "almond/", "beta/", "one.txt")
	tab := h.w.current()
	sep := string(filepath.Separator)
	same := func(what string, want ...string) {
		t.Helper()
		if got := candidateNames(tab); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("%s: candidates %q, want %q", what, got, want)
		}
	}

	// Nothing until the address is typed in.
	h.key(ui.Cmd, ui.KeyL)
	same("the address as it opens")

	// The folders of the folder typed whose names begin with what follows
	// it, files left out, whatever the case typed.
	h.tt.Type(h.path("alpha") + sep + "IN")
	h.settle()
	same("a beginning", "index", "inner")
	if !h.tt.HasText("inner") {
		t.Fatalf("the candidates do not show; texts %q", h.tt.Texts())
	}
	// Enter with none chosen goes to what was typed, which is not there.
	h.key(0, ui.KeyEnter)
	if tab.editing || tab.Path != h.root || tab.cands != nil {
		t.Fatalf("after Enter on the typed text: editing %v, path %s", tab.editing, tab.Path)
	}

	// Down goes among them, and Enter to the one it is on.
	h.typeAddress(h.path("alpha") + sep + "in")
	h.key(0, ui.KeyDown)
	h.key(0, ui.KeyDown)
	h.key(0, ui.KeyDown)
	if tab.cands.at != 1 {
		t.Fatalf("after three Downs among two: at %d", tab.cands.at)
	}
	h.key(0, ui.KeyUp)
	h.key(0, ui.KeyEnter)
	if tab.editing || tab.Path != h.path("alpha/index") {
		t.Fatalf("after Enter on a candidate: editing %v, path %s", tab.editing, tab.Path)
	}
	// Up from the first is back at the text typed.
	h.typeAddress(h.path("alpha") + sep + "in")
	h.key(0, ui.KeyDown)
	h.key(0, ui.KeyUp)
	if tab.cands.at != -1 {
		t.Fatalf("after Down and Up: at %d", tab.cands.at)
	}
	h.key(0, ui.KeyEscape)
	if tab.editing || tab.cands != nil || tab.Path != h.path("alpha/index") {
		t.Fatalf("after Escape: editing %v, path %s", tab.editing, tab.Path)
	}

	// Names holding what was typed come after those beginning with it.
	h.typeAddress(h.root + sep + "a")
	same("a letter", "almond", "alpha", "beta")
	// Tab completes the address with the one the arrows are on, and lists
	// what it goes on
	// to; what is typed next follows it.
	h.key(0, ui.KeyDown)
	h.key(0, ui.KeyDown)
	h.key(0, ui.KeyTab)
	if want := h.path("alpha") + sep; tab.addr != want || !tab.editing {
		t.Fatalf("after Tab: address %q, want %q", tab.addr, want)
	}
	same("a folder completed", "index", "inner", "other")
	h.tt.Type("o")
	h.settle()
	if want := h.path("alpha") + sep + "o"; tab.addr != want {
		t.Fatalf("typed after Tab: address %q, want %q", tab.addr, want)
	}
	same("typed after Tab", "other")
	h.key(0, ui.KeyTab)
	same("a folder without folders")
	h.key(0, ui.KeyEnter)
	if tab.Path != h.path("alpha/other") {
		t.Fatalf("after Tab and Enter: %s", tab.Path)
	}

	// A name alone is one of the folder shown.
	h.typeAddress("..")
	h.tt.Type(sep + "in")
	h.settle()
	same("under the folder above", "index", "inner")
	// A click goes to the candidate.
	h.click("inner")
	if tab.editing || tab.Path != h.path("alpha/inner") {
		t.Fatalf("after a click: editing %v, path %s", tab.editing, tab.Path)
	}
	h.typeAddress("de")
	same("a name alone", "deep")
	h.key(0, ui.KeyTab)
	if want := "deep" + sep; tab.addr != want {
		t.Fatalf("a name alone completed: %q, want %q", tab.addr, want)
	}
	h.key(0, ui.KeyEnter)
	if tab.Path != h.path("alpha/inner/deep") {
		t.Fatalf("after a name alone: %s", tab.Path)
	}

	// A forward slash typed is the one a completion goes on with.
	h.typeAddress(strings.ReplaceAll(h.path("alpha"), sep, "/") + "/ot")
	h.key(0, ui.KeyTab)
	if !strings.HasSuffix(tab.addr, "/alpha/other/") {
		t.Fatalf("completed after a slash: %q", tab.addr)
	}
	h.key(0, ui.KeyEscape)
}

func TestAddressCandidateDrives(t *testing.T) {
	volumes := fsys.Volumes()
	if len(volumes) == 0 {
		t.Skip("no drives")
	}
	root := volumes[0].Root
	h := newHarness(t, "sub/")
	tab := h.w.current()
	h.typeAddress(root[:1])
	if tab.cands == nil || len(tab.cands.shown) == 0 || tab.cands.shown[0].path != root {
		t.Fatalf("typed %q: candidates %q, want %s first", root[:1], candidateNames(tab), root)
	}
	h.key(0, ui.KeyTab)
	if tab.addr != root {
		t.Fatalf("a drive completed: %q, want %q", tab.addr, root)
	}
	h.key(0, ui.KeyEscape)
}

// TestAddressOutlastsARefresh checks that the folder shown, read again as
// a file comes to it, leaves the address being typed and its candidates.
func TestAddressOutlastsARefresh(t *testing.T) {
	h := newHarness(t, "alpha/inner/", "one.txt")
	tab := h.w.current()
	h.typeAddress("al")
	if err := os.WriteFile(h.path("two.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.wait("the new file", func() bool { return slices.Contains(h.rows(tab), "two.txt") })
	if !tab.editing || tab.addr != "al" || strings.Join(candidateNames(tab), "|") != "alpha" {
		t.Fatalf("after the folder was read again: editing %v, address %q, candidates %q", tab.editing, tab.addr, candidateNames(tab))
	}
	h.tt.Type("pha")
	h.key(0, ui.KeyEnter)
	if tab.Path != h.path("alpha") {
		t.Fatalf("typed on after it: %s", tab.Path)
	}
}

// TestAddressCandidatesHidden checks that the folders the system hides
// are listed once a part of a name is typed, and before that as the
// setting has it.
func TestAddressCandidatesHidden(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("hides folders by an attribute, as Windows does")
	}
	h := newHarness(t, "alpha/shown/", "alpha/secret/")
	if out, err := exec.Command("attrib", "+h", h.path("alpha/secret")).CombinedOutput(); err != nil {
		t.Fatalf("attrib: %v: %s", err, out)
	}
	tab := h.w.current()
	sep := string(filepath.Separator)
	h.typeAddress("alpha" + sep)
	if got := strings.Join(candidateNames(tab), "|"); got != "shown" {
		t.Fatalf("after the separator: %q", got)
	}
	h.tt.Type("s")
	h.settle()
	if got := strings.Join(candidateNames(tab), "|"); got != "secret|shown" {
		t.Fatalf("after a letter: %q", got)
	}
	h.key(0, ui.KeyEscape)
	h.a.set.ShowHidden = true
	h.typeAddress("alpha" + sep)
	if got := strings.Join(candidateNames(tab), "|"); got != "secret|shown" {
		t.Fatalf("after the separator, hidden files shown: %q", got)
	}
	h.key(0, ui.KeyEscape)

	// With nothing to complete, Tab is the focus's, which leaves the
	// address.
	h.typeAddress("nothing-so-named")
	if len(candidateNames(tab)) != 0 || !tab.editing {
		t.Fatalf("candidates %q, editing %v", candidateNames(tab), tab.editing)
	}
	h.key(0, ui.KeyTab)
	if tab.editing {
		t.Fatal("Tab without candidates did not leave the address")
	}
}

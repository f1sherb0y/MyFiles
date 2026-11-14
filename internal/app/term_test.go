package app

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/config"
	"myfiles/internal/workspace"
)

// termHarness is a window at a folder named "workdir", whose terminals
// run the system's plain shell: it starts at once, and says where it is.
func termHarness(t *testing.T) *harness {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("the terminals of these tests run cmd.exe")
	}
	h := newHarness(t, "workdir/a.txt", "workdir/b.txt", "other/c.txt")
	h.a.termCommand = []string{"cmd.exe"}
	h.w.navigate(h.w.current(), h.path("workdir"), navPush, "")
	h.settle()
	return h
}

// shown waits for the terminal of the tab with the keyboard to show text.
func (h *harness) shown(text string) {
	h.t.Helper()
	h.wait("the terminal to show "+text, func() bool {
		t := h.w.current()
		return t != nil && t.term != nil && t.term.t != nil && strings.Contains(strings.ToLower(t.term.t.Text()), strings.ToLower(text))
	})
}

// run types a command in the terminal with the keyboard.
func (h *harness) run(command string) {
	h.t.Helper()
	for _, r := range command {
		h.tt.Type(string(r))
	}
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
}

// Ctrl+` opens a terminal in the lower half of the pane, in the folder
// shown, with the keyboard; pressed again it goes back to the folder, and
// again to the terminal, without opening another.
func TestTerminalUnderFolder(t *testing.T) {
	h := termHarness(t)
	folder := h.w.current()
	h.key(ui.Ctrl, ui.KeyBackquote)
	panes := h.w.layout.Panes()
	if len(panes) != 2 || h.w.layout.Under(panes[0].ID) != panes[1] {
		t.Fatalf("the panes: %v", h.panes())
	}
	term := h.w.current()
	if term == folder || term.term == nil || h.w.layout.PaneOf(term.ID) != panes[1] {
		t.Fatalf("the keyboard is not with a terminal under the folder: %v", h.panes())
	}
	if h.w.activeTab(panes[0]) != folder || len(folder.view) != 2 {
		t.Fatalf("the folder no longer shows over it: %v", h.panes())
	}
	if !strings.EqualFold(term.term.dir, h.path("workdir")) {
		t.Fatalf("the terminal is in %q, not the folder's %q", term.term.dir, h.path("workdir"))
	}
	// The shell is there, in the folder, and takes what is typed.
	h.shown(`workdir>`)
	h.run("echo one-two")
	h.shown("\none-two")
	if folder.searching() || folder.filter != "" {
		t.Errorf("what was typed in the terminal searched the folder for %q", folder.filter)
	}
	if got := term.title(); got != locales.Terminal() && !strings.Contains(strings.ToLower(got), "cmd") {
		t.Errorf("the terminal's tab is called %q", got)
	}

	// Back to the folder, and to the terminal again.
	h.key(ui.Ctrl, ui.KeyBackquote)
	if h.w.current() != folder {
		t.Fatalf("Ctrl+` in the terminal left the keyboard with %q", h.w.current().title())
	}
	h.key(ui.Ctrl, ui.KeyBackquote)
	if h.w.current() != term || len(h.w.layout.Panes()) != 2 || len(h.w.tabs) != 2 {
		t.Fatalf("Ctrl+` again: the keyboard with %q, panes %v", h.w.current().title(), h.panes())
	}
	// The terminal has the keyboard again, not only the pane.
	h.run("echo three-four")
	h.shown("\nthree-four")
}

// In a terminal the keys of the window are the shell's: Ctrl+W closes no
// tab and Ctrl+T opens none. Ctrl+Shift+` opens one more terminal, beside
// the first, and a shell that ends takes its tab with it.
func TestTerminalKeysAndTabs(t *testing.T) {
	h := termHarness(t)
	folder := h.w.current()
	h.key(ui.Ctrl, ui.KeyBackquote)
	first := h.w.current()
	h.shown(`workdir>`)
	for _, key := range []ui.Key{ui.KeyW, ui.KeyT, ui.KeyL, ui.KeyR, ui.KeyB} {
		h.key(ui.Ctrl, key)
	}
	// They went to the shell, on whose line they are now: Escape clears it.
	h.key(0, ui.KeyEscape)
	if len(h.w.tabs) != 2 || h.w.current() != first || h.w.tabs[folder.ID] != folder || folder.editing {
		t.Fatalf("the window took a key of the terminal's: %v", h.panes())
	}

	h.key(ui.Ctrl|ui.Shift, ui.KeyBackquote)
	second := h.w.current()
	low := h.w.layout.PaneOf(first.ID)
	if second == first || second.term == nil || h.w.layout.PaneOf(second.ID) != low || len(low.Tabs) != 2 || len(h.w.layout.Panes()) != 2 {
		t.Fatalf("one more terminal: %v", h.panes())
	}
	h.shown(`workdir>`)

	// From the folder, Ctrl+Shift+` adds to the terminals under it.
	h.w.focusPane(h.w.layout.PaneOf(folder.ID))
	h.settle()
	h.key(ui.Ctrl|ui.Shift, ui.KeyBackquote)
	third := h.w.current()
	if third.term == nil || len(low.Tabs) != 3 || len(h.w.layout.Panes()) != 2 {
		t.Fatalf("a terminal more from the folder: %v", h.panes())
	}
	h.shown(`workdir>`)

	// Ctrl+Shift+W closes the terminal's tab.
	h.key(ui.Ctrl|ui.Shift, ui.KeyW)
	if h.w.tabs[third.ID] != nil || len(low.Tabs) != 2 {
		t.Fatalf("Ctrl+Shift+W left %v", h.panes())
	}
	// The shells of the two left end: their tabs go, and with the last the
	// pane.
	for _, tab := range []*Tab{h.w.current(), nil} {
		if tab == nil {
			tab = h.w.current()
		}
		if tab.term == nil {
			t.Fatalf("the keyboard is with %q, not a terminal: %v", tab.title(), h.panes())
		}
		id := tab.ID
		h.shown(`workdir>`)
		h.run("exit")
		h.wait("the tab of the shell that ended to close", func() bool { return h.w.tabs[id] == nil })
	}
	if len(h.w.layout.Panes()) != 1 || len(h.w.tabs) != 1 || h.w.current() != folder {
		t.Fatalf("after the shells ended: %v", h.panes())
	}
	if h.f.closed {
		t.Error("the window closed with the terminals")
	}
}

// A folder chosen in the sidebar from a terminal shows in the pane over
// the terminal, and a new tab beside a terminal is of the folder it is in.
func TestTerminalBesideFolders(t *testing.T) {
	h := termHarness(t)
	folder := h.w.current()
	h.key(ui.Ctrl, ui.KeyBackquote)
	term := h.w.current()
	if got := h.w.folderTab(); got != folder {
		t.Fatalf("the folder asked for from the terminal would show in %v", got)
	}
	if term.place() != h.path("workdir") || term.tip() != h.path("workdir") {
		t.Errorf("the terminal is at %q, its tab says %q", term.place(), term.tip())
	}
	// With the folder's tab closed only the terminal is left: there is no
	// tab for a folder.
	h.w.closeTab(folder.ID)
	h.settle()
	if len(h.w.layout.Panes()) != 1 || h.w.current() != term || h.w.folderTab() != nil {
		t.Fatalf("with the folder closed: %v", h.panes())
	}
	if n := len(h.w.reopen); n != 1 || h.w.reopen[0] != h.path("workdir") {
		t.Errorf("what Ctrl+Shift+T opens again: %q", h.w.reopen)
	}
}

// The button beside a terminal's tab opens one more terminal, where the
// first is.
func TestNewTabBesideTerminal(t *testing.T) {
	h := termHarness(t)
	h.key(ui.Ctrl, ui.KeyBackquote)
	first := h.w.current()
	low := h.w.layout.PaneOf(first.ID)
	h.shown(`workdir>`)
	// The button is after the terminal's tab, the pane's only one.
	tab, ok := h.tt.Find(first.title())
	if !ok {
		t.Fatalf("no tab called %q; texts %q", first.title(), h.tt.Texts())
	}
	x, y := tab.X+tab.W+3+(controlSize+2)/2, tab.Y+tab.H/2
	h.tt.ClickAt(x, y)
	h.settle()
	second := h.w.current()
	if second == first || second.term == nil || len(low.Tabs) != 2 || second.term.dir != h.path("workdir") {
		t.Fatalf("the button beside the terminal: %v", h.panes())
	}
}

// A session keeps a terminal as the folder it was in, and brings it back
// as a terminal there, whose shell starts only once the tab shows.
func TestTerminalInSession(t *testing.T) {
	h := termHarness(t)
	dir := h.path("other")
	layout := workspace.New(7, 8)
	layout.Split(layout.Focus, 8, workspace.ZoneBottom)
	var kept workspace.Layout
	raw, _ := json.Marshal(layout)
	if err := json.Unmarshal(raw, &kept); err != nil {
		t.Fatal(err)
	}
	w := h.a.window(&config.Session{Layout: &kept, Tabs: []config.Tab{
		{ID: 7, Path: h.path("workdir")},
		{ID: 8, Path: terminalPath + dir},
	}})
	t.Cleanup(func() {
		for _, tab := range w.tabs {
			w.release(tab)
		}
	})
	term := w.tabs[8]
	if term == nil || term.term == nil || term.term.dir != dir || term.term.t != nil || term.loading {
		t.Fatalf("the terminal the session brought back: %+v", term)
	}
	if w.tabs[7] == nil || w.tabs[7].term != nil {
		t.Fatal("the folder beside it came back as a terminal")
	}
	if got := terminalPath + term.place(); got != terminalPath+dir {
		t.Errorf("a session would keep it as %q", got)
	}
}

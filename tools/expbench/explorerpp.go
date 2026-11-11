package main

import (
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// What Explorer++ 1.4.0 is made of, from its source (Explorer++_internal.h,
// Helper/Controls.cpp, ShellBrowser/ShellBrowser.cpp, AddressBar.cpp):
// one top-level window of class "Explorer++"; a list view per tab, of which
// only the selected tab's is visible; and the address bar, a ComboBoxEx32
// whose edit control is subclassed to take Enter in its WM_KEYDOWN.
const (
	mainClass     = "Explorer++"
	listViewClass = "SysListView32"
	addressClass  = "ComboBoxEx32"
	titleSuffix   = " - Explorer++" // IDS_MAIN_WINDOW_TITLE is "%s - %s"
)

// viewModes are Explorer++'s names for its views and the numbers its
// settings file keeps them as (ShellBrowser/ViewModes.h).
var viewModes = map[string]int{
	"icons": 1, "small-icons": 2, "list": 3, "details": 4,
	"tiles": 5, "thumbnails": 6, "extra-large-icons": 7, "large-icons": 8,
}

// listViewNames are the list view's own names for what LVM_GETVIEW
// answers.
var listViewNames = []string{"icons", "details", "small icons", "list", "tiles"}

// sandbox is a private copy of Explorer++ in a folder of its own.
//
// Explorer++ keeps its settings in the registry unless a config.xml lies
// beside its program file, when it reads and writes only that
// (TestConfigFileInternal in MsgHandler.cpp). So a copy of the program
// beside a config.xml of the benchmark's neither sees nor changes the
// user's settings, tabs or bookmarks.
type sandbox struct {
	dir     string
	exe     string
	version string
	view    string // one of viewModes, or "" for Explorer++'s default
}

// newSandbox copies the installed Explorer++ to a folder under the
// temporary folder, unless the copy there is already the same file.
func newSandbox(installed, view string) (*sandbox, error) {
	if view != "" {
		if _, ok := viewModes[view]; !ok {
			return nil, fmt.Errorf("no view called %q", view)
		}
	}
	s := &sandbox{dir: filepath.Join(os.TempDir(), "cairn-expbench"), view: view}
	s.exe = filepath.Join(s.dir, "Explorer++.exe")
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, err
	}
	src, err := os.Stat(installed)
	if err != nil {
		return nil, fmt.Errorf("Explorer++ is not at %s (say where with -exe): %w", installed, err)
	}
	if dst, err := os.Stat(s.exe); err != nil || dst.Size() != src.Size() || !dst.ModTime().Equal(src.ModTime()) {
		if err := copyFile(installed, s.exe, src.ModTime()); err != nil {
			return nil, fmt.Errorf("copying Explorer++ to %s: %w", s.dir, err)
		}
	}
	if s.version, err = fileVersion(s.exe); err != nil {
		return nil, fmt.Errorf("reading the version of %s: %w", s.exe, err)
	}
	return s, nil
}

func copyFile(from, to string, modTime time.Time) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(to, modTime, modTime)
}

// writeConfig writes the settings a start begins from: Explorer++'s
// defaults, which is what it uses for whatever the file does not say,
// with its navigation sound off. It is written afresh before every start,
// because Explorer++ saves its settings and open tabs to it every thirty
// seconds.
func (s *sandbox) writeConfig() error {
	var b strings.Builder
	b.WriteString("<?xml version='1.0'?>\n<!-- Preference file for Explorer++ -->\n<ExplorerPlusPlus>\n\t<Settings>\n")
	// The one default not kept: by default a sound is played as each
	// folder is entered (PlaySound, asynchronous, at the end of
	// ShellBrowser::BrowseFolder), and a benchmark must be silent.
	b.WriteString("\t\t<Setting name=\"PlayNavigationSound\">no</Setting>\n")
	if s.view != "" {
		fmt.Fprintf(&b, "\t\t<Setting name=\"ViewModeGlobal\">%d</Setting>\n", viewModes[s.view])
	}
	b.WriteString("\t</Settings>\n</ExplorerPlusPlus>\n")
	return os.WriteFile(filepath.Join(s.dir, "config.xml"), []byte(b.String()), 0o644)
}

// explorerpp is a running private Explorer++ and the windows of it the
// benchmark drives.
type explorerpp struct {
	process windows.Handle
	pid     uint32
	started ticks // just before CreateProcess

	main    windows.HWND
	address windows.HWND // the address bar
	edit    windows.HWND // the edit control inside it

	// sawUnpainted is whether, as it started, the list view was seen
	// with something to paint before it was seen with nothing.
	sawUnpainted bool

	// pictures is whether to keep what the list view shows as each
	// measurement ends; startPicture is that of its start.
	pictures     bool
	startPicture *image.RGBA

	keepsForeground  bool         // it may be the foreground window
	foregroundWas    windows.HWND // the foreground window before it started
	foregroundLocked bool         // others are kept from the foreground
}

// launch starts the private Explorer++ with one tab, on a folder. Its
// window is shown where and as large as Explorer++ puts it by default.
//
// Unless asked otherwise, the window is not to become the foreground
// window, so that whoever is typing elsewhere goes on typing there. Asking
// for it to be shown without being activated is not enough, because
// Explorer++ gives its list view the focus as it starts, which activates
// its window; so other programs are also kept from taking the foreground
// while it starts, where the system lets this program say so.
func (s *sandbox) launch(folder string, activate bool) (*explorerpp, error) {
	if err := s.writeConfig(); err != nil {
		return nil, err
	}
	x := &explorerpp{keepsForeground: activate}
	show := uint16(swShowNormal)
	if !activate {
		show = swShowNoActivate
		x.foregroundWas = windows.GetForegroundWindow()
		x.foregroundLocked = lockForeground()
	}
	// The folder to work in matters: Explorer++ 1.4.0 looks for its
	// config.xml beside the program, but then loads it by its bare name.
	var err error
	if x.process, x.pid, x.started, err = start(s.exe, []string{folder}, s.dir, show); err != nil {
		x.releaseForeground()
		return nil, err
	}
	return x, nil
}

// releaseForeground lets programs take the foreground again.
func (x *explorerpp) releaseForeground() {
	if x.foregroundLocked {
		unlockForeground()
		x.foregroundLocked = false
	}
}

// yieldForeground is called once Explorer++ has started. It says whether
// Explorer++ took the foreground after all, and, the window that had it
// having then been given it back, whether Explorer++ has it still.
func (x *explorerpp) yieldForeground() (took, stillHas bool) {
	if x.keepsForeground {
		return false, false
	}
	x.releaseForeground()
	if foregroundProcess() != x.pid {
		return false, false
	}
	if x.foregroundWas != 0 && windows.IsWindow(x.foregroundWas) {
		procSetForegroundWindow.Call(uintptr(x.foregroundWas))
	}
	return true, foregroundProcess() == x.pid
}

// kill ends the process. It is only ever one this program started.
func (x *explorerpp) kill() {
	if x == nil || x.process == 0 {
		return
	}
	x.releaseForeground()
	windows.TerminateProcess(x.process, 0)
	windows.WaitForSingleObject(x.process, 5000)
	windows.CloseHandle(x.process)
	x.process = 0
}

// listView is the selected tab's list view: the visible one.
func (x *explorerpp) listView() windows.HWND {
	return findDescendant(x.main, listViewClass, windows.IsWindowVisible)
}

// awaitStarted waits until a just started Explorer++ shows its folder,
// and returns the counter at that moment.
//
// Explorer++ creates its window, and in creating it reads the folder and
// fills the list view, before it shows anything (Explorerplusplus::OnCreate
// and RestoreTabs); then WinMain shows the window and updates it. So the
// folder is shown once the window and its list view are visible and the
// list view has painted for the first time.
func (x *explorerpp) awaitStarted(limit time.Duration) (ticks, error) {
	deadline := time.Now().Add(limit)
	var lv windows.HWND
	for {
		if x.main == 0 {
			x.main = topLevelWindow(x.pid, mainClass)
		}
		if x.main != 0 && windows.IsWindowVisible(x.main) {
			if lv = x.listView(); lv != 0 {
				break
			}
		}
		if exited(x.process) {
			return 0, errors.New("Explorer++ exited before it showed a window")
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("Explorer++'s window: %w", errTimeout)
		}
		// Looked for only every millisecond: looking takes the lock on
		// the system's windows that Explorer++, making its own, needs,
		// and the list view paints tens of milliseconds after it is first
		// visible, so nothing is lost by it.
		time.Sleep(time.Millisecond)
	}
	// Being shown is what gives the list view something to paint, a
	// moment after it becomes visible (measured: within half a
	// millisecond, and it then waits some 30 ms for its turn). Wait to see
	// that, so that an update region still empty is not taken for one
	// emptied by painting.
	x.sawUnpainted = false
	for wait := now(); ms(wait, now()) < 200; spin(pollEvery) {
		if paintPending(lv) {
			x.sawUnpainted = true
			break
		}
	}
	shown, err := awaitPainted(lv)
	if err != nil {
		return 0, err
	}
	if x.pictures {
		x.startPicture = capture(lv)
	}

	// Not timed: find the address bar, for the navigations to come.
	if x.address = findDescendant(x.main, addressClass, nil); x.address == 0 {
		return 0, errors.New("Explorer++ has no address bar: is it hidden?")
	}
	edit, err := send(x.address, cbemGetEditControl, 0, 0)
	if err != nil || edit == 0 {
		return 0, fmt.Errorf("the address bar has no edit control: %v", err)
	}
	x.edit = windows.HWND(edit)
	return shown, nil
}

// awaitPainted waits until a window has painted what it holds, and
// returns the counter at that moment.
//
// A window with something to paint has an update region, which it empties
// as it begins to paint (BeginPaint). So this waits for the region to be
// empty, and then for the window's thread to answer a message, which it
// does only once it is out of its paint handler and back in its message
// loop. Nothing is forced: the window paints when it would have anyway,
// after whatever else its thread had to do first.
func awaitPainted(h windows.HWND) (ticks, error) {
	deadline := time.Now().Add(messageTimeout)
	for n := 0; paintPending(h); n++ {
		if n%1024 == 1023 && time.Now().After(deadline) {
			return 0, fmt.Errorf("window %#x painting: %w", h, errTimeout)
		}
		spin(pollEvery)
	}
	if _, err := send(h, wmNull, 0, 0); err != nil {
		return 0, err
	}
	return now(), nil
}

// visit is one timed entry into a folder.
type visit struct {
	shown   ticks   // when the list view had painted
	shownMs float64 // Enter in the address bar to then
	syncMs  float64 // the part of it until Enter was handled
	entries int     // what the list view held then
	view    string  // the list view's view
	title   string  // the window's title then
	// dirty is whether the list view had something to paint when Enter
	// had been handled: it must, or the wait for its paint waited for
	// nothing.
	dirty bool
	lv    windows.HWND
	// picture is what the list view showed when the clock stopped, if
	// pictures are being taken.
	picture *image.RGBA
}

// navigate enters a folder in the selected tab, as typing its path in
// the address bar and pressing Enter does, and times it.
//
// The address bar's edit control handles Enter in its own WM_KEYDOWN
// (AddressBar::EditSubclass), with no need of the keyboard focus, and
// goes straight, on the same thread, through parsing the path
// (SHParseDisplayName) into ShellBrowser::BrowseFolder, which empties the
// list view, enumerates the folder, inserts and sorts its items, and lets
// the list view redraw. Sending the key therefore returns when all of
// that is done, and all that is left is the list view's paint.
func (x *explorerpp) navigate(path string) (visit, error) {
	var v visit
	if minimized(x.main) {
		return v, errors.New("Explorer++'s window is minimized, and paints nothing")
	}
	v.lv = x.listView()
	if v.lv == 0 {
		return v, errors.New("no visible list view")
	}
	// Typing the path is not timed; pressing Enter is.
	if err := setText(x.edit, path); err != nil {
		return v, err
	}
	// AddressBar::OnGo reads the text from the ComboBoxEx, not the edit.
	if typed, err := text(x.address); err != nil || typed != path {
		return v, fmt.Errorf("the address bar holds %q, not %q (%v)", typed, path, err)
	}

	begin := now()
	_, err := send(x.edit, wmKeyDown, vkReturn, enterLParam)
	handled := now()
	if err != nil {
		return v, err
	}
	v.dirty = paintPending(v.lv)
	shown, err := awaitPainted(v.lv)
	if err != nil {
		return v, err
	}
	v.shown = shown
	v.shownMs = ms(begin, shown)
	v.syncMs = ms(begin, handled)
	if x.pictures {
		v.picture = capture(v.lv)
	}

	// Not timed: what was shown.
	if lv := x.listView(); lv != v.lv {
		return v, fmt.Errorf("the list view was replaced during the navigation (%#x, now %#x)", v.lv, lv)
	}
	if v.entries, err = x.entries(v.lv); err != nil {
		return v, err
	}
	if view, err := send(v.lv, lvmGetView, 0, 0); err == nil && int(view) < len(listViewNames) {
		v.view = listViewNames[view]
	}
	if v.title, err = text(x.main); err != nil {
		return v, err
	}
	return v, nil
}

// entries is how many items a list view holds.
func (x *explorerpp) entries(lv windows.HWND) (int, error) {
	n, err := send(lv, lvmGetItemCount, 0, 0)
	return int(n), err
}

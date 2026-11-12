// Package app is MyFiles's interface: windows of panes of tabs, each tab a
// folder. It draws with MyGo's native UI and is the same on every
// platform; what differs between systems is behind the fsys and shell
// packages.
package app

import (
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"myfiles/internal/cli"
	"myfiles/internal/config"
	"myfiles/internal/fsys"
	"myfiles/internal/shell"
	"myfiles/internal/watch"
	"myfiles/internal/workspace"
)

// Version is the program's version, set when it is built.
var Version = "0.2.1"

// App is the running program: what its windows share.
type App struct {
	set   config.Settings
	icons *icons
	// desk is the system outside the windows: the clipboard, and the
	// main thread.
	desk desktop
	// exe is the path of the program, as the system starts it.
	exe     string
	windows []*Window
	nextTab workspace.TabID

	// cut are the paths the user cut to the clipboard, which show
	// dimmed until they are pasted or the clipboard takes something
	// else. cutGen tells one cut from the next, for the clipboard's
	// release of an earlier one.
	cut    map[string]bool
	cutGen int

	// registered is what the program opens for the system, as the
	// settings show it, read off the main thread as they open.
	registered shell.Default

	themes themeCache
	// ephemeral keeps the settings and the session as they are on disk:
	// set for benchmarks and tests, which must leave no trace.
	ephemeral bool
	// columns are the order and widths of the columns of files, the
	// same in every tab.
	columns ui.TableLayout
	bench   *bench

	// saving is the timer that writes the session a moment after it
	// changed.
	saveMu sync.Mutex
	saving bool

	// fileCols and driveCols are the table's columns with their titles in
	// the language shown, made anew when it changes.
	fileCols, driveCols, trashCols []ui.TableColumn
	// lang is the language chosen in the settings, "" for the system's.
	lang  string
	langs []string
	// settingsRead is set once the settings read what they show.
	settingsRead bool
	// side is what the sidebars show beside the file system's folders.
	side *places
	// hub watches the folders the tabs show, by the drives they are on.
	hub *watch.Hub
	// updateReady is the newer version downloaded and in place, which
	// shows once the program starts again; "" for none. updating is set
	// while one is looked for or downloaded.
	updateReady string
	updating    bool
	// apps are the programs that open files, by the files' extension.
	apps map[string]*typeApps
}

// Run shows what a command line asks for and runs until the last window
// closes. args and cwd are the command line as given, for a running
// instance to take over.
func Run(cmd *cli.Command) error {
	a := &App{set: config.Load(), nextTab: 1, desk: system{}}
	a.side = newPlaces(&a.set, config.LoadRecent())
	a.hub = watch.New()
	a.columns = ui.TableLayout{Order: a.set.ColumnOrder, Widths: a.set.Columns}
	a.exe, _ = os.Executable()
	shell.Quiet()

	solo := cmd.Bench != "" || cmd.Screenshot != "" || cmd.Ephemeral
	a.ephemeral = solo
	if !solo && !mygo.App.RequestSingleInstanceLock() {
		// The running instance got the command line and shows it.
		return nil
	}
	// The GPU's driver loads while the window is made.
	ui.Prewarm()
	mygo.App.SetName("MyFiles")
	if runtime.GOOS == "windows" {
		// No menu bar: the window has none to show, and its menus are
		// drawn in it.
		mygo.App.SetMenu(nil)
	}
	mygo.App.SetVersion(Version)
	if a.set.Theme == "light" || a.set.Theme == "dark" {
		a.desk.appearance(a.set.Theme)
	}
	mygo.App.OnSecondInstance(func(args []string, dir string) {
		next, err := cli.Parse(args, dir)
		if err != nil {
			next = &cli.Command{}
		}
		a.open(next, false)
	})
	mygo.App.OnWindowAllClosed(func() { mygo.App.Quit() })
	mark("run")
	mygo.App.WhenReady(func() {
		mark("ready")
		scale := mygo.Screen.PrimaryDisplay().ScaleFactor
		if scale <= 0 {
			scale = 1
		}
		// What is known of types is kept between runs. It is no setting:
		// runs that leave the settings alone use it too.
		a.icons = newIcons(scale, a.desk.post, a.invalidate, openIconStore())
		if cmd.Bench != "" || cmd.Screenshot != "" {
			a.bench = newBench(cmd)
		}
		a.importPins()
		a.open(cmd, true)
		mark("opened")
		if !solo {
			// Once the window shows and its folder is read: not in their
			// way.
			time.AfterFunc(time.Second, a.warmSystemMenu)
			a.watchUpdates()
		} else if cmd.Ephemeral && os.Getenv("MYFILES_UPDATE_DIR") != "" {
			// Trying an update, beside the copy installed.
			a.watchUpdates()
		}
	})
	return mygo.App.Run()
}

// open shows a command's folders: in a new window when it asks for one or
// none is open, else as tabs of the window used last. first is set for
// the command the program started with, which brings the last session
// back when it names no folder.
func (a *App) open(cmd *cli.Command, first bool) {
	for _, arg := range cmd.Delegate {
		// Places that are no folders, as the Control Panel, are the
		// system's file manager's to show.
		_ = a.desk.openInSystem(arg)
	}
	_ = cmd.Resolve()
	var w *Window
	fresh := false
	if len(a.windows) > 0 && !cmd.NewWindow {
		w = a.windows[len(a.windows)-1]
	}
	if w == nil {
		var session *config.Session
		if first && len(cmd.Targets) == 0 && len(cmd.Delegate) == 0 && a.set.RestoreTabs && !a.ephemeral {
			session = config.LoadSession()
		}
		if len(cmd.Targets) == 0 && session == nil {
			if len(cmd.Delegate) > 0 && first {
				// Started only to show such a place: nothing of
				// ours to show.
				mygo.App.Quit()
				return
			}
			cmd.Targets = []cli.Target{{Path: fsys.Home()}}
		}
		w = a.window(session)
		fresh = true
	} else if len(cmd.Targets) == 0 {
		if len(cmd.Delegate) == 0 {
			// The program started again with nothing to show: a new
			// tab, as the system's file manager opens a window.
			w.newTab(w.layout.Focus, fsys.Home(), "")
		}
		w.raise()
		return
	}
	home := w.layout.Focus
	pane := home
	for i, t := range cmd.Targets {
		id := w.newTab(pane, t.Path, t.Select)
		if i == 1 && cmd.Split != cli.SplitNone {
			// The second folder and those after it go beside the first.
			zone := workspace.ZoneRight
			if cmd.Split == cli.SplitDown {
				zone = workspace.ZoneBottom
			}
			if w.layout.Split(pane, id, zone) {
				pane = w.layout.Focus
			}
		}
	}
	if pane != home {
		w.layout.Focus = home
	}
	if fresh {
		a.show(w)
	} else {
		w.raise()
	}
	w.changed()
}

// invalidate draws every window again.
func (a *App) invalidate() {
	for _, w := range a.windows {
		w.invalidate()
	}
}

// remove forgets a window that closed.
func (a *App) remove(w *Window) {
	for i, o := range a.windows {
		if o == w {
			a.windows = append(a.windows[:i], a.windows[i+1:]...)
			break
		}
	}
}

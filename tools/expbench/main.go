// Expbench times how fast Explorer++ shows a folder, from outside, for
// comparison with MyFiles's own benchmark (myfiles --bench).
//
// It runs a private copy of the installed Explorer++, with settings of
// its own, takes it through a list of folders by its address bar, and
// times each from the request to the folder's items being painted:
//
//	expbench -out out\explorerpp.json
//
// It also times MyFiles's start the way it times Explorer++'s, and prints
// two reports side by side:
//
//	expbench -myfiles-startup bin\myfiles.exe C:\ -out out\myfiles-startup.json
//	expbench -compare out\myfiles.json out\explorerpp.json -startup out\myfiles-startup.json -md out\compare.md
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// installedExe is where winget puts Explorer++.
func installedExe() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"),
		`Microsoft\WinGet\Packages\derceg.Explorer++_Microsoft.Winget.Source_8wekyb3d8bbwe\Explorer++.exe`)
}

// files is a flag that may be given more than once.
type files []string

func (f *files) String() string     { return strings.Join(*f, ", ") }
func (f *files) Set(v string) error { *f = append(*f, v); return nil }

func main() {
	var (
		o              options
		compare        bool
		md             string
		startups       files
		myfilesStartup string
	)
	fs := flag.NewFlagSet("expbench", flag.ExitOnError)
	fs.StringVar(&o.folders, "folders", `tools\bench-folders.txt`, "the `file` listing the folders to enter, one a line")
	fs.IntVar(&o.rounds, "rounds", 5, "how many times to go through the folders")
	fs.DurationVar(&o.pause, "pause", 250*time.Millisecond, "how long to rest in a folder once it is shown")
	fs.StringVar(&o.out, "out", "", "the `file` to write the report to, as JSON")
	fs.StringVar(&o.exe, "exe", installedExe(), "the installed Explorer++, of which a private copy is run")
	fs.StringVar(&o.start, "start", `C:\`, "the `folder` Explorer++ is started in")
	fs.IntVar(&o.startups, "startups", 3, "how many times to start the program, for its startup time")
	fs.StringVar(&o.view, "view", "", "a view to use instead of Explorer++'s default: "+strings.Join(viewNames(), ", "))
	fs.BoolVar(&o.activate, "activate", false, "let Explorer++'s window become the active window")
	fs.BoolVar(&o.verbose, "v", false, "print every navigation")
	fs.StringVar(&o.shots, "shots", "", "a `folder` to save pictures in, of what the list view shows as each measurement ends")
	fs.DurationVar(&messageTimeout, "timeout", messageTimeout, "how long Explorer++ may take over one folder")
	fs.StringVar(&myfilesStartup, "myfiles-startup", "", "time the start of this MyFiles `exe` in the folder that follows, instead")
	fs.BoolVar(&compare, "compare", false, "print the two reports that follow side by side, instead")
	fs.StringVar(&md, "md", "", "with -compare, the `file` to write the table to, as Markdown")
	fs.Var(&startups, "startup", "with -compare, a `file` from -myfiles-startup -out: the startup time to show for its app")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: expbench [flags]")
		fmt.Fprintln(os.Stderr, "       expbench -myfiles-startup myfiles.exe folder [-startups n] [-out file]")
		fmt.Fprintln(os.Stderr, "       expbench -compare a.json b.json [-startup file] [-md file]")
		fs.PrintDefaults()
	}

	// Flags may come after the names they go with, as in
	// "-compare a.json b.json -md out.md".
	var names []string
	for args := os.Args[1:]; ; {
		fs.Parse(args)
		if fs.NArg() == 0 {
			break
		}
		names = append(names, fs.Arg(0))
		args = fs.Args()[1:]
	}

	// One thread sends every message and reads every time. The shell,
	// asked for folders' names, wants COM on it.
	runtime.LockOSThread()
	windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)

	var err error
	switch {
	case compare:
		if len(names) != 2 {
			fs.Usage()
			os.Exit(2)
		}
		err = runCompare(names[0], names[1], startups, md)
	case myfilesStartup != "":
		if len(names) != 1 {
			fs.Usage()
			os.Exit(2)
		}
		err = runMyFilesStartup(myfilesStartup, names[0], o.startups, o.out)
	default:
		if len(names) != 0 || o.rounds < 1 || o.startups < 1 {
			fs.Usage()
			os.Exit(2)
		}
		err = runBench(o)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "expbench:", err)
		os.Exit(1)
	}
}

func viewNames() []string {
	names := make([]string, 0, len(viewModes))
	for name := range viewModes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

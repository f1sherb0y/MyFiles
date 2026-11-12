// Package cli reads the command line: MyFiles's own options, and the
// arguments Windows' explorer.exe takes, which other programs pass to
// whatever opens folders for them.
//
//	myfiles [options] [path...]
//	myfiles /select,C:\Users\ada\notes.txt
//	myfiles /e,/root,C:\Projects
package cli

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"myfiles/internal/fsys"
	"myfiles/internal/shell"
)

// Target is something to show: a folder in a tab, with or without one of
// its entries chosen.
type Target struct {
	// Path is the folder, fsys.Computer for the list of drives.
	Path string
	// Select is the name of the entry to choose in it, or empty.
	Select string
}

// Split is where a target's tab goes beside the others.
type Split uint8

const (
	SplitNone Split = iota
	SplitRight
	SplitDown
)

// Command is what a command line asks for.
type Command struct {
	// Targets are the folders to open, one tab each, in order.
	Targets []Target
	// NewWindow asks for a window of its own rather than tabs in the
	// running one (explorer's /n and /separate).
	NewWindow bool
	// Split puts the second target and those after it in a pane beside
	// the first.
	Split Split
	// Delegate are arguments MyFiles cannot show itself, as the Recycle
	// Bin or the Control Panel, to hand to the system's file manager
	// one by one.
	Delegate []string

	Help, Version bool
	Licenses      bool
	// SetDefault is "on" or "off" to make MyFiles the default file manager,
	// or stop being it, and exit.
	SetDefault string
	// Bench is a file of folders to time navigating through.
	Bench string
	// BenchOut is where the timings go, standard output when empty.
	BenchOut string
	// BenchRounds is how many times each folder is entered.
	BenchRounds int
	// Screenshot writes a picture of the window to a file once the first
	// folder shows, and exits: for checking the interface from scripts.
	Screenshot string
	// Debug names things to do once the window shows, for the same
	// scripts.
	Debug []string
	// Ephemeral runs an instance of its own that reads no session and
	// writes neither settings nor session: for tests of the built app.
	Ephemeral bool
}

// Usage is the text --help prints.
const Usage = `MyFiles, a fast file manager.

Usage:
  myfiles [options] [path...]

Each path opens in a tab: a folder, or a file, which is chosen in its
folder. Without a path, the tabs of the last session come back.

Options:
  -n, --new-window        open a new window rather than tabs in the running one
      --select <path>     open the folder of path and choose it there
      --split <right|down> put the paths after the first in a pane beside it
      --set-default       make MyFiles the default file manager, and exit
      --unset-default     give folders back to Explorer, and exit
      --bench <file>      time entering the folders listed in file, and exit
      --bench-out <file>  write the timings there as JSON
      --bench-rounds <n>  enter each folder n times (default 5)
  -v, --version           print the version
      --licenses          print association-code license notices
  -h, --help              print this help

The arguments of Windows' explorer.exe work too:
  /select,<path>          open the folder of path and choose it there
  /root,<path>  /e,<path> open the folder
  /n  /separate           open a new window
  shell:Downloads, ::{20D04FE0-3AEA-1069-A2D8-08002B30309D}, file:///C:/...
`

// Parse reads the arguments after the program's name. cwd is the directory
// relative paths are in. Paths are made absolute and clean, but not looked
// at: Resolve does that.
func Parse(args []string, cwd string) (*Command, error) {
	cmd := &Command{BenchRounds: 5}
	// selecting is set by a "/select," whose path is the next argument,
	// as a shell splits "explorer /select, C:\dir\file".
	selecting := false
	value := func(i *int, name string) (string, error) {
		if *i+1 >= len(args) {
			return "", fmt.Errorf("%s needs a value", name)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, inline, hasInline := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "--") {
			name, hasInline = arg, false
		}
		take := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			return value(&i, name)
		}
		switch name {
		case "-h", "--help", "/?":
			cmd.Help = true
		case "-v", "--version":
			cmd.Version = true
		case "--licenses":
			cmd.Licenses = true
		case "-n", "--new-window":
			cmd.NewWindow = true
		case "--set-default":
			cmd.SetDefault = "on"
		case "--unset-default":
			cmd.SetDefault = "off"
		case "--select":
			v, err := take()
			if err != nil {
				return nil, err
			}
			cmd.addSelect(v, cwd)
		case "--split":
			v, err := take()
			if err != nil {
				return nil, err
			}
			switch strings.ToLower(v) {
			case "right", "h", "horizontal":
				cmd.Split = SplitRight
			case "down", "v", "vertical":
				cmd.Split = SplitDown
			default:
				return nil, fmt.Errorf("--split takes right or down, not %q", v)
			}
		case "--bench":
			v, err := take()
			if err != nil {
				return nil, err
			}
			cmd.Bench = v
		case "--bench-out":
			v, err := take()
			if err != nil {
				return nil, err
			}
			cmd.BenchOut = v
		case "--bench-rounds":
			v, err := take()
			if err != nil {
				return nil, err
			}
			if _, err := fmt.Sscanf(v, "%d", &cmd.BenchRounds); err != nil || cmd.BenchRounds < 1 {
				return nil, fmt.Errorf("--bench-rounds takes a count, not %q", v)
			}
		case "--screenshot":
			v, err := take()
			if err != nil {
				return nil, err
			}
			cmd.Screenshot = v
		case "--ephemeral":
			cmd.Ephemeral = true
		case "--debug":
			v, err := take()
			if err != nil {
				return nil, err
			}
			cmd.Debug = append(cmd.Debug, v)
		default:
			switch {
			case strings.HasPrefix(arg, "/") && explorerSwitch(arg):
				selecting = cmd.explorer(arg, cwd)
			case arg == "-Embedding" || arg == "-embedding":
				// What COM passes a server it starts: nothing to open.
			case strings.HasPrefix(arg, "--"):
				return nil, fmt.Errorf("unknown option %s", name)
			case strings.Trim(arg, ", ") == "":
				// A comma left over from "explorer /e, C:\dir".
			case selecting:
				selecting = false
				cmd.addSelect(arg, cwd)
			default:
				cmd.addPath(arg, cwd)
			}
		}
	}
	return cmd, nil
}

// explorerSwitch reports whether an argument starting with a slash is one
// of explorer.exe's switches rather than a path, as /usr on another system
// would be.
func explorerSwitch(arg string) bool {
	first, _, _ := strings.Cut(arg[1:], ",")
	switch strings.ToLower(strings.TrimSpace(first)) {
	case "n", "e", "root", "select", "separate", "idlist", "factory", "s":
		return true
	}
	return false
}

// explorer reads one argument of explorer.exe's switches, which commas
// join: "/e,/root,C:\dir", "/select,C:\dir\file", "/n,C:\dir".
//
// It reports whether it ended in a /select without its path, which is then
// the next argument.
func (cmd *Command) explorer(arg, cwd string) (selecting bool) {
	fields := strings.Split(arg, ",")
	for i := 0; i < len(fields); i++ {
		f := strings.TrimSpace(fields[i])
		rest := func() string {
			// A path may hold commas of its own: what follows the
			// switch is the path.
			v := strings.TrimSpace(strings.Join(fields[i+1:], ","))
			i = len(fields)
			return strings.Trim(v, `"`)
		}
		switch strings.ToLower(f) {
		case "/n", "/separate":
			cmd.NewWindow = true
		case "/e", "/s":
			// With the folder tree, which MyFiles has no mode for.
		case "/select":
			if v := rest(); v != "" {
				cmd.addSelect(v, cwd)
			} else {
				selecting = true
			}
		case "/root":
			if v := rest(); v != "" {
				cmd.addPath(v, cwd)
			}
		case "/idlist", "/factory":
			// Handles into Explorer's own process, which mean nothing
			// here.
			i = len(fields)
		case "":
		default:
			if strings.HasPrefix(f, "/") && explorerSwitch(f) {
				continue
			}
			v := strings.Trim(strings.TrimSpace(strings.Join(fields[i:], ",")), `"`)
			i = len(fields)
			cmd.addPath(v, cwd)
		}
	}
	return selecting
}

func (cmd *Command) addPath(arg, cwd string) {
	path, kind := Locate(arg, cwd)
	switch kind {
	case locPath:
		cmd.Targets = append(cmd.Targets, Target{Path: path})
	case locDelegate:
		cmd.Delegate = append(cmd.Delegate, arg)
	}
}

func (cmd *Command) addSelect(arg, cwd string) {
	path, kind := Locate(arg, cwd)
	switch kind {
	case locPath:
		if parent, ok := fsys.Parent(path); ok {
			cmd.Targets = append(cmd.Targets, Target{Path: parent, Select: fsys.Base(path)})
		} else {
			cmd.Targets = append(cmd.Targets, Target{Path: path})
		}
	case locDelegate:
		cmd.Delegate = append(cmd.Delegate, "/select,"+arg)
	}
}

type locKind uint8

const (
	locNone locKind = iota
	locPath
	locDelegate
)

// The shell's names for the list of drives: This PC's class, and what
// "shell:" calls it.
const computerCLSID = "{20D04FE0-3AEA-1069-A2D8-08002B30309D}"

// Locate turns what a command line or an address bar names into a clean
// absolute path: a path, absolute or relative to cwd, with %VARIABLES%; a
// file: URL; "shell:Name"; or the shell's name for This PC, which gives
// fsys.Computer, or for the Recycle Bin, shell.RecycleBin. Other places of
// the shell, which are no directories, are for the system's file manager
// (locDelegate).
func Locate(arg, cwd string) (string, locKind) {
	arg = strings.TrimSpace(strings.Trim(strings.TrimSpace(arg), `"`))
	if arg == "" {
		return "", locNone
	}
	lower := strings.ToLower(arg)
	switch {
	case strings.HasPrefix(lower, "file:"):
		u, err := url.Parse(arg)
		if err != nil {
			return "", locNone
		}
		p := u.Path
		if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
			p = `\\` + u.Host + p
		} else if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
		arg = p
	case strings.HasPrefix(lower, "shell:"):
		name := arg[len("shell:"):]
		if strings.HasPrefix(name, "::") {
			return locateCLSID(name)
		}
		// "shell:Downloads\sub" names a folder under a known one.
		head, tail, _ := strings.Cut(strings.ReplaceAll(name, "/", `\`), `\`)
		if strings.EqualFold(head, "MyComputerFolder") {
			return fsys.Computer, locPath
		}
		if strings.EqualFold(head, "RecycleBinFolder") && tail == "" {
			return shell.RecycleBin, locPath
		}
		base, ok := fsys.KnownFolder(head)
		if !ok {
			return "", locDelegate
		}
		arg = fsys.Join(base, tail)
	case strings.HasPrefix(arg, "::"):
		return locateCLSID(arg)
	case strings.Contains(lower, "://"):
		return "", locDelegate
	}
	arg = fsys.ExpandEnv(arg)
	if arg == "~" || strings.HasPrefix(arg, `~\`) || strings.HasPrefix(arg, "~/") {
		arg = fsys.Home() + arg[1:]
	}
	if path, ok := fsys.Clean(arg); ok {
		return path, locPath
	}
	if cwd == "" {
		return "", locNone
	}
	if path, ok := fsys.Clean(filepath.Join(cwd, arg)); ok {
		return path, locPath
	}
	return "", locNone
}

// placed tells the places that are no directories, and so are not looked
// for on a disk: the list of drives, the Recycle Bin.
func placed(path string) bool { return path == fsys.Computer || path == shell.RecycleBin }

func locateCLSID(name string) (string, locKind) {
	if strings.EqualFold(strings.TrimPrefix(name, "::"), computerCLSID) {
		return fsys.Computer, locPath
	}
	if strings.EqualFold(name, shell.RecycleBin) {
		return shell.RecycleBin, locPath
	}
	return "", locDelegate
}

// Find looks for what an address names, as one typed in an address bar:
// a folder to show, or a file, shown chosen in its folder. cwd is the
// folder relative paths are in, or empty.
func Find(text, cwd string) (Target, error) {
	path, kind := Locate(text, cwd)
	if kind != locPath {
		return Target{}, fmt.Errorf("cannot show %q", strings.TrimSpace(text))
	}
	if placed(path) {
		return Target{Path: path}, nil
	}
	e, err := fsys.Stat(path)
	if err != nil {
		return Target{}, err
	}
	if !e.IsDir() {
		if parent, ok := fsys.Parent(path); ok {
			return Target{Path: parent, Select: fsys.Base(path)}, nil
		}
	}
	return Target{Path: path}, nil
}

// Resolve looks at the targets on disk: one that names a file becomes its
// folder with the file chosen, as a file given to a file manager is shown,
// and one that names nothing is dropped, with an error naming the first.
func (cmd *Command) Resolve() error {
	var first error
	kept := cmd.Targets[:0]
	for _, t := range cmd.Targets {
		if placed(t.Path) {
			kept = append(kept, t)
			continue
		}
		e, err := fsys.Stat(t.Path)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if !e.IsDir() && t.Select == "" {
			if parent, ok := fsys.Parent(t.Path); ok {
				t = Target{Path: parent, Select: fsys.Base(t.Path)}
			}
		}
		kept = append(kept, t)
	}
	cmd.Targets = kept
	return first
}

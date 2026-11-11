package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// folderTimes is the timings of one folder, as Cairn's own benchmark
// writes them.
type folderTimes struct {
	Path    string `json:"path"`
	Entries int    `json:"entries"`
	// ShownMs is, for each visit, the time from asking for the folder to
	// its items being painted.
	ShownMs []float64 `json:"shownMs"`
	Median  float64   `json:"medianShownMs"`
	Min     float64   `json:"minShownMs"`
	// HandledMs is the part of it Explorer++ took to handle the request:
	// to parse the path, enumerate the folder, and fill and sort the list.
	HandledMs []float64 `json:"handledMs,omitempty"`
	// EntriesOnDisk is how many entries the folder holds, hidden ones
	// included, by this program's own reading of it.
	EntriesOnDisk int `json:"entriesOnDisk,omitempty"`
}

// report is what a benchmark writes: Cairn's shape, and a little more.
type report struct {
	App string `json:"app"`
	// StartupMs is the median of StartupRunsMs: each the time from just
	// before the process was created to its first folder being shown.
	StartupMs     float64   `json:"startupMs"`
	StartupRunsMs []float64 `json:"startupRunsMs,omitempty"`
	// StartupFrom is where StartupMs is counted from: "CreateProcess"
	// here; Cairn's own report, which has no such field, counts from the
	// first thing its process does.
	StartupFrom   string        `json:"startupFrom,omitempty"`
	StartupFolder string        `json:"startupFolder,omitempty"`
	View          string        `json:"view,omitempty"`
	Rounds        int           `json:"rounds"`
	Folders       []folderTimes `json:"folders"`
	Method        string        `json:"method,omitempty"`
	// Conditions is how the program was run, where that bears on its
	// times.
	Conditions string   `json:"conditions,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`

	// Cairn's report has these: the performance counter when its first
	// folder was shown, and the counter's frequency.
	FirstShownTicks int64 `json:"firstShownTicks,omitempty"`
	TicksPerSecond  int64 `json:"ticksPerSecond,omitempty"`
}

const explorerppMethod = "A navigation starts just before Enter (WM_KEYDOWN) is sent to the address bar's edit control, " +
	"which already holds the folder's path, and ends when the tab's list view, holding the folder's items, has an empty update region " +
	"and its thread is back in its message loop: when it has painted them, of its own accord. " +
	"Startup is from just before CreateProcess of Explorer++ with the folder as its argument to the same end."

// fullSpeedCondition says what start does to every process it starts.
const fullSpeedCondition = "its process scheduled as a foreground program's is, wherever its window (power throttling off)"

// options are what a benchmark run is asked for.
type options struct {
	exe      string
	folders  string
	out      string
	start    string
	view     string
	shots    string
	rounds   int
	startups int
	pause    time.Duration
	activate bool
	verbose  bool
}

// settle is how long Explorer++ is left alone after it starts, before
// the first folder is asked for.
const settle = 1500 * time.Millisecond

// runBench times a private Explorer++ entering the listed folders.
func runBench(o options) error {
	paths, err := readFolders(o.folders)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("%s lists no folders", o.folders)
	}
	box, err := newSandbox(o.exe, o.view)
	if err != nil {
		return err
	}
	r := report{
		App:           "Explorer++ " + box.version,
		StartupFrom:   "CreateProcess",
		StartupFolder: o.start,
		Rounds:        o.rounds,
		Method:        explorerppMethod,
	}
	warn := func(format string, args ...any) {
		w := fmt.Sprintf(format, args...)
		r.Warnings = append(r.Warnings, w)
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	// The title Explorer++'s window has in each folder, to tell that it
	// went there. Asking the shell for a name does not read the folder.
	startTitle := displayName(o.start) + titleSuffix
	titles := make([]string, len(paths))
	for i, path := range paths {
		titles[i] = displayName(path) + titleSuffix
	}

	// Startup, a few times over; the last one started stays for the
	// navigations.
	var x *explorerpp
	defer func() { x.kill() }()
	startEntries := 0
	for i := range o.startups {
		x.kill()
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}
		if x, err = box.launch(o.start, o.activate); err != nil {
			return err
		}
		x.pictures = o.shots != ""
		shown, err := x.awaitStarted(time.Minute)
		if err != nil {
			return err
		}
		took := ms(x.started, shown)
		if x.pictures {
			if err := savePicture(o.shots, fmt.Sprintf("startup %d", i+1), x.startPicture); err != nil {
				return err
			}
		}
		if !x.sawUnpainted {
			warn("startup %d: the list view was never seen waiting to paint; its time may end too early", i+1)
		}
		switch took, stillHas := x.yieldForeground(); {
		case stillHas:
			warn("startup %d: Explorer++'s window took the keyboard, and it could not be given back", i+1)
		case took && o.verbose:
			fmt.Printf("startup %d: Explorer++'s window took the keyboard; it was given back\n", i+1)
		}
		if startEntries, err = x.entries(x.listView()); err != nil {
			return err
		}
		if title, err := text(x.main); err != nil {
			return err
		} else if title != startTitle {
			return fmt.Errorf("Explorer++ started in %q, not %q", title, startTitle)
		}
		r.StartupRunsMs = append(r.StartupRunsMs, round3(took))
		fmt.Printf("startup %d: %.1f ms  (%s, %d items)\n", i+1, took, o.start, startEntries)
	}
	r.StartupMs = median(r.StartupRunsMs)

	time.Sleep(settle)

	r.Folders = make([]folderTimes, len(paths))
	for i, p := range paths {
		r.Folders[i].Path = p
	}
	for round := range o.rounds {
		for i, path := range paths {
			f := &r.Folders[i]
			v, err := x.navigate(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if o.verbose {
				fmt.Printf("round %d  %-32s %9.2f ms  (handled %9.2f ms, %5d items, %s)\n",
					round+1, path, v.shownMs, v.syncMs, v.entries, v.view)
			}

			// What was timed must have been this folder, shown.
			if v.title != titles[i] {
				return fmt.Errorf("%s: Explorer++ is in %q, not %q: the navigation did not happen", path, v.title, titles[i])
			}
			if !v.dirty {
				warn("%s, round %d: the list view had nothing to paint when the navigation returned", path, round+1)
			}
			if round > 0 && v.entries != f.Entries {
				warn("%s, round %d: the list view holds %d items; it held %d the round before", path, round+1, v.entries, f.Entries)
			}
			if r.View != "" && v.view != r.View {
				warn("%s, round %d: the view is %s, not %s", path, round+1, v.view, r.View)
			}
			r.View = v.view
			f.Entries = v.entries
			f.ShownMs = append(f.ShownMs, round3(v.shownMs))
			f.HandledMs = append(f.HandledMs, round3(v.syncMs))
			if v.picture != nil {
				if err := savePicture(o.shots, fmt.Sprintf("round %d %s", round+1, path), v.picture); err != nil {
					return err
				}
			}

			// Rest in the folder, as someone would, and as Cairn's
			// benchmark does: from when it was shown.
			time.Sleep(o.pause - time.Duration(ms(v.shown, now())*float64(time.Millisecond)))

			// Were the items all there when the clock stopped? If
			// Explorer++ went on adding them afterwards, it was not done.
			if n, err := x.entries(v.lv); err != nil {
				return err
			} else if n != v.entries {
				warn("%s, round %d: the list view held %d items when timed, %d after the rest", path, round+1, v.entries, n)
			}
		}
	}
	x.kill()

	// Did the list view hold what the folders hold? Counted only now, so
	// that this program's reading of the folders came after Explorer++'s.
	if n, err := shellEntries(o.start); err != nil || n != startEntries {
		warn("startup: the list view held %d items; the shell lists %d in %s (%v)", startEntries, n, o.start, err)
	}
	for i := range r.Folders {
		f := &r.Folders[i]
		f.Median, f.Min = median(f.ShownMs), minimum(f.ShownMs)
		f.EntriesOnDisk = entriesOnDisk(f.Path)
		if n, err := shellEntries(f.Path); err != nil || n != f.Entries {
			warn("%s: the list view held %d items; the shell lists %d (%v)", f.Path, f.Entries, n, err)
		}
	}
	window := "shown but kept from becoming the active window"
	if o.activate {
		window = "shown and active"
	}
	settings := "its default settings but for its navigation sound, off"
	if o.view != "" {
		settings += ", and its view, set to " + o.view
	}
	r.Conditions = fmt.Sprintf("A private copy of Explorer++ on %s; one tab, in %s view; its window %s; %s.",
		settings, r.View, window, fullSpeedCondition)

	printReport(r)
	if o.out == "" {
		return nil
	}
	return writeJSON(o.out, r)
}

// readFolders reads the folders to enter: one a line, with blank lines
// and lines beginning with # left out.
func readFolders(file string) ([]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var paths []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\uFEFF"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		paths = append(paths, filepath.Clean(line))
	}
	return paths, sc.Err()
}

// entriesOnDisk is how many entries a folder holds, hidden and system
// ones included, or -1 if it cannot be read.
func entriesOnDisk(path string) int {
	entries, err := os.ReadDir(path)
	if err != nil {
		return -1
	}
	return len(entries)
}

// median is the middle of some timings: the upper middle of an even
// number of them, as Cairn's benchmark takes it.
func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sorted := append([]float64(nil), v...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

func minimum(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		m = min(m, x)
	}
	return m
}

// round3 keeps a time in milliseconds to the microsecond.
func round3(v float64) float64 {
	return float64(int64(v*1000+0.5)) / 1000
}

func printReport(r report) {
	fmt.Printf("\n%s, %s view, %d rounds\n", r.App, r.View, r.Rounds)
	fmt.Printf("startup: %.1f ms (median of %v)\n\n", r.StartupMs, r.StartupRunsMs)
	fmt.Printf("%-34s %8s %12s %12s\n", "folder", "entries", "median ms", "min ms")
	for _, f := range r.Folders {
		fmt.Printf("%-34s %8d %12.2f %12.2f\n", f.Path, f.Entries, f.Median, f.Min)
	}
}

func writeJSON(file string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o644)
}

func readReport(file string) (report, error) {
	var r report
	data, err := os.ReadFile(file)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("%s: %w", file, err)
	}
	return r, nil
}

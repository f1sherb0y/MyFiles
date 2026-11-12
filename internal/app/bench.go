package app

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"myfiles/internal/cli"
	"myfiles/internal/clock"
	"myfiles/internal/fsys"
	"myfiles/internal/workspace"
)

// Started is when the program started, as nearly as it can tell.
var Started = clock.Now()

// mark prints how long after the program's start something happened,
// when MYFILES_TRACE is set: where the time to the first frame goes.
func mark(what string) {
	if tracing {
		os.Stderr.WriteString("trace " + what + " at " + formatDuration(clock.Since(Started)) + "\n")
	}
}

// benchPause is how long the benchmark rests in a folder before it goes
// to the next, as someone would, and as the same run of another file
// manager does.
const benchPause = 250 * time.Millisecond

// benchFolder is the timings of one folder.
type benchFolder struct {
	Path    string `json:"path"`
	Entries int    `json:"entries"`
	// ShownMs is, for each visit, the time from asking for the folder
	// to the frame showing its rows being handed to the display.
	ShownMs []float64 `json:"shownMs"`
	// ListMs is the part of it spent reading and sorting the folder.
	ListMs []float64 `json:"listMs"`
	Median float64   `json:"medianShownMs"`
	Min    float64   `json:"minShownMs"`
}

// benchReport is what a benchmark writes.
type benchReport struct {
	App string `json:"app"`
	// StartupMs is the time from the program's start to its first
	// folder's rows being on screen.
	StartupMs float64 `json:"startupMs"`
	// FirstShownTicks is the system's performance counter when the
	// first folder's rows were on screen, and TicksPerSecond its
	// frequency: whoever started the program, and read the counter
	// before, knows how long the start took, the process's included.
	FirstShownTicks int64         `json:"firstShownTicks"`
	TicksPerSecond  int64         `json:"ticksPerSecond"`
	Rounds          int           `json:"rounds"`
	Folders         []benchFolder `json:"folders"`
}

// bench times entering folders: each of a list, in turn, for a number of
// rounds, in one tab, as a user going from folder to folder.
type bench struct {
	cmd     *cli.Command
	paths   []string
	report  benchReport
	at      int
	round   int
	started bool
	first   bool
	// shot and done are for --screenshot, which the same machinery
	// serves: once the first folder shows, wait a moment and capture.
	shotDone bool
}

func newBench(cmd *cli.Command) *bench {
	b := &bench{cmd: cmd}
	b.report.App = "myfiles " + Version
	b.report.Rounds = cmd.BenchRounds
	if f, err := os.Open(cmd.Bench); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\uFEFF"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if path, ok := fsys.Clean(line); ok {
				b.paths = append(b.paths, path)
				b.report.Folders = append(b.report.Folders, benchFolder{Path: path})
			}
		}
	}
	return b
}

// shown is called when a folder's rows reached the screen.
func (b *bench) shown(w *Window, t *Tab) {
	if !b.first {
		b.first = true
		b.report.StartupMs = ms(clock.Since(Started))
		b.report.FirstShownTicks, b.report.TicksPerSecond = clock.Ticks()
	}
	if b.cmd.Screenshot != "" {
		if !b.shotDone {
			b.shotDone = true
			b.debug(w)
			// The icons come a moment after the rows.
			time.AfterFunc(900*time.Millisecond, func() { b.capture(w) })
		}
		return
	}
	if len(b.paths) == 0 {
		b.finish()
		return
	}
	if b.started {
		f := &b.report.Folders[b.at]
		if fsys.SameName(t.Path, f.Path) {
			f.Entries = len(t.entries)
			f.ShownMs = append(f.ShownMs, ms(t.shown))
			f.ListMs = append(f.ListMs, ms(t.listed))
		}
		b.at++
		if b.at == len(b.paths) {
			b.at = 0
			b.round++
		}
		if b.round >= b.report.Rounds {
			b.finish()
			return
		}
	}
	b.started = true
	path := b.paths[b.at]
	time.AfterFunc(benchPause, func() {
		w.update(func() { w.navigate(t, path, navPush, "") })
	})
}

// debug does what --debug asks for before the picture is taken, to show
// a state of the interface: "settings", "edit" (the address as text),
// "split:<path>" (a folder in a pane beside), "select:<name>".
func (b *bench) debug(w *Window) {
	t := w.current()
	for _, action := range b.cmd.Debug {
		name, arg, _ := strings.Cut(action, ":")
		switch name {
		case "settings":
			w.openSettings()
		case "edit":
			if t != nil {
				w.editAddress(t)
			}
		case "split", "split-down":
			if p := w.layout.Focused(); p != nil {
				zone := workspace.ZoneRight
				if name == "split-down" {
					zone = workspace.ZoneBottom
				}
				id := w.newTab(p.ID, arg, "")
				w.layout.Split(p.ID, id, zone)
			}
		case "tab":
			if p := w.layout.Focused(); p != nil {
				w.newTab(p.ID, arg, "")
			}
		case "select":
			if t != nil {
				t.choose(strings.Split(arg, "|")...)
			}
		case "filter":
			if t != nil {
				t.filter = arg
			}
		case "view":
			if t != nil {
				w.setView(t, viewOf(arg))
			}
		case "dark", "light":
			w.app.desk.appearance(name)
		}
	}
	w.invalidate()
}

// capture writes a picture of the window, and quits.
func (b *bench) capture(w *Window) {
	png, err := w.win.CapturePage()
	if err == nil {
		err = os.WriteFile(b.cmd.Screenshot, png, 0o644)
	}
	if err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
	}
	mygo.App.Quit()
}

func (b *bench) finish() {
	for i := range b.report.Folders {
		f := &b.report.Folders[i]
		if len(f.ShownMs) == 0 {
			continue
		}
		sorted := append([]float64(nil), f.ShownMs...)
		sort.Float64s(sorted)
		f.Min = sorted[0]
		f.Median = sorted[len(sorted)/2]
	}
	data, _ := json.MarshalIndent(b.report, "", "  ")
	if b.cmd.BenchOut != "" {
		_ = os.WriteFile(b.cmd.BenchOut, data, 0o644)
	} else {
		os.Stdout.Write(append(data, '\n'))
	}
	mygo.App.Quit()
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// formatDuration writes how long a folder took to show: "0.8 ms",
// "12 ms", "1.4 s".
func formatDuration(d time.Duration) string {
	switch {
	case d < 10*time.Millisecond:
		return trimFloat(float64(d.Microseconds())/1000, 1) + " ms"
	case d < time.Second:
		return trimFloat(float64(d.Milliseconds()), 0) + " ms"
	}
	return trimFloat(d.Seconds(), 1) + " s"
}

func trimFloat(v float64, digits int) string {
	b, _ := json.Marshal(roundTo(v, digits))
	return string(b)
}

func roundTo(v float64, digits int) float64 {
	p := 1.0
	for range digits {
		p *= 10
	}
	return float64(int64(v*p+0.5)) / p
}

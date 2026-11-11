package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runCompare prints two benchmark reports side by side, folder by
// folder, and writes the same as Markdown.
//
// startups are reports that hold only a startup time, measured from
// outside (-cairn-startup): each replaces the startup time of the report
// of the same app, which is that app's own count.
func runCompare(fileA, fileB string, startups []string, md string) error {
	a, err := readReport(fileA)
	if err != nil {
		return err
	}
	b, err := readReport(fileB)
	if err != nil {
		return err
	}
	for _, file := range startups {
		s, err := readReport(file)
		if err != nil {
			return err
		}
		var of *report
		switch s.App {
		case a.App:
			of = &a
		case b.App:
			of = &b
		default:
			return fmt.Errorf("%s is the startup of %q, which is neither %q nor %q", file, s.App, a.App, b.App)
		}
		of.StartupMs, of.StartupRunsMs, of.StartupFrom, of.StartupFolder = s.StartupMs, s.StartupRunsMs, s.StartupFrom, s.StartupFolder
		of.Method = strings.TrimSpace(of.Method + " " + s.Method)
		of.Conditions = strings.TrimSpace(of.Conditions + " " + s.Conditions)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# Time to show a folder: %s and %s\n\n", a.App, b.App)
	fmt.Fprintf(&out, "Milliseconds from asking for a folder to its entries being painted; median and fastest of %s.\n", roundsOf(a, b))
	fmt.Fprintf(&out, "The ratio is %s's median over %s's.\n\n", b.App, a.App)

	// The columns are headed by the apps' names without their versions.
	nameA, nameB := shortName(a.App), shortName(b.App)
	rows := [][]string{{
		"Folder",
		"Entries, " + nameA, nameB,
		"Median ms, " + nameA, nameB,
		"Min ms, " + nameA, nameB,
		"Ratio",
	}}
	inB := make(map[string]folderTimes, len(b.Folders))
	for _, f := range b.Folders {
		inB[strings.ToLower(f.Path)] = f
	}
	for _, fa := range a.Folders {
		fb, ok := inB[strings.ToLower(fa.Path)]
		if !ok || len(fa.ShownMs) == 0 || len(fb.ShownMs) == 0 {
			continue
		}
		rows = append(rows, []string{
			fa.Path, fmt.Sprint(fa.Entries), fmt.Sprint(fb.Entries),
			millis(fa.Median), millis(fb.Median),
			millis(fa.Min), millis(fb.Min),
			ratio(fb.Median, fa.Median),
		})
	}
	writeTable(&out, rows)

	fmt.Fprintf(&out, "\nStartup, to the first folder shown: %s %s, %s %s; ratio %s.\n",
		a.App, startupOf(a), b.App, startupOf(b), ratio(b.StartupMs, a.StartupMs))
	for _, r := range []report{a, b} {
		if r.Method != "" {
			fmt.Fprintf(&out, "\n%s: %s\n", r.App, r.Method)
		}
		if r.Conditions != "" {
			fmt.Fprintf(&out, "\n%s: %s\n", r.App, r.Conditions)
		}
		for _, w := range r.Warnings {
			fmt.Fprintf(&out, "\n%s, warning: %s\n", r.App, w)
		}
	}

	fmt.Print(out.String())
	if md == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(md), 0o755); err != nil {
		return err
	}
	return os.WriteFile(md, []byte(out.String()), 0o644)
}

// shortName is an app's name without its version: "Explorer++" of
// "Explorer++ 1.4.0.2293".
func shortName(app string) string {
	name, _, _ := strings.Cut(app, " ")
	return name
}

// roundsOf says how many visits the timings are of.
func roundsOf(a, b report) string {
	if a.Rounds == b.Rounds {
		return fmt.Sprintf("%d visits", a.Rounds)
	}
	return fmt.Sprintf("%d and %d visits", a.Rounds, b.Rounds)
}

// startupOf says an app's startup time, and from when it is counted.
func startupOf(r report) string {
	from := "its process's start, by its own count"
	if r.StartupFrom != "" {
		from = r.StartupFrom
	}
	s := fmt.Sprintf("%s ms from %s", millis(r.StartupMs), from)
	if r.StartupFolder != "" {
		s += " in " + r.StartupFolder
	}
	if n := len(r.StartupRunsMs); n > 1 {
		s += fmt.Sprintf(" (median of %d)", n)
	}
	return s
}

// millis writes a time with the digits that mean something.
func millis(v float64) string {
	switch {
	case v < 10:
		return fmt.Sprintf("%.2f", v)
	case v < 100:
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.0f", v)
}

func ratio(v, base float64) string {
	if base <= 0 {
		return "–"
	}
	r := v / base
	if r < 10 {
		return fmt.Sprintf("%.1f×", r)
	}
	return fmt.Sprintf("%.0f×", r)
}

// writeTable writes rows, the first of them the header, as a Markdown
// table with its columns lined up, so that it reads as well printed as
// rendered.
func writeTable(out *strings.Builder, rows [][]string) {
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], len([]rune(cell)))
		}
	}
	line := func(row []string) {
		for i, cell := range row {
			pad := strings.Repeat(" ", widths[i]-len([]rune(cell)))
			if i == 0 {
				fmt.Fprintf(out, "| %s%s ", cell, pad) // names to the left
			} else {
				fmt.Fprintf(out, "| %s%s ", pad, cell) // numbers to the right
			}
		}
		out.WriteString("|\n")
	}
	line(rows[0])
	for i, w := range widths {
		if i == 0 {
			fmt.Fprintf(out, "| %s ", strings.Repeat("-", w))
		} else {
			fmt.Fprintf(out, "| %s: ", strings.Repeat("-", w-1))
		}
	}
	out.WriteString("|\n")
	for _, row := range rows[1:] {
		line(row)
	}
}

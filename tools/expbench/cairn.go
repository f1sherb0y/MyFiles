package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

const cairnStartupMethod = "Startup is from just before CreateProcess of Cairn with the folder as its argument " +
	"to the performance counter Cairn itself read when the frame with that folder's rows was handed to the display (firstShownTicks)."

// runCairnStartup times Cairn's start the way Explorer++'s is timed:
// from just before its process is created to its first folder being
// shown. Cairn says when that was, as a reading of the same counter this
// program reads before starting it.
func runCairnStartup(exe, folder string, runs int, out string) error {
	exe, err := filepath.Abs(exe)
	if err != nil {
		return err
	}
	if _, err := os.Stat(exe); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "cairn-expbench-startup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	// A benchmark of no folders: Cairn shows the folder it was started
	// in, writes its report, and exits.
	none := filepath.Join(tmp, "no-folders.txt")
	if err := os.WriteFile(none, nil, 0o644); err != nil {
		return err
	}

	r := report{
		StartupFrom:   "CreateProcess",
		StartupFolder: folder,
		Method:        cairnStartupMethod,
		Conditions:    "Startup only: Cairn started by expbench, " + fullSpeedCondition + ".",
	}
	for i := range runs {
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}
		result := filepath.Join(tmp, fmt.Sprintf("run%d.json", i+1))
		process, _, before, err := start(exe, []string{"--bench", none, "--bench-out", result, folder}, "", 0)
		if err != nil {
			return err
		}
		event, _ := windows.WaitForSingleObject(process, 60_000)
		if event != windows.WAIT_OBJECT_0 {
			windows.TerminateProcess(process, 1)
		}
		windows.CloseHandle(process)
		if event != windows.WAIT_OBJECT_0 {
			return errors.New("Cairn did not finish in a minute, and was ended")
		}
		own, err := readReport(result)
		if err != nil {
			return fmt.Errorf("Cairn wrote no report: %w", err)
		}
		if own.FirstShownTicks == 0 || own.TicksPerSecond == 0 {
			return errors.New("Cairn's report has no firstShownTicks")
		}
		took := float64(own.FirstShownTicks-int64(before)) * 1000 / float64(own.TicksPerSecond)
		r.App = own.App
		r.StartupRunsMs = append(r.StartupRunsMs, round3(took))
		fmt.Printf("startup %d: %.1f ms from CreateProcess  (%.1f ms by its own count, from its process's start)\n",
			i+1, took, own.StartupMs)
	}
	r.StartupMs = median(r.StartupRunsMs)
	fmt.Printf("%s startup in %s: %.1f ms (median of %v)\n", r.App, folder, r.StartupMs, r.StartupRunsMs)
	if out == "" {
		return nil
	}
	return writeJSON(out, r)
}

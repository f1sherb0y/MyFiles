package main

import (
	"os"
	"runtime"
	"runtime/pprof"
	"time"
)

// profile writes a CPU profile of the program's first moments to the
// file MYFILES_PROFILE names, for finding what its start waits on.
func profile() {
	path := os.Getenv("MYFILES_PROFILE")
	if path == "" {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	runtime.SetCPUProfileRate(4000)
	if pprof.StartCPUProfile(f) != nil {
		f.Close()
		return
	}
	time.AfterFunc(400*time.Millisecond, func() {
		pprof.StopCPUProfile()
		f.Close()
	})
}

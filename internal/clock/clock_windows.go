// Package clock tells the time between two moments precisely, for
// measuring how long things take: Go's own clock on Windows steps with
// the system's timer, half a millisecond at best and 15 at worst.
package clock

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32  = syscall.NewLazyDLL("kernel32.dll")
	procQPC   = kernel32.NewProc("QueryPerformanceCounter")
	procQPF   = kernel32.NewProc("QueryPerformanceFrequency")
	frequency = func() int64 {
		var f int64
		procQPF.Call(uintptr(unsafe.Pointer(&f)))
		if f <= 0 {
			f = 1
		}
		return f
	}()
)

// Time is a moment, to measure from.
type Time int64

// Now is the moment it is called.
func Now() Time {
	var c int64
	procQPC.Call(uintptr(unsafe.Pointer(&c)))
	return Time(c)
}

// Ticks is the system's counter now, and how many it counts a second:
// the same clock in every process.
func Ticks() (now, perSecond int64) { return int64(Now()), frequency }

// Since is how long ago t was.
func Since(t Time) time.Duration { return Now().Sub(t) }

// Sub is the time from u to t.
func (t Time) Sub(u Time) time.Duration {
	d := int64(t - u)
	sec := d / frequency
	rem := d % frequency
	return time.Duration(sec)*time.Second + time.Duration(rem*int64(time.Second)/frequency)
}

// IsZero reports whether t was never set.
func (t Time) IsZero() bool { return t == 0 }

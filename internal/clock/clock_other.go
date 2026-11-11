//go:build !windows

// Package clock tells the time between two moments precisely, for
// measuring how long things take.
package clock

import "time"

var base = time.Now()

// Time is a moment, to measure from.
type Time int64

// Now is the moment it is called.
func Now() Time { return Time(time.Since(base)) + 1 }

// Ticks is the clock now, and how many it counts a second.
func Ticks() (now, perSecond int64) { return time.Now().UnixNano(), int64(time.Second) }

// Since is how long ago t was.
func Since(t Time) time.Duration { return Now().Sub(t) }

// Sub is the time from u to t.
func (t Time) Sub(u Time) time.Duration { return time.Duration(t - u) }

// IsZero reports whether t was never set.
func (t Time) IsZero() bool { return t == 0 }

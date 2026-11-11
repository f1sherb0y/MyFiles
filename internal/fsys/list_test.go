package fsys

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestListSpeed(t *testing.T) {
	for _, dir := range []string{`C:\Windows\System32`, `C:\Windows\WinSxS`, `C:\Windows`, `D:\Code`} {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		var l *Listing
		var err error
		var view []int32
		best, bestSort := time.Hour, time.Hour
		for range 5 {
			start := time.Now()
			l, err = List(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			took := time.Since(start)
			best = min(best, took)
			start = time.Now()
			view = All(l.Entries)
			Sort(l.Entries, view, Order{DirsFirst: true})
			bestSort = min(bestSort, time.Since(start))
		}
		t.Logf("%-24s %6d entries  list %8v  sort %8v  first=%q last=%q", dir, len(l.Entries), best, bestSort, l.Entries[view[0]].Name, l.Entries[view[len(view)-1]].Name)
	}
}

func TestSplit(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{`C:\`, []string{`C:\`}},
		{`C:\Users\fish`, []string{`C:\`, `C:\Users`, `C:\Users\fish`}},
		{`\\srv\share\a\b`, []string{`\\srv\share\`, `\\srv\share\a`, `\\srv\share\a\b`}},
	} {
		parts := Split(tc.in)
		if len(parts) != len(tc.want) {
			t.Fatalf("%s: %v", tc.in, parts)
		}
		for i, p := range parts {
			if p.Path != tc.want[i] {
				t.Errorf("%s: part %d is %q, want %q", tc.in, i, p.Path, tc.want[i])
			}
		}
	}
	for in, want := range map[string]string{
		`c:/users/fish/`:      `C:\users\fish`,
		`C:`:                  `C:\`,
		`C:\a\..\b\.\c`:       `C:\b\c`,
		`\\?\C:\x`:            `C:\x`,
		`\\srv\share`:         `\\srv\share\`,
		`\\?\UNC\srv\share\d`: `\\srv\share\d`,
		`"relative"`:          ``,
	} {
		got, _ := Clean(in)
		if got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

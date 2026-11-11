//go:build !windows

package fsys

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file is the file system as every system but Windows has it, with
// the standard library alone: enough for the program to build and run
// there. A port gives a system files of its own where it pays to, as
// list_windows.go reads a directory in one pass.

const pathSep = '/'

// SameName reports whether two names, or paths, are the same to the file
// system.
func SameName(a, b string) bool { return a == b }

// Clean makes a path the one form the app keeps, and returns false for
// what is no absolute path.
func Clean(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/") {
		return "", false
	}
	return filepath.Clean(path), true
}

// Split is the components of a clean path, the root first.
func Split(path string) []Part {
	if !strings.HasPrefix(path, "/") {
		return nil
	}
	parts := []Part{{Name: "/", Path: "/"}}
	at := 0
	for _, name := range strings.Split(strings.Trim(path, "/"), "/") {
		if name == "" {
			continue
		}
		at += 1 + len(name)
		parts = append(parts, Part{Name: name, Path: path[:at]})
	}
	return parts
}

// LongPath is path as the system takes paths of any length.
func LongPath(path string) string { return path }

// List reads a directory whole, unsorted.
func List(ctx context.Context, path string) (*Listing, error) {
	start := time.Now()
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, &PathError{Op: "read", Path: path, Err: err}
	}
	l := &Listing{Path: path, Entries: make([]Entry, 0, len(entries))}
	for _, d := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		l.Entries = append(l.Entries, entryOf(d.Name(), info))
	}
	l.Took = time.Since(start)
	return l, nil
}

func entryOf(name string, info os.FileInfo) Entry {
	e := Entry{Name: name, ModTime: info.ModTime().UnixNano(), Created: info.ModTime().UnixNano()}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		e.Attr |= AttrLink
	}
	if mode.IsDir() {
		e.Attr |= AttrDir
	} else {
		e.Size = info.Size()
	}
	if strings.HasPrefix(name, ".") {
		e.Attr |= AttrHidden
	}
	if mode.Perm()&0o200 == 0 {
		e.Attr |= AttrReadOnly
	}
	return e
}

// Stat describes one entry by its path.
func Stat(path string) (Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Entry{}, &PathError{Op: "stat", Path: path, Err: err}
	}
	return entryOf(Base(path), info), nil
}

// keyer makes sort keys.
type keyer struct{ buf []byte }

// key is a key that sorts a name in natural order, without case.
func (k *keyer) key(name string) string {
	k.buf = naturalKey(k.buf[:0], name)
	return string(k.buf)
}

// Volumes lists the places files are kept under roots of their own: the
// root alone, until a port lists the volumes mounted.
func Volumes() []Volume {
	return []Volume{{Root: "/", Name: "/", Label: "Computer", Ready: true}}
}

// Places lists the user's folders a sidebar shows, those that exist.
func Places() []Place {
	home := Home()
	out := []Place{{Kind: PlaceHome, Name: Base(home), Path: home}}
	for _, p := range []struct {
		kind PlaceKind
		name string
	}{
		{PlaceDesktop, "Desktop"}, {PlaceDownloads, "Downloads"}, {PlaceDocuments, "Documents"},
		{PlacePictures, "Pictures"}, {PlaceMusic, "Music"}, {PlaceVideos, "Movies"}, {PlaceVideos, "Videos"},
	} {
		path := filepath.Join(home, p.name)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			out = append(out, Place{Kind: p.kind, Name: p.name, Path: path})
		}
	}
	return out
}

// Home is the user's own folder.
func Home() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

// KnownFolder is the path of a folder by a name the system's file
// manager gives it; other systems have no such names.
func KnownFolder(name string) (string, bool) { return "", false }

// ExpandEnv replaces $NAME in a path with the environment's values.
func ExpandEnv(s string) string { return os.ExpandEnv(s) }

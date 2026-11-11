// Package fsys reads directories and describes their entries. It knows
// nothing of the interface: the platform files (list_windows.go and the
// like) fill Entry values as fast as the system gives them, and the rest
// is portable.
package fsys

import (
	"strings"
	"time"
)

// Attr is what an entry is, apart from its name and size.
type Attr uint16

const (
	AttrDir Attr = 1 << iota
	AttrHidden
	AttrSystem
	AttrReadOnly
	// AttrLink is a symbolic link, a junction or another reparse point
	// that leads elsewhere.
	AttrLink
	// AttrOffline is a file whose content is not on the disk, as a cloud
	// placeholder: reading it would fetch it.
	AttrOffline
	// AttrCustomIcon is a directory that may name an icon of its own
	// (desktop.ini on Windows, which the shell reads only for directories
	// marked read-only or system).
	AttrCustomIcon
)

// Entry is one file or directory of a listing.
type Entry struct {
	Name string
	// Size is the file's length in bytes, 0 for directories.
	Size int64
	// ModTime is when the entry was last written, in nanoseconds since
	// the Unix epoch.
	ModTime int64
	// Created is when the entry was made, as ModTime.
	Created int64
	Attr    Attr
	// key sorts names as the system's file manager does (natural order,
	// without case); Listing.Sort fills it.
	key string
}

// IsDir reports whether the entry is a directory.
func (e *Entry) IsDir() bool { return e.Attr&AttrDir != 0 }

// Hidden reports whether file managers leave the entry out by default.
func (e *Entry) Hidden() bool { return e.Attr&AttrHidden != 0 }

// Modified is ModTime as a time.
func (e *Entry) Modified() time.Time { return time.Unix(0, e.ModTime) }

// Ext is the name's extension in lower case, with its dot, "" for
// directories and names without one.
func (e *Entry) Ext() string {
	if e.IsDir() {
		return ""
	}
	return ExtOf(e.Name)
}

// ExtOf is a file name's extension in lower case, with its dot.
func ExtOf(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 || i == len(name)-1 {
		return ""
	}
	ext := name[i:]
	for j := 1; j < len(ext); j++ {
		if c := ext[j]; c >= 'A' && c <= 'Z' || c >= 0x80 {
			return strings.ToLower(ext)
		}
	}
	return ext
}

// Listing is a directory read whole.
type Listing struct {
	// Path is the directory, as the system names it.
	Path    string
	Entries []Entry
	// Took is how long reading the directory took, without sorting.
	Took time.Duration
}

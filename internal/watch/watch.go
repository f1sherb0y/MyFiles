// Package watch tells of the changes of folders shown, without holding
// the folders.
//
// A program that watches a folder keeps it open, and Windows renames no
// folder while one inside it is open: a tab on D:\a\b\c would keep D:\a
// from being renamed, moved, or sent to the Recycle Bin, by any program.
// So a Hub opens one thing only on each drive, its root, which nothing
// renames, watches all that changes below it, and tells each folder
// watched of the changes that are its own: what it holds changed, it was
// renamed or one above it was, with the path it has now, or it is gone.
//
// Every change on the drive is told to the Hub, most of them of no folder
// watched; each costs a comparison of names and no more. When another
// program writes files by the thousand a second, the Hub stops reading
// them one by one: a few times a second it looks whether the folders
// watched changed, by their modification times, until the drive is calm.
package watch

import "time"

// Kind is what happened to a folder watched.
type Kind int

const (
	// Changed: what the folder holds changed, or may have.
	Changed Kind = iota
	// Moved: the folder, or one above it, was renamed; To is its path now,
	// and the watch goes on there.
	Moved
	// Gone: the folder, or one above it, was deleted or moved away, or
	// renamed where its new path cannot be told.
	Gone
	// Lost: the watch ended, as when the drive was taken out; the folder
	// may be there still, watched by no one.
	Lost
)

// Event is something that happened to a folder watched.
type Event struct {
	Kind Kind
	// To is the folder's path after a Moved.
	To string
}

// The changes of a folder are told once they stop coming for quiet, and
// no later than latest after the first.
const (
	quiet  = 80 * time.Millisecond
	latest = 400 * time.Millisecond
)

// A drive is in flood when more changes a second than floodRate come from
// it, and calm again below calmRate; in flood it is read every floodPause.
var (
	floodRate  = 2000.0
	calmRate   = 500.0
	floodPause = 250 * time.Millisecond
)

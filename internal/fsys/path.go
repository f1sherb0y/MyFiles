package fsys

import (
	"strings"
)

// PathError is an error of an operation on a path.
type PathError struct {
	Op, Path string
	Err      error
}

func (e *PathError) Error() string { return e.Op + " " + e.Path + ": " + e.Err.Error() }
func (e *PathError) Unwrap() error { return e.Err }

// Computer is the place above every volume, which lists them: This PC on
// Windows. It is no path of the file system.
const Computer = "::computer"

// Sep is the path separator of the platform.
const Sep = pathSep

// Base is the last component of path: "C:\" for a drive's root, the share
// for a UNC root.
func Base(path string) string {
	parts := Split(path)
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1].Name
}

// Part is one component of a path, with the path up to and including it:
// what a breadcrumb shows and where it leads.
type Part struct {
	Name, Path string
}

// Join appends a name to a directory.
func Join(dir, name string) string {
	if dir == "" {
		return name
	}
	if strings.HasSuffix(dir, string(Sep)) {
		return dir + name
	}
	return dir + string(Sep) + name
}

// Parent is the directory holding path, and false for a root.
func Parent(path string) (string, bool) {
	parts := Split(path)
	if len(parts) < 2 {
		return "", false
	}
	return parts[len(parts)-2].Path, true
}

// Within reports whether path is dir or inside it.
func Within(path, dir string) bool {
	if !SameName(path[:min(len(path), len(dir))], dir) {
		return false
	}
	return len(path) == len(dir) || strings.HasSuffix(dir, string(Sep)) || path[len(dir)] == Sep
}

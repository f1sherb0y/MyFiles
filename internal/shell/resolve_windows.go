package shell

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"myfiles/internal/fsys"
	"myfiles/internal/lnk"
)

// Which program opens a file is found here, not by the system as it
// opens the file. Asked to open a file, Windows may send it to
// OpenWith.exe, the dialog that asks which program to use: for a type no
// program is set for, and also for one whose program the user chose, when
// another that opens the type was installed since (seen with .rar and
// Bandizip, after WPS took up archives: Windows 11 26300, 2026-11).
// OpenWith.exe shows its window for Explorer only. Started for any other
// program it ends with 0x80073D54, "the process has no package identity":
// nothing opens, and nothing tells of it.
//
// So a file is opened by the class of its program, as "Bandizip.rar",
// which the system's opening of a file does not look past; a shortcut to
// a document by the document; a link to an address by the program of the
// address's scheme; and a program picked from "Open with" by its own
// class, or as itself with the file. What has no program is told as
// ErrChoose, for the caller to ask with its own list.

// The mask a file is opened with: by its identifier, which the menus of
// the shell go by; waited for; and with nothing shown by the system.
const openMask = seeMaskInvokeIDList | seeMaskNoAsync | seeMaskNoUI

const (
	assocExecutable = 2  // ASSOCSTR_EXECUTABLE
	assocProgID     = 20 // ASSOCSTR_PROGID
)

// undecided reports a class that stands for no program: what the system
// names a type, or a scheme, that it would ask about.
func undecided(class string) bool {
	return class == "" || strings.EqualFold(class, "Unknown") || strings.EqualFold(class, "Undecided")
}

// classOf is the class of the program that opens files of a path's type.
// own is set for a type the shell opens itself, with no program to name,
// and ok unset for one nothing opens: without an extension, without a
// program, or with one that is gone.
func classOf(path string) (class string, own, ok bool) {
	ext := filepath.Ext(path)
	if ext == "" {
		return "", false, false
	}
	class, found := assocString(ext, assocProgID)
	if !found || undecided(class) || needsChoice(path) {
		return "", false, false
	}
	_, named := assocString(ext, assocExecutable)
	return class, !named, true
}

// opensItself reports a type that is a program: what it is opened with
// is the file.
func opensItself(path string) bool {
	exe, ok := assocString(filepath.Ext(path), assocExecutable)
	return ok && strings.Contains(exe, "%")
}

// openResolved opens a file with the program found for it here.
func openResolved(owner Window, path, dir string, depth int) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".lnk":
		if target, ok := linkedDocument(path); ok && depth < 4 {
			// A shortcut to a document: the document, by its program.
			return openResolved(owner, target, filepath.Dir(target), depth+1)
		}
		// To a program, a folder, or what only the shell can follow: the
		// shell follows it, and what is at its end opens itself.
		return onSTA(func() error { return execute(owner, "", path, dir, openMask) })
	case ".url":
		if address, ok := linkedAddress(path); ok {
			return openAddress(owner, address, depth)
		}
	}
	class, own, ok := classOf(path)
	if !ok {
		return errNoProgram
	}
	return onSTA(func() error {
		err := executeAs(owner, "", path, dir, class, "", openMask)
		switch {
		case err == nil:
			return nil
		case own && errors.Is(err, windows.ERROR_NO_ASSOCIATION):
			// A type the shell opens by a part of its own, which a class
			// does not name: the shell opens it, and asks nobody.
			return execute(owner, "", path, dir, openMask)
		case gone(err):
			if _, statErr := os.Stat(path); statErr == nil {
				// The file is there: its program is not.
				return errNoProgram
			}
		}
		return err
	})
}

// gone reports an error of something not being there to open, or to open
// with.
func gone(err error) bool {
	return errors.Is(err, windows.ERROR_NO_ASSOCIATION) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}

// linkedDocument is the file a shortcut points at, when it is one that a
// program opens: not a program, which the shortcut may give arguments,
// nor a folder.
func linkedDocument(path string) (string, bool) {
	t, ok := lnk.Read(path)
	if !ok || t.Path == "" || t.Dir {
		return "", false
	}
	info, err := os.Stat(t.Path)
	if err != nil || info.IsDir() || opensItself(t.Path) {
		return "", false
	}
	return t.Path, true
}

// linkedAddress is the address an Internet shortcut holds.
func linkedAddress(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 1<<20 {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if len(line) > 4 && strings.EqualFold(line[:4], "URL=") {
			address := strings.TrimSpace(line[4:])
			return address, address != ""
		}
	}
	return "", false
}

// openAddress opens an address with the program set for its scheme, as
// the browser for "https".
func openAddress(owner Window, address string, depth int) error {
	scheme, _, ok := strings.Cut(address, ":")
	if !ok || scheme == "" {
		return errNoProgram
	}
	if strings.EqualFold(scheme, "file") {
		// A file: opened as one.
		u, err := url.Parse(address)
		if err != nil || depth >= 4 {
			return errNoProgram
		}
		path := filepath.FromSlash(strings.TrimPrefix(u.Path, "/"))
		if u.Host != "" {
			path = `\\` + u.Host + filepath.FromSlash(u.Path)
		}
		return openResolved(owner, path, filepath.Dir(path), depth+1)
	}
	class, ok := assocString(scheme, assocProgID)
	if !ok || undecided(class) {
		return errNoProgram
	}
	if exe, ok := assocString(scheme, assocExecutable); ok && strings.EqualFold(filepath.Base(exe), "OpenWith.exe") {
		return errNoProgram
	}
	return onSTA(func() error {
		// An address is no item of the file system to open by identifier.
		return executeAs(owner, "", address, "", class, "", seeMaskNoAsync|seeMaskNoUI)
	})
}

// openWith opens a file with the program of a handler the user picked:
// by the class the program has for such files; else by the program's
// file, which a handler is named by, or keeps its icon in: by the class
// the program has under its own name, or as itself with the file. The
// handler's own Invoke is not used: it goes by the system's opening of
// the file. It runs on a thread of the shell's.
func openWith(owner Window, path string, h *object, app App) error {
	if class := handlerProgID(h); class != "" {
		if err := executeAs(owner, "", path, "", class, "", openMask); err == nil {
			return nil
		}
	}
	exe := app.ID
	if !isProgram(exe) {
		// Named otherwise, as "Adobe Illustrator 2025": where its icon is
		// is most often the program.
		exe = ""
		var p *uint16
		var index int32
		if !h.call(handlerGetIconLocation, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&index))).failed() && p != nil {
			if where := windows.UTF16PtrToString(p); isProgram(where) {
				exe = where
			}
			procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
		}
	}
	if exe == "" {
		// Nothing tells which program it is: none to start here.
		return &fsys.PathError{Op: "open", Path: path, Err: windows.ERROR_NO_ASSOCIATION}
	}
	return openWithProgram(owner, path, exe)
}

// openWithProgram opens a file with a program, by the program's file: by
// the class the program registered under its own name for the files it
// is handed, or as itself with the file.
func openWithProgram(owner Window, path, exe string) error {
	class := `Applications\` + filepath.Base(exe)
	if k, err := registry.OpenKey(registry.CLASSES_ROOT, class+`\shell`, registry.QUERY_VALUE); err == nil {
		k.Close()
		if executeAs(owner, "", path, "", class, "", openMask) == nil {
			return nil
		}
	}
	return executeAs(owner, "", exe, filepath.Dir(path), "exefile", `"`+path+`"`, seeMaskNoAsync|seeMaskNoUI)
}

// isProgram reports a path of a program's file that is there.
func isProgram(path string) bool {
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".exe") {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

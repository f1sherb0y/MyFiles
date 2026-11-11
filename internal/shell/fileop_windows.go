package shell

import (
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"cairn/internal/fsys"
)

// The flags of a file operation (FOF_ and FOFX_).
const (
	fofSilent            = 0x4
	fofRenameOnCollision = 0x8
	fofNoConfirmation    = 0x10
	fofAllowUndo         = 0x40
	fofNoConfirmMkdir    = 0x200
	fofNoErrorUI         = 0x400
	fofWantNukeWarning   = 0x4000
	fofxRecycleOnDelete  = 0x00080000
	fofxAddUndoRecord    = 0x20000000
)

// The methods of IFileOperation, by their place in its table.
const (
	fileOpAdvise                  = 3
	fileOpUnadvise                = 4
	fileOpSetOperationFlags       = 5
	fileOpSetOwnerWindow          = 9
	fileOpRenameItem              = 12
	fileOpMoveItem                = 14
	fileOpCopyItem                = 16
	fileOpDeleteItem              = 18
	fileOpPerformOperations       = 21
	fileOpGetAnyOperationsAborted = 22
)

// testFlags is added to the flags of every file operation. The package's
// tests set it, so that nothing they do can show a window; it is 0
// otherwise.
var testFlags uint32

// fileOp is one use of the shell's IFileOperation: items are added to it,
// and perform does to them all what was asked, with the shell's progress
// window, questions and undo. It lives on the thread that made it.
type fileOp struct {
	o *object
	// quiet says the shell shows no errors of the operation, which are
	// then ours to return.
	quiet bool
	// sink hears how each item went; cookie and forget are what take it
	// away again.
	sink   *progressSink
	cookie uint32
	forget func()
}

// newFileOp starts an operation whose windows owner owns.
func newFileOp(owner Window, flags uint32) (*fileOp, error) {
	o, err := coCreate(&clsidFileOperation, &iidFileOperation)
	if err != nil {
		return nil, err
	}
	flags |= testFlags
	f := &fileOp{o: o, quiet: flags&fofNoErrorUI != 0}
	f.sink, f.forget = newProgressSink()
	if err := o.call(fileOpSetOperationFlags, uintptr(flags)).err(); err != nil {
		f.release()
		return nil, err
	}
	if owner != 0 {
		o.call(fileOpSetOwnerWindow, uintptr(owner))
	}
	o.call(fileOpAdvise, uintptr(unsafe.Pointer(f.sink)), uintptr(unsafe.Pointer(&f.cookie)))
	return f, nil
}

func (f *fileOp) release() {
	if f.cookie != 0 {
		f.o.call(fileOpUnadvise, uintptr(f.cookie))
	}
	f.o.release()
	f.forget()
}

// perform does what was added. It returns ErrCanceled when the user
// stopped the operation or a part of it, which is also how an error ends
// that the shell showed the user itself.
func (f *fileOp) perform() error {
	hr := f.o.call(fileOpPerformOperations)
	var aborted int32
	f.o.call(fileOpGetAnyOperationsAborted, uintptr(unsafe.Pointer(&aborted)))
	if hr.failed() && !hr.canceled() {
		return hr.err()
	}
	if !hr.canceled() && aborted == 0 {
		return nil
	}
	// With no window for errors, the shell gives up on the first one
	// and calls that stopped: the sink heard what it was.
	if f.quiet && f.sink.failure != 0 {
		return f.sink.failure.err()
	}
	return ErrCanceled
}

// each adds an operation on every path: add queues it for the path's
// item.
func (f *fileOp) each(op string, paths []string, add func(item *object) hresult) error {
	for _, path := range paths {
		item, err := shellItem(path)
		if err != nil {
			return &fsys.PathError{Op: op, Path: path, Err: err}
		}
		err = add(item).err()
		item.release()
		if err != nil {
			return &fsys.PathError{Op: op, Path: path, Err: err}
		}
	}
	return nil
}

var errIntoItself = errors.New("the destination is the folder itself or inside it")

func transfer(owner Window, sources []string, destDir string, move bool) error {
	op := "copy"
	if move {
		op = "move"
	}
	// beside are the sources already in the destination: copies of them
	// get new names there, and moving them is nothing to do.
	var beside, others []string
	for _, src := range sources {
		if fsys.Within(destDir, src) {
			return &fsys.PathError{Op: op, Path: src, Err: errIntoItself}
		}
		if parent, ok := fsys.Parent(src); ok && fsys.SameName(parent, destDir) {
			if !move {
				beside = append(beside, src)
			}
			continue
		}
		others = append(others, src)
	}
	if len(beside)+len(others) == 0 {
		return nil
	}
	const flags = fofAllowUndo | fofxAddUndoRecord | fofNoConfirmMkdir
	method := fileOpCopyItem
	if move {
		method = fileOpMoveItem
	}
	run := func(paths []string, flags uint32) error {
		if len(paths) == 0 {
			return nil
		}
		f, err := newFileOp(owner, flags)
		if err != nil {
			return err
		}
		defer f.release()
		dest, err := shellItem(destDir)
		if err != nil {
			return &fsys.PathError{Op: op, Path: destDir, Err: err}
		}
		defer dest.release()
		err = f.each(op, paths, func(item *object) hresult {
			// No new name, and no sink for progress.
			return f.o.call(method, uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(dest)), 0, 0)
		})
		if err != nil {
			return err
		}
		return f.perform()
	}
	return onSTA(func() error {
		// The flag is the whole operation's, so the copies that are to
		// get new names are one of their own.
		if err := run(beside, flags|fofRenameOnCollision); err != nil {
			return err
		}
		return run(others, flags)
	})
}

func remove(owner Window, paths []string, permanent bool) error {
	if len(paths) == 0 {
		return nil
	}
	flags := uint32(fofAllowUndo | fofxRecycleOnDelete | fofNoConfirmation | fofWantNukeWarning | fofxAddUndoRecord)
	if permanent {
		flags = fofNoConfirmation
	}
	return onSTA(func() error {
		f, err := newFileOp(owner, flags)
		if err != nil {
			return err
		}
		defer f.release()
		err = f.each("delete", paths, func(item *object) hresult {
			return f.o.call(fileOpDeleteItem, uintptr(unsafe.Pointer(item)), 0)
		})
		if err != nil {
			return err
		}
		return f.perform()
	})
}

func rename(owner Window, path, newName string) error {
	fail := func(err error) error { return &fsys.PathError{Op: "rename", Path: path, Err: err} }
	parent, ok := fsys.Parent(path)
	if !ok || newName == "" || strings.ContainsAny(newName, `\/`) {
		return fail(windows.ERROR_INVALID_NAME)
	}
	old := fsys.Base(path)
	if newName == old {
		return nil
	}
	// Changing the case of a name finds the file itself under the new
	// one, which is no file in the way.
	if !fsys.SameName(newName, old) {
		if _, err := fsys.Stat(fsys.Join(parent, newName)); err == nil {
			return fail(fs.ErrExist)
		}
	}
	name, err := windows.UTF16PtrFromString(newName)
	if err != nil {
		return fail(err)
	}
	const flags = fofAllowUndo | fofxAddUndoRecord | fofSilent | fofNoErrorUI | fofNoConfirmation
	return onSTA(func() error {
		f, err := newFileOp(owner, flags)
		if err != nil {
			return err
		}
		defer f.release()
		err = f.each("rename", []string{path}, func(item *object) hresult {
			return f.o.call(fileOpRenameItem, uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(name)), 0)
		})
		if err != nil {
			return err
		}
		if err := f.perform(); err != nil {
			if err == ErrCanceled {
				return err
			}
			return fail(err)
		}
		return nil
	})
}

func newFolder(dir, name string) (string, error) {
	return create(dir, name, false)
}

func newFile(dir, name string) (string, error) {
	return create(dir, name, true)
}

// create makes an empty file or a directory in dir, named name or, when
// that is taken, name with the first number from 2 that is free: after
// the name of a directory, before the extension of a file.
func create(dir, name string, file bool) (string, error) {
	stem, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); file && i > 0 {
		stem, ext = name[:i], name[i:]
	}
	const tries = 10000
	for n := 1; n <= tries; n++ {
		candidate := name
		if n > 1 {
			candidate = stem + " (" + strconv.Itoa(n) + ")" + ext
		}
		path := fsys.Join(dir, candidate)
		p, err := windows.UTF16PtrFromString(fsys.LongPath(path))
		if err != nil {
			return "", &fsys.PathError{Op: "create", Path: path, Err: err}
		}
		if file {
			var h windows.Handle
			h, err = windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
			if err == nil {
				windows.CloseHandle(h)
			}
		} else {
			err = windows.CreateDirectory(p, nil)
		}
		switch err {
		case nil:
			return path, nil
		case windows.ERROR_ALREADY_EXISTS, windows.ERROR_FILE_EXISTS:
			continue
		}
		// A directory in the way of a file is refused as access denied.
		if _, statErr := fsys.Stat(path); err == windows.ERROR_ACCESS_DENIED && statErr == nil {
			continue
		}
		return "", &fsys.PathError{Op: "create", Path: path, Err: err}
	}
	return "", &fsys.PathError{Op: "create", Path: fsys.Join(dir, name), Err: fs.ErrExist}
}

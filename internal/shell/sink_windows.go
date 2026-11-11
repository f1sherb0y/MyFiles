package shell

import (
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iidUnknown                   = guid("00000000-0000-0000-c000-000000000046")
	iidFileOperationProgressSink = guid("04b0f1a7-9490-44bc-96e1-4296a31252e2")
)

// progressSink is the IFileOperationProgressSink of a file operation: a
// COM object made here, which the shell tells how each item went. It is
// there for one thing, the reason an operation without error windows
// failed, which the operation itself reports only as stopped.
type progressSink struct {
	// vtbl makes the address of the sink a COM object's: it must stay
	// first.
	vtbl *[19]uintptr
	// failure is the result of the first item that failed, or 0.
	failure hresult
}

// sinks are the sinks in use, by their addresses: how the methods the
// shell calls find them.
var sinks sync.Map // uintptr to *progressSink

// newProgressSink makes a sink, in use until done is called.
func newProgressSink() (s *progressSink, done func()) {
	s = &progressSink{vtbl: sinkMethods()}
	this := uintptr(unsafe.Pointer(s))
	sinks.Store(this, s)
	return s, func() { sinks.Delete(this) }
}

// sinkMethods is the table of the sinks' methods, in the interface's
// order. Its callbacks are made once: the system has room for a limited
// number, for as long as the program runs.
var sinkMethods = sync.OnceValue(func() *[19]uintptr {
	// Most methods only have to say yes, and differ by how many
	// arguments they are called with.
	ok1 := syscall.NewCallback(func(this uintptr) uintptr { return 0 })
	ok2 := syscall.NewCallback(func(this, a uintptr) uintptr { return 0 })
	ok3 := syscall.NewCallback(func(this, a, b uintptr) uintptr { return 0 })
	ok4 := syscall.NewCallback(func(this, a, b, c uintptr) uintptr { return 0 })
	ok5 := syscall.NewCallback(func(this, a, b, c, d uintptr) uintptr { return 0 })
	count := syscall.NewCallback(func(this uintptr) uintptr { return 1 })
	postTransfer := syscall.NewCallback(func(this, flags, item, dest, name, result, created uintptr) uintptr {
		return note(this, result)
	})
	return &[19]uintptr{
		syscall.NewCallback(sinkQueryInterface),
		count, // AddRef: the sink lives as long as Go refers to it
		count, // Release
		ok1,   // StartOperations
		ok2,   // FinishOperations
		ok4,   // PreRenameItem
		syscall.NewCallback(func(this, flags, item, name, result, created uintptr) uintptr {
			return note(this, result)
		}), // PostRenameItem
		ok5,          // PreMoveItem
		postTransfer, // PostMoveItem
		ok5,          // PreCopyItem
		postTransfer, // PostCopyItem
		ok3,          // PreDeleteItem
		syscall.NewCallback(func(this, flags, item, result, created uintptr) uintptr {
			return note(this, result)
		}), // PostDeleteItem
		ok4, // PreNewItem
		syscall.NewCallback(func(this, flags, dest, name, template, attributes, result, created uintptr) uintptr {
			return note(this, result)
		}), // PostNewItem
		ok3, // UpdateProgress
		ok1, // ResetTimer
		ok1, // PauseTimer
		ok1, // ResumeTimer
	}
})

func sinkQueryInterface(this uintptr, iid *windows.GUID, out *uintptr) uintptr {
	if *iid != iidUnknown && *iid != iidFileOperationProgressSink {
		*out = 0
		return 0x80004002 // E_NOINTERFACE
	}
	*out = this
	return 0
}

// note remembers how an item went, if it is the first to fail.
func note(this, result uintptr) uintptr {
	// An HRESULT is 32 bits of the register it comes in.
	hr := hresult(uint32(result))
	if s, ok := sinks.Load(this); ok && hr.failed() && !hr.canceled() {
		if s := s.(*progressSink); s.failure == 0 {
			s.failure = hr
		}
	}
	return 0
}

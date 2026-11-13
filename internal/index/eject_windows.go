package index

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A drive that can be taken out, as a USB stick, is not let go by the
// system while a program has it open, and an index that follows a drive
// has it open. The system asks first: a program that registered for it is
// told that the drive is about to go, and closes what it has open there.

var (
	cfgmgr           = windows.NewLazySystemDLL("cfgmgr32.dll")
	procCMRegister   = cfgmgr.NewProc("CM_Register_Notification")
	procCMUnregister = cfgmgr.NewProc("CM_Unregister_Notification")
)

// cmNotifyFilter is CM_NOTIFY_FILTER for a device by a handle to it.
type cmNotifyFilter struct {
	size       uint32
	flags      uint32
	filterType uint32
	reserved   uint32
	target     windows.Handle
	// The rest of the union, the longest of which is a device's name.
	_ [400 - unsafe.Sizeof(windows.Handle(0))]byte
}

const (
	cmFilterDeviceHandle = 1

	cmQueryRemove       = 2
	cmQueryRemoveFailed = 3
	cmRemovePending     = 4
	cmRemoveComplete    = 5
)

// ejectGuard tells when a drive is about to be taken out.
type ejectGuard struct {
	id     uintptr
	notify uintptr
	// mu guards h, the guard's own handle to the drive, which it closes
	// with the rest.
	mu sync.Mutex
	h  windows.Handle
	// asked is called, on a thread of the system's, when the drive is
	// asked for: it must close what is open on the drive before it returns.
	asked func()
	once  sync.Once
}

var (
	ejectMu     sync.Mutex
	ejectGuards = map[uintptr]*ejectGuard{}
	ejectLast   uintptr
	ejectNotify = sync.OnceValue(func() uintptr { return windows.NewCallback(deviceNotified) })
)

// deviceNotified is what the system calls with news of a device.
func deviceNotified(notify, context, action, data, size uintptr) uintptr {
	ejectMu.Lock()
	g := ejectGuards[context]
	ejectMu.Unlock()
	if g != nil {
		g.told(uint32(action))
	}
	return 0
}

// told is the news of the drive: about to go, or gone.
func (g *ejectGuard) told(action uint32) {
	switch action {
	case cmQueryRemove, cmRemovePending, cmRemoveComplete:
		g.asked()
		g.closeHandle()
	}
}

func (g *ejectGuard) closeHandle() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.h != 0 {
		windows.CloseHandle(g.h)
		g.h = 0
	}
}

// watchEject has asked called when the drive of a root is about to be
// taken out. It returns nil when the system does not tell.
func watchEject(root string, asked func()) *ejectGuard {
	p, err := windows.UTF16PtrFromString(longPath(root))
	if err != nil {
		return nil
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil
	}
	if procCMRegister.Find() != nil {
		windows.CloseHandle(h)
		return nil
	}
	g := &ejectGuard{h: h, asked: asked}
	ejectMu.Lock()
	ejectLast++
	g.id = ejectLast
	ejectGuards[g.id] = g
	ejectMu.Unlock()
	filter := cmNotifyFilter{filterType: cmFilterDeviceHandle, target: h}
	filter.size = uint32(unsafe.Sizeof(filter))
	r, _, _ := procCMRegister.Call(uintptr(unsafe.Pointer(&filter)), g.id, ejectNotify(), uintptr(unsafe.Pointer(&g.notify)))
	if r != 0 {
		ejectMu.Lock()
		delete(ejectGuards, g.id)
		ejectMu.Unlock()
		windows.CloseHandle(h)
		return nil
	}
	return g
}

// Close stops listening. It is not to be called from asked: it waits for
// what the system is telling to be told.
func (g *ejectGuard) Close() {
	if g == nil {
		return
	}
	g.once.Do(func() {
		procCMUnregister.Call(g.notify)
		ejectMu.Lock()
		delete(ejectGuards, g.id)
		ejectMu.Unlock()
		g.closeHandle()
	})
}

// drivesPresent are the drive letters there are now, a bit each from A.
func drivesPresent() uint32 {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return 0
	}
	return mask
}

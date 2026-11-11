package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The window messages and constants the benchmark uses. All of the
// messages but the two that carry text have no pointers in them, so they
// cross to another process as they are; WM_SETTEXT and WM_GETTEXT the
// system copies across itself.
const (
	wmNull      = 0x0000
	wmSetText   = 0x000C
	wmGetText   = 0x000D
	wmGetLength = 0x000E
	wmKeyDown   = 0x0100

	lvmGetItemCount = 0x1004       // LVM_FIRST + 4
	lvmGetView      = 0x1000 + 143 // LVM_GETVIEW

	cbemGetEditControl = 0x0400 + 7 // WM_USER + 7

	vkReturn = 0x0D
	// enterLParam is what a real press of Enter carries with WM_KEYDOWN:
	// a repeat count of one and the key's scan code.
	enterLParam = 0x001C0001

	gwHwndNext = 2
	gwChild    = 5

	swShowNormal     = 1
	swShowNoActivate = 4
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")

	procQueryPerformanceCounter   = kernel32.NewProc("QueryPerformanceCounter")
	procQueryPerformanceFrequency = kernel32.NewProc("QueryPerformanceFrequency")
	procSetProcessInformation     = kernel32.NewProc("SetProcessInformation")

	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
	procFindWindowExW       = user32.NewProc("FindWindowExW")
	procGetWindow           = user32.NewProc("GetWindow")
	procGetUpdateRect       = user32.NewProc("GetUpdateRect")
	procIsIconic            = user32.NewProc("IsIconic")

	procLockSetForegroundWindow = user32.NewProc("LockSetForegroundWindow")
	procSetForegroundWindow     = user32.NewProc("SetForegroundWindow")
)

// ticks is a reading of the system's performance counter.
type ticks int64

// ticksPerSecond is the counter's frequency, which never changes while
// the system is up.
var ticksPerSecond = func() int64 {
	var f int64
	procQueryPerformanceFrequency.Call(uintptr(unsafe.Pointer(&f)))
	return f
}()

// now reads the performance counter.
func now() ticks {
	var t int64
	procQueryPerformanceCounter.Call(uintptr(unsafe.Pointer(&t)))
	return ticks(t)
}

// ms is the time from one reading to a later one, in milliseconds.
func ms(from, to ticks) float64 {
	return float64(to-from) * 1000 / float64(ticksPerSecond)
}

// pollEvery is how often a condition in another process is looked at
// while waiting for it: often enough to time it to well under a tenth of
// a millisecond, seldom enough to leave the system's window lock alone.
const pollEvery = 50 * time.Microsecond

// spin waits a short time without sleeping, which on Windows is no finer
// than half a millisecond.
func spin(d time.Duration) {
	until := now() + ticks(int64(d)*ticksPerSecond/int64(time.Second))
	for now() < until {
	}
}

// errTimeout is a window not answering, or a condition not coming, in
// the time allowed.
var errTimeout = errors.New("timed out")

// messageTimeout is how long a window may take over one message. A
// folder of tens of thousands of entries keeps Explorer++'s only thread
// for several seconds, so it is generous.
var messageTimeout = 2 * time.Minute

// send sends a message to a window of another process and waits for its
// thread to have handled it, as SendMessage does, but not for ever.
func send(h windows.HWND, msg uint32, wparam, lparam uintptr) (uintptr, error) {
	var result uintptr
	// SMTO_NORMAL (0): a thread busy for seconds is not "hung" here.
	ok, _, err := procSendMessageTimeoutW.Call(uintptr(h), uintptr(msg), wparam, lparam,
		0, uintptr(messageTimeout.Milliseconds()), uintptr(unsafe.Pointer(&result)))
	if ok == 0 {
		if err == windows.ERROR_TIMEOUT || err == windows.ERROR_SUCCESS {
			return 0, fmt.Errorf("message %#x to window %#x: %w", msg, h, errTimeout)
		}
		return 0, fmt.Errorf("message %#x to window %#x: %w", msg, h, err)
	}
	return result, nil
}

// setText gives a window its text.
func setText(h windows.HWND, text string) error {
	p, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return err
	}
	var result uintptr
	ok, _, err := procSendMessageTimeoutW.Call(uintptr(h), wmSetText, 0, uintptr(unsafe.Pointer(p)),
		0, uintptr(messageTimeout.Milliseconds()), uintptr(unsafe.Pointer(&result)))
	if ok == 0 {
		return fmt.Errorf("setting the text of window %#x: %w", h, err)
	}
	return nil
}

// text reads a window's text.
func text(h windows.HWND) (string, error) {
	n, err := send(h, wmGetLength, 0, 0)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, n+1)
	var result uintptr
	ok, _, err := procSendMessageTimeoutW.Call(uintptr(h), wmGetText, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])),
		0, uintptr(messageTimeout.Milliseconds()), uintptr(unsafe.Pointer(&result)))
	if ok == 0 {
		return "", fmt.Errorf("reading the text of window %#x: %w", h, err)
	}
	return windows.UTF16ToString(buf), nil
}

// className is a window's class. It asks the system, not the window, so
// it does not wait for the window's thread.
func className(h windows.HWND) string {
	var buf [128]uint16
	n, _ := windows.GetClassName(h, &buf[0], int32(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

// topLevelWindow finds a top-level window of a class that a process owns.
func topLevelWindow(pid uint32, class string) windows.HWND {
	name, _ := windows.UTF16PtrFromString(class)
	var h uintptr
	for {
		h, _, _ = procFindWindowExW.Call(0, h, uintptr(unsafe.Pointer(name)), 0)
		if h == 0 {
			return 0
		}
		var owner uint32
		windows.GetWindowThreadProcessId(windows.HWND(h), &owner)
		if owner == pid {
			return windows.HWND(h)
		}
	}
}

// descendants walks the windows inside a window, depth first, until the
// function says to stop.
func descendants(parent windows.HWND, visit func(h windows.HWND) (more bool)) bool {
	child, _, _ := procGetWindow.Call(uintptr(parent), gwChild)
	for child != 0 {
		h := windows.HWND(child)
		if !visit(h) || !descendants(h, visit) {
			return false
		}
		child, _, _ = procGetWindow.Call(child, gwHwndNext)
	}
	return true
}

// findDescendant is the first window of a class inside a window that the
// function accepts.
func findDescendant(parent windows.HWND, class string, accept func(h windows.HWND) bool) windows.HWND {
	var found windows.HWND
	descendants(parent, func(h windows.HWND) bool {
		if strings.EqualFold(className(h), class) && (accept == nil || accept(h)) {
			found = h
			return false
		}
		return true
	})
	return found
}

// paintPending is whether a window has anything left to paint: whether
// its update region is not empty. It is the system's answer, without a
// message to the window.
func paintPending(h windows.HWND) bool {
	r, _, _ := procGetUpdateRect.Call(uintptr(h), 0, 0)
	return r != 0
}

// minimized is whether a window is minimized, when it paints nothing.
func minimized(h windows.HWND) bool {
	r, _, _ := procIsIconic.Call(uintptr(h))
	return r != 0
}

// fileVersion is the version a program's file says it is, as "1.4.0.2293".
func fileVersion(path string) (string, error) {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil {
		return "", err
	}
	data := make([]byte, size)
	if err := windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&data[0])); err != nil {
		return "", err
	}
	var info *windows.VS_FIXEDFILEINFO
	var n uint32
	if err := windows.VerQueryValue(unsafe.Pointer(&data[0]), `\`, unsafe.Pointer(&info), &n); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d.%d",
		info.FileVersionMS>>16, info.FileVersionMS&0xFFFF,
		info.FileVersionLS>>16, info.FileVersionLS&0xFFFF), nil
}

// killOnExit is a job whose processes the system ends when this program
// does, however it ends: nothing it started is left running.
var killOnExit = func() windows.Handle {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0
	}
	return job
}()

// start starts a program, in a folder, with its window shown the given
// way (0 for however it likes), and returns the process and the counter
// as it was just before CreateProcess. The program runs at full speed
// wherever its window ends up, and ends when this one does.
func start(exe string, args []string, dir string, show uint16) (windows.Handle, uint32, ticks, error) {
	exePtr, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return 0, 0, 0, err
	}
	cmdPtr, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{exe}, args...)))
	if err != nil {
		return 0, 0, 0, err
	}
	var dirPtr *uint16
	if dir != "" {
		if dirPtr, err = windows.UTF16PtrFromString(dir); err != nil {
			return 0, 0, 0, err
		}
	}
	si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	if show != 0 {
		si.Flags = windows.STARTF_USESHOWWINDOW
		si.ShowWindow = show
	}
	var pi windows.ProcessInformation

	// The process is created with its first thread not yet running, so
	// that what follows holds from its first instruction; that takes some
	// tens of microseconds of the time measured.
	before := now()
	if err := windows.CreateProcess(exePtr, cmdPtr, nil, nil, false, windows.CREATE_SUSPENDED,
		nil, dirPtr, &si, &pi); err != nil {
		return 0, 0, 0, fmt.Errorf("starting %s: %w", exe, err)
	}
	defer windows.CloseHandle(pi.Thread)
	if killOnExit != 0 {
		// Best effort: it fails only where jobs cannot nest.
		_ = windows.AssignProcessToJobObject(killOnExit, pi.Process)
	}
	err = fullSpeed(pi.Process)
	if err == nil {
		_, err = windows.ResumeThread(pi.Thread)
	}
	if err != nil {
		windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Process)
		return 0, 0, 0, fmt.Errorf("starting %s: %w", exe, err)
	}
	return pi.Process, pi.ProcessId, before, nil
}

// fullSpeed tells the system not to slow a process down for being in the
// background: to schedule it as it does the program whose window is in
// front and in use (what Windows calls high quality of service), on the
// fast cores and at full clock speed, whether or not its window is.
//
// It matters. A program timed with its window behind another is timed
// slower than its user would find it: Explorer++ on this machine by a
// tenth to a half, and unevenly. With this, its times are those it has
// with its window in front, to within what they vary by anyway.
func fullSpeed(process windows.Handle) error {
	// PROCESS_POWER_THROTTLING_STATE: control of the execution speed
	// taken, and throttling of it switched off.
	state := struct{ version, controlMask, stateMask uint32 }{
		version:     1, // PROCESS_POWER_THROTTLING_CURRENT_VERSION
		controlMask: 1, // PROCESS_POWER_THROTTLING_EXECUTION_SPEED
	}
	const processPowerThrottling = 4
	ok, _, err := procSetProcessInformation.Call(uintptr(process), processPowerThrottling,
		uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
	if ok == 0 {
		return err
	}
	return nil
}

// lockForeground keeps other programs from making a window of theirs the
// foreground window, the one the keyboard goes to, until unlockForeground
// or until the user clicks or switches windows, which always works. It
// says whether it could: only a program that could itself take the
// foreground may.
func lockForeground() bool {
	const lsfwLock = 1
	r, _, _ := procLockSetForegroundWindow.Call(lsfwLock)
	return r != 0
}

func unlockForeground() {
	const lsfwUnlock = 2
	procLockSetForegroundWindow.Call(lsfwUnlock)
}

// foregroundProcess is the process whose window has the keyboard.
func foregroundProcess() uint32 {
	var pid uint32
	if h := windows.GetForegroundWindow(); h != 0 {
		windows.GetWindowThreadProcessId(h, &pid)
	}
	return pid
}

// exited is whether a process has ended.
func exited(process windows.Handle) bool {
	event, _ := windows.WaitForSingleObject(process, 0)
	return event == windows.WAIT_OBJECT_0
}

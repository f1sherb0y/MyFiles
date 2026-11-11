//go:build windows

// Deskcheck checks, on the real desktop, what tests without a window
// cannot: a drag of files with the real mouse through the system's drag
// loop, real keys that would make a window without a menu beep, and
// whether anything played a sound meanwhile.
//
//	deskcheck drag  -pid P -from x,y -to x,y   drag with the mouse, inside P's window
//	deskcheck keys  -pid P alt+q f10 esc z     press keys, while P's window is in front
//	deskcheck hotkey win+e                      press a key of the system's
//	deskcheck listen -pid P -for 12s [-control [-system]] report the loudest sound played
//
// Points are in DIPs from the top left of the window's content, as the
// automation plugin gives the bounds of elements. The mouse and the keys
// are the user's own for the moment: drag lets go only over the window it
// started in, and gives the drag up with Escape otherwise; keys presses
// nothing unless that window is in front. Both put the pointer and the
// window in front back as they were.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSendInput           = user32.NewProc("SendInput")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetCursorPos        = user32.NewProc("SetCursorPos")
	procWindowFromPoint     = user32.NewProc("WindowFromPoint")
	procGetAncestor         = user32.NewProc("GetAncestor")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procClientToScreen      = user32.NewProc("ClientToScreen")
	procGetDpiForWindow     = user32.NewProc("GetDpiForWindow")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
)

type point struct{ x, y int32 }

// input is the system's INPUT, for a mouse or a keyboard event.
type input struct {
	kind uint32
	_    uint32
	data [32]byte
}

type mouseInput struct {
	dx, dy                 int32
	mouseData, flags, time uint32
	extra                  uintptr
}

type keyInput struct {
	vk, scan    uint16
	flags, time uint32
	extra       uintptr
}

const (
	inputMouse, inputKeyboard = 0, 1

	mouseMove, mouseLeftDown, mouseLeftUp = 0x1, 0x2, 0x4
	mouseAbsolute, mouseVirtualDesk       = 0x8000, 0x4000

	keyUp = 0x2
)

func send(in input) {
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

func mouse(flags uint32, at point) {
	// Absolute moves are in 65536ths of the virtual desktop.
	metric := func(i uintptr) int32 { v, _, _ := procGetSystemMetrics.Call(i); return int32(v) }
	vx, vy, vw, vh := metric(76), metric(77), metric(78), metric(79)
	in := input{kind: inputMouse}
	m := (*mouseInput)(unsafe.Pointer(&in.data))
	m.dx = int32((int64(at.x-vx)*65535 + int64(vw-1)/2) / int64(vw-1))
	m.dy = int32((int64(at.y-vy)*65535 + int64(vh-1)/2) / int64(vh-1))
	m.flags = flags | mouseAbsolute | mouseVirtualDesk
	send(in)
}

func key(vk uint16, up bool) {
	in := input{kind: inputKeyboard}
	k := (*keyInput)(unsafe.Pointer(&in.data))
	k.vk = vk
	if up {
		k.flags = keyUp
	}
	send(in)
}

var keyCodes = map[string]uint16{
	"alt": 0x12, "ctrl": 0x11, "shift": 0x10, "win": 0x5B,
	"esc": 0x1B, "enter": 0x0D, "backspace": 0x08, "tab": 0x09, "space": 0x20, "delete": 0x2E,
	"f2": 0x71, "f4": 0x73, "f5": 0x74, "f6": 0x75, "f10": 0x79,
	"left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28,
}

// chord presses keys joined by "+", as "alt+q", and lets go of them.
func chord(s string) error {
	var codes []uint16
	for _, name := range strings.Split(strings.ToLower(s), "+") {
		code, ok := keyCodes[name]
		if !ok && len(name) == 1 {
			code, ok = uint16(strings.ToUpper(name)[0]), true
		}
		if !ok {
			return fmt.Errorf("no key %q", name)
		}
		codes = append(codes, code)
	}
	for _, c := range codes {
		key(c, false)
		time.Sleep(15 * time.Millisecond)
	}
	for i := len(codes) - 1; i >= 0; i-- {
		key(codes[i], true)
		time.Sleep(15 * time.Millisecond)
	}
	return nil
}

// windowOf is the visible top-level window of a process.
func windowOf(pid uint32) uintptr {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var owner uint32
		procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if visible, _, _ := procIsWindowVisible.Call(hwnd); owner == pid && visible != 0 {
			found = hwnd
			return 0
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return found
}

// screenPoint is a point of a window's content, in DIPs, on the screen,
// in pixels.
func screenPoint(hwnd uintptr, s string) (point, error) {
	xs, ys, ok := strings.Cut(s, ",")
	x, err1 := strconv.ParseFloat(xs, 64)
	y, err2 := strconv.ParseFloat(ys, 64)
	if !ok || err1 != nil || err2 != nil {
		return point{}, fmt.Errorf("%q is no point", s)
	}
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	scale := float64(dpi) / 96
	p := point{int32(x*scale + 0.5), int32(y*scale + 0.5)}
	procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&p)))
	return p, nil
}

// over reports whether the pointer is over a window, or one inside it.
func over(hwnd uintptr) bool {
	var at point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&at)))
	// WindowFromPoint takes the point by value, packed in one argument.
	under, _, _ := procWindowFromPoint.Call(uintptr(uint32(at.x)) | uintptr(uint32(at.y))<<32)
	root, _, _ := procGetAncestor.Call(under, 2)
	return root == hwnd
}

func inFront(hwnd uintptr) bool {
	fg, _, _ := procGetForegroundWindow.Call()
	return fg == hwnd
}

// borrow puts a window above the others, and returns what puts the
// pointer, that window and the one in front back as they were.
func borrow(hwnd uintptr) (restore func()) {
	var was point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&was)))
	front, _, _ := procGetForegroundWindow.Call()
	const topmost, notTopmost, keepPlace = ^uintptr(0), ^uintptr(1), 0x1 | 0x2 | 0x10
	procSetWindowPos.Call(hwnd, topmost, 0, 0, 0, 0, keepPlace)
	return func() {
		procSetWindowPos.Call(hwnd, notTopmost, 0, 0, 0, 0, keepPlace)
		procSetCursorPos.Call(uintptr(was.x), uintptr(was.y))
		if front != 0 && front != hwnd {
			procSetForegroundWindow.Call(front)
		}
	}
}

func drag(pid uint32, from, to string) error {
	hwnd := windowOf(pid)
	if hwnd == 0 {
		return fmt.Errorf("process %d shows no window", pid)
	}
	a, err := screenPoint(hwnd, from)
	if err != nil {
		return err
	}
	b, err := screenPoint(hwnd, to)
	if err != nil {
		return err
	}
	defer borrow(hwnd)()
	time.Sleep(80 * time.Millisecond)
	mouse(mouseMove, a)
	time.Sleep(60 * time.Millisecond)
	if !over(hwnd) {
		return fmt.Errorf("the window is not under the pointer at %s", from)
	}
	mouse(mouseLeftDown, a)
	time.Sleep(120 * time.Millisecond)
	const steps = 16
	for i := 1; i <= steps; i++ {
		at := point{a.x + (b.x-a.x)*int32(i)/steps, a.y + (b.y-a.y)*int32(i)/steps}
		mouse(mouseMove, at)
		time.Sleep(25 * time.Millisecond)
	}
	// The system's drag loop looks at the pointer as it comes to rest.
	mouse(mouseMove, point{b.x + 1, b.y})
	time.Sleep(150 * time.Millisecond)
	mouse(mouseMove, b)
	time.Sleep(150 * time.Millisecond)
	if !over(hwnd) {
		// Not where it should drop: give the drag up, and drop nothing
		// anywhere.
		key(keyCodes["esc"], false)
		key(keyCodes["esc"], true)
		time.Sleep(50 * time.Millisecond)
		mouse(mouseLeftUp, b)
		return fmt.Errorf("the pointer left the window: the drag was given up")
	}
	mouse(mouseLeftUp, b)
	time.Sleep(300 * time.Millisecond)
	fmt.Printf("dragged from %d,%d to %d,%d\n", a.x, a.y, b.x, b.y)
	return nil
}

func keys(pid uint32, click string, chords []string) error {
	hwnd := windowOf(pid)
	if hwnd == 0 {
		return fmt.Errorf("process %d shows no window", pid)
	}
	front, _, _ := procGetForegroundWindow.Call()
	procSetForegroundWindow.Call(hwnd)
	time.Sleep(150 * time.Millisecond)
	if !inFront(hwnd) && click != "" {
		// The system keeps programs in the background from coming to the
		// front on their own: a click on the window brings it there.
		at, err := screenPoint(hwnd, click)
		if err != nil {
			return err
		}
		var was point
		procGetCursorPos.Call(uintptr(unsafe.Pointer(&was)))
		const topmost, notTopmost, keepPlace = ^uintptr(0), ^uintptr(1), 0x1 | 0x2 | 0x10
		procSetWindowPos.Call(hwnd, topmost, 0, 0, 0, 0, keepPlace)
		time.Sleep(80 * time.Millisecond)
		mouse(mouseMove, at)
		time.Sleep(60 * time.Millisecond)
		if over(hwnd) {
			mouse(mouseLeftDown, at)
			time.Sleep(30 * time.Millisecond)
			mouse(mouseLeftUp, at)
			time.Sleep(150 * time.Millisecond)
		}
		// The pointer goes back; the window stays in front for the keys.
		procSetWindowPos.Call(hwnd, notTopmost, 0, 0, 0, 0, keepPlace)
		procSetCursorPos.Call(uintptr(was.x), uintptr(was.y))
	}
	defer func() {
		if front != 0 && front != hwnd {
			procSetForegroundWindow.Call(front)
		}
	}()
	for _, c := range chords {
		if !inFront(hwnd) {
			return fmt.Errorf("the window is not in front: %q and what follows were not pressed", c)
		}
		if err := chord(c); err != nil {
			return err
		}
		time.Sleep(120 * time.Millisecond)
	}
	fmt.Printf("pressed %s\n", strings.Join(chords, " "))
	return nil
}

func main() {
	// Points are in the pixels of the screen, not scaled for a program
	// that knows nothing of the display's scale.
	const perMonitorAwareV2 = ^uintptr(3)
	user32.NewProc("SetProcessDpiAwarenessContext").Call(perMonitorAwareV2)
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: deskcheck drag|keys|hotkey|listen ...")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	pid := fs.Uint("pid", 0, "the process whose window is acted on, or listened to")
	from := fs.String("from", "", "where a drag starts: x,y in DIPs of the window's content")
	to := fs.String("to", "", "where it ends")
	dur := fs.Duration("for", 10*time.Second, "how long to listen")
	control := fs.Bool("control", false, "play a sound too quiet to hear, to check that listening hears")
	click := fs.String("click", "", "for keys: a point of the window to click, to bring it to the front when it is not")
	asSystem := fs.Bool("system", false, "with -control, play it as a system sound, where beeps play")
	fs.Parse(os.Args[2:])
	var err error
	switch os.Args[1] {
	case "drag":
		err = drag(uint32(*pid), *from, *to)
	case "keys":
		err = keys(uint32(*pid), *click, fs.Args())
	case "hotkey":
		for _, c := range fs.Args() {
			if err = chord(c); err != nil {
				break
			}
		}
	case "listen":
		err = listen(uint32(*pid), *dur, *control, *asSystem)
	default:
		err = fmt.Errorf("no command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "deskcheck:", err)
		os.Exit(1)
	}
}

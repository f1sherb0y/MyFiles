package shell

import (
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"cairn/internal/fsys"
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procWideCharToMultiByte = kernel32.NewProc("WideCharToMultiByte")

	procSHBindToParent = shell32.NewProc("SHBindToParent")
	procSHBindToObject = shell32.NewProc("SHBindToObject")
	procILFindLastID   = shell32.NewProc("ILFindLastID")

	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetMenuItemCount    = user32.NewProc("GetMenuItemCount")
	procGetMenuItemInfoW    = user32.NewProc("GetMenuItemInfoW")
	procTrackPopupMenuEx    = user32.NewProc("TrackPopupMenuEx")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
)

// The methods of IShellFolder and of IContextMenu and its later
// versions, by their place in their tables.
const (
	folderCreateViewObject = 8
	folderGetUIObjectOf    = 10

	menuQueryContextMenu = 3
	menuInvokeCommand    = 4
	menuGetCommandString = 5
	menuHandleMenuMsg    = 6 // IContextMenu2
	menuHandleMenuMsg2   = 7 // IContextMenu3
)

const (
	// The commands of the shell's menu are numbered from firstCommand,
	// 0 being what tracking a menu returns for none chosen.
	firstCommand = 1
	lastCommand  = 0x7FFF

	// The flags of QueryContextMenu (CMF_): the menu of a file manager,
	// which can rename, and with Shift held the verbs kept for it.
	cmfExplore       = 0x4
	cmfCanRename     = 0x10
	cmfExtendedVerbs = 0x100

	// The flags of an invocation (CMIC_MASK_).
	cmicMaskUnicode     = 0x4000
	cmicMaskShiftDown   = 0x10000000
	cmicMaskPtInvoke    = 0x20000000
	cmicMaskControlDown = 0x40000000

	wmNull          = 0x0000
	wmDrawItem      = 0x002B
	wmMeasureItem   = 0x002C
	wmInitMenuPopup = 0x0117
	wmMenuChar      = 0x0120

	vkShift   = 0x10
	vkControl = 0x11

	// renameVerb is the name of the command that renames.
	renameVerb = "rename"
)

// shellMenu is the shell's context menu for some items, built and not
// yet shown. It lives on the thread that built it.
type shellMenu struct {
	// menu is the shell's IContextMenu; menu2 and menu3 are its later
	// versions where it has them, which fill and draw its submenus.
	menu, menu2, menu3 *object
	// handle is the menu itself (an HMENU).
	handle uintptr
	// dir is the directory the items are in, or the one whose
	// background the menu is of.
	dir string
}

// buildMenu builds the shell's menu for paths, which are in one
// directory, or with none for the background of a directory. The windows
// the shell may show while building it are owner's.
func buildMenu(owner Window, paths []string, background string, flags uint32) (*shellMenu, error) {
	m := &shellMenu{dir: background}
	var err error
	if len(paths) > 0 {
		m.dir, _ = fsys.Parent(paths[0])
		m.menu, err = itemsMenu(owner, paths)
	} else {
		m.menu, err = backgroundMenu(owner, background)
	}
	if err != nil {
		return nil, err
	}
	if m.handle, _, err = procCreatePopupMenu.Call(); m.handle == 0 {
		m.close()
		return nil, err
	}
	hr := m.menu.call(menuQueryContextMenu, m.handle, 0, firstCommand, lastCommand, uintptr(flags))
	if err := hr.err(); err != nil {
		m.close()
		return nil, err
	}
	if m.menu3 = m.menu.query(&iidContextMenu3); m.menu3 == nil {
		m.menu2 = m.menu.query(&iidContextMenu2)
	}
	return m, nil
}

// itemsMenu is the IContextMenu of items of one folder.
func itemsMenu(owner Window, paths []string) (*object, error) {
	ids := make([]pidl, 0, len(paths))
	defer func() { freePIDLs(ids) }()
	for _, path := range paths {
		id, err := parsePIDL(path)
		if err != nil {
			return nil, &fsys.PathError{Op: "menu", Path: path, Err: err}
		}
		ids = append(ids, id)
	}
	// The folder is asked about its items by the last parts of their
	// identifiers, which point into the whole ones.
	var folder *object
	hr, _, _ := procSHBindToParent.Call(uintptr(ids[0]), uintptr(unsafe.Pointer(&iidShellFolder)), uintptr(unsafe.Pointer(&folder)), 0)
	if err := hresult(hr).err(); err != nil {
		return nil, err
	}
	defer folder.release()
	children := make([]pidl, len(ids))
	for i, id := range ids {
		last, _, _ := procILFindLastID.Call(uintptr(id))
		children[i] = pidl(last)
	}
	var menu *object
	err := folder.call(folderGetUIObjectOf, uintptr(owner), uintptr(len(children)), uintptr(unsafe.Pointer(&children[0])),
		uintptr(unsafe.Pointer(&iidContextMenu)), 0, uintptr(unsafe.Pointer(&menu))).err()
	return menu, err
}

// backgroundMenu is the IContextMenu of a folder's background: what a
// view of the folder shows where no item is.
func backgroundMenu(owner Window, dir string) (*object, error) {
	id, err := parsePIDL(dir)
	if err != nil {
		return nil, &fsys.PathError{Op: "menu", Path: dir, Err: err}
	}
	defer id.free()
	var folder *object
	hr, _, _ := procSHBindToObject.Call(0, uintptr(id), 0, uintptr(unsafe.Pointer(&iidShellFolder)), uintptr(unsafe.Pointer(&folder)))
	if err := hresult(hr).err(); err != nil {
		return nil, &fsys.PathError{Op: "menu", Path: dir, Err: err}
	}
	defer folder.release()
	var menu *object
	err = folder.call(folderCreateViewObject, uintptr(owner), uintptr(unsafe.Pointer(&iidContextMenu)), uintptr(unsafe.Pointer(&menu))).err()
	return menu, err
}

// close destroys the menu and gives up the shell's objects.
func (m *shellMenu) close() {
	if m.handle != 0 {
		procDestroyMenu.Call(m.handle)
	}
	m.menu3.release()
	m.menu2.release()
	m.menu.release()
}

// menuItemInfo is the system's MENUITEMINFOW.
type menuItemInfo struct {
	size      uint32
	mask      uint32
	typ       uint32
	state     uint32
	id        uint32
	subMenu   uintptr
	checked   uintptr
	unchecked uintptr
	itemData  uintptr
	typeData  *uint16
	cch       uint32
	bitmap    uintptr
}

// menuItem is one item of the top level of a menu.
type menuItem struct {
	// text is what the item says, with & before its access key; "" for
	// an item its handler draws itself.
	text string
	// verb is the name the command goes by, as "delete", where it has
	// one.
	verb      string
	separator bool
	// submenu is the menu the item opens (an HMENU), or 0.
	submenu uintptr
}

// items lists the top level of the menu.
func (m *shellMenu) items() []menuItem {
	const (
		miimID      = 0x2
		miimSubmenu = 0x4
		miimString  = 0x40
		miimFType   = 0x100

		mftOwnerDraw = 0x100
		mftSeparator = 0x800
	)
	count, _, _ := procGetMenuItemCount.Call(m.handle)
	var items []menuItem
	for i := 0; i < int(int32(count)); i++ {
		var text [256]uint16
		info := menuItemInfo{mask: miimID | miimSubmenu | miimString | miimFType, typeData: &text[0], cch: uint32(len(text))}
		info.size = uint32(unsafe.Sizeof(info))
		if r, _, _ := procGetMenuItemInfoW.Call(m.handle, uintptr(i), 1, uintptr(unsafe.Pointer(&info))); r == 0 {
			continue
		}
		item := menuItem{separator: info.typ&mftSeparator != 0, submenu: info.subMenu}
		if info.typ&mftOwnerDraw == 0 {
			item.text = windows.UTF16ToString(text[:])
		}
		if !item.separator && item.submenu == 0 && info.id >= firstCommand && info.id <= lastCommand {
			item.verb = m.verb(int(info.id))
		}
		items = append(items, item)
	}
	return items
}

// verb is the name of a command of the menu, or "" for one without.
func (m *shellMenu) verb(command int) string {
	const gcsVerbW = 0x4
	var name [256]uint16
	hr := m.menu.call(menuGetCommandString, uintptr(command-firstCommand), gcsVerbW, 0, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
	if hr.failed() {
		return ""
	}
	return windows.UTF16ToString(name[:])
}

// handleMessage passes a message of the menu's window on to the shell's
// menu, which fills its submenus as they open and draws the items that
// are its handlers' own. It reports the answer, and whether there is
// one.
func (m *shellMenu) handleMessage(msg, wParam, lParam uintptr) (uintptr, bool) {
	// The drawing messages are answered TRUE, the others 0, by handlers
	// that leave the answer alone.
	var answer uintptr
	if msg == wmDrawItem || msg == wmMeasureItem {
		answer = 1
	}
	switch {
	case m.menu3 != nil:
		if !m.menu3.call(menuHandleMenuMsg2, msg, wParam, lParam, uintptr(unsafe.Pointer(&answer))).failed() {
			return answer, true
		}
	case m.menu2 != nil && msg != wmMenuChar:
		if !m.menu2.call(menuHandleMenuMsg, msg, wParam, lParam).failed() {
			return answer, true
		}
	}
	return 0, false
}

// track shows the menu at a point of the screen and waits for the user:
// it returns the command chosen, or 0. window is the menu's window, of
// the calling thread.
func (m *shellMenu) track(window uintptr, x, y int) int {
	const (
		tpmRightButton = 0x2
		tpmReturnCmd   = 0x100
	)
	// A menu closes when the user clicks elsewhere only if its window
	// is the foreground one, and stays up after the next click unless a
	// message follows it.
	procSetForegroundWindow.Call(window)
	command, _, _ := procTrackPopupMenuEx.Call(m.handle, tpmRightButton|tpmReturnCmd, uintptr(x), uintptr(y), window, 0)
	procPostMessageW.Call(window, wmNull, 0, 0)
	return int(command)
}

// invokeInfo is the system's CMINVOKECOMMANDINFOEX. Its verbs are
// numbers here, never names.
type invokeInfo struct {
	size        uint32
	mask        uint32
	hwnd        uintptr
	verb        uintptr
	parameters  *byte
	directory   *byte
	show        int32
	hotKey      uint32
	icon        uintptr
	title       *byte
	verbW       uintptr
	parametersW *uint16
	directoryW  *uint16
	titleW      *uint16
	invokedAt   point
}

// invoke runs a command of the menu, as chosen at a point of the screen.
// The windows the command shows are owner's.
func (m *shellMenu) invoke(owner uintptr, command, x, y int) error {
	info := invokeInfo{
		mask: cmicMaskUnicode | cmicMaskPtInvoke,
		hwnd: owner,
		// A command is named by its place among the menu's own
		// (MAKEINTRESOURCE).
		verb:      uintptr(command - firstCommand),
		verbW:     uintptr(command - firstCommand),
		show:      swShowNormal,
		invokedAt: point{int32(x), int32(y)},
	}
	info.size = uint32(unsafe.Sizeof(info))
	if keyDown(vkShift) {
		info.mask |= cmicMaskShiftDown
	}
	if keyDown(vkControl) {
		info.mask |= cmicMaskControlDown
	}
	// Where the command starts: what New makes, it makes there.
	if dir, err := windows.UTF16FromString(m.dir); err == nil && m.dir != "" {
		info.directoryW = &dir[0]
		if ansi := ansiString(dir); len(ansi) > 0 {
			info.directory = &ansi[0]
		}
	}
	return m.menu.call(menuInvokeCommand, uintptr(unsafe.Pointer(&info))).err()
}

// keyDown reports whether a key is held now.
func keyDown(key uintptr) bool {
	state, _, _ := procGetAsyncKeyState.Call(key)
	return int16(state) < 0
}

// ansiString is a string, with its end, in the system's code page, for
// the handlers that read no other.
func ansiString(s []uint16) []byte {
	const cpACP = 0
	n, _, _ := procWideCharToMultiByte.Call(cpACP, 0, uintptr(unsafe.Pointer(&s[0])), uintptr(len(s)), 0, 0, 0, 0)
	if n == 0 {
		return nil
	}
	out := make([]byte, n)
	procWideCharToMultiByte.Call(cpACP, 0, uintptr(unsafe.Pointer(&s[0])), uintptr(len(s)), uintptr(unsafe.Pointer(&out[0])), n, 0, 0)
	return out
}

// menuWindows are the windows of the menus showing now, with their
// menus: where menuWindowProc finds whose messages it has.
var menuWindows sync.Map // HWND to *shellMenu

// windowClass is the system's WNDCLASSEXW.
type windowClass struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

// menuClass registers the class of the menus' windows, once, and returns
// its name and the module it belongs to.
var menuClass = sync.OnceValues(func() (*uint16, uintptr) {
	name, _ := windows.UTF16PtrFromString("CairnShellMenu")
	instance, _, _ := procGetModuleHandleW.Call(0)
	class := windowClass{
		// The one callback of the package: the system has room for few.
		wndProc:   syscall.NewCallback(menuWindowProc),
		instance:  instance,
		className: name,
	}
	class.size = uint32(unsafe.Sizeof(class))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class)))
	return name, instance
})

// menuWindowProc is the window procedure of the menus' windows.
func menuWindowProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmInitMenuPopup, wmDrawItem, wmMeasureItem, wmMenuChar:
		if m, ok := menuWindows.Load(hwnd); ok {
			if answer, ok := m.(*shellMenu).handleMessage(msg, wParam, lParam); ok {
				return answer
			}
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

// newMenuWindow makes the window a menu needs while it shows: one of the
// calling thread, never visible, that gets the menu's messages.
func newMenuWindow(m *shellMenu) (uintptr, error) {
	const (
		wsPopup        = 0x80000000
		wsExToolWindow = 0x80 // no button on the taskbar
	)
	name, instance := menuClass()
	hwnd, _, err := procCreateWindowExW.Call(wsExToolWindow, uintptr(unsafe.Pointer(name)), 0, wsPopup,
		0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		return 0, err
	}
	menuWindows.Store(hwnd, m)
	return hwnd, nil
}

// closeMenuWindow destroys a menu's window, and gives the foreground
// back to owner if the window still has it: if the user did not go
// elsewhere, and the command opened nothing.
func closeMenuWindow(hwnd uintptr, owner Window) {
	if foreground, _, _ := procGetForegroundWindow.Call(); foreground == hwnd && owner != 0 {
		procSetForegroundWindow.Call(uintptr(owner))
	}
	menuWindows.Delete(hwnd)
	procDestroyWindow.Call(hwnd)
}

// contextMenu runs on a host thread, which is there for what the command
// chosen leaves behind.
func contextMenu(owner Window, paths []string, background string, x, y int) (MenuAction, error) {
	if len(paths) == 0 && background == "" {
		return MenuDone, nil
	}
	action := MenuDone
	err := onHost(func() error {
		flags := uint32(cmfExplore | cmfCanRename)
		if keyDown(vkShift) {
			flags |= cmfExtendedVerbs
		}
		m, err := buildMenu(owner, paths, background, flags)
		if err != nil {
			return err
		}
		defer m.close()
		window, err := newMenuWindow(m)
		if err != nil {
			return err
		}
		defer closeMenuWindow(window, owner)
		if x < 0 || y < 0 {
			// No point given: where the pointer is.
			var at point
			procGetCursorPos.Call(uintptr(unsafe.Pointer(&at)))
			x, y = int(at.x), int(at.y)
		}
		command := m.track(window, x, y)
		if command == 0 {
			return nil
		}
		if m.verb(command) == renameVerb {
			// Renaming is done in place by the view the menu was shown
			// in, which the shell's command asks to; it has none to ask
			// here, and fails: the caller renames.
			action = MenuRename
			return nil
		}
		parent := uintptr(owner)
		if parent == 0 {
			parent = window
		}
		err = m.invoke(parent, command, x, y)
		// Some handlers finish their work in messages they post to
		// themselves.
		dispatchMessages()
		return err
	})
	return action, err
}

package shell

import (
	"image"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemMenu is the system's context menu held for a program to draw
// itself: built on a host thread of its own, where its objects live and
// every call on it is made.
type systemMenu struct {
	t     *thread
	m     *shellMenu
	owner Window
	// files is how many files the menu is of, 0 for a folder's own.
	files int
	items []MenuItem
	// subs are the submenus met so far: Sub of an item is the place of
	// its submenu here, plus one.
	subs []submenu

	mu     sync.Mutex
	closed bool
}

// submenu is a menu an item opens (an HMENU), and the place of the item
// in its own menu, which the handlers that fill it are told.
type submenu struct {
	handle uintptr
	index  int
}

func loadMenu(owner Window, paths []string, background string) (*SystemMenu, error) {
	if len(paths) == 0 && background == "" {
		return nil, ErrUnsupported
	}
	s := &systemMenu{t: takeHost(), owner: owner, files: len(paths)}
	var err error
	s.t.run(func() {
		flags := uint32(cmfExplore | cmfCanRename)
		if keyDown(vkShift) {
			flags |= cmfExtendedVerbs
		}
		if s.m, err = buildMenu(owner, paths, background, flags); err == nil {
			s.items = s.read(s.m.handle)
		}
	})
	if err != nil {
		giveHost(s.t)
		return nil, err
	}
	return &SystemMenu{s: s}, nil
}

// read lists a menu of the system's, on the menu's thread.
func (s *systemMenu) read(handle uintptr) []MenuItem {
	const (
		miimState   = 0x1
		miimID      = 0x2
		miimSubmenu = 0x4
		miimString  = 0x40
		miimBitmap  = 0x80
		miimFType   = 0x100

		mftOwnerDraw = 0x100
		mftSeparator = 0x800

		mfsDisabled = 0x3
		mfsChecked  = 0x8

		// Bitmaps up to this are no bitmaps but names of pictures the
		// system draws, or one the item's window is asked for (-1).
		hbmMenuLast = 11
	)
	count, _, _ := procGetMenuItemCount.Call(handle)
	var items []MenuItem
	for i := 0; i < int(int32(count)); i++ {
		var text [512]uint16
		info := menuItemInfo{mask: miimState | miimID | miimSubmenu | miimString | miimBitmap | miimFType, typeData: &text[0], cch: uint32(len(text))}
		info.size = uint32(unsafe.Sizeof(info))
		if r, _, _ := procGetMenuItemInfoW.Call(handle, uintptr(i), 1, uintptr(unsafe.Pointer(&info))); r == 0 {
			continue
		}
		if info.typ&mftSeparator != 0 {
			items = append(items, MenuItem{Separator: true})
			continue
		}
		item := MenuItem{
			Text:     menuText(windows.UTF16ToString(text[:])),
			Disabled: info.state&mfsDisabled != 0,
			Checked:  info.state&mfsChecked != 0,
		}
		if info.typ&mftOwnerDraw != 0 || item.Text == "" {
			// Drawn by its handler, which says nothing of what it shows.
			continue
		}
		switch {
		case info.subMenu != 0:
			s.subs = append(s.subs, submenu{handle: info.subMenu, index: i})
			item.Sub = len(s.subs)
		case info.id >= firstCommand && info.id <= lastCommand:
			item.ID = int(info.id)
			item.Verb = s.m.verb(item.ID)
		default:
			continue
		}
		if info.bitmap > hbmMenuLast && int64(info.bitmap) != -1 {
			item.Icon = menuBitmap(info.bitmap)
		}
		items = append(items, item)
	}
	return items
}

// menuText is the text of an item as a menu shows it: without the mark of
// its access key, and without the shortcut after a tab.
func menuText(s string) string {
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	if !strings.Contains(s, "&") {
		return strings.TrimSpace(s)
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		// "打开(&O)", as translations write access keys: the brackets go
		// with the key.
		if s[i] == '(' && i+3 < len(s) && s[i+1] == '&' && s[i+2] != '&' && s[i+3] == ')' {
			i += 3
			continue
		}
		if s[i] == '&' {
			if i+1 < len(s) && s[i+1] == '&' {
				b.WriteByte('&')
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return strings.TrimSpace(b.String())
}

// menuBitmap is the picture of an item's bitmap, nil when it cannot be
// read. The bitmap is the menu's, and is left alone.
func menuBitmap(bitmap uintptr) *image.RGBA {
	dc, _, _ := procGetDC.Call(0)
	if dc == 0 {
		return nil
	}
	defer procReleaseDC.Call(0, dc)
	img, err := bitmapPixels(dc, bitmap)
	if err != nil || img.Rect.Dx() == 0 || img.Rect.Dx() > 256 {
		return nil
	}
	orderPixels(img)
	return img
}

// orderPixels puts the pixels of a bitmap as the system gives them (blue,
// green, red and alpha, the colors multiplied by the alpha already) in the
// image's order. A bitmap without transparency may leave the alpha at
// zero throughout: it is opaque.
func orderPixels(img *image.RGBA) {
	pix := img.Pix
	noAlpha := true
	for i := 3; i < len(pix); i += 4 {
		if pix[i] != 0 {
			noAlpha = false
			break
		}
	}
	for i := 0; i < len(pix); i += 4 {
		pix[i], pix[i+2] = pix[i+2], pix[i]
		if noAlpha {
			pix[i+3] = 255
		}
	}
}

func (s *systemMenu) sub(token int) []MenuItem {
	var items []MenuItem
	s.run(func() {
		if token < 1 || token > len(s.subs) {
			return
		}
		sub := s.subs[token-1]
		// The handlers fill their submenus as they open.
		s.m.handleMessage(wmInitMenuPopup, sub.handle, uintptr(sub.index))
		items = s.read(sub.handle)
	})
	return items
}

func (s *systemMenu) invoke(id int) (MenuAction, error) {
	action := MenuDone
	var err error
	s.run(func() {
		if s.m.verb(id) == renameVerb {
			// The view the menu is of renames in place: see contextMenu.
			action = MenuRename
			return
		}
		if own := ownAction(s.m.verb(id), s.files); own != MenuDone {
			action = own
			return
		}
		var at point
		procGetCursorPos.Call(uintptr(unsafe.Pointer(&at)))
		err = s.m.invoke(uintptr(s.owner), id, int(at.x), int(at.y))
		// Some handlers finish their work in messages they post to
		// themselves.
		dispatchMessages()
	})
	return action, err
}

// run runs fn on the menu's thread, unless the menu is closed.
func (s *systemMenu) run(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.t.run(fn)
	}
}

func (s *systemMenu) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.t.run(s.m.close)
	giveHost(s.t)
}

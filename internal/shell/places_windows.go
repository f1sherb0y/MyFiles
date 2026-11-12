package shell

import (
	"image"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"myfiles/internal/fsys"
)

var (
	mpr      = windows.NewLazySystemDLL("mpr.dll")
	netapi32 = windows.NewLazySystemDLL("netapi32.dll")

	procILCombine             = shell32.NewProc("ILCombine")
	procSHGetNameFromIDList   = shell32.NewProc("SHGetNameFromIDList")
	procWNetOpenEnumW         = mpr.NewProc("WNetOpenEnumW")
	procWNetEnumResourceW     = mpr.NewProc("WNetEnumResourceW")
	procWNetCloseEnum         = mpr.NewProc("WNetCloseEnum")
	procNetShareEnum          = netapi32.NewProc("NetShareEnum")
	procNetApiBufferFree      = netapi32.NewProc("NetApiBufferFree")
	clsidKnownFolderManager   = guid("4df0c730-df9d-4ae3-9153-aa6b82e9795a")
	iidKnownFolderManager     = guid("8be2d872-86aa-4d47-b776-32cca40c7018")
	frequentFoldersParsedName = "shell:::{3936E9E4-D92C-4EEE-A85A-BC16D5EA0819}"
)

// The system's own places, by the names its namespace parses.
const (
	placeRecycleBin = "::{645FF040-5081-101B-9F08-00AA002F954E}"
	placeComputer   = "::{20D04FE0-3AEA-1069-A2D8-08002B30309D}"
	placeNetwork    = "::{F02C1A0D-BE21-4350-88B0-7367FC96EF3C}"
	placeLinux      = "::{B2B4A4D1-2754-4140-A2EB-9A76D9D7CDC6}"
)

// knownFolderDefinition is the system's KNOWNFOLDER_DEFINITION.
type knownFolderDefinition struct {
	category      uint32
	name          *uint16
	description   *uint16
	parent        windows.GUID
	relativePath  *uint16
	parsingName   *uint16
	tooltip       *uint16
	localizedName *uint16
	icon          *uint16
	security      *uint16
	attributes    uint32
	flags         uint32
	folderType    windows.GUID
}

// knownFolderIcon is the icon the system gives a folder it knows by more
// than its path, as Downloads or Pictures, wherever the user moved it and
// whatever its attributes: nil for any other folder. The system's file
// manager shows these icons the same way; a folder's desktop.ini, which
// names its icon too, is only read for folders marked read-only.
func knownFolderIcon(path string, px int) *image.RGBA {
	manager, err := coCreate(&clsidKnownFolderManager, &iidKnownFolderManager)
	if err != nil {
		return nil
	}
	defer manager.release()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	const (
		findFolderFromPath  = 10
		getFolderDefinition = 11
		exactMatch          = 0
	)
	var folder *object
	if manager.call(findFolderFromPath, uintptr(unsafe.Pointer(p)), exactMatch, uintptr(unsafe.Pointer(&folder))).failed() || folder == nil {
		return nil
	}
	defer folder.release()
	var def knownFolderDefinition
	if folder.call(getFolderDefinition, uintptr(unsafe.Pointer(&def))).failed() {
		return nil
	}
	where := ""
	if def.icon != nil {
		where = windows.UTF16PtrToString(def.icon)
	}
	for _, s := range []*uint16{def.name, def.description, def.relativePath, def.parsingName, def.tooltip, def.localizedName, def.icon, def.security} {
		if s != nil {
			procCoTaskMemFree.Call(uintptr(unsafe.Pointer(s)))
		}
	}
	// "%SystemRoot%\system32\imageres.dll,-184": a file of icons, and
	// which of them.
	i := strings.LastIndexByte(where, ',')
	if i <= 0 {
		return nil
	}
	index, err := strconv.Atoi(strings.TrimSpace(where[i+1:]))
	if err != nil {
		return nil
	}
	return extractIcon(fsys.ExpandEnv(where[:i]), int32(index), px)
}

// placeIcon is the icon of a place of the system's namespace that is no
// path, by the name the namespace parses.
func placeIcon(name string, class iconClass) (*image.RGBA, error) {
	id, err := parsePIDL(name)
	if err != nil {
		return nil, err
	}
	defer id.free()
	const shgfiPIDL = 0x8
	flags := uintptr(shgfiPIDL | shgfiSysIconIndex)
	if class == iconSmall {
		flags |= shgfiSmallIcon
	}
	var info fileInfo
	if r, _, _ := procSHGetFileInfoW.Call(uintptr(id), 0, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), flags); r == 0 {
		return nil, errNoIcon
	}
	return iconOf(iconKey{index: info.index, class: class})
}

func displayName(path string) string {
	const (
		shgfiDisplayName = 0x200
		shgfiPIDL        = 0x8
	)
	var info fileInfo
	if strings.HasPrefix(path, "::") {
		id, err := parsePIDL(path)
		if err != nil {
			return ""
		}
		defer id.free()
		procSHGetFileInfoW.Call(uintptr(id), 0, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), shgfiDisplayName|shgfiPIDL)
	} else {
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return ""
		}
		procSHGetFileInfoW.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), shgfiDisplayName)
	}
	return windows.UTF16ToString(info.displayName[:])
}

// quickAccess lists the folders of the system's file manager's Quick
// access, the pinned ones first, as it shows them.
func quickAccess() []QuickFolder {
	var out []QuickFolder
	_ = onSTA(func() error {
		root, err := parsePIDL(frequentFoldersParsedName)
		if err != nil {
			return err
		}
		defer root.free()
		var folder *object
		hr, _, _ := procSHBindToObject.Call(0, uintptr(root), 0, uintptr(unsafe.Pointer(&iidShellFolder)), uintptr(unsafe.Pointer(&folder)))
		if err := hresult(hr).err(); err != nil {
			return err
		}
		defer folder.release()
		const (
			enumObjects    = 4
			enumNext       = 3
			contentFolders = 0x20
			// SIGDN_DESKTOPABSOLUTEPARSING: a path, for what has one.
			absoluteParsing = 0x80028000
		)
		var enum *object
		if folder.call(enumObjects, 0, contentFolders, uintptr(unsafe.Pointer(&enum))).failed() || enum == nil {
			return nil
		}
		defer enum.release()
		for len(out) < 64 {
			var child pidl
			var fetched uint32
			if hr := enum.call(enumNext, 1, uintptr(unsafe.Pointer(&child)), uintptr(unsafe.Pointer(&fetched))); hr != 0 || fetched == 0 {
				break
			}
			q := QuickFolder{Pinned: unpins(folder, child)}
			if abs, _, _ := procILCombine.Call(uintptr(root), uintptr(child)); abs != 0 {
				var name *uint16
				if hr, _, _ := procSHGetNameFromIDList.Call(abs, absoluteParsing, uintptr(unsafe.Pointer(&name))); !hresult(hr).failed() && name != nil {
					q.Path = windows.UTF16PtrToString(name)
					procCoTaskMemFree.Call(uintptr(unsafe.Pointer(name)))
				}
				procCoTaskMemFree.Call(abs)
			}
			child.free()
			if q.Path != "" {
				out = append(out, q)
			}
		}
		return nil
	})
	return out
}

// unpins tells whether an item of Quick access is pinned there: its menu
// then has the command that unpins it.
func unpins(folder *object, child pidl) bool {
	var menu *object
	if folder.call(folderGetUIObjectOf, 0, 1, uintptr(unsafe.Pointer(&child)), uintptr(unsafe.Pointer(&iidContextMenu)), 0, uintptr(unsafe.Pointer(&menu))).failed() || menu == nil {
		return false
	}
	m := &shellMenu{menu: menu}
	defer m.close()
	if m.handle, _, _ = procCreatePopupMenu.Call(); m.handle == 0 {
		return false
	}
	if m.menu.call(menuQueryContextMenu, m.handle, 0, firstCommand, lastCommand, 0).failed() {
		return false
	}
	for _, item := range m.items() {
		if strings.HasPrefix(strings.ToLower(item.verb), "unpin") {
			return true
		}
	}
	return false
}

// distros lists the Linux distributions of the Windows Subsystem for
// Linux, whose files are under \\wsl.localhost\<name>.
func distros() []string {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Lxss`, registry.READ)
	if err != nil {
		return nil
	}
	defer key.Close()
	ids, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var out []string
	for _, id := range ids {
		sub, err := registry.OpenKey(key, id, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if name, _, err := sub.GetStringValue("DistributionName"); err == nil && name != "" {
			out = append(out, name)
		}
		sub.Close()
	}
	return out
}

// netResource is the system's NETRESOURCEW.
type netResource struct {
	scope, typ, displayType, usage           uint32
	localName, remoteName, comment, provider *uint16
}

// computers lists the computers the network shows nearby, as `\\NAME`.
// It asks the network, which may take seconds.
func computers() []string {
	const (
		resourceContext = 5
		displayServer   = 2
		noMoreItems     = 259
	)
	var handle uintptr
	if r, _, _ := procWNetOpenEnumW.Call(resourceContext, 0, 0, 0, uintptr(unsafe.Pointer(&handle))); r != 0 {
		return nil
	}
	defer procWNetCloseEnum.Call(handle)
	var out []string
	buf := make([]byte, 32<<10)
	for {
		count, size := uint32(0xFFFFFFFF), uint32(len(buf))
		r, _, _ := procWNetEnumResourceW.Call(handle, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
		if r != 0 {
			break // none left, or the network gave up
		}
		entries := unsafe.Slice((*netResource)(unsafe.Pointer(&buf[0])), count)
		for i := range entries {
			if e := &entries[i]; e.displayType == displayServer && e.remoteName != nil {
				out = append(out, windows.UTF16PtrToString(e.remoteName))
			}
		}
	}
	return out
}

// shareInfo is the system's SHARE_INFO_1.
type shareInfo struct {
	name   *uint16
	typ    uint32
	remark *uint16
}

// shares lists the folders a computer shares, by their names.
func shares(server string) ([]string, error) {
	p, err := windows.UTF16PtrFromString(server)
	if err != nil {
		return nil, err
	}
	const (
		preferredLength = 0xFFFFFFFF
		typeMask        = 0xFF
		special         = 0x80000000 // as C$ and IPC$
	)
	var buf *shareInfo
	var read, total uint32
	r, _, _ := procNetShareEnum.Call(uintptr(unsafe.Pointer(p)), 1, uintptr(unsafe.Pointer(&buf)), preferredLength,
		uintptr(unsafe.Pointer(&read)), uintptr(unsafe.Pointer(&total)), 0)
	if buf != nil {
		defer procNetApiBufferFree.Call(uintptr(unsafe.Pointer(buf)))
	}
	if r != 0 {
		return nil, &os.PathError{Op: "list", Path: server, Err: windows.Errno(r)}
	}
	var out []string
	for _, s := range unsafe.Slice(buf, read) {
		if s.typ&typeMask == 0 && s.typ&special == 0 && s.name != nil {
			out = append(out, windows.UTF16PtrToString(s.name))
		}
	}
	return out, nil
}

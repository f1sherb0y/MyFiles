package fsys

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Volumes lists the drives with a letter. It may wait on drives that
// answer slowly, as network ones: call it off the main thread.
func Volumes() []Volume {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []Volume
	for i := range 26 {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		v := Volume{Root: root, Name: root[:2]}
		p, _ := windows.UTF16PtrFromString(root)
		switch windows.GetDriveType(p) {
		case windows.DRIVE_REMOVABLE:
			v.Kind = VolumeRemovable
		case windows.DRIVE_REMOTE:
			v.Kind = VolumeNetwork
		case windows.DRIVE_CDROM:
			v.Kind = VolumeOptical
		case windows.DRIVE_NO_ROOT_DIR:
			continue
		}
		var label, fs [windows.MAX_PATH + 1]uint16
		if windows.GetVolumeInformation(p, &label[0], uint32(len(label)), nil, nil, nil, &fs[0], uint32(len(fs))) == nil {
			v.Label = windows.UTF16ToString(label[:])
			v.FileSystem = windows.UTF16ToString(fs[:])
			v.Ready = true
		}
		var free, total, totalFree uint64
		if windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree) == nil {
			v.Free, v.Total = int64(free), int64(total)
		}
		out = append(out, v)
	}
	return out
}

// Places lists the user's folders a sidebar shows, those that exist.
func Places() []Place {
	var out []Place
	add := func(kind PlaceKind, id *windows.KNOWNFOLDERID) {
		path, err := windows.KnownFolderPath(id, windows.KF_FLAG_DONT_VERIFY)
		if err != nil || path == "" {
			return
		}
		if _, err := Stat(path); err != nil {
			return
		}
		out = append(out, Place{Kind: kind, Name: Base(path), Path: path})
	}
	add(PlaceHome, windows.FOLDERID_Profile)
	add(PlaceDesktop, windows.FOLDERID_Desktop)
	add(PlaceDownloads, windows.FOLDERID_Downloads)
	add(PlaceDocuments, windows.FOLDERID_Documents)
	add(PlacePictures, windows.FOLDERID_Pictures)
	add(PlaceMusic, windows.FOLDERID_Music)
	add(PlaceVideos, windows.FOLDERID_Videos)
	return out
}

// Home is the user's own folder.
func Home() string {
	path, err := windows.KnownFolderPath(windows.FOLDERID_Profile, windows.KF_FLAG_DONT_VERIFY)
	if err != nil {
		return `C:\`
	}
	return path
}

// knownFolders are the names "shell:" paths take, as Explorer does, in
// lower case.
var knownFolders = map[string]*windows.KNOWNFOLDERID{
	"desktop":           windows.FOLDERID_Desktop,
	"downloads":         windows.FOLDERID_Downloads,
	"personal":          windows.FOLDERID_Documents,
	"documents":         windows.FOLDERID_Documents,
	"my pictures":       windows.FOLDERID_Pictures,
	"pictures":          windows.FOLDERID_Pictures,
	"my music":          windows.FOLDERID_Music,
	"music":             windows.FOLDERID_Music,
	"my video":          windows.FOLDERID_Videos,
	"videos":            windows.FOLDERID_Videos,
	"profile":           windows.FOLDERID_Profile,
	"userprofiles":      windows.FOLDERID_UserProfiles,
	"public":            windows.FOLDERID_Public,
	"appdata":           windows.FOLDERID_RoamingAppData,
	"local appdata":     windows.FOLDERID_LocalAppData,
	"localappdatalow":   windows.FOLDERID_LocalAppDataLow,
	"programfiles":      windows.FOLDERID_ProgramFiles,
	"programfilesx86":   windows.FOLDERID_ProgramFilesX86,
	"common appdata":    windows.FOLDERID_ProgramData,
	"windows":           windows.FOLDERID_Windows,
	"system":            windows.FOLDERID_System,
	"fonts":             windows.FOLDERID_Fonts,
	"startup":           windows.FOLDERID_Startup,
	"common startup":    windows.FOLDERID_CommonStartup,
	"start menu":        windows.FOLDERID_StartMenu,
	"common start menu": windows.FOLDERID_CommonStartMenu,
	"programs":          windows.FOLDERID_Programs,
	"common programs":   windows.FOLDERID_CommonPrograms,
	"sendto":            windows.FOLDERID_SendTo,
	"recent":            windows.FOLDERID_Recent,
	"templates":         windows.FOLDERID_Templates,
	"quick launch":      windows.FOLDERID_QuickLaunch,
	"screenshots":       windows.FOLDERID_Screenshots,
	"savedgames":        windows.FOLDERID_SavedGames,
	"links":             windows.FOLDERID_Links,
	"favorites":         windows.FOLDERID_Favorites,
	"contacts":          windows.FOLDERID_Contacts,
	"onedrive":          windows.FOLDERID_SkyDrive,
	"3d objects":        windows.FOLDERID_Objects3D,
	"camera roll":       windows.FOLDERID_CameraRoll,
}

// KnownFolder is the path of a folder by the name Explorer's "shell:"
// paths give it, as "shell:Downloads", and false for names of folders that
// are no directories, as the Recycle Bin, or that it does not know.
func KnownFolder(name string) (string, bool) {
	id, ok := knownFolders[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return "", false
	}
	path, err := windows.KnownFolderPath(id, windows.KF_FLAG_DONT_VERIFY)
	if err != nil || path == "" {
		return "", false
	}
	return path, true
}

// ExpandEnv replaces %NAME% in a path with the environment's values, as
// the shell does for what is typed in an address bar.
func ExpandEnv(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return s
	}
	buf := make([]uint16, 1024)
	for {
		n, err := windows.ExpandEnvironmentStrings(p, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			return s
		}
		if int(n) <= len(buf) {
			return windows.UTF16ToString(buf[:n])
		}
		buf = make([]uint16, n)
	}
}

var _ = unsafe.Sizeof(0)

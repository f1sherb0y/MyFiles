package shell

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"myfiles/internal/assochash"
)

const fileExts = `Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\`

var associationMu sync.Mutex
var assocQueryString = windows.NewLazySystemDLL("shlwapi.dll").NewProc("AssocQueryStringW")

// EffectiveDefaultFor asks the shell, rather than trusting registry values
// which may be stale, rejected by Windows, or superseded by policy.
func EffectiveDefaultFor(ext string) (string, error) {
	ext = "." + strings.TrimPrefix(strings.ToLower(ext), ".")
	p, err := windows.UTF16PtrFromString(ext)
	if err != nil {
		return "", err
	}
	var buf [32768]uint16
	n := uint32(len(buf))
	const assocstrProgID = 20
	hr, _, _ := assocQueryString.Call(0, assocstrProgID, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if hr != 0 {
		return "", fmt.Errorf("query %s default: HRESULT 0x%08x", ext, uint32(hr))
	}
	return windows.UTF16ToString(buf[:]), nil
}

func notifyAssociations() { shell32.NewProc("SHChangeNotify").Call(0x08000000, 0, 0, 0) }

type choice struct {
	Extension string
	Latest    bool
	Exists    bool
	ProgID    string
	Hash      string
	Filetime  uint64
}

func choicePath(ext string, latest bool) string {
	if latest {
		return fileExts + "." + ext + `\UserChoiceLatest`
	}
	return fileExts + "." + ext + `\UserChoice`
}

func keyTime(k registry.Key) (uint64, error) {
	info, err := k.Stat()
	if err != nil {
		return 0, err
	}
	// Convert the UTC time to FILETIME without rounding through Unix seconds.
	ft := windows.NsecToFiletime(info.ModTime().UnixNano())
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime), nil
}

func readChoice(ext string, latest bool) (choice, error) {
	c := choice{Extension: ext, Latest: latest}
	k, err := registry.OpenKey(registry.CURRENT_USER, choicePath(ext, latest), registry.READ)
	if errors.Is(err, registry.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer k.Close()
	c.Exists = true
	c.Hash, _, err = k.GetStringValue("Hash")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return c, err
	}
	pk := k
	if latest {
		pk, err = registry.OpenKey(k, "ProgId", registry.READ)
		if err != nil {
			return c, fmt.Errorf("%s: unknown UserChoiceLatest layout: %w", ext, err)
		}
		defer pk.Close()
	}
	c.ProgID, _, err = pk.GetStringValue("ProgId")
	if err != nil {
		return c, err
	}
	c.Filetime, err = keyTime(pk)
	return c, err
}

type hashContext struct {
	sid, machine string
	latest       bool
}

func (h hashContext) hash(c choice) string {
	if c.Latest {
		return assochash.Latest("."+c.Extension, c.ProgID, h.sid, h.machine, c.Filetime)
	}
	return assochash.Legacy("."+c.Extension, c.ProgID, h.sid, c.Filetime)
}

// associationContext selects the layout. Windows 10 uses the legacy format,
// including on a fresh profile with no previous choices to compare. On newer
// Windows, confirm the codec against an existing choice before using it.
func associationContext() (hashContext, error) {
	var h hashContext
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return h, err
	}
	h.sid = user.User.Sid.String()
	v := windows.RtlGetVersion()
	if v.MajorVersion == 10 && v.BuildNumber < 22000 {
		return h, nil
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\SQMClient`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err == nil {
		h.machine, _, _ = k.GetStringValue("MachineId")
		k.Close()
	}
	root, err := registry.OpenKey(registry.CURRENT_USER, strings.TrimSuffix(fileExts, `\`), registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return h, err
	}
	names, err := root.ReadSubKeyNames(-1)
	root.Close()
	if err != nil {
		return h, err
	}
	// Detect the layout even if some records cannot be read or validated.
	for _, name := range names {
		k, e := registry.OpenKey(registry.CURRENT_USER, fileExts+name+`\UserChoiceLatest`, registry.READ)
		if e == nil {
			k.Close()
			h.latest = true
			break
		}
		if !errors.Is(e, registry.ErrNotExist) {
			return h, e
		}
	}
	for _, name := range names {
		if !strings.HasPrefix(name, ".") {
			continue
		}
		c, e := readChoice(strings.TrimPrefix(name, "."), h.latest)
		if e == nil && c.Exists && c.Hash != "" && c.Hash == h.hash(c) {
			return h, nil
		}
	}
	return h, errors.New("this Windows association checksum is not supported; no defaults were changed (use Windows Default Apps)")
}

// writeChoice only changes the requested per-user choice. It neither changes
// ACLs nor disables UCPD, Windows features or system services.
func (h hashContext) writeChoice(ext, id string, latest bool) error {
	if _, err := windows.UTF16FromString(id); err != nil {
		return err
	}
	path := choicePath(ext, latest)
	if !latest {
		// Windows normally denies SetValue on legacy UserChoice, but allows its
		// owner to replace the leaf key. No unrelated keys are removed.
		if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.READ|registry.WRITE)
	if err != nil {
		return err
	}
	defer k.Close()
	pk := k
	if latest {
		pk, _, err = registry.CreateKey(k, "ProgId", registry.READ|registry.WRITE)
		if err != nil {
			return err
		}
		defer pk.Close()
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := pk.SetStringValue("ProgId", id); err != nil {
			return err
		}
		stamp, err := keyTime(pk)
		if err != nil {
			return err
		}
		c := choice{Extension: ext, ProgID: id, Latest: latest, Filetime: stamp}
		hash := h.hash(c)
		if hash == "" {
			return errors.New("unsupported association checksum input")
		}
		if err := k.SetStringValue("Hash", hash); err != nil {
			return err
		}
		after, err := keyTime(pk)
		if err != nil {
			return err
		}
		// Latest's hash lives in the parent; legacy's hash write can cross a
		// minute boundary, in which case recompute it rather than leaving it bad.
		if latest || stamp/600000000 == after/600000000 {
			return nil
		}
	}
	return errors.New("association timestamp kept changing")
}

func (h hashContext) restore(c choice) error {
	if c.Exists {
		return h.writeChoice(c.Extension, c.ProgID, c.Latest)
	}
	path := choicePath(c.Extension, c.Latest)
	if c.Latest {
		if err := registry.DeleteKey(registry.CURRENT_USER, path+`\ProgId`); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	err := registry.DeleteKey(registry.CURRENT_USER, path)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func verifyDefault(ext, id string) error {
	current, err := EffectiveDefaultFor(ext)
	if err != nil {
		return err
	}
	if !strings.EqualFold(current, id) {
		return fmt.Errorf("Windows selected %q instead of %q", current, id)
	}
	return nil
}

// changeChoice keeps a single write and its rollback together. Verification
// runs after notifying the shell; tests can exercise rejection after a real
// registry write without depending on a particular machine's policy.
func (h hashContext) changeChoice(c choice, id string, verify func() error) error {
	now, err := readChoice(c.Extension, c.Latest)
	if err != nil {
		return fmt.Errorf("read current choice: %w", err)
	}
	if now != c {
		return errors.New("choice changed during setup; skipped")
	}
	previous, previousErr := EffectiveDefaultFor(c.Extension)
	err = h.writeChoice(c.Extension, id, c.Latest)
	if err == nil {
		notifyAssociations()
		err = verify()
	}
	if err == nil {
		return nil
	}
	// A denied write may have left the original record completely intact.
	// Avoid rewriting it (and changing its timestamp) in that case.
	after, readErr := readChoice(c.Extension, c.Latest)
	if readErr == nil && after == c {
		return err
	}
	rollbackErr := h.restore(c)
	notifyAssociations()
	if rollbackErr == nil && previousErr == nil {
		rollbackErr = verifyDefault(c.Extension, previous)
	}
	if rollbackErr != nil {
		return errors.Join(err, fmt.Errorf("restore previous choice: %w", rollbackErr))
	}
	return err
}

// setDefaultProgID changes one extension and verifies the shell accepted it.
func setDefaultProgID(ext, id string) error {
	associationMu.Lock()
	defer associationMu.Unlock()
	h, err := associationContext()
	if err != nil {
		return err
	}
	c, err := readChoice(strings.TrimPrefix(ext, "."), h.latest)
	if err != nil {
		return err
	}
	return h.changeChoice(c, id, func() error { return verifyDefault(ext, id) })
}

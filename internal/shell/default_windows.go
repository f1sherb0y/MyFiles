package shell

import (
	"errors"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	procRegSetValueExW = windows.NewLazySystemDLL("advapi32.dll").NewProc("RegSetValueExW")
	procSHChangeNotify = shell32.NewProc("SHChangeNotify")
)

// userClasses is where the current user's file types are registered,
// under HKEY_CURRENT_USER: what is there comes before the machine's.
const userClasses = `Software\Classes`

// The keys the registration is written to and remembered in, under
// HKEY_CURRENT_USER. They are variables for the package's tests alone,
// which point both at keys of their own: nothing else may change them.
var (
	// classesRoot is where folders and the shell's objects are
	// registered.
	classesRoot = userClasses
	// backupRoot is where what was registered before is kept, to put it
	// back.
	backupRoot = `Software\Cairn\DefaultBackup`
)

const (
	// folderVerb is the name of the verb that opens a folder here.
	folderVerb = "cairn"
	// folderVerbTitle is what the verb is called in menus.
	folderVerbTitle = "Open in Cairn"
	// explorerLaunch is the shell's object that Win+E, and explorer.exe
	// without a folder, start: its "opennewwindow" verb opens the file
	// manager.
	explorerLaunch = `CLSID\{52205fd8-5dfb-447d-801a-d0b52f2e83e1}`
	launchCommand  = `shell\opennewwindow\command`
	// delegateExecute names, in the machine's registration of the
	// object, the system's handler of the verb. A per-user value of the
	// name that is empty has the command run instead.
	delegateExecute = "DelegateExecute"

	// The values of a backup: what a default value was (absent when
	// there was none), whether the object's key was made by us, whether
	// it had a command, and the command we wrote.
	backupDefault    = "Default"
	backupCreated    = "Created"
	backupHadCommand = "HadCommand"
	backupCommand    = "Command"
)

// folderClasses are the types whose default verb opens a folder: the
// directories of file systems, and the roots of drives.
var folderClasses = []string{"Directory", "Drive"}

// folderCommand is the command line that opens a folder with the
// program.
func folderCommand(exe string) string { return `"` + exe + `" "%1"` }

// launchCommandLine is the one that starts the program with no folder.
func launchCommandLine(exe string) string { return `"` + exe + `"` }

func defaultFileManager(exe string) Default {
	d := Default{Folders: true}
	for _, class := range folderClasses {
		shell := classesRoot + `\` + class + `\shell`
		verb, _ := stringValue(shell, "")
		command, _ := stringValue(shell+`\`+folderVerb+`\command`, "")
		if !strings.EqualFold(verb, folderVerb) || !strings.EqualFold(command, folderCommand(exe)) {
			d.Folders = false
		}
	}
	key := classesRoot + `\` + explorerLaunch + `\` + launchCommand
	command, _ := stringValue(key, "")
	delegate, overridden := stringValue(key, delegateExecute)
	d.Shortcut = overridden && delegate == "" && strings.EqualFold(command, launchCommandLine(exe))
	return d
}

func setDefaultFileManager(exe string, d Default) error {
	var errs []error
	for _, class := range folderClasses {
		if d.Folders {
			errs = append(errs, registerFolders(class, exe))
		} else {
			errs = append(errs, unregisterFolders(class))
		}
	}
	if d.Shortcut {
		errs = append(errs, registerLaunch(exe))
	} else {
		errs = append(errs, unregisterLaunch())
	}
	if classesRoot == userClasses {
		// Tell the shell that what opens things changed, so that it
		// forgets what it remembers of it.
		const shcneAssocChanged, shcnfIDList = 0x08000000, 0x0
		procSHChangeNotify.Call(shcneAssocChanged, shcnfIDList, 0, 0)
	}
	return errors.Join(errs...)
}

// registerFolders makes the program's verb the default one of a class,
// after noting which was.
func registerFolders(class, exe string) error {
	shellPath := classesRoot + `\` + class + `\shell`
	shell, _, err := registry.CreateKey(registry.CURRENT_USER, shellPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer shell.Close()

	// The backup is taken once: a second registration, as from another
	// place the program moved to, finds our own verb there.
	backup, taken, err := registry.CreateKey(registry.CURRENT_USER, backupRoot+`\Folders\`+class, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer backup.Close()
	if !taken {
		if previous, _ := stringValue(shellPath, ""); !strings.EqualFold(previous, folderVerb) {
			if err := copyValue(backup, backupDefault, shell, ""); err != nil {
				return err
			}
		}
	}

	if err := setString(shellPath+`\`+folderVerb, "", folderVerbTitle); err != nil {
		return err
	}
	if err := setString(shellPath+`\`+folderVerb+`\command`, "", folderCommand(exe)); err != nil {
		return err
	}
	return shell.SetStringValue("", folderVerb)
}

// unregisterFolders takes the program's verb away from a class and puts
// back the default verb it had, or none.
func unregisterFolders(class string) error {
	shellPath := classesRoot + `\` + class + `\shell`
	backupPath := backupRoot + `\Folders\` + class
	shell, err := registry.OpenKey(registry.CURRENT_USER, shellPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		// Nothing of ours can be there.
		return forget(backupPath)
	}
	defer shell.Close()

	var errs []error
	// A default verb that is no longer ours is another program's, set
	// since: it stays.
	if isOurs(shellPath) {
		backup, err := registry.OpenKey(registry.CURRENT_USER, backupPath, registry.QUERY_VALUE)
		if err == nil {
			errs = append(errs, copyValue(shell, "", backup, backupDefault))
			backup.Close()
		} else {
			// Ours without a note of what was before: back to none.
			errs = append(errs, deleteValue(shell, ""))
		}
	}
	errs = append(errs, deleteTree(shellPath+`\`+folderVerb), forget(backupPath))
	prune(shellPath, classesRoot)
	return errors.Join(errs...)
}

// isOurs reports whether the default verb under a key is the program's.
func isOurs(shellPath string) bool {
	verb, _ := stringValue(shellPath, "")
	return strings.EqualFold(verb, folderVerb)
}

// registerLaunch has the system's key for its file manager start the
// program, after noting what the user had there, if anything.
func registerLaunch(exe string) error {
	objectPath := classesRoot + `\` + explorerLaunch
	commandPath := objectPath + `\` + launchCommand

	backup, taken, err := registry.CreateKey(registry.CURRENT_USER, backupRoot+`\Shortcut`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer backup.Close()
	if !taken {
		var created, hadCommand uint32
		if !keyExists(objectPath) {
			created = 1
		} else if command, err := registry.OpenKey(registry.CURRENT_USER, commandPath, registry.QUERY_VALUE); err == nil {
			hadCommand = 1
			err = errors.Join(copyValue(backup, backupDefault, command, ""), copyValue(backup, delegateExecute, command, delegateExecute))
			command.Close()
			if err != nil {
				return err
			}
		}
		err := errors.Join(backup.SetDWordValue(backupCreated, created), backup.SetDWordValue(backupHadCommand, hadCommand))
		if err != nil {
			return err
		}
	}

	if err := backup.SetStringValue(backupCommand, launchCommandLine(exe)); err != nil {
		return err
	}
	if err := setString(commandPath, "", launchCommandLine(exe)); err != nil {
		return err
	}
	return setString(commandPath, delegateExecute, "")
}

// unregisterLaunch gives the key back: it removes what registerLaunch
// made, or puts back the values the user had.
func unregisterLaunch() error {
	objectPath := classesRoot + `\` + explorerLaunch
	commandPath := objectPath + `\` + launchCommand
	backupPath := backupRoot + `\Shortcut`
	backup, err := registry.OpenKey(registry.CURRENT_USER, backupPath, registry.QUERY_VALUE)
	if err != nil {
		// Never registered: what is there is not ours to touch.
		return nil
	}
	created, _, _ := backup.GetIntegerValue(backupCreated)
	hadCommand, _, _ := backup.GetIntegerValue(backupHadCommand)
	ours, _, _ := backup.GetStringValue(backupCommand)
	current, _ := stringValue(commandPath, "")
	var errs []error
	switch {
	case !strings.EqualFold(current, ours):
		// Another program registered itself since: its command stays.
	case created != 0:
		errs = append(errs, deleteTree(objectPath))
		prune(parentKey(objectPath), classesRoot)
	case hadCommand != 0:
		command, _, err := registry.CreateKey(registry.CURRENT_USER, commandPath, registry.SET_VALUE)
		if err != nil {
			errs = append(errs, err)
			break
		}
		errs = append(errs, copyValue(command, "", backup, backupDefault), copyValue(command, delegateExecute, backup, delegateExecute))
		command.Close()
	default:
		// The object was the user's, the command ours.
		errs = append(errs, deleteTree(commandPath))
		prune(parentKey(commandPath), objectPath)
	}
	backup.Close()
	errs = append(errs, forget(backupPath))
	return errors.Join(errs...)
}

// forget deletes a backup, and the keys above it that it leaves empty.
func forget(backupPath string) error {
	err := deleteTree(backupPath)
	prune(parentKey(backupPath), parentKey(parentKey(backupRoot)))
	return err
}

// parentKey is the key a key is in.
func parentKey(path string) string {
	return path[:max(strings.LastIndexByte(path, '\\'), 0)]
}

// keyExists reports whether the current user has a key.
func keyExists(path string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// stringValue reads a string of the current user's, "" for the default
// value of the key, and reports whether there is one.
func stringValue(path, name string) (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	s, _, err := k.GetStringValue(name)
	return s, err == nil
}

// setString writes a string of the current user's, making the key.
func setString(path, name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

// copyValue makes a value of dst what a value of src is, of whatever
// type, and deletes it when src has none.
func copyValue(dst registry.Key, dstName string, src registry.Key, srcName string) error {
	// Without a buffer the answer is the size, as a buffer too short.
	n, _, err := src.GetValue(srcName, nil)
	if err == registry.ErrNotExist {
		return deleteValue(dst, dstName)
	}
	if err != nil && err != registry.ErrShortBuffer {
		return err
	}
	data := make([]byte, n+1) // never empty, to have an address
	n, typ, err := src.GetValue(srcName, data)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(dstName)
	if err != nil {
		return err
	}
	r, _, _ := procRegSetValueExW.Call(uintptr(dst), uintptr(unsafe.Pointer(name)), 0, uintptr(typ),
		uintptr(unsafe.Pointer(&data[0])), uintptr(n))
	if r != 0 {
		return windows.Errno(r)
	}
	return nil
}

// deleteValue deletes a value, which need not be there.
func deleteValue(k registry.Key, name string) error {
	if err := k.DeleteValue(name); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}

// deleteTree deletes a key of the current user's and all below it. The
// key need not be there.
func deleteTree(path string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.ENUMERATE_SUB_KEYS)
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return err
	}
	names, err := k.ReadSubKeyNames(-1)
	k.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := deleteTree(path + `\` + name); err != nil {
			return err
		}
	}
	return registry.DeleteKey(registry.CURRENT_USER, path)
}

// prune deletes a key if it holds nothing, then the key it was in if
// that holds nothing now, and so on up to stop, which stays. Empty keys
// mean nothing to the system; they are what registering and
// unregistering would otherwise leave behind.
func prune(path, stop string) {
	for len(path) > len(stop) && strings.HasPrefix(path, stop) {
		k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
		if err != nil {
			return
		}
		info, err := k.Stat()
		k.Close()
		if err != nil || info.SubKeyCount != 0 || info.ValueCount != 0 {
			return
		}
		if registry.DeleteKey(registry.CURRENT_USER, path) != nil {
			return
		}
		path = parentKey(path)
	}
}

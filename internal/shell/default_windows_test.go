package shell

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// sandbox points the registration at keys of the test's own, so that the
// user's settings for folders are never touched, and deletes them when
// the test is over. It returns the root of the classes.
func sandbox(t *testing.T) string {
	t.Helper()
	root := fmt.Sprintf(`Software\MyFilesTest\%08x`, rand.Uint32())
	oldClasses, oldBackup := classesRoot, backupRoot
	classesRoot, backupRoot = root+`\Classes`, root+`\MyFiles\DefaultBackup`
	if classesRoot == userClasses {
		t.Fatal("the sandbox is the user's own classes")
	}
	if err := setString(classesRoot, "", ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		classesRoot, backupRoot = oldClasses, oldBackup
		if err := deleteTree(root); err != nil {
			t.Errorf("cleaning up: %v", err)
		}
		prune(`Software\MyFilesTest`, `Software`)
	})
	return classesRoot
}

// tree is every key and value under a key of the current user's, as
// lines, to compare states of the registry.
func tree(t *testing.T, path string) []string {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.READ)
	if err != nil {
		return nil
	}
	defer k.Close()
	lines := []string{path}
	values, _ := k.ReadValueNames(-1)
	for _, name := range values {
		buf := make([]byte, 1024)
		n, typ, _ := k.GetValue(name, buf)
		lines = append(lines, fmt.Sprintf("%s [%s] type %d = %q", path, name, typ, buf[:n]))
	}
	subkeys, _ := k.ReadSubKeyNames(-1)
	for _, name := range subkeys {
		lines = append(lines, tree(t, path+`\`+name)...)
	}
	return lines
}

func sameTree(t *testing.T, what string, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) == fmt.Sprint(want) {
		return
	}
	t.Errorf("%s: the registry is", what)
	for _, line := range got {
		t.Logf("  %s", line)
	}
	t.Log("want")
	for _, line := range want {
		t.Logf("  %s", line)
	}
}

const (
	testExe  = `C:\Program Files\MyFiles\myfiles.exe`
	otherExe = `D:\Elsewhere\myfiles.exe`
)

func set(t *testing.T, exe string, d Default) {
	t.Helper()
	if err := SetDefaultFileManager(exe, d); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultFileManagerFromNothing(t *testing.T) {
	classes := sandbox(t)
	before := tree(t, classes)
	if got := DefaultFileManager(testExe); got != (Default{}) {
		t.Fatalf("before anything: %+v", got)
	}

	set(t, testExe, Default{Folders: true, Shortcut: true})
	if got := DefaultFileManager(testExe); got != (Default{Folders: true, Shortcut: true}) {
		t.Errorf("after turning both on: %+v", got)
	}
	for path, want := range map[string]string{
		classes + `\Directory\shell`:                 "myfiles",
		classes + `\Directory\shell\myfiles`:         "Open in MyFiles",
		classes + `\Directory\shell\myfiles\command`: `"C:\Program Files\MyFiles\myfiles.exe" "%1"`,
		classes + `\Drive\shell`:                     "myfiles",
		classes + `\Drive\shell\myfiles\command`:     `"C:\Program Files\MyFiles\myfiles.exe" "%1"`,
		classes + `\CLSID\{52205fd8-5dfb-447d-801a-d0b52f2e83e1}\shell\opennewwindow\command`: `"C:\Program Files\MyFiles\myfiles.exe"`,
	} {
		if got, _ := stringValue(path, ""); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	delegate, ok := stringValue(classes+`\CLSID\{52205fd8-5dfb-447d-801a-d0b52f2e83e1}\shell\opennewwindow\command`, "DelegateExecute")
	if !ok || delegate != "" {
		t.Errorf("DelegateExecute = %q, there %v; want it there and empty", delegate, ok)
	}
	// The path of the program is compared without case, and another
	// program's registration is not ours.
	if got := DefaultFileManager(`c:\program files\myfiles\MYFILES.EXE`); got != (Default{Folders: true, Shortcut: true}) {
		t.Errorf("in another case: %+v", got)
	}
	if got := DefaultFileManager(otherExe); got != (Default{}) {
		t.Errorf("for another program: %+v", got)
	}

	// One at a time.
	set(t, testExe, Default{Folders: true})
	if got := DefaultFileManager(testExe); got != (Default{Folders: true}) {
		t.Errorf("after turning the shortcut off: %+v", got)
	}
	set(t, testExe, Default{Shortcut: true})
	if got := DefaultFileManager(testExe); got != (Default{Shortcut: true}) {
		t.Errorf("after swapping: %+v", got)
	}

	// Off leaves the registry as it was, and nothing of the backup.
	set(t, testExe, Default{})
	if got := DefaultFileManager(testExe); got != (Default{}) {
		t.Errorf("after turning both off: %+v", got)
	}
	sameTree(t, "after turning both off", tree(t, classes), before)
	if left := tree(t, backupRoot); left != nil {
		t.Errorf("the backup is left: %q", left)
	}
	// Off again changes nothing.
	set(t, testExe, Default{})
	sameTree(t, "after turning off twice", tree(t, classes), before)
}

func TestDefaultFileManagerRestores(t *testing.T) {
	classes := sandbox(t)
	object := classes + `\CLSID\{52205fd8-5dfb-447d-801a-d0b52f2e83e1}`
	// What a user may have: a default verb for directories, of the kind
	// that expands, none for drives but another verb there; and a
	// command of their own for the system's key.
	k, _, err := registry.CreateKey(registry.CURRENT_USER, classes+`\Directory\shell`, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SetExpandStringValue("", "explore"); err != nil {
		t.Fatal(err)
	}
	k.Close()
	for path, value := range map[string]string{
		classes + `\Directory\shell\other\command`: `other.exe "%1"`,
		classes + `\Drive\shell\other\command`:     `other.exe "%1"`,
		object + `\shell\opennewwindow\command`:    `"C:\Other\fm.exe"`,
		object + `\shell\open`:                     "kept",
	} {
		if err := setString(path, "", value); err != nil {
			t.Fatal(err)
		}
	}
	if err := setString(object+`\shell\opennewwindow\command`, "DelegateExecute", "{11111111-2222-3333-4444-555555555555}"); err != nil {
		t.Fatal(err)
	}
	before := tree(t, classes)
	if got := DefaultFileManager(testExe); got != (Default{}) {
		t.Fatalf("before anything: %+v", got)
	}

	set(t, testExe, Default{Folders: true, Shortcut: true})
	if got := DefaultFileManager(testExe); got != (Default{Folders: true, Shortcut: true}) {
		t.Errorf("after turning both on: %+v", got)
	}
	// On again, from another place: the backup is the first one still.
	set(t, otherExe, Default{Folders: true, Shortcut: true})
	if got := DefaultFileManager(otherExe); got != (Default{Folders: true, Shortcut: true}) {
		t.Errorf("after moving: %+v", got)
	}
	if got := DefaultFileManager(testExe); got != (Default{}) {
		t.Errorf("the old place after moving: %+v", got)
	}

	set(t, otherExe, Default{})
	sameTree(t, "after turning both off", tree(t, classes), before)
	if left := tree(t, backupRoot); left != nil {
		t.Errorf("the backup is left: %q", left)
	}
}

// TestDefaultFileManagerKeepsOthers turns the registration off after
// another program took its place: that program's stays.
func TestDefaultFileManagerKeepsOthers(t *testing.T) {
	classes := sandbox(t)
	command := classes + `\CLSID\{52205fd8-5dfb-447d-801a-d0b52f2e83e1}\shell\opennewwindow\command`
	set(t, testExe, Default{Folders: true, Shortcut: true})
	for path, value := range map[string]string{
		classes + `\Directory\shell`: "other",
		classes + `\Drive\shell`:     "other",
		command:                      `"C:\Other\fm.exe"`,
	} {
		if err := setString(path, "", value); err != nil {
			t.Fatal(err)
		}
	}
	if got := DefaultFileManager(testExe); got != (Default{}) {
		t.Errorf("after another program took over: %+v", got)
	}
	set(t, testExe, Default{})
	for _, path := range []string{classes + `\Directory\shell`, classes + `\Drive\shell`} {
		if got, _ := stringValue(path, ""); got != "other" {
			t.Errorf("%s = %q, want the other program's verb", path, got)
		}
		if keyExists(path + `\myfiles`) {
			t.Errorf("%s still has our verb", path)
		}
	}
	if got, _ := stringValue(command, ""); got != `"C:\Other\fm.exe"` {
		t.Errorf("the command is %q, want the other program's", got)
	}
	if left := tree(t, backupRoot); left != nil {
		t.Errorf("the backup is left: %q", left)
	}
}

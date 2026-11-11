package shell

import (
	"os"
	"testing"
	"unsafe"
)

// TestMain keeps the tests out of sight: no file operation of theirs may
// show its progress or an error of the system's. They show nothing else
// either: no menu is tracked, nothing is opened, and the registry they
// write is a key of their own.
func TestMain(m *testing.M) {
	testFlags = fofSilent | fofNoErrorUI
	// Whatever a test forgets, it cannot reach the user's registration.
	classesRoot, backupRoot = `Software\CairnTest\unset\Classes`, `Software\CairnTest\unset\Backup`
	os.Exit(m.Run())
}

// onWorker runs fn on a worker and waits for it.
func onWorker(t testing.TB, fn func()) {
	t.Helper()
	w := NewWorker()
	defer w.Close()
	done := make(chan struct{})
	w.Do(func() {
		defer close(done)
		fn()
	})
	<-done
}

// TestLayouts checks the structures passed to the system against the
// sizes it has for them on 64-bit Windows.
func TestLayouts(t *testing.T) {
	for _, s := range []struct {
		name      string
		got, want uintptr
	}{
		{"SHFILEINFOW", unsafe.Sizeof(fileInfo{}), 696},
		{"ICONINFO", unsafe.Sizeof(iconInfo{}), 32},
		{"BITMAP", unsafe.Sizeof(bitmapHeader{}), 32},
		{"BITMAPINFOHEADER and masks", unsafe.Sizeof(bitmapInfo{}), 40 + 12},
		{"SHELLEXECUTEINFOW", unsafe.Sizeof(executeInfo{}), 112},
		{"CMINVOKECOMMANDINFOEX", unsafe.Sizeof(invokeInfo{}), 104},
		{"MENUITEMINFOW", unsafe.Sizeof(menuItemInfo{}), 80},
		{"WNDCLASSEXW", unsafe.Sizeof(windowClass{}), 80},
		{"MSG", unsafe.Sizeof(message{}), 48},
	} {
		if s.got != s.want {
			t.Errorf("%s is %d bytes, want %d", s.name, s.got, s.want)
		}
	}
}

func TestCutData(t *testing.T) {
	if got := cutData(true); len(got) != 4 || got[0] != 2 || !isCut(got) {
		t.Errorf("cutData(true) = %v, isCut %v", got, isCut(got))
	}
	if got := cutData(false); len(got) != 4 || got[0] != 5 || isCut(got) {
		t.Errorf("cutData(false) = %v, isCut %v", got, isCut(got))
	}
	if isCut(nil) || isCut([]byte{2}) {
		t.Error("isCut of too little data")
	}
	if CutFormat != "Preferred DropEffect" {
		t.Errorf("CutFormat = %q", CutFormat)
	}
}

// TestQuiet checks only that turning the sounds off does not fail
// loudly.
func TestQuiet(t *testing.T) {
	Quiet()
}

package shell

import (
	"path/filepath"
	"testing"
	"time"
)

var procSendMessageW = user32.NewProc("SendMessageW")

// builtMenu builds a menu on a host thread, as ContextMenu does, and
// hands it to use there. The menu is never shown.
func builtMenu(t *testing.T, paths []string, background string, use func(m *shellMenu)) {
	t.Helper()
	err := within(t, func() error {
		return onHost(func() error {
			m, err := buildMenu(0, paths, background, cmfExplore|cmfCanRename)
			if err != nil {
				return err
			}
			defer m.close()
			use(m)
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// logItems logs the top level of a menu and returns how many commands
// and submenus it has.
func logItems(t *testing.T, m *shellMenu) (count int) {
	t.Helper()
	for _, item := range m.items() {
		switch {
		case item.separator:
			t.Log("  ----")
			continue
		case item.submenu != 0:
			t.Logf("  %q >", item.text)
		case item.verb != "":
			t.Logf("  %q (%s)", item.text, item.verb)
		default:
			t.Logf("  %q", item.text)
		}
		count++
	}
	return count
}

func TestMenuOfFiles(t *testing.T) {
	dir := t.TempDir()
	one, two := filepath.Join(dir, "one.txt"), filepath.Join(dir, "two.txt")
	write(t, one, "1")
	write(t, two, "2")

	builtMenu(t, []string{one}, "", func(m *shellMenu) {
		t.Log("one file:")
		if logItems(t, m) == 0 {
			t.Error("the menu of a file is empty")
		}
		if m.dir != dir {
			t.Errorf("the menu's directory is %q", m.dir)
		}
		verbs := map[string]bool{}
		for _, item := range m.items() {
			verbs[item.verb] = true
		}
		for _, verb := range []string{"delete", "rename", "properties"} {
			if !verbs[verb] {
				t.Errorf("no %s in the menu of a file", verb)
			}
		}
	})
	builtMenu(t, []string{one, two}, "", func(m *shellMenu) {
		t.Log("two files:")
		if logItems(t, m) == 0 {
			t.Error("the menu of two files is empty")
		}
	})
	builtMenu(t, []string{dir}, "", func(m *shellMenu) {
		t.Log("a directory:")
		if logItems(t, m) == 0 {
			t.Error("the menu of a directory is empty")
		}
	})
}

func TestMenuOfBackground(t *testing.T) {
	dir := t.TempDir()
	builtMenu(t, nil, dir, func(m *shellMenu) {
		t.Log("background:")
		if logItems(t, m) == 0 {
			t.Error("the menu of a directory's background is empty")
		}
		if m.dir != dir {
			t.Errorf("the menu's directory is %q", m.dir)
		}
	})
}

func TestMenuOfMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.txt")
	err := within(t, func() error {
		return onHost(func() error {
			m, err := buildMenu(0, []string{missing}, "", 0)
			if err == nil {
				m.close()
			}
			return err
		})
	})
	if err == nil {
		t.Error("a menu for what is not there")
	}
	t.Logf("the menu of a missing file: %v", err)
	if _, err := ContextMenu(0, nil, "", 0, 0); err != nil {
		t.Errorf("no paths and no background: %v", err)
	}
}

// TestMenuWindow opens the submenus of a file's menu as the system
// would, by the message a menu sends its window before it shows one, and
// checks that the shell filled them: the window passes the messages on.
// Nothing shows: the window is hidden and no menu is tracked.
func TestMenuWindow(t *testing.T) {
	file := filepath.Join(t.TempDir(), "one.txt")
	write(t, file, "1")
	builtMenu(t, []string{file}, "", func(m *shellMenu) {
		// On the host's thread a test can fail, not stop.
		if m.menu2 == nil && m.menu3 == nil {
			t.Error("the menu takes no messages")
			return
		}
		window, err := newMenuWindow(m)
		if err != nil {
			t.Error(err)
			return
		}
		defer closeMenuWindow(window, 0)
		filled := 0
		for i, item := range m.items() {
			if item.submenu == 0 {
				continue
			}
			before, _, _ := procGetMenuItemCount.Call(item.submenu)
			start := time.Now()
			procSendMessageW.Call(window, wmInitMenuPopup, item.submenu, uintptr(i))
			after, _, _ := procGetMenuItemCount.Call(item.submenu)
			t.Logf("  %q: %d items, %d once opened (%v)", item.text, int32(before), int32(after), time.Since(start).Round(time.Millisecond))
			if int32(after) > 0 {
				filled++
			}
		}
		if filled == 0 {
			t.Error("no submenu has items")
		}
	})
}

func TestMenuWindowClass(t *testing.T) {
	// Several windows of the one class, made on different threads.
	for range 3 {
		err := onSTA(func() error {
			window, err := newMenuWindow(&shellMenu{})
			if err != nil {
				return err
			}
			closeMenuWindow(window, 0)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

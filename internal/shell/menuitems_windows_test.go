package shell

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMenuText(t *testing.T) {
	for in, want := range map[string]string{
		"&Open":            "Open",
		"Cu&t\tCtrl+X":     "Cut",
		"打开(&O)":           "打开",
		"Save && Close":    "Save & Close",
		"  Plain  ":        "Plain",
		"7-Zip":            "7-Zip",
		"Send &to\tAlt+S ": "Send to",
	} {
		if got := menuText(in); got != want {
			t.Errorf("menuText(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLoadMenu reads the system's menu of a file without showing it: it
// has commands with names, and submenus that can be listed. Nothing is
// run.
func TestLoadMenu(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if long, err := filepath.EvalSymlinks(path); err == nil {
		path = long
	}
	m, err := LoadMenu(0, []string{path}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	items := m.Items()
	if len(items) < 5 {
		t.Fatalf("the menu of a file has %d items: %+v", len(items), items)
	}
	verbs := map[string]bool{}
	subs, icons := 0, 0
	for _, item := range items {
		switch {
		case item.Separator:
			continue
		case item.Text == "":
			t.Errorf("an item without text: %+v", item)
		case (item.ID == 0) == (item.Sub == 0):
			t.Errorf("%q is neither a command nor a submenu, or both: %+v", item.Text, item)
		}
		verbs[item.Verb] = true
		if item.Icon != nil {
			icons++
		}
		if item.Sub != 0 {
			subs++
			t.Logf("%s > %d items", item.Text, len(m.Submenu(item.Sub)))
		}
	}
	for _, verb := range []string{"delete", "rename", "properties"} {
		if !verbs[verb] {
			t.Errorf("the menu has no %q", verb)
		}
	}
	t.Logf("%d items, %d submenus, %d with icons", len(items), subs, icons)
	m.Close()
	if got := m.Submenu(1); got != nil {
		t.Errorf("a closed menu lists a submenu: %v", got)
	}
}

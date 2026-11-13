package shell

import (
	"strings"
	"testing"
)

// The programs and the settings the system lists: some of each, each
// with a name and what it is found by, and with an icon.
func TestPrograms(t *testing.T) {
	list := Programs()
	apps, settings := 0, 0
	var notepad, firstSetting Program
	for _, p := range list {
		if p.Name == "" || !strings.HasPrefix(p.Path, "::") {
			t.Fatalf("listed: %+v", p)
		}
		if p.Setting {
			settings++
			if firstSetting.Path == "" {
				firstSetting = p
			}
		} else {
			apps++
			if strings.Contains(strings.ToLower(p.Path), "notepad") || strings.Contains(p.Name, "记事本") || strings.EqualFold(p.Name, "Notepad") {
				notepad = p
			}
		}
	}
	t.Logf("%d programs, %d settings; e.g. %+v and %+v", apps, settings, notepad, firstSetting)
	if apps < 10 || settings < 20 {
		t.Fatalf("%d programs and %d settings", apps, settings)
	}
	for _, p := range []Program{notepad, firstSetting} {
		if p.Path == "" {
			continue
		}
		if img, err := Icon(p.Path, 0, 20, false); err != nil || img == nil {
			t.Errorf("no icon of %s: %v", p.Name, err)
		}
	}
}

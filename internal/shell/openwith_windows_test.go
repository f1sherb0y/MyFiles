package shell

import (
	"slices"
	"testing"
)

// TestApps lists the programs for a type every system has some for.
// Listing shows nothing and starts nothing.
func TestApps(t *testing.T) {
	apps := Apps(`C:\nowhere\notes.txt`)
	if len(apps) == 0 {
		t.Fatal("no program opens text files")
	}
	seen := map[string]bool{}
	for _, app := range apps {
		if app.Name == "" || app.ID == "" {
			t.Errorf("a program without a name or an identity: %+v", app)
		}
		if seen[app.ID] {
			t.Errorf("%q is listed twice", app.ID)
		}
		seen[app.ID] = true
		t.Logf("%s (%s): icon %v", app.Name, app.ID, app.Icon != nil)
	}
	if !slices.ContainsFunc(apps, func(a App) bool { return a.Icon != nil && a.Icon.Rect.Dx() > 0 }) {
		t.Error("no program for text files has an icon")
	}
	if apps := Apps(`C:\nowhere\README`); len(apps) != 0 {
		t.Errorf("a file without an extension has programs: %v", apps)
	}
}

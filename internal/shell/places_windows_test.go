package shell

import (
	"testing"
	"time"

	"myfiles/internal/fsys"
)

// TestPlaces reads what the sidebar shows of the system: nothing is
// opened or changed.
func TestPlaces(t *testing.T) {
	onWorker(t, func() {
		for _, place := range []string{RecycleBin, ThisPC, Network} {
			name := DisplayName(place)
			img, err := Icon(place, fsys.AttrDir, 16, false)
			if name == "" || err != nil || img == nil {
				t.Errorf("%s: name %q, icon %v, %v", place, name, img != nil, err)
			}
			t.Logf("%s is %q", place, name)
		}
		for _, p := range fsys.Places() {
			known := knownFolderIcon(p.Path, 32)
			t.Logf("%-24s shown as %-12q known folder icon %v", p.Path, DisplayName(p.Path), known != nil)
		}
		if knownFolderIcon(t.TempDir(), 32) != nil {
			t.Error("a folder the system does not know has a known folder's icon")
		}
	})
	start := time.Now()
	quick := QuickAccess()
	pinned := 0
	for _, q := range quick {
		if q.Pinned {
			pinned++
		}
	}
	t.Logf("Quick access: %d folders, %d pinned, in %v", len(quick), pinned, time.Since(start).Round(time.Millisecond))
	for _, q := range quick {
		if q.Path == "" {
			t.Error("a folder of Quick access without a path")
		}
	}
	t.Logf("Linux: %v", Distros())
	start = time.Now()
	t.Logf("network: %d computers in %v", len(Computers()), time.Since(start).Round(time.Millisecond))
}

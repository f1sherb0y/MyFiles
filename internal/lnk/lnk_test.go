package lnk

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// make writes a shortcut as the shell does, by the shell's own script
// host, which every Windows has.
func makeLink(t *testing.T, link, target string) {
	t.Helper()
	script := `$s = (New-Object -ComObject WScript.Shell).CreateShortcut('` + link + `'); $s.TargetPath = '` + target + `'; $s.Save()`
	if out, err := exec.Command("powershell", "-NoProfile", "-Command", script).CombinedOutput(); err != nil {
		t.Skipf("no shortcut made: %v %s", err, out)
	}
}

func TestRead(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("shortcuts are made by Windows")
	}
	dir := t.TempDir()
	doc := filepath.Join(dir, "报告 final.docx")
	os.WriteFile(doc, nil, 0o644)
	sub := filepath.Join(dir, "sub folder")
	os.Mkdir(sub, 0o755)
	exe, _ := os.Executable()
	for _, c := range []struct {
		target string
		dir    bool
	}{{doc, false}, {sub, true}, {exe, false}, {`%windir%\notepad.exe`, false}} {
		link := filepath.Join(dir, "to.lnk")
		makeLink(t, link, c.target)
		got, ok := Read(link)
		want := c.target
		if strings.Contains(want, "%") {
			want = filepath.Join(os.Getenv("windir"), "notepad.exe")
		}
		if !ok || !strings.EqualFold(got.Path, want) || got.Dir != c.dir {
			t.Errorf("a shortcut to %s reads as %+v, %v", c.target, got, ok)
		}
		os.Remove(link)
	}
	// What is no shortcut is told so.
	if _, ok := Read(doc); ok {
		t.Error("an empty file reads as a shortcut")
	}
	if _, ok := Parse([]byte(strings.Repeat("x", 200))); ok {
		t.Error("noise reads as a shortcut")
	}
}

// The shortcuts of the Start menu, which are what a search meets: each
// reads, and most name a path.
func TestReadStartMenu(t *testing.T) {
	root := filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs`)
	total, read, named := 0, 0, 0
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".lnk") {
			total++
			if got, ok := Read(path); ok {
				read++
				if got.Path != "" {
					named++
				}
			} else {
				t.Logf("not read: %s", path)
			}
		}
		return nil
	})
	if total == 0 {
		t.Skip("no Start menu here")
	}
	t.Logf("%d shortcuts, %d read, %d name a path", total, read, named)
	if read != total || named < total/2 {
		t.Errorf("of %d shortcuts %d read and %d name a path", total, read, named)
	}
}

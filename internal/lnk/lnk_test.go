package lnk

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// maker is a script that writes shortcuts as Explorer does, by the shell's
// own object for them. Its arguments are pairs: a shortcut's file, and what
// it points at.
//
// The script host every Windows has (WScript.Shell) writes them too, but
// passes the target through the system's code page: for a name that page
// cannot spell it writes a shortcut to nothing, and says nothing.
const maker = `
Add-Type @'
using System; using System.Text; using System.Runtime.InteropServices; using System.Runtime.InteropServices.ComTypes;
[ComImport, Guid("00021401-0000-0000-C000-000000000046")] public class ShellLink {}
[ComImport, InterfaceType(ComInterfaceType.InterfaceIsIUnknown), Guid("000214F9-0000-0000-C000-000000000046")]
public interface IShellLinkW {
    void GetPath([Out, MarshalAs(UnmanagedType.LPWStr)] StringBuilder file, int max, IntPtr data, int flags);
    void GetIDList(out IntPtr list);
    void SetIDList(IntPtr list);
    void GetDescription([Out, MarshalAs(UnmanagedType.LPWStr)] StringBuilder name, int max);
    void SetDescription([MarshalAs(UnmanagedType.LPWStr)] string name);
    void GetWorkingDirectory([Out, MarshalAs(UnmanagedType.LPWStr)] StringBuilder dir, int max);
    void SetWorkingDirectory([MarshalAs(UnmanagedType.LPWStr)] string dir);
    void GetArguments([Out, MarshalAs(UnmanagedType.LPWStr)] StringBuilder args, int max);
    void SetArguments([MarshalAs(UnmanagedType.LPWStr)] string args);
    void GetHotkey(out short key);
    void SetHotkey(short key);
    void GetShowCmd(out int show);
    void SetShowCmd(int show);
    void GetIconLocation([Out, MarshalAs(UnmanagedType.LPWStr)] StringBuilder icon, int max, out int index);
    void SetIconLocation([MarshalAs(UnmanagedType.LPWStr)] string icon, int index);
    void SetRelativePath([MarshalAs(UnmanagedType.LPWStr)] string path, int reserved);
    void Resolve(IntPtr window, int flags);
    void SetPath([MarshalAs(UnmanagedType.LPWStr)] string file);
}
public static class Maker {
    public static void Make(string link, string target) {
        var l = (IShellLinkW)new ShellLink();
        l.SetPath(target);
        ((IPersistFile)l).Save(link, false);
    }
}
'@
for ($i = 0; $i + 1 -lt $args.Count; $i += 2) { [Maker]::Make($args[$i], $args[$i + 1]) }
`

// makeLinks writes shortcuts: pairs of a shortcut's file and its target.
func makeLinks(t *testing.T, dir string, pairs ...string) {
	t.Helper()
	// With the mark of UTF-8 at its start, which is how Windows's own
	// PowerShell knows the script's encoding.
	script := filepath.Join(dir, "make.ps1")
	if err := os.WriteFile(script, []byte("\xEF\xBB\xBF"+maker), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}, pairs...)
	if out, err := exec.Command("powershell", args...).CombinedOutput(); err != nil {
		t.Skipf("no shortcuts made: %v %s", err, out)
	}
}

func TestRead(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("shortcuts are made by Windows")
	}
	// The folder by its long names, which are what a shortcut keeps: the
	// temporary folder may be named by a short one.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "报告 final.docx")
	os.WriteFile(doc, nil, 0o644)
	// Names no single code page spells: wherever this runs the shortcut
	// cannot keep them in the system's.
	mixed := filepath.Join(dir, "보고서 报告 отчёт.docx")
	os.WriteFile(mixed, nil, 0o644)
	deep := filepath.Join(dir, "폴더 文件夹", "sub")
	os.MkdirAll(deep, 0o755)
	sub := filepath.Join(dir, "sub folder")
	os.Mkdir(sub, 0o755)
	exe, _ := os.Executable()
	if long, err := filepath.EvalSymlinks(exe); err == nil {
		exe = long
	}
	cases := []struct {
		target string
		dir    bool
		link   string
	}{{target: doc}, {target: mixed}, {target: deep, dir: true}, {target: sub, dir: true}, {target: exe}, {target: `%windir%\notepad.exe`}}
	var pairs []string
	for i := range cases {
		cases[i].link = filepath.Join(dir, "to"+string(rune('a'+i))+".lnk")
		pairs = append(pairs, cases[i].link, cases[i].target)
	}
	makeLinks(t, dir, pairs...)
	for _, c := range cases {
		got, ok := Read(c.link)
		want := c.target
		if strings.Contains(want, "%") {
			want = filepath.Join(os.Getenv("windir"), "notepad.exe")
		}
		if !ok || !strings.EqualFold(got.Path, want) || got.Dir != c.dir {
			t.Errorf("a shortcut to %s reads as %+v, %v", c.target, got, ok)
			if data, err := os.ReadFile(c.link); err == nil {
				t.Logf("the shortcut:\n%s", hex.Dump(data))
			}
		}
		// The shell's items name the same file, where they name one.
		data, _ := os.ReadFile(c.link)
		if _, _, byItems, _ := parse(data); !strings.Contains(c.target, "%") && !strings.EqualFold(byItems, want) {
			t.Errorf("the items of a shortcut to %s name %q", c.target, byItems)
			t.Logf("the shortcut:\n%s", hex.Dump(data[:min(len(data), 0x280)]))
		}
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
// reads, and most name a path. Where both the place a shortcut keeps its
// path in and its items name a file, they name the same.
func TestReadStartMenu(t *testing.T) {
	root := filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs`)
	total, read, named, both, differ := 0, 0, 0, 0, 0
	look := func(path string, d os.DirEntry, err error) error {
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
			data, _ := os.ReadFile(path)
			if _, byInfo, byItems, ok := parse(data); ok && byInfo != "" && byItems != "" {
				both++
				if !strings.EqualFold(byInfo, byItems) {
					differ++
					t.Logf("%s: kept as %q, its items name %q", filepath.Base(path), byInfo, byItems)
				}
			}
		}
		return nil
	}
	filepath.WalkDir(root, look)
	// The user's own, whose shortcuts start from the user's folder.
	if own := os.Getenv("APPDATA"); own != "" {
		filepath.WalkDir(filepath.Join(own, "Microsoft", "Windows", "Start Menu", "Programs"), look)
	}
	if total == 0 {
		t.Skip("no Start menu here")
	}
	t.Logf("%d shortcuts, %d read, %d name a path; %d name it both ways, %d of them differently", total, read, named, both, differ)
	if read != total || named < total/2 {
		t.Errorf("of %d shortcuts %d read and %d name a path", total, read, named)
	}
	if differ > both/10 {
		t.Errorf("of %d shortcuts that name a file both ways %d name different ones", both, differ)
	}
}

package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What opens a file is found by its type: a class for what has a program,
// nothing for what has none, which the caller asks about.
func TestClassOf(t *testing.T) {
	for _, path := range []string{`C:\x\file.myfiles-no-such-type`, `C:\x\README`} {
		if class, _, ok := classOf(path); ok {
			t.Errorf("%s would be opened by the class %q", path, class)
		}
	}
	// Programs open as themselves, by the class of programs.
	if class, own, ok := classOf(`C:\x\setup.exe`); !ok || own || class != "exefile" || !opensItself(`C:\x\setup.exe`) {
		t.Errorf("a program: class %q, the shell's own %v, ok %v", class, own, ok)
	}
	if opensItself(`C:\x\notes.txt`) {
		t.Error("a text file is taken for a program")
	}
	// A type with a program has its class; which, the machine says.
	class, _, ok := classOf(`C:\x\notes.txt`)
	t.Logf(".txt is opened by the class %q (%v)", class, ok)
	for _, name := range []string{"Unknown", "undecided", ""} {
		if !undecided(name) {
			t.Errorf("%q is taken for a program's class", name)
		}
	}
}

// processes are the processes running, their names by their ids.
func processes() map[string]string {
	out, _ := exec.Command("tasklist", "/fo", "csv", "/nh").Output()
	m := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Split(line, ","); len(f) > 1 {
			m[strings.Trim(f[1], `"`)] = strings.Trim(f[0], `"`)
		}
	}
	return m
}

// started waits for a process of a name that was not running before, ends
// it, and reports whether there was one; with it, whether OpenWith.exe
// was started, which shows nothing for a program that is not Explorer.
func started(before map[string]string, name string) (ok, asked bool) {
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) && !ok {
		time.Sleep(300 * time.Millisecond)
		for pid, pname := range processes() {
			if _, was := before[pid]; was {
				continue
			}
			switch {
			case strings.EqualFold(pname, "OpenWith.exe"):
				asked = true
				exec.Command("taskkill", "/f", "/pid", pid).Run()
			case strings.Contains(strings.ToLower(pname), strings.ToLower(name)):
				ok = true
				exec.Command("taskkill", "/f", "/pid", pid).Run()
			}
		}
	}
	return ok, asked
}

// TestOpenLive opens real files with the programs the machine has for
// them, and with the one chosen from "Open with": the program starts, and
// the system's own asking does not. It starts programs, so it is asked
// for: MYFILES_OPEN_TEST lists files and the names of the programs that
// are to start, as "D:\a.rar=Bandizip;D:\b.txt=Notepad".
func TestOpenLive(t *testing.T) {
	spec := os.Getenv("MYFILES_OPEN_TEST")
	if spec == "" {
		t.Skip("set MYFILES_OPEN_TEST to files and the programs that open them")
	}
	for _, item := range strings.Split(spec, ";") {
		path, program, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		class, _, _ := classOf(path)
		t.Logf("%s: the class is %q", path, class)
		before := processes()
		err := Open(0, path, "")
		if ok, asked := started(before, program); err != nil || !ok || asked {
			t.Errorf("opening %s: %v; %s started: %v; OpenWith.exe started: %v", path, err, program, ok, asked)
		}
		for _, app := range Apps(path) {
			if !strings.Contains(strings.ToLower(app.Name+app.ID), strings.ToLower(program)) {
				continue
			}
			before := processes()
			err := OpenIn(0, path, app)
			if ok, asked := started(before, program); err != nil || !ok || asked {
				t.Errorf("opening %s with %s: %v; started: %v; OpenWith.exe started: %v", path, app.Name, err, ok, asked)
			}
		}
	}
}

// A file nothing is set to open is told as one to choose a program for,
// and nothing is started for it: not the system's own asking either.
func TestOpenAsksForWhatHasNoProgram(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"README", "data.myfiles-no-such-type"} {
		path := dir + `\` + name
		os.WriteFile(path, []byte("x"), 0o644)
		before := processes()
		if err := Open(0, path, dir); err != ErrChoose {
			t.Errorf("opening %s: %v, want ErrChoose", name, err)
		}
		time.Sleep(500 * time.Millisecond)
		for pid, pname := range processes() {
			if _, was := before[pid]; !was && strings.EqualFold(pname, "OpenWith.exe") {
				t.Errorf("opening %s started OpenWith.exe", name)
				exec.Command("taskkill", "/f", "/pid", pid).Run()
			}
		}
	}
	// A link to an address whose scheme has no program is such a file too.
	link := dir + `\odd.url`
	os.WriteFile(link, []byte("[InternetShortcut]\r\nURL=myfiles-no-such-scheme:whatever\r\n"), 0o644)
	if address, ok := linkedAddress(link); !ok || address != "myfiles-no-such-scheme:whatever" {
		t.Errorf("the link's address reads as %q, %v", address, ok)
	}
	if err := Open(0, link, dir); err != ErrChoose {
		t.Errorf("opening a link to a scheme without a program: %v, want ErrChoose", err)
	}
}

// TestOpenLinksLive opens shortcuts: one to a document opens the document
// with its program, one to a program starts the program, and a link to an
// address opens in the program of its scheme. MYFILES_OPEN_LINKS names a
// document and its program, as "D:\a.rar=Bandizip"; MYFILES_OPEN_URL an
// address and the program for it, as "https://example.com/=chrome".
func TestOpenLinksLive(t *testing.T) {
	spec := os.Getenv("MYFILES_OPEN_LINKS")
	if spec == "" {
		t.Skip("set MYFILES_OPEN_LINKS to a document and the program that opens it")
	}
	doc, program, _ := strings.Cut(spec, "=")
	dir := t.TempDir()
	link := func(name, target string) string {
		path := dir + `\` + name
		script := `$s = (New-Object -ComObject WScript.Shell).CreateShortcut('` + path + `'); $s.TargetPath = '` + target + `'; $s.Save()`
		if out, err := exec.Command("powershell", "-NoProfile", "-Command", script).CombinedOutput(); err != nil {
			t.Fatalf("no shortcut made: %v %s", err, out)
		}
		return path
	}
	toDoc := link("to-document.lnk", doc)
	if target, ok := linkedDocument(toDoc); !ok || !strings.EqualFold(target, doc) {
		t.Fatalf("the shortcut's document reads as %q, %v", target, ok)
	}
	before := processes()
	err := Open(0, toDoc, dir)
	if ok, asked := started(before, program); err != nil || !ok || asked {
		t.Errorf("a shortcut to %s: %v; %s started: %v; OpenWith.exe started: %v", doc, err, program, ok, asked)
	}
	toProgram := link("to-program.lnk", `C:\Windows\System32\charmap.exe`)
	if _, ok := linkedDocument(toProgram); ok {
		t.Error("a shortcut to a program is taken for one to a document")
	}
	before = processes()
	err = Open(0, toProgram, dir)
	if ok, asked := started(before, "charmap"); err != nil || !ok || asked {
		t.Errorf("a shortcut to a program: %v; started: %v; OpenWith.exe started: %v", err, ok, asked)
	}
	// A program picked that has no class for the file: as itself, with it.
	if exe, ok := assocString(strings.ToLower(doc[strings.LastIndex(doc, "."):]), assocExecutable); ok {
		before = processes()
		err := onSTA(func() error { return openWithProgram(0, doc, exe) })
		if ok, asked := started(before, program); err != nil || !ok || asked {
			t.Errorf("%s started with the file: %v; started: %v; OpenWith.exe started: %v", exe, err, ok, asked)
		}
	}
	if spec := os.Getenv("MYFILES_OPEN_URL"); spec != "" {
		address, browser, _ := strings.Cut(spec, "=")
		path := dir + `\site.url`
		os.WriteFile(path, []byte("[{000214A0-0000-0000-C000-000000000046}]\r\nProp3=19,11\r\n[InternetShortcut]\r\nIDList=\r\nURL="+address+"\r\n"), 0o644)
		before := processes()
		err := Open(0, path, dir)
		time.Sleep(2 * time.Second)
		asked := false
		for pid, pname := range processes() {
			if _, was := before[pid]; !was && strings.EqualFold(pname, "OpenWith.exe") {
				asked = true
				exec.Command("taskkill", "/f", "/pid", pid).Run()
			}
		}
		class, _ := assocString("https", assocProgID)
		t.Logf("a link to %s: %v, by the class %q (%s); OpenWith.exe started: %v", address, err, class, browser, asked)
		if err != nil || asked {
			t.Errorf("a link to an address: %v, OpenWith.exe started: %v", err, asked)
		}
	}
}

// Of the system's menu, opening a file and choosing what opens it are
// handed back to the caller: the system's own may end in OpenWith.exe.
func TestOwnActions(t *testing.T) {
	for _, c := range []struct {
		verb  string
		files int
		want  MenuAction
	}{
		{"open", 1, MenuOpen}, {"Open", 3, MenuOpen}, {"openas", 1, MenuOpenWith},
		{"open", 0, MenuDone}, {"properties", 1, MenuDone}, {"", 1, MenuDone}, {"edit", 1, MenuDone},
	} {
		if got := ownAction(c.verb, c.files); got != c.want {
			t.Errorf("%q of %d files is %v, want %v", c.verb, c.files, got, c.want)
		}
	}
}

// Nothing in the package starts the system's dialog of programs, nor
// opens a file by the handler's own Invoke.
func TestNoOpenWith(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			code, _, _ := strings.Cut(line, "//")
			if strings.Contains(code, "handlerInvoke") || strings.Contains(code, `"openas"`) && !strings.Contains(code, "EqualFold") {
				t.Errorf("%s:%d: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

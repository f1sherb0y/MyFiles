package cli

import (
	"reflect"
	"testing"

	"cairn/internal/fsys"
)

func TestParse(t *testing.T) {
	home := fsys.Home()
	downloads, _ := fsys.KnownFolder("Downloads")
	for _, tc := range []struct {
		name string
		args []string
		want Command
	}{
		{"a path", []string{`C:\Windows`}, Command{Targets: []Target{{Path: `C:\Windows`}}}},
		{"several paths", []string{`C:\Windows`, `D:/Code/`}, Command{Targets: []Target{{Path: `C:\Windows`}, {Path: `D:\Code`}}}},
		{"relative", []string{`sub\dir`, `..`, `.`}, Command{Targets: []Target{{Path: `C:\work\here\sub\dir`}, {Path: `C:\work`}, {Path: `C:\work\here`}}}},
		{"select", []string{"--select", `C:\Windows\notepad.exe`}, Command{Targets: []Target{{Path: `C:\Windows`, Select: "notepad.exe"}}}},
		{"select=", []string{`--select=C:\Windows\notepad.exe`, "-n"}, Command{NewWindow: true, Targets: []Target{{Path: `C:\Windows`, Select: "notepad.exe"}}}},
		{"split", []string{"--split", "right", `C:\a`, `C:\b`}, Command{Split: SplitRight, Targets: []Target{{Path: `C:\a`}, {Path: `C:\b`}}}},

		// explorer.exe's own.
		{"explorer select", []string{`/select,C:\Windows\notepad.exe`}, Command{Targets: []Target{{Path: `C:\Windows`, Select: "notepad.exe"}}}},
		{"explorer select, spaced", []string{`/select,`, `C:\Program Files\a.txt`}, Command{Targets: []Target{{Path: `C:\Program Files`, Select: "a.txt"}}}},
		{"explorer select with a comma", []string{`/select,C:\a,b\c.txt`}, Command{Targets: []Target{{Path: `C:\a,b`, Select: "c.txt"}}}},
		{"explorer e root", []string{`/e,/root,C:\Projects`}, Command{Targets: []Target{{Path: `C:\Projects`}}}},
		{"explorer e path", []string{`/e,C:\Projects`}, Command{Targets: []Target{{Path: `C:\Projects`}}}},
		{"explorer e, path", []string{`/e,`, `C:\Projects`}, Command{Targets: []Target{{Path: `C:\Projects`}}}},
		{"explorer n", []string{`/n,C:\Projects`}, Command{NewWindow: true, Targets: []Target{{Path: `C:\Projects`}}}},
		{"explorer n e", []string{`/n,/e,C:\Projects`}, Command{NewWindow: true, Targets: []Target{{Path: `C:\Projects`}}}},
		{"explorer separate", []string{`/separate`, `C:\Projects`}, Command{NewWindow: true, Targets: []Target{{Path: `C:\Projects`}}}},
		{"quoted", []string{`"C:\Program Files"`}, Command{Targets: []Target{{Path: `C:\Program Files`}}}},
		{"file url", []string{`file:///C:/Program%20Files/x`}, Command{Targets: []Target{{Path: `C:\Program Files\x`}}}},
		{"file url of a share", []string{`file://srv/share/x`}, Command{Targets: []Target{{Path: `\\srv\share\x`}}}},
		{"this pc", []string{`::{20D04FE0-3AEA-1069-A2D8-08002B30309D}`}, Command{Targets: []Target{{Path: fsys.Computer}}}},
		{"shell this pc", []string{`shell:MyComputerFolder`}, Command{Targets: []Target{{Path: fsys.Computer}}}},
		{"shell folder", []string{`shell:Downloads`}, Command{Targets: []Target{{Path: downloads}}}},
		{"shell folder sub", []string{`shell:Profile\x`}, Command{Targets: []Target{{Path: home + `\x`}}}},
		{"env", []string{`%USERPROFILE%\x`}, Command{Targets: []Target{{Path: home + `\x`}}}},
		{"recycle bin", []string{`::{645FF040-5081-101B-9F08-00AA002F954E}`}, Command{Delegate: []string{`::{645FF040-5081-101B-9F08-00AA002F954E}`}}},
		{"shell virtual", []string{`shell:RecycleBinFolder`}, Command{Delegate: []string{`shell:RecycleBinFolder`}}},
		{"embedding", []string{`/factory,{75dff2b7-6936-4c06-a8bb-676a7b00b24b}`, `-Embedding`}, Command{}},
		{"unc", []string{`\\srv\share\dir`}, Command{Targets: []Target{{Path: `\\srv\share\dir`}}}},
	} {
		got, err := Parse(tc.args, `C:\work\here`)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		tc.want.BenchRounds = 5
		if !reflect.DeepEqual(*got, tc.want) {
			t.Errorf("%s: %v\n got  %+v\n want %+v", tc.name, tc.args, *got, tc.want)
		}
	}
	if _, err := Parse([]string{"--nope"}, ""); err == nil {
		t.Error("an unknown option passed")
	}
	if _, err := Parse([]string{"--select"}, ""); err == nil {
		t.Error("--select without a value passed")
	}
}

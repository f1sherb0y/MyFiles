package app

import (
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/config"
	"myfiles/internal/index"
	"myfiles/internal/shell"
)

// searchTree is a folder with files at several depths.
func searchTree(t *testing.T) *harness {
	return newHarness(t,
		"report.txt",
		"notes.md",
		"docs/",
		"docs/report-final.pdf",
		"docs/old/report-2019.pdf",
		"docs/old/photo.jpg",
		"src/main.go",
		"src/report/",
		"src/report/index.html",
		"音乐/重庆之夜.mp3",
	)
}

func TestTypingSearchesTheFolder(t *testing.T) {
	h := searchTree(t)
	tab := h.w.current()
	// Typed among the files, with no field asked for.
	h.tt.Type("rep")
	h.settle()
	if !tab.searching() {
		t.Fatal("typing among the files does not search")
	}
	// What the folder holds itself first, then what is deeper; the name
	// that is the query before the names that start with it.
	if got, want := h.found(tab), []string{"report.txt", "src/report", "docs/old/report-2019.pdf", "docs/report-final.pdf"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("found %v, want %v", got, want)
	}
	if tab.filter != "rep" {
		t.Errorf("the field holds %q", tab.filter)
	}
	if tab.title() != "Search: rep" {
		t.Errorf("the tab is called %q", tab.title())
	}
	// More typed goes on in the field.
	h.tt.Type("ort-fi")
	h.settle()
	if got, want := h.found(tab), []string{"docs/report-final.pdf"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("found %v, want %v", got, want)
	}
	// Escape shows the folder again, as it was.
	h.key(0, ui.KeyEscape)
	h.wait("the folder again", func() bool { return !tab.searching() && len(tab.view) == 5 })
	if tab.Path != h.root || tab.filter != "" {
		t.Errorf("after the search the tab shows %q, its field %q", tab.Path, tab.filter)
	}

	// A folder shown as icons shows what is found in it as a table, with
	// the folders beside the names, and is shown as icons again after; and
	// a search of everything is asked for each time: what is typed among
	// the files next searches the folder.
	h.w.setView(tab, viewIcons)
	h.key(ui.Cmd|ui.Shift, ui.KeyF)
	h.tt.Type("rep")
	h.settle()
	if h.found(tab); tab.mode != viewDetails || !tab.find.everywhere {
		t.Errorf("searching everywhere: view %d, everywhere %v", tab.mode, tab.find.everywhere)
	}
	h.key(0, ui.KeyEscape)
	h.wait("the folder again", func() bool { return !tab.searching() && len(tab.view) == 5 })
	if tab.mode != viewIcons || tab.everywhere {
		t.Errorf("after the search: view %d, everywhere %v", tab.mode, tab.everywhere)
	}
	if len(tab.back) != 0 {
		t.Errorf("the search left %d places to go back to", len(tab.back))
	}
}

func TestSearchQueries(t *testing.T) {
	h := searchTree(t)
	tab := h.w.current()
	ask := func(query string) []string {
		t.Helper()
		tab.filter = query
		h.w.setQuery(tab)
		h.settle()
		return h.found(tab)
	}
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"cqzy", []string{"音乐/重庆之夜.mp3"}},
		{"report :.pdf", []string{"docs/old/report-2019.pdf", "docs/report-final.pdf"}},
		{"report :F", []string{"src/report"}},
		{"report !final :@doc", []string{"report.txt", "docs/old/report-2019.pdf"}},
		{`docs\report`, []string{"docs/old/report-2019.pdf", "docs/report-final.pdf"}},
		{"index report", []string{"src/report/index.html"}},
		{"^report.txt$", []string{"report.txt"}},
		{"photo | notes", []string{"notes.md", "docs/old/photo.jpg"}},
		{"zzzz", []string{}},
	} {
		if got := ask(c.query); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: found %v, want %v", c.query, got, c.want)
		}
	}
	// The kinds under the toolbar narrow what is found.
	ask("report")
	h.click("Folders")
	h.settle()
	if got, want := h.found(tab), []string{"src/report"}; !reflect.DeepEqual(got, want) {
		t.Errorf("folders: found %v, want %v", got, want)
	}
	h.click("All")
	h.settle()
	if got := h.found(tab); len(got) != 4 {
		t.Errorf("all kinds: found %v", got)
	}
}

func TestSearchResultsAct(t *testing.T) {
	h := searchTree(t)
	tab := h.w.current()
	tab.filter = "report-final"
	h.w.setQuery(tab)
	h.settle()
	if got := h.found(tab); len(got) != 1 {
		t.Fatalf("found %v", got)
	}
	path := h.path("docs/report-final.pdf")
	tab.choose(path)
	if got := tab.chosenPaths(); !reflect.DeepEqual(got, []string{path}) {
		t.Fatalf("chosen %v", got)
	}
	// Copied as from its folder.
	h.w.copyChosen(tab, false)
	if got, _ := h.desk.data.Files(); !reflect.DeepEqual(got, []string{path}) {
		t.Errorf("on the clipboard: %v", got)
	}
	// Its folder, with it chosen, in place of the search.
	h.w.openLocation(tab, path)
	h.wait("the file's folder", func() bool { return tab.Path == h.path("docs") && !tab.loading })
	if tab.searching() || tab.focusName() != "report-final.pdf" {
		t.Errorf("at %q, searching %v, on %q", tab.Path, tab.searching(), tab.focusName())
	}
	// And back from there to where the search was typed.
	h.w.goBack(tab)
	h.wait("the folder searched from", func() bool { return tab.Path == h.root && !tab.loading })

	// A folder found opens in the tab; a file found deleted goes from
	// what shows.
	tab.filter = "report :F"
	h.w.setQuery(tab)
	h.settle()
	h.found(tab)
	tab.choose(h.path("src/report"))
	h.w.openChosen(tab)
	h.wait("the folder found", func() bool { return tab.Path == h.path("src/report") && !tab.loading })
	h.w.navigate(tab, h.root, navPush, "")
	h.wait("the folder searched from", func() bool { return tab.Path == h.root && !tab.loading })

	tab.filter = "photo"
	h.w.setQuery(tab)
	h.settle()
	if got := h.found(tab); len(got) != 1 {
		t.Fatalf("found %v", got)
	}
	if err := os.Remove(h.path("docs/old/photo.jpg")); err != nil {
		t.Fatal(err)
	}
	// Nothing indexes the tests' folder: read again, it is gone.
	h.a.finder.SetRules(index.DefaultRules())
	h.w.search(tab, true)
	h.wait("the deleted file gone", func() bool { return tab.find.stop == nil && len(tab.view) == 0 })
}

func TestSearchEverywhereToggle(t *testing.T) {
	h := searchTree(t)
	tab := h.w.current()
	h.key(ui.Cmd|ui.Shift, ui.KeyF)
	if !tab.everywhere {
		t.Fatal("Ctrl+Shift+F does not search everywhere")
	}
	h.key(ui.Cmd, ui.KeyF)
	if tab.everywhere {
		t.Fatal("Ctrl+F does not search the folder")
	}
	// Lines pasted are as many names, any of which will do: the field
	// takes them as one line, and the clipboard tells they were several.
	h.desk.data = transfer.TextData("report.txt\r\nnotes.md\r\n")
	h.tt.Type("report.txt\r\nnotes.md\r\n")
	h.settle()
	if tab.filter != "report.txt | notes.md" {
		t.Errorf("lines pasted became %q", tab.filter)
	}
	if got, want := h.found(tab), []string{"notes.md", "report.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("found %v, want %v", got, want)
	}
}

func TestRulesAsText(t *testing.T) {
	lines := []string{"!node_modules", `-%SystemRoot%`, "+D:\\Work", "++D:\\Work\\Now", `\!odd`, "plain"}
	rules := parseRules(lines)
	want := []index.Rule{
		{Pattern: "node_modules", Effect: index.Exclude},
		{Pattern: "%SystemRoot%", Effect: index.Low},
		{Pattern: `D:\Work`, Effect: index.High},
		{Pattern: `D:\Work\Now`, Effect: index.High, Top: true},
		{Pattern: "!odd", Effect: index.Normal},
		{Pattern: "plain", Effect: index.Normal},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("read %+v", rules)
	}
	if got := formatRules(rules); !reflect.DeepEqual(got, lines) {
		t.Errorf("written %q", got)
	}
}

// TestSearchPicture writes pictures of a search, for a look at them, into
// the folder MYFILES_PICTURES names.
func TestSearchPicture(t *testing.T) {
	dir := os.Getenv("MYFILES_PICTURES")
	if dir == "" {
		t.Skip("set MYFILES_PICTURES to a folder to write pictures to")
	}
	h := searchTree(t)
	tab := h.w.current()
	shot := func(name string) {
		h.settle()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, h.tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	shot("0-folder")
	h.tt.Type("rep")
	h.found(tab)
	shot("1-typed")
	tab.filter = "index report"
	h.w.setQuery(tab)
	h.found(tab)
	shot("2-by-folder")
	tab.filter = "cq"
	h.w.setQuery(tab)
	h.found(tab)
	h.w.setView(tab, viewTiles)
	shot("3-pinyin-tiles")
	h.w.openSettings()
	shot("4-settings")
}

// TestSearchFollowsTheDrive searches a folder of a drive that is indexed
// and followed, and sees what is found change as the folder's files do.
// It reads the whole drive: MYFILES_INDEX_LIVE_TEST=1 asks for it.
func TestSearchFollowsTheDrive(t *testing.T) {
	if os.Getenv("MYFILES_INDEX_LIVE_TEST") == "" {
		t.Skip("set MYFILES_INDEX_LIVE_TEST=1 to read and follow a real drive")
	}
	h := newHarness(t, "sub/zebra-unicorn-one.txt")
	tab := h.w.current()
	var off []string
	for c := 'A'; c <= 'Z'; c++ {
		if root := string(c) + `:\`; !strings.EqualFold(root, filepath.VolumeName(h.root)+`\`) {
			off = append(off, root)
		}
	}
	h.a.finder = index.NewManager(index.Config{Dir: t.TempDir(), Off: off, Changed: h.a.indexChanged})
	h.a.finder.Start()
	t.Cleanup(h.a.finder.Close)
	tab.filter = "zebra-unicorn"
	h.w.setQuery(tab)
	shows := func(want ...string) {
		t.Helper()
		h.wait(strings.Join(want, ", "), func() bool {
			return tab.find != nil && tab.find.stop == nil && !tab.find.slow && reflect.DeepEqual(h.rows(tab), want)
		})
	}
	shows("sub/zebra-unicorn-one.txt")
	// The drive's own index answers now, not one of the folder alone.
	// Which takes as long as the drive is large: a system drive read
	// folder by folder, half a minute.
	read := func() bool {
		for _, d := range h.a.finder.Drives() {
			if d.State == index.StateReady && d.Live {
				return true
			}
		}
		return false
	}
	for deadline := time.Now().Add(3 * time.Minute); !read(); {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the drive to be read")
		}
		h.settle()
		time.Sleep(50 * time.Millisecond)
	}
	if err := os.WriteFile(h.path("zebra-unicorn-two.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	shows("zebra-unicorn-two.txt", "sub/zebra-unicorn-one.txt")
	if err := os.Rename(h.path("sub"), h.path("moved")); err != nil {
		t.Fatal(err)
	}
	shows("zebra-unicorn-two.txt", "moved/zebra-unicorn-one.txt")
	if err := os.Remove(h.path("zebra-unicorn-two.txt")); err != nil {
		t.Fatal(err)
	}
	shows("moved/zebra-unicorn-one.txt")
}

// The two buttons beside the field start the two searches: of the folder,
// and of the whole computer.
func TestSearchButtons(t *testing.T) {
	h := searchTree(t)
	tab := h.w.current()
	h.click(locales.SearchIn(placeTitle(tab.place())))
	h.tt.Type("report")
	h.settle()
	if tab.everywhere || !tab.searching() || len(h.found(tab)) == 0 {
		t.Fatalf("the search button: everywhere %v, searching %v, found %v", tab.everywhere, tab.searching(), h.found(tab))
	}
	for _, name := range h.found(tab) {
		if !strings.Contains(strings.ToLower(name), "report") {
			t.Errorf("found %q for report", name)
		}
	}
	h.click(locales.SearchScopeEverywhere())
	if !tab.everywhere || tab.find == nil || !tab.find.everywhere {
		t.Fatal("the button of the whole computer does not search everywhere")
	}
	h.click(locales.SearchIn(placeTitle(tab.place())))
	if tab.everywhere || tab.find.everywhere {
		t.Fatal("the search button does not come back to the folder")
	}
}

// What a search found is renamed in place, where it is, and shows under
// its new name.
func TestSearchRename(t *testing.T) {
	h := searchTree(t)
	tab := h.w.current()
	tab.filter = "notes"
	h.w.setQuery(tab)
	h.settle()
	path := h.path("notes.md")
	if got := h.found(tab); len(got) != 1 || got[0] != "notes.md" {
		t.Fatalf("found %v", got)
	}
	tab.choose(path)
	// As the menu's Rename asks, and F2.
	tab.rename = path
	h.settle()
	h.settle()
	if tab.find.renaming != path {
		t.Fatalf("renaming %q", tab.find.renaming)
	}
	// The name before the extension is selected: what is typed replaces it.
	h.tt.Type("notes-2")
	h.key(0, ui.KeyEnter)
	h.wait("the file under its new name", func() bool {
		_, err := os.Stat(h.path("notes-2.md"))
		return err == nil
	})
	if _, err := os.Stat(path); err == nil {
		t.Error("the file is still there under its old name")
	}
	h.wait("the result under its new name", func() bool {
		got := h.found(tab)
		return len(got) == 1 && got[0] == "notes-2.md" && tab.find.renaming == ""
	})

	// Escape leaves the name as it is, and the row shows as a result again.
	tab.rename = h.path("notes-2.md")
	h.settle()
	h.settle()
	h.tt.Type("other")
	h.key(0, ui.KeyEscape)
	h.wait("the rename to end", func() bool { return tab.find.renaming == "" })
	if _, err := os.Stat(h.path("notes-2.md")); err != nil {
		t.Error("Escape renamed the file")
	}
}

// A file opened three times from the results of a query is found by the
// query from then on, whatever it is called: last, after what matches.
func TestSearchLearns(t *testing.T) {
	for query, want := range map[string]string{
		"  Sou  Suo ": "sousuo", "sousuo jieguo": "sousuojieguo", "a report": "a report",
		"Q3 plan": "q3 plan", "报告 2024": "报告 2024", "single": "single",
	} {
		if got := learnedKey(query); got != want {
			t.Errorf("the key of %q is %q, want %q", query, got, want)
		}
	}
	h := searchTree(t)
	tab := h.w.current()
	h.a.learned = map[string][]config.Learned{}
	budget := h.path("docs/report-final.pdf")
	search := func(text string) []string {
		tab.filter = text
		h.w.setQuery(tab)
		h.settle()
		return h.found(tab)
	}
	if got := search("money"); len(got) != 0 {
		t.Fatalf("money finds %v", got)
	}
	// Opened for the query twice: not yet.
	h.a.learn("money", budget)
	h.a.learn(" Money ", budget)
	tab.filter = ""
	h.w.setQuery(tab)
	if got := search("money"); len(got) != 0 {
		t.Fatalf("after two openings money finds %v", got)
	}
	h.a.learn("money", budget)
	tab.filter = ""
	h.w.setQuery(tab)
	if got := search("money"); len(got) != 1 || got[0] != "docs/report-final.pdf" || tab.find.learned != budget {
		t.Fatalf("after three openings money finds %v, learned %q", got, tab.find.learned)
	}
	// Opening it from there says nothing new of the query.
	h.a.noteOpened(tab.find, budget)
	if n := h.a.learned["money"][0].Opens; n != 3 {
		t.Errorf("opened from being recalled, it counts %d", n)
	}
	// With what matches, it comes last, and once.
	h.a.learn("report", h.path("notes.md"))
	h.a.learn("report", h.path("notes.md"))
	h.a.learn("report", h.path("notes.md"))
	got := search("report")
	if len(got) < 2 || got[len(got)-1] != "notes.md" {
		t.Errorf("report finds %v, want notes.md last", got)
	}
	// Not of another folder's search, nor when it is gone.
	os.Remove(h.path("notes.md"))
	h.a.finder.Forget(h.path("notes.md"))
	tab.filter = ""
	h.w.setQuery(tab)
	for _, name := range search("report") {
		if name == "notes.md" {
			t.Error("a file that is gone is recalled")
		}
	}
}

// The kinds of files the user names are asked for by their keywords, and
// those that ask for one have a button among the kinds.
func TestCustomKinds(t *testing.T) {
	lines := []string{"+code (Code) = go rs *.min.js @web", "web = html css", "#old = bak", "bad line", "+pics=@img", " = nothing", "sp ace = x"}
	got := parseFilters(lines)
	want := []config.CustomFilter{
		{Keyword: "code", Label: "Code", Rule: "go rs *.min.js @web", Row: true},
		{Keyword: "web", Rule: "html css"},
		{Keyword: "old", Rule: "bak", Off: true},
		{Keyword: "pics", Rule: "@img", Row: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("read as %+v", got)
	}
	if again := parseFilters(formatFilters(got)); !reflect.DeepEqual(again, want) {
		t.Errorf("written and read again: %+v", again)
	}

	h := newHarness(t, "main.go", "site/index.html", "site/app.min.js", "site/logo.png", "notes.txt", "old.bak")
	tab := h.w.current()
	h.a.set.Search.Custom = got
	search := func(text string) []string {
		tab.filter = text
		h.w.setQuery(tab)
		h.settle()
		names := h.found(tab)
		sort.Strings(names)
		return names
	}
	if got, want := search(":@code"), []string{"main.go", "site/app.min.js", "site/index.html"}; !reflect.DeepEqual(got, want) {
		t.Errorf(":@code finds %v, want %v", got, want)
	}
	if got, want := search(":@web|pics"), []string{"site/index.html", "site/logo.png"}; !reflect.DeepEqual(got, want) {
		t.Errorf(":@web|pics finds %v, want %v", got, want)
	}
	// One switched off is no keyword.
	if got := search(":@old"); len(got) != 0 {
		t.Errorf(":@old finds %v", got)
	}
	// The buttons: the built-in kinds, then the user's that ask for one.
	list := h.a.kindList()
	if n := len(list); n != len(kinds)+2 || list[n-2].title() != "Code" || list[n-1].title() != "pics" {
		t.Fatalf("%d kinds", len(list))
	}
	if got, want := search("in"), []string{"main.go", "site/app.min.js", "site/index.html"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("in finds %v, want %v", got, want)
	}
	h.a.set.Search.Custom[0].Rule = "go *.min.js"
	h.click("Code")
	if got, want := h.found(tab), []string{"main.go", "site/app.min.js"}; !reflect.DeepEqual(sorted(got), want) {
		t.Errorf("the button of code leaves %v, want %v", got, want)
	}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// A search that starts with the keyword of contents looks in the files:
// those that hold every word are found, with the text around the first.
func TestSearchInsideFiles(t *testing.T) {
	h := newHarness(t, "notes.txt", "docs/plan.md", "docs/other.md", "pics/photo.png", "budget.csv")
	os.WriteFile(h.path("notes.txt"), []byte("Remember the aardvark budget for March."), 0o644)
	os.WriteFile(h.path("docs/plan.md"), []byte("# Plan\n\nThe AARDVARK enclosure opens in May.\n"), 0o644)
	os.WriteFile(h.path("docs/other.md"), []byte("nothing to see"), 0o644)
	os.WriteFile(h.path("pics/photo.png"), []byte("aardvark, in a file of a kind that is not read"), 0o644)
	os.WriteFile(h.path("budget.csv"), []byte("item,cost\naardvark food,120\n"), 0o644)
	tab := h.w.current()
	search := func(text string) []string {
		tab.filter = text
		h.w.setQuery(tab)
		h.wait("the files read: "+text, func() bool { return tab.find != nil && tab.find.stop == nil })
		return sorted(h.found(tab))
	}
	if got, want := search("cs aardvark"), []string{"budget.csv", "docs/plan.md", "notes.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cs aardvark finds %v, want %v", got, want)
	}
	if !tab.find.content || !strings.Contains(tab.find.snippets[h.path("docs/plan.md")], "The AARDVARK enclosure opens in May.") {
		t.Errorf("the snippets are %v", tab.find.snippets)
	}
	// Every word, and the query's tokens narrow the files that are read.
	if got, want := search("cs aardvark budget"), []string{"notes.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("two words find %v, want %v", got, want)
	}
	if got, want := search("cs aardvark :.md"), []string{"docs/plan.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("with :.md the search finds %v, want %v", got, want)
	}
	// The keyword alone says what it is for, and a name that starts with
	// it is a name.
	if got := search("cs"); len(got) != 0 || tab.find.note == "" {
		t.Errorf("cs alone finds %v, says %q", got, tab.find.note)
	}
	if got := search("csv"); len(got) != 1 || got[0] != "budget.csv" || tab.find.content {
		t.Errorf("csv finds %v, as contents %v", got, tab.find.content)
	}
	// Switched off, the keyword is a word like another.
	h.a.set.Search.ContentOff = true
	if got := search("cs aardvark"); len(got) != 0 || tab.find.content {
		t.Errorf("switched off, cs aardvark finds %v", got)
	}
}

// A search of the whole computer finds programs and the system's
// settings beside the files, a program before a file that matches as
// surely; a search of a folder finds files only.
func TestSearchFindsPrograms(t *testing.T) {
	h := newHarness(t, "paint notes.txt", "sub/paintbrush.txt", "other.txt")
	tab := h.w.current()
	h.a.set.Search.NoPrograms = false
	h.a.programs = []shell.Program{
		{Name: "Paint", Path: `::{apps}\Paint`},
		{Name: "Paint settings", Path: `::{settings}\Paint`, Setting: true},
		{Name: "Calculator", Path: `::{apps}\Calc`},
	}
	h.a.programsAt = time.Now()
	names := func() []string {
		var out []string
		for i := range tab.find.found {
			out = append(out, tab.find.found[i].Name)
		}
		return out
	}
	// In the folder: its files.
	tab.filter = "paint"
	h.w.setQuery(tab)
	h.settle()
	if got := sorted(names()); !reflect.DeepEqual(got, []string{"paint notes.txt", "paintbrush.txt"}) {
		t.Fatalf("in the folder paint finds %v", got)
	}
	// The whole computer, which here has no drive's index: the programs.
	h.w.setEverywhere(tab, true)
	h.wait("the search of the whole computer", func() bool { return tab.find != nil && tab.find.stop == nil && tab.find.everywhere })
	got := names()
	if len(got) < 2 || got[0] != "Paint" {
		t.Fatalf("everywhere paint finds %v, want the program first", got)
	}
	if i, j := slices.Index(got, "Paint"), slices.Index(got, "Paint settings"); j < 0 || j < i {
		t.Errorf("everywhere paint finds %v, want the setting after the program", got)
	}
	if slices.Contains(got, "Calculator") {
		t.Errorf("paint finds %v", got)
	}
	// Chosen, a program is no file to copy or delete; and narrowed to
	// documents, a search finds no programs.
	tab.choose(`::{apps}\Paint`)
	if paths := tab.chosenPaths(); len(paths) != 0 {
		t.Errorf("a program chosen gives the paths %v", paths)
	}
	tab.filter = "paint :@doc"
	h.w.setQuery(tab)
	h.settle()
	for _, n := range names() {
		if n == "Paint" || n == "Paint settings" {
			t.Errorf("among documents: %v", names())
		}
	}
	tab.filter = "paint :@app"
	h.w.setQuery(tab)
	h.settle()
	if got := names(); len(got) == 0 || got[0] != "Paint" {
		t.Errorf("among programs paint finds %v", got)
	}
}

// A tab made beside one that shows what a search found is at the folder
// the search is of, by the + of the tabs and by the tab's own menu.
func TestNewTabBesideSearch(t *testing.T) {
	h := searchTree(t)
	tab := h.w.current()
	tab.filter = "report"
	h.w.setQuery(tab)
	h.settle()
	if !tab.searching() || tab.Path != searchPath {
		t.Fatalf("not searching: at %q", tab.Path)
	}
	h.click(locales.TabNewTip())
	fresh := h.w.current()
	h.wait("the new tab's folder", func() bool { return !fresh.loading })
	if fresh == tab || fresh.Path != h.root || fresh.searching() || fresh.err != nil {
		t.Fatalf("the new tab is at %q, searching %v, error %v; want %q", fresh.Path, fresh.searching(), fresh.err, h.root)
	}
	if len(fresh.view) == 0 {
		t.Error("the new tab shows nothing")
	}
	// The tab it was made beside still shows its search.
	if !tab.searching() || len(h.found(tab)) == 0 {
		t.Error("the search is gone from its tab")
	}
}

// The list of files takes what is typed through the system's input
// method, as the search field does: the method is there before the first
// key, to be switched to another language, and what it composes among the
// files shows in the field and is searched for once it is given.
func TestTypingThroughInputMethod(t *testing.T) {
	h := newHarness(t, "报告.docx", "年度报告.txt", "notes.md", "sub/报告-旧.docx")
	tab := h.w.current()
	h.settle()
	if !tab.listFocus {
		t.Fatal("the list of files does not have the keyboard")
	}
	// An input method would compose here, and its candidates have a place.
	if at, on := h.tt.TextCaret(); !on || at.H == 0 {
		t.Fatalf("no input method for the list of files: %v at %+v", on, at)
	}
	// Composing: shown, not yet searched for, and the keyboard stays.
	h.tt.Compose("bao", 3)
	h.settle()
	h.tt.Compose("bao'gao", 7)
	h.settle()
	if tab.composing != "bao'gao" || tab.filter != "" || tab.searching() || !tab.listFocus {
		t.Fatalf("composing %q, the field %q, searching %v, the list has the keyboard %v", tab.composing, tab.filter, tab.searching(), tab.listFocus)
	}
	// What is composed shows in the search field: for a look at it,
	// MYFILES_PICTURES names a folder to write the picture to.
	if dir := os.Getenv("MYFILES_PICTURES"); dir != "" {
		if f, err := os.Create(filepath.Join(dir, "composing.png")); err == nil {
			png.Encode(f, h.tt.Image())
			f.Close()
		}
	}
	// Given: searched for, and the field has the keyboard for what follows.
	h.tt.Type("报告")
	h.settle()
	if tab.composing != "" || tab.filter != "报告" || !tab.searching() {
		t.Fatalf("after the input method gave its text: composing %q, the field %q, searching %v", tab.composing, tab.filter, tab.searching())
	}
	if got, want := sorted(h.found(tab)), []string{"sub/报告-旧.docx", "年度报告.txt", "报告.docx"}; !reflect.DeepEqual(got, want) {
		t.Errorf("found %v, want %v", got, want)
	}
	if tab.listFocus {
		t.Error("the keyboard did not go to the search field")
	}
	// A composition given up leaves nothing behind.
	h.key(0, ui.KeyEscape)
	h.settle()
	if tab.searching() || !tab.listFocus {
		t.Fatalf("Escape: searching %v, the list has the keyboard %v", tab.searching(), tab.listFocus)
	}
	h.tt.Compose("nian", 4)
	h.settle()
	h.tt.Compose("", 0)
	h.settle()
	if tab.composing != "" || tab.filter != "" || tab.searching() {
		t.Errorf("after a composition given up: composing %q, the field %q", tab.composing, tab.filter)
	}
	// The keys of the list are keys still: a file chosen is renamed by F2,
	// and while its name is typed nothing is searched for.
	tab.choose("notes.md")
	h.settle()
	h.key(0, ui.KeyF2)
	h.settle()
	if tab.listFocus {
		t.Fatal("F2 among the files does not start renaming: the list has the keyboard still")
	}
	h.tt.Type("x")
	h.settle()
	if tab.searching() {
		t.Error("a name being typed is searched for")
	}
}

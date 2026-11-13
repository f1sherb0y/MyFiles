package index

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"myfiles/internal/search"
)

// tree makes a view of the files of paths under D:\, a path ending with a
// separator a folder; a name starting with "." is hidden when hide is set.
type tree struct {
	scan *Scan
	ids  map[string]uint64
	next uint64
}

const treeRoot = 5

func newTree(paths ...string) *tree {
	t := &tree{scan: NewScan(nil), ids: map[string]uint64{"": treeRoot}, next: 100}
	for _, p := range paths {
		t.add(p, 0)
	}
	return t
}

func (t *tree) add(path string, flags uint16) uint64 {
	dir := strings.HasSuffix(path, `\`)
	parts := strings.Split(strings.Trim(path, `\`), `\`)
	parent := uint64(treeRoot)
	at := ""
	var id uint64
	for i, part := range parts {
		at += `\` + strings.ToLower(part)
		last := i == len(parts)-1
		known, ok := t.ids[at]
		if !ok {
			t.next++
			known = t.next
			e := Entry{ID: known, Parent: parent, Size: uint64(len(part)) * 100, Modified: uint32(known)}
			if !last || dir {
				e.Flags = FlagDir
				t.ids[at] = known
			}
			if last {
				e.Flags |= flags
			}
			t.scan.AddString(e, part)
		}
		parent, id = known, known
	}
	return id
}

func (t *tree) view(rules []Rule) *View {
	ix := Build(`D:\`, treeRoot, []*Scan{t.scan}, true)
	return NewView(ix, NewRules(rules, nil))
}

func find(t *testing.T, v *View, query, scope string, mod ...func(*Request)) []string {
	t.Helper()
	req := &Request{Query: search.ParseQuery(query, search.DefaultOptions(), ':', nil), Scope: scope, Limit: 1000}
	for _, m := range mod {
		m(req)
	}
	res, total := v.Search(context.Background(), req)
	// What a view found is not to be changed: a copy is sorted.
	res = slices.Clone(res)
	if total < len(res) {
		t.Errorf("%q: %d results of %d counted", query, len(res), total)
	}
	sort.SliceStable(res, func(i, j int) bool { return Less(&res[i], &res[j]) })
	out := make([]string, len(res))
	for i := range res {
		out[i] = res[i].Path
	}
	return out
}

func want(t *testing.T, got []string, paths ...string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(paths) {
		t.Errorf("found\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(paths, "\n  "))
	}
}

func sample() *tree {
	return newTree(
		`work\report.docx`,
		`work\2024\report-final.docx`,
		`work\2024\notes.txt`,
		`work\2024\deep\old report.txt`,
		`work\archive\`,
		`src\core\main.go`,
		`src\core\main_test.go`,
		`src\ui\main.css`,
		`src\node_modules\pkg\main.js`,
		`dcj\d01j.txt`,
		`music\重庆之夜.mp3`,
		`Windows\System32\report.dll`,
	)
}

func TestSearchNames(t *testing.T) {
	v := sample().view(nil)
	// A name that is the query, the more of it the better; then a name
	// that starts with it; then one that holds it as a word.
	want(t, find(t, v, "report", ""),
		`D:\Windows\System32\report.dll`,
		`D:\work\report.docx`,
		`D:\work\2024\report-final.docx`,
		`D:\work\2024\deep\old report.txt`,
	)
	// In a folder: what it holds itself first, then what is deeper.
	want(t, find(t, v, "report", `D:\work`),
		`D:\work\report.docx`,
		`D:\work\2024\report-final.docx`,
		`D:\work\2024\deep\old report.txt`,
	)
	want(t, find(t, v, "report", `D:\work\2024`),
		`D:\work\2024\report-final.docx`,
		`D:\work\2024\deep\old report.txt`,
	)
	want(t, find(t, v, "cqzy", ""), `D:\music\重庆之夜.mp3`)
	want(t, find(t, v, "d: 'main.go", ""), `D:\src\core\main.go`)
	want(t, find(t, v, "c: main.go", ""))
	want(t, find(t, v, "nothing-like-this", ""))
	if got := find(t, v, "report", `D:\nowhere`); len(got) != 0 {
		t.Errorf("a folder that is not there: %v", got)
	}
}

func TestSearchTokens(t *testing.T) {
	v := sample().view(nil)
	want(t, find(t, v, "main :.go", ""), `D:\src\core\main.go`, `D:\src\core\main_test.go`)
	want(t, find(t, v, "work :F", ""), `D:\work`)
	want(t, find(t, v, "a :F", `D:\work`), `D:\work\archive`)
	want(t, find(t, v, "report :@doc", ""), `D:\work\report.docx`, `D:\work\2024\report-final.docx`, `D:\work\2024\deep\old report.txt`)
	want(t, find(t, v, "report ::2024", ""), `D:\work\2024\report-final.docx`, `D:\work\2024\deep\old report.txt`)
	// The largest first: sizes follow the length of names here.
	got := find(t, v, "main :S-", "")
	if len(got) == 0 || got[0] != `D:\src\core\main_test.go` {
		t.Errorf("by size: %v", got)
	}
	want(t, find(t, v, "main :nonsense", ""))
}

func TestSearchByFolder(t *testing.T) {
	v := sample().view(nil)
	want(t, find(t, v, "d01j dcj", ""), `D:\dcj\d01j.txt`)
	// What matches by its own name comes first.
	want(t, find(t, v, "main core", ""), `D:\src\core\main.go`, `D:\src\core\main_test.go`)
	want(t, find(t, v, "notes work", ""), `D:\work\2024\notes.txt`)
}

func TestSearchPaths(t *testing.T) {
	v := sample().view(nil)
	want(t, find(t, v, `core\main`, ""), `D:\src\core\main.go`, `D:\src\core\main_test.go`)
	want(t, find(t, v, `src\main`, ""),
		`D:\src\core\main.go`, `D:\src\node_modules\pkg\main.js`, `D:\src\ui\main.css`, `D:\src\core\main_test.go`)
	want(t, find(t, v, `src\core\main`, ""), `D:\src\core\main.go`, `D:\src\core\main_test.go`)
	want(t, find(t, v, `core\src\main`, ""))
	want(t, find(t, v, `D:\work\`, ""), `D:\work\report.docx`, `D:\work\2024`, `D:\work\archive`)
	want(t, find(t, v, `d:\work\rep`, ""), `D:\work\report.docx`)
	want(t, find(t, v, `c:\work\`, ""))
}

func TestRules(t *testing.T) {
	v := sample().view(DefaultRules())
	want(t, find(t, v, "main.js", ""))
	want(t, find(t, v, "*main.js", ""), `D:\src\node_modules\pkg\main.js`)
	// In the folder left out, its files show.
	want(t, find(t, v, "main.js", `D:\src\node_modules`), `D:\src\node_modules\pkg\main.js`)

	v = sample().view([]Rule{{Pattern: `D:\Windows`, Effect: Low}, {Pattern: `D:\work\2024\deep`, Effect: High, Top: true}})
	want(t, find(t, v, "report", ""),
		`D:\work\2024\deep\old report.txt`,
		`D:\work\report.docx`,
		`D:\work\2024\report-final.docx`,
		`D:\Windows\System32\report.dll`,
	)
	v = sample().view([]Rule{{Pattern: `work\2024`, Effect: Exclude}, {Pattern: "*.dll", Effect: Exclude}})
	want(t, find(t, v, "report", ""), `D:\work\report.docx`)
}

func TestHidden(t *testing.T) {
	tr := sample()
	tr.add(`work\.git\`, FlagHidden)
	tr.add(`work\.git\report-index`, 0)
	v := tr.view(nil)
	if got := find(t, v, "report-index", ""); len(got) != 0 {
		t.Errorf("a hidden folder's file shows: %v", got)
	}
	show := func(r *Request) { r.ShowHidden = true }
	want(t, find(t, v, "report-index", "", show), `D:\work\.git\report-index`)
	want(t, find(t, v, "report-index", `D:\work\.git`), `D:\work\.git\report-index`)
}

func TestBoost(t *testing.T) {
	v := sample().view(nil)
	fav := func(r *Request) { r.Favorites = []string{`D:\work\2024\report-final.docx`} }
	if got := find(t, v, "report", "", fav); got[0] != `D:\work\2024\report-final.docx` {
		t.Errorf("a favorite does not come first: %v", got)
	}
	// A file opened often comes before those that match as surely, and
	// after those that are named as the query.
	hist := func(r *Request) { r.History = map[string]int{`D:\work\2024\deep\old report.txt`: 600} }
	if got := find(t, v, "report", "", hist); got[2] != `D:\work\2024\deep\old report.txt` {
		t.Errorf("a file opened often: %v", got)
	}
}

func TestDelta(t *testing.T) {
	tr := sample()
	v := tr.view(DefaultRules())
	rules := NewRules(DefaultRules(), nil)
	id := func(path string) uint64 { return tr.ids[strings.ToLower(path)] }
	fileID := func(path string) uint64 {
		ref, ok := v.Resolve(path)
		if !ok {
			t.Fatalf("%s is not in the index", path)
		}
		return v.ix.id[ref]
	}

	// A file made.
	if !v.Apply(Change{Entry{ID: 900, Parent: id(`\work`), Size: 5}, "report-new.txt"}, rules) {
		t.Fatal("a file made in a known folder is not taken")
	}
	want(t, find(t, v, "report-new", ""), `D:\work\report-new.txt`)
	want(t, find(t, v, "report-new", `D:\work`), `D:\work\report-new.txt`)
	want(t, find(t, v, "report-new", `D:\src`))
	if ref, ok := v.Resolve(`D:\work\report-new.txt`); !ok || v.Path(ref) != `D:\work\report-new.txt` {
		t.Error("the file made is not found by its path")
	}

	// A file renamed.
	v.Apply(Change{Entry{ID: fileID(`D:\work\2024\notes.txt`), Parent: id(`\work\2024`)}, "minutes.txt"}, rules)
	want(t, find(t, v, "notes", ""))
	want(t, find(t, v, "minutes", ""), `D:\work\2024\minutes.txt`)

	// A folder renamed: what it holds follows.
	v.Apply(Change{Entry{ID: id(`\work\2024`), Parent: id(`\work`), Flags: FlagDir}, "2025"}, rules)
	want(t, find(t, v, "report-final", ""), `D:\work\2025\report-final.docx`)
	want(t, find(t, v, "2025 :F", ""), `D:\work\2025`)
	want(t, find(t, v, "2024 :F", ""))
	want(t, find(t, v, "minutes", `D:\work\2025`), `D:\work\2025\minutes.txt`)
	want(t, find(t, v, "report ::2025", ""), `D:\work\2025\report-final.docx`, `D:\work\2025\deep\old report.txt`)

	// A folder moved: it is searched where it is now.
	v.Apply(Change{Entry{ID: id(`\work\2024`), Parent: id(`\src`), Flags: FlagDir}, "2025"}, rules)
	want(t, find(t, v, "report-final", ""), `D:\src\2025\report-final.docx`)
	want(t, find(t, v, "report-final", `D:\work`))
	want(t, find(t, v, "report-final", `D:\src`), `D:\src\2025\report-final.docx`)
	want(t, find(t, v, "report-final", `D:\src\2025`), `D:\src\2025\report-final.docx`)
	want(t, find(t, v, "report", `D:\work`), `D:\work\report.docx`, `D:\work\report-new.txt`)

	// A file made in a folder made since.
	v.Apply(Change{Entry{ID: 901, Parent: id(`\work`), Flags: FlagDir}, "fresh"}, rules)
	v.Apply(Change{Entry{ID: 902, Parent: 901}, "fresh-report.txt"}, rules)
	want(t, find(t, v, "fresh-report", `D:\work\fresh`), `D:\work\fresh\fresh-report.txt`)
	want(t, find(t, v, "fresh-report", `D:\work`), `D:\work\fresh\fresh-report.txt`)

	// A file under a folder the rules leave out is left out too.
	v.Apply(Change{Entry{ID: 903, Parent: id(`\src\node_modules\pkg`)}, "fresh-module.js"}, rules)
	want(t, find(t, v, "fresh-module", ""))
	want(t, find(t, v, "*fresh-module", ""), `D:\src\node_modules\pkg\fresh-module.js`)

	// Deleted: a file, then a folder with all it holds.
	v.Remove(fileID(`D:\work\report.docx`))
	want(t, find(t, v, "^report.docx$", ""))
	v.Remove(id(`\work\2024`))
	want(t, find(t, v, "report-final", ""))
	want(t, find(t, v, "minutes", ""))
	v.Remove(901)
	want(t, find(t, v, "fresh-report", ""))

	// Built again with what changed, the index says the same.
	c := v.Compact(rules)
	want(t, find(t, c, "report", ""), `D:\Windows\System32\report.dll`, `D:\work\report-new.txt`)
	want(t, find(t, c, "*fresh-module", ""), `D:\src\node_modules\pkg\fresh-module.js`)
	if len(c.d.rows) != 0 {
		t.Errorf("an index just built has %d changes beside it", len(c.d.rows))
	}
}

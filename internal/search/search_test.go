package search

import (
	"reflect"
	"strings"
	"testing"
)

// The cases here are those of Lertaro's tests (Tests/Core/SearchIndex,
// Tests/Plugins/PinyinAlias), which say what a query means.

var fuzzy = DefaultOptions()

func matches(t *testing.T, opts Options, query, name string) bool {
	t.Helper()
	var s Scratch
	_, ok := Parse(query, opts).MatchString(name, &s)
	return ok
}

func TestParseDrive(t *testing.T) {
	for _, c := range []struct {
		query string
		drive rune
		terms int
	}{
		{"c: readme", 'c', 1},
		{"c:readme", 'c', 1},
		{"c:", 'c', 0},
		{"c: d: readme", 'd', 1},
		{"readme", 0, 1},
	} {
		p := Parse(c.query, fuzzy)
		if p.Drive != c.drive || len(p.Sets) != c.terms {
			t.Errorf("%q: drive %q, %d sets; want %q, %d", c.query, p.Drive, len(p.Sets), c.drive, c.terms)
		}
	}
}

func TestMatch(t *testing.T) {
	exact := Options{Pinyin: true}
	orFirst := Options{Fuzzy: true, Pinyin: true, OrFirst: true}
	for _, c := range []struct {
		opts  Options
		query string
		name  string
		want  bool
	}{
		{fuzzy, "rdm", "readme.md", true},
		{fuzzy, "read md", "readme.md", true},
		{fuzzy, "read md", "readme.txt", false},
		{fuzzy, "read !md", "readme.txt", true},
		{fuzzy, "read !md", "readme.md", false},
		{fuzzy, "^read", "readme.md", true},
		{fuzzy, "^read", "unread.md", false},
		{fuzzy, "md$", "readme.md", true},
		{fuzzy, "md$", "md5sum.txt", false},
		{fuzzy, "^readme.md$", "readme.md", true},
		{fuzzy, "^readme.md$", "readme.md.bak", false},
		{fuzzy, "'read'", "my.read.txt", true},
		{fuzzy, "'read'", "readme.md", false},
		{fuzzy, "'read'", "r-e-a-d.md", false},
		{fuzzy, "README", "readme.md", true},
		{fuzzy, "README", "README.md", true},
		{fuzzy, `my\ file`, "my file.txt", true},
		{fuzzy, `my\ file`, "myfile.txt", false},
		{fuzzy, "'cad acb'", "cad acb.txt", true},
		{fuzzy, "'cad acb'", "cad-acb.txt", false},
		{fuzzy, "txt !'cad acb'", "other.txt", true},
		{fuzzy, "txt !'cad acb'", "cad acb.txt", false},
		{fuzzy, "'md$", "readme.md", true},
		{fuzzy, "'md$", "md5sum.txt", false},
		{exact, "ab cd", "cad acb.txt", false},
		{exact, "ab cd", "ab cd.txt", true},
		{exact, "'ab", "cad acb.txt", true},
		// The space binds tighter than the bar.
		{fuzzy, "report | summary 2024", "report.txt", true},
		{fuzzy, "report | summary 2024", "summary-2024.txt", true},
		{fuzzy, "report | summary 2024", "summary.txt", false},
		{fuzzy, "report | summary 2024", "2024.txt", false},
		// And the other way, as Lertaro once read it.
		{orFirst, "report | summary 2024", "summary-2024.txt", true},
		{orFirst, "report | summary 2024", "report-2024.txt", true},
		{orFirst, "report | summary 2024", "summary.txt", false},
		{fuzzy, "dare", "read", false},
		// Wide forms, and accents written apart.
		{fuzzy, "fast　stone", "FastStone Capture", true},
		{fuzzy, "fast\tstone", "FastStone Capture", true},
		{fuzzy, "ＦＳＣａｐｔｕｒｅ", "FSCapture2024", true},
		{fuzzy, "café", "cafe\u0301.txt", true},
		{fuzzy, "atlas !code", "code-atlas.txt", false},
		// Accented letters answer to the letters under them.
		{fuzzy, "cancion", "Canción.mp3", true},
		{fuzzy, "nino", "Niño.txt", true},
		{fuzzy, "ciguena", "Cigüeña.png", true},
	} {
		if got := matches(t, c.opts, c.query, c.name); got != c.want {
			t.Errorf("%q in %q: %v, want %v", c.query, c.name, got, c.want)
		}
	}
}

func TestTermKinds(t *testing.T) {
	p := Parse("'cad acb'", fuzzy)
	if len(p.Sets) != 1 || p.Sets[0][0].Kind != Boundary || string(p.Sets[0][0].Text) != "cad acb" {
		t.Errorf("phrase: %+v", p.Sets)
	}
	if p := Parse("don't stop", fuzzy); len(p.Sets) != 2 {
		t.Errorf("don't stop: %d sets", len(p.Sets))
	}
	p = Parse("'cad acb", fuzzy)
	if len(p.Sets) != 2 || p.Sets[0][0].Kind != Exact || string(p.Sets[0][0].Text) != "cad" {
		t.Errorf("unclosed quote: %+v", p.Sets)
	}
	p = Parse("'foo | 'bar'", fuzzy)
	if len(p.Sets) != 1 || len(p.Sets[0]) != 2 {
		t.Errorf("quoted alternatives: %+v", p.Sets)
	}
	if n := Parse("readme | rdm | rd", fuzzy).termLen; n != 6 {
		t.Errorf("term length of alternatives: %d", n)
	}
	if n := Parse("read me", fuzzy).termLen; n != 6 {
		t.Errorf("term length of words: %d", n)
	}
}

func TestFuzzyScores(t *testing.T) {
	var s Scratch
	score := func(query, name string) Match {
		m, ok := Parse(query, fuzzy).MatchString(name, &s)
		if !ok {
			t.Fatalf("%q does not match %q", query, name)
		}
		return m
	}
	if m := score("lro", "lertaro"); m.Begin != 0 || m.End != 7 {
		t.Errorf("lro in lertaro: %+v", m)
	}
	if m := score("a", "a"); m.Begin != 0 || m.End != 1 || m.Score != 36 {
		t.Errorf("a in a: %+v", m)
	}
	if m := score("a", "x---a-more-a"); m.Begin != 4 || m.End != 5 {
		t.Errorf("a in x---a-more-a: %+v", m)
	}
	// 16 a letter, the first at the start of a word worth twice its 10,
	// the rest carrying it.
	if m := score("read", "readme.md"); m.Score != 114 {
		t.Errorf("read in readme.md: %d", m.Score)
	}
	if m := score("^readme.md$", "readme.md"); m.Score != 244 {
		t.Errorf("^readme.md$: %d", m.Score)
	}
	var tx Text
	tx.SetString("hello world")
	if start, end, ok := fuzzyScope(tx.Chars, []rune("wor")); !ok || start != 5 || end != 9 {
		t.Errorf("scope of wor in hello world: %d %d %v", start, end, ok)
	}
}

func TestStripTokens(t *testing.T) {
	for _, c := range []struct {
		query  string
		clean  string
		tokens []string
	}{
		{"readme :a,b,c", "readme", []string{"a", "b", "c"}},
		{":a,b readme", ":a,b readme", nil},
		{"readme :a,,c", "readme :a,,c", nil},
		{"readme :", "readme :", nil},
		{"file ::'hello world'", "file", []string{":hello world"}},
		{`file ::hello\ world`, "file", []string{":hello world"}},
		{`C:\path\file`, `C:\path\file`, nil},
		{"12:30", "12:30", nil},
		{`C:\Project  Files\report.txt :S`, `C:\Project  Files\report.txt`, []string{"S"}},
		{"report :@doc,M-", "report", []string{"@doc", "M-"}},
	} {
		clean, tokens := stripTokens(c.query, ':')
		if clean != c.clean || !reflect.DeepEqual(tokens, c.tokens) {
			t.Errorf("%q: %q %q, want %q %q", c.query, clean, tokens, c.clean, c.tokens)
		}
	}
}

func TestParseQuery(t *testing.T) {
	q := ParseQuery("*readme", fuzzy, ':', nil)
	if !q.Bypass || q.Text != "readme" {
		t.Errorf("bypass: %+v", q)
	}
	q = ParseQuery("backup :.zip,S-,F-", fuzzy, ':', nil)
	if q.Sort != SortSize || !q.Descending || !q.Filter.FilesOnly {
		t.Errorf("tokens: %+v", q)
	}
	if !q.Filter.Admits([]byte("old.ZIP"), false) || q.Filter.Admits([]byte("old.rar"), false) || q.Filter.Admits([]byte("x.zip"), true) {
		t.Error(".zip lets through what it should not, or not what it should")
	}
	q = ParseQuery("clip :@video|audio", fuzzy, ':', nil)
	if !q.Filter.Admits([]byte("a.mp4"), false) || !q.Filter.Admits([]byte("a.mp3"), false) || q.Filter.Admits([]byte("a.txt"), false) {
		t.Error("@video|audio")
	}
	q = ParseQuery("photo :?IMG_????.jpg|DSC_????.jpg", fuzzy, ':', nil)
	if !q.Filter.Admits([]byte("IMG_0012.JPG"), false) || q.Filter.Admits([]byte("IMG_12.jpg"), false) {
		t.Error("wildcard token")
	}
	if q := ParseQuery("x :nonsense", fuzzy, ':', nil); !q.Unknown {
		t.Error("an unknown token is not told")
	}
	q = ParseQuery("report ::2024", fuzzy, ':', nil)
	if len(q.Segments) != 1 {
		t.Errorf("path segment token: %+v", q)
	}
	q = ParseQuery(`c\foo`, fuzzy, ':', nil)
	if q.Path == nil || q.Path.Drive != 'c' || q.Path.Exact != `c:\foo` {
		t.Errorf(`c\foo: %+v`, q.Path)
	}
	q = ParseQuery(`c:\foo\bar\`, fuzzy, ':', nil)
	if q.Path == nil || !q.Path.Listing || q.Path.Exact != `c:\foo\bar` {
		t.Errorf(`c:\foo\bar\: %+v`, q.Path)
	}
	q = ParseQuery(`src\core\main`, fuzzy, ':', nil)
	if q.Path == nil || len(q.Path.Dirs) != 2 || q.Path.File == nil {
		t.Errorf(`src\core\main: %+v`, q.Path)
	}
}

func rankOf(query, name string) Rank {
	var s Scratch
	return Parse(query, fuzzy).Rank(name, &s)
}

func TestShape(t *testing.T) {
	for _, c := range []struct {
		query, name string
		shape       int
	}{
		{"atlas", "code-atlas", ShapeWholeWord},
		{"atlas", "atlas.png", ShapeWholeWord},
		{"atlas", "CodeAtlas", ShapeWholeWord},
		{"atlas", "atlasing", ShapeWordStart},
		{"atlas", "metatlas.txt", ShapeInsideWord},
		{"atlas", "x_atlas", ShapeWholeWord},
		{"屏幕截图", "屏幕截图.txt", ShapeWholeWord},
		{"屏幕", "屏幕截图.txt", ShapeWordStart},
		{"截图", "屏幕截图.txt", ShapeInsideWord},
		{"图", "屏幕截图.txt", ShapeInsideWord},
		{"atls", "AutofillStates", ShapeScattered},
	} {
		if r := rankOf(c.query, c.name); !r.Matched || r.Shape != c.shape {
			t.Errorf("%q in %q: shape %d (matched %v), want %d", c.query, c.name, r.Shape, r.Matched, c.shape)
		}
	}
}

func TestRank(t *testing.T) {
	if r := rankOf("ab", "abcd"); r.Coverage != 0.5 || r.Contiguity != 1 || r.Start != 0 {
		t.Errorf("ab in abcd: %+v", r)
	}
	if a, b := rankOf("wx", "wxfef.doc"), rankOf("wx", "iwxfe.mp"); a.Start != 0 || b.Start != 1 || b.Coverage <= a.Coverage {
		t.Errorf("wx: %+v %+v", a, b)
	}
	if r := rankOf("alpha beta", "alpha x beta"); r.Contiguity != 1 {
		t.Errorf("alpha beta in alpha x beta: %+v", r)
	}
	if r := rankOf("alpha beta", "a-l-p-h-a beta"); r.Contiguity >= 1 {
		t.Errorf("alpha beta in a-l-p-h-a beta: %+v", r)
	}
	if r := rankOf("!zzz", "abc.txt"); !r.Matched || r.LeftContiguous() {
		t.Errorf("!zzz in abc.txt: %+v", r)
	}
	if r := rankOf("atlas !code", "code-atlas.txt"); r.Matched {
		t.Errorf("atlas !code in code-atlas.txt: %+v", r)
	}
	// A whole word before the start of one, before the inside of one,
	// before scattered letters.
	keys := []uint32{
		rankOf("atlas", "code-atlas").Key(),
		rankOf("atlas", "atlasing").Key(),
		rankOf("atlas", "metatlas.txt").Key(),
		rankOf("atls", "AutofillStates").Key(),
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Errorf("rank %d does not come before rank %d: %x %x", i-1, i, keys[i-1], keys[i])
		}
	}
}

func marksOf(query, name string) string {
	var s Scratch
	var b strings.Builder
	for _, on := range Parse(query, fuzzy).Highlight(name, &s) {
		if on {
			b.WriteByte('x')
		} else {
			b.WriteByte('.')
		}
	}
	return b.String()
}

func TestHighlight(t *testing.T) {
	for _, c := range []struct{ query, name, want string }{
		{"ab", "ababab", "xxxxxx"},
		{"我 | 爱 | 你", "我爱我家", "xxx."},
		{"bac", "北岸村邦出版.pdf", "xxx......."},
		{"bangc", "北岸村邦出版.pdf", "...xx....."},
		{"ban", "邦出版.txt", "x......"},
		{"bcb", "邦出版.txt", "xxx...."},
		{"ba", "XX邦出版手册.pdf", "..x........"},
		{"café", "cafe\u0301.txt", "xxxxx...."},
		{"rdm", "readme.md", "x..xx...."},
	} {
		if got := marksOf(c.query, c.name); got != c.want {
			t.Errorf("%q in %q: %s, want %s", c.query, c.name, got, c.want)
		}
	}
}

func TestPinyinReader(t *testing.T) {
	find := func(term, name string, common bool) (int, int, bool) {
		return newReader([]rune(term), common).find([]rune(strings.ToLower(name)))
	}
	for _, term := range []string{"bg", "baogao", "baog", "bgao", "bga"} {
		if _, _, ok := find(term, "报告", false); !ok {
			t.Errorf("%s does not read 报告", term)
		}
	}
	if s, e, ok := find("bg", "x报告", false); !ok || s != 1 || e != 3 {
		t.Errorf("bg in x报告: %d %d %v", s, e, ok)
	}
	for term, want := range map[string]bool{"su": true, "suan": true, "uan": false} {
		if _, _, ok := find(term, "算", false); ok != want {
			t.Errorf("%s reads 算: %v", term, ok)
		}
	}
	for _, c := range []struct {
		term, name string
		want       bool
	}{
		{"bj", "笔abc记", false},
		{"babcj", "笔abc记", true},
		{"bg", "报-告", false},
		{"lv", "绿", true},
		{"lu", "绿", true},
		{"nv", "女", true},
		{"nu", "女", true},
		{"di01ji", "第01集", true},
	} {
		if _, _, ok := find(c.term, c.name, false); ok != c.want {
			t.Errorf("%s reads %s: %v", c.term, c.name, ok)
		}
	}
	for term, want := range map[string]bool{"zhong": true, "chong": true, "tong": false} {
		if _, _, ok := find(term, "重", true); ok != want {
			t.Errorf("%s reads 重 by its common readings: %v", term, ok)
		}
	}
	if s, e, ok := find("cfwj", "重复文件2", true); !ok || s != 0 || e != 4 {
		t.Errorf("cfwj in 重复文件2: %d %d %v", s, e, ok)
	}
}

func TestPinyinMatch(t *testing.T) {
	exact := Options{Pinyin: true}
	both := func(query, name string, want bool) {
		t.Helper()
		for _, opts := range []Options{fuzzy, exact} {
			if got := matches(t, opts, query, name); got != want {
				t.Errorf("%q in %q (fuzzy %v): %v, want %v", query, name, opts.Fuzzy, got, want)
			}
		}
	}
	for _, q := range strings.Fields("pj pingj pjia ping jia pingjia pingji pij piji pinj pijia") {
		both(q, "评价.txt", true)
	}
	for _, q := range strings.Fields("ing ia ja zhengp") {
		both(q, "评价.txt", false)
	}
	for _, q := range strings.Fields("gl zhengsh glyhzshu guanliyonghu") {
		both(q, "管理用户证书.txt", true)
	}
	for _, q := range strings.Fields("gsh glzs guanlishu") {
		both(q, "管理用户证书.txt", false)
	}
	both("bi", "4分贝.pdf", false)
	both("fbi", "4分贝.pdf", false)
	both("bei", "4分贝.pdf", true)
	both("fbei", "4分贝.pdf", true)
	both("bi", "4分笔.pdf", true)
	both("fbi", "4分笔.pdf", true)
	both("ex", "学习笔记.txt", false)
	both("ex", "人行道.pdf", false)
	both("huchu", "简单输出式笔记架构.txt", false)
	for _, q := range strings.Fields("jdsc jiandansc jdshuc shuchu") {
		both(q, "简单输出式笔记架构.txt", true)
	}
	both("chongqing", "重庆.docx", true)
	both("cq", "重庆.docx", true)
	both("wzry", "王者荣耀.exe", true)
	// Words read as one.
	both("sousuo jieguo", "搜索结果.txt", true)
	// A term that mixes the two.
	if !matches(t, fuzzy, "电nao", "电脑.txt") {
		t.Error("电nao does not find 电脑.txt")
	}
	if matches(t, exact, "电nao", "电脑.txt") {
		t.Error("电nao finds 电脑.txt with fuzzy matching off")
	}
	// Without pinyin, none of it.
	if matches(t, Options{Fuzzy: true}, "cq", "重庆.docx") {
		t.Error("cq finds 重庆.docx with pinyin off")
	}
}

func TestPinyinRank(t *testing.T) {
	if r := rankOf("cfwj", "重复文件2"); r.Tier != TierInitials || r.Start != 0 {
		t.Errorf("cfwj in 重复文件2: %+v", r)
	}
	p := Parse("pixpin", fuzzy)
	var s Scratch
	if g := p.Grade([]byte("PixPin.lnk"), false, p.Rank("PixPin.lnk", &s)); g != GradeExact {
		t.Errorf("pixpin for PixPin.lnk: grade %d", g)
	}
	p = Parse("editor", fuzzy)
	if g := p.Grade([]byte("editor.txt"), false, p.Rank("editor.txt", &s)); g != GradeExact {
		t.Errorf("editor for editor.txt: grade %d", g)
	}
	if g := p.Grade([]byte("old-editor.txt"), false, p.Rank("old-editor.txt", &s)); g != GradeStrong {
		t.Errorf("editor for old-editor.txt: grade %d", g)
	}
	if g := p.Grade([]byte("editor"), true, p.Rank("editor", &s)); g != GradeStrong {
		t.Errorf("editor for the folder editor: grade %d", g)
	}
}

func TestMask(t *testing.T) {
	for _, c := range []struct{ query, name string }{
		{"rdm", "README.md"},
		{"cq", "重庆.docx"},
		{"chongqing", "重庆.docx"},
		{"cancion", "Canción.mp3"},
		{"报告", "年度报告.pdf"},
		{"ＦＳ", "fs.txt"},
		{"lu", "绿.txt"},
	} {
		p := Parse(c.query, fuzzy)
		if !p.Admits(Mask([]byte(c.name))) {
			t.Errorf("the mask of %q keeps %q out", c.name, c.query)
		}
	}
	if Parse("xyz", fuzzy).Admits(Mask([]byte("readme.md"))) {
		t.Error("the mask of readme.md lets xyz in")
	}
	if !Parse("report | summary 2024", fuzzy).Admits(Mask([]byte("report.txt"))) {
		t.Error("alternatives: the mask of report.txt is kept out")
	}
}

func TestCompareNames(t *testing.T) {
	for _, c := range [][2]string{{"a2", "a10"}, {"Same", "same"}, {"1x", "ax"}, {"ab", "abc"}} {
		if CompareNames(c[0], c[1]) >= 0 || CompareNames(c[1], c[0]) <= 0 {
			t.Errorf("%q does not come before %q", c[0], c[1])
		}
	}
}

func TestGlob(t *testing.T) {
	for _, c := range []struct {
		pat, name string
		want      bool
	}{
		{"*.log", "a.LOG", true},
		{"*128*", "icon-128.png", true},
		{"img_????.jpg", "IMG_0001.jpg", true},
		{"img_????.jpg", "IMG_001.jpg", false},
		{"*", "", true},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxbyy", false},
	} {
		if got := GlobMatch(c.pat, []byte(c.name)); got != c.want {
			t.Errorf("%q against %q: %v", c.pat, c.name, got)
		}
	}
}

func BenchmarkMatchFuzzy(b *testing.B) {
	p := Parse("rdme", fuzzy)
	names := [][]byte{[]byte("README.md"), []byte("renderer_frame_data_manager.cc"), []byte("node_modules"), []byte("Microsoft.VisualStudio.Shell.Interop.dll")}
	var s Scratch
	for i := 0; b.Loop(); i++ {
		p.Match(names[i&3], &s)
	}
}

func BenchmarkEval(b *testing.B) {
	names := [][]byte{[]byte("README.md"), []byte("renderer_frame_data_manager.cc"), []byte("node_modules"), []byte("Microsoft.VisualStudio.Shell.Interop.dll")}
	for _, q := range []string{"e", "rdme", "data frame", "年度"} {
		p := Parse(q, fuzzy)
		b.Run(q, func(b *testing.B) {
			var s Scratch
			for i := 0; b.Loop(); i++ {
				p.Eval(names[i&3], &s)
			}
		})
	}
}

// A name in Traditional characters is found by a query in Simplified
// ones, and the other way, and by its pinyin, where the system tells the
// two scripts' characters apart.
func TestTraditionalAndSimplified(t *testing.T) {
	if simplified()['報'-hanLow] != '报' {
		t.Skip("no table of Simplified forms here")
	}
	opts := DefaultOptions()
	for _, c := range []struct {
		query, name string
		want        bool
	}{
		{"报告", "年度報告.docx", true},
		{"報告", "年度报告.docx", true},
		{"報告", "年度報告.docx", true},
		{"baogao", "年度報告.docx", true},
		{"bg", "年度報告.docx", true},
		{"图书馆", "圖書館開放時間.txt", true},
		{"圖書館", "图书馆开放时间.txt", true},
		{"报告", "年度計劃.docx", false},
	} {
		p := Parse(c.query, opts)
		var s Scratch
		if _, ok := p.Eval([]byte(c.name), &s); ok != c.want {
			t.Errorf("%q finds %q: %v, want %v", c.query, c.name, ok, c.want)
		}
		// What a scan asks of a name's mask before it reads the name.
		if c.want && !p.Admits(Mask([]byte(c.name))) {
			t.Errorf("the mask of %q turns %q away", c.name, c.query)
		}
	}
}

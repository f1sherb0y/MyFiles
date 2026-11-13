package index

import (
	"os"
	"testing"
)

// Marking a real index by the rules: MYFILES_INDEX_FILE names its file.
func BenchmarkMark(b *testing.B) {
	v := corpus(60_000, 100)
	if path := os.Getenv("MYFILES_INDEX_FILE"); path != "" {
		ix, err := Open(path)
		if err != nil {
			b.Fatal(err)
		}
		defer ix.Close()
		v = NewView(ix, nil)
	}
	rules := NewRules(DefaultRules(), os.ExpandEnv)
	b.ReportAllocs()
	for b.Loop() {
		v.Mark(rules)
	}
}

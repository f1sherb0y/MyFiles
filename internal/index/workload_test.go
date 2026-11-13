package index

import (
	"context"
	"fmt"
	"testing"

	"myfiles/internal/search"
)

// lertaroWorkload is the data of Lertaro's own measurements of searching
// (Tests/Core/IndexV2/Search/SearchPerformanceWorkloadTests.cs): a hundred
// folders on a drive Z, and sixty thousand files, six hundred a folder,
// every tenth a .md and the rest .txt.
func lertaroWorkload() *View {
	s := NewScan(nil)
	for i := range 100 {
		s.AddString(Entry{ID: uint64(i + 2), Parent: 1, Flags: FlagDir}, fmt.Sprintf("Archive%03d", i))
	}
	for i := range 60_000 {
		ext := "txt"
		if i%10 == 0 {
			ext = "md"
		}
		s.AddString(Entry{ID: uint64(i + 102), Parent: uint64(i/600 + 2)}, fmt.Sprintf("report-%06d.%s", i, ext))
	}
	return NewView(Build(`Z:\`, 1, []*Scan{s}, true), nil)
}

// BenchmarkLertaroWorkload runs the queries Lertaro measures, on its data,
// with its limits and filters, for the two to be told apart on one
// machine. See docs/benchmark.md.
func BenchmarkLertaroWorkload(b *testing.B) {
	v := lertaroWorkload()
	const all = 1 << 30
	for _, c := range []struct {
		query, filter string
		limit, want   int
	}{
		{"report", "", 204, 204},
		{"report", "", all, 60_000},
		{"archive report", "", all, 60_000},
		{`archive\report`, "", all, 60_000},
		{`archive\report`, "*.txt", 204, 204},
		{`z:\archive000\`, "*.txt", 204, 204},
	} {
		name := fmt.Sprintf("%s/%d", c.query, c.limit)
		if c.limit == all {
			name = c.query + "/all"
		}
		if c.filter != "" {
			name += "/" + c.filter
		}
		b.Run(name, func(b *testing.B) {
			req := &Request{Query: search.ParseQuery(c.query, search.DefaultOptions(), ':', nil), Limit: c.limit}
			if c.filter != "" {
				req.Filter = search.NewFilter(c.filter, nil)
			}
			res, _ := v.Search(context.Background(), req)
			if len(res) != c.want {
				b.Fatalf("%d results, want %d", len(res), c.want)
			}
			b.ReportAllocs()
			for b.Loop() {
				// As the first time: nothing kept of the search before.
				v.gen++
				v.Search(context.Background(), req)
			}
		})
		if c.limit != all {
			b.Run(name+"/again", func(b *testing.B) {
				req := &Request{Query: search.ParseQuery(c.query, search.DefaultOptions(), ':', nil), Limit: c.limit}
				if c.filter != "" {
					req.Filter = search.NewFilter(c.filter, nil)
				}
				b.ReportAllocs()
				for b.Loop() {
					// The same search again, nothing having changed.
					v.Search(context.Background(), req)
				}
			})
		}
	}
}

package app

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/clock"
	"myfiles/internal/content"
	"myfiles/internal/index"
	"myfiles/internal/search"
)

// A search that starts with the keyword of contents, "cs" unless the
// settings say otherwise, looks in the files, not at their names: "cs
// budget 2026" finds the documents under the folder that hold both words.
// The tokens of a search of names narrow which files are read, as in
// "cs budget :@doc".

// contentAtMost is the most files a search of contents reads.
const contentAtMost = 200_000

// contentQuery is what a search of contents looks for, when the text of
// the search field asks for one.
func (a *App) contentQuery(text string) (rest string, ok bool) {
	kw := a.set.Search.ContentKeyword
	if kw == "" {
		kw = "cs"
	}
	switch {
	case a.set.Search.ContentOff:
		return "", false
	case strings.EqualFold(text, kw):
		return "", true
	case len(text) > len(kw) && strings.EqualFold(text[:len(kw)], kw) && text[len(kw)] == ' ':
		return strings.TrimSpace(text[len(kw):]), true
	}
	return "", false
}

// contentExts are the kinds of files a search of contents reads.
func (a *App) contentExts() []string {
	list := a.set.Search.ContentExts
	if strings.TrimSpace(list) == "" {
		list = content.DefaultExts
	}
	var out []string
	for _, ext := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if ext = strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(ext, "*"), ".")); ext != "" {
			out = append(out, ext)
		}
	}
	return out
}

// contentOptions are how files are read for their contents.
func (a *App) contentOptions() content.Options {
	opts := content.Options{MaxSize: int64(a.set.Search.ContentMaxMB) << 20}
	if dir := index.DefaultDir(); dir != "" && !a.ephemeral {
		opts.CacheDir = filepath.Join(filepath.Dir(dir), "content")
	}
	return opts
}

// searchContent runs a search of contents in the tab: the files to read
// are asked of the index of names, read, and shown as they are found.
func (w *Window) searchContent(t *Tab, gen uint64, ctx context.Context, cancel context.CancelFunc, rest string) {
	a, f := w.app, t.find
	filters := a.filters()
	q := search.ParseQuery(rest, a.searchOptions(), ':', filters)
	words := content.Terms(q.Text)
	f.content, f.query, f.learned = true, q, ""
	f.read, f.toRead = &atomic.Int64{}, 0
	f.snippets = nil
	if len(words) == 0 || q.Unknown {
		// The keyword alone: what it is for.
		cancel()
		f.stop = nil
		w.found(t, index.Found{}, false)
		f.note = locales.SearchContentHint()
		return
	}
	// The files of the kinds that are read, under the folder, of the
	// kinds the query's own tokens leave.
	all := search.ParseQuery("", a.searchOptions(), ':', filters)
	all.Filter = q.Filter.And(search.ExtFilter(a.contentExts()))
	all.Filter.FilesOnly = true
	// Not a search to keep the answer of: no two are told apart by it.
	all.Key = ""
	req := &index.Request{Query: all, Limit: contentAtMost, ShowHidden: a.set.ShowHidden}
	if !f.everywhere && real(f.origin) {
		req.Scope = f.origin
	}
	opts := a.contentOptions()
	read := f.read
	start := clock.Now()
	a.startIndex()
	stale := func() bool {
		return w.tabs[t.ID] != t || t.find != f || f.gen != gen || ctx.Err() != nil
	}
	go func() {
		found := a.finder.Search(ctx, req)
		if found.Uncovered && ctx.Err() == nil {
			found, _ = a.finder.SearchFolder(ctx, req)
		}
		if ctx.Err() != nil {
			return
		}
		files := make([]content.File, len(found.Results))
		byPath := make(map[string]int, len(found.Results))
		for i := range found.Results {
			r := &found.Results[i]
			files[i] = content.File{Path: r.Path, Size: int64(r.Size), Modified: r.Modified}
			byPath[r.Path] = i
		}
		w.update(func() {
			if !stale() {
				f.toRead = len(files)
			}
		})
		var mu sync.Mutex
		var hits []index.Result
		snippets := map[string]string{}
		first := true
		show := func(done bool) {
			mu.Lock()
			res, sn := slices.Clone(hits), maps.Clone(snippets)
			mu.Unlock()
			if done {
				// Those changed last first: what is worked on.
				slices.SortStableFunc(res, func(x, y index.Result) int { return int(int64(y.Modified) - int64(x.Modified)) })
			}
			w.update(func() {
				if stale() {
					return
				}
				f.snippets = sn
				if done {
					cancel()
					f.stop = nil
					f.took = clock.Since(start)
				}
				f.note = ""
				w.found(t, index.Found{Results: res, Total: len(res)}, !first)
				first = false
			})
		}
		// What is found shows while the rest is read.
		done := make(chan struct{})
		go func() {
			tick := time.NewTicker(250 * time.Millisecond)
			defer tick.Stop()
			shown := 0
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case <-tick.C:
					mu.Lock()
					n := len(hits)
					mu.Unlock()
					if n != shown {
						shown = n
						show(false)
					} else {
						// How far the reading is.
						w.update(func() { w.invalidate() })
					}
				}
			}
		}()
		content.Search(ctx, files, words, opts, read, func(h content.Hit) {
			mu.Lock()
			hits = append(hits, found.Results[byPath[h.File.Path]])
			snippets[h.File.Path] = h.Snippet
			mu.Unlock()
		})
		close(done)
		if ctx.Err() == nil {
			show(true)
		}
	}()
}

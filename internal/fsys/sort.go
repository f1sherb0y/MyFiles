package fsys

import (
	"cmp"
	"runtime"
	"slices"
	"sync"
)

// SortBy is the column a listing is sorted by.
type SortBy uint8

const (
	ByName SortBy = iota
	BySize
	ByModified
	ByType
	ByCreated
)

// Order is how a listing is sorted.
type Order struct {
	By         SortBy
	Descending bool
	// DirsFirst keeps directories before files whatever the direction, as
	// file managers do.
	DirsFirst bool
}

// parallelKeys is the number of entries from which sort keys are made on
// several threads: below it, starting them costs more than it saves.
const parallelKeys = 4096

// Sort orders view, indices into entries, leaving entries where they are:
// what points at an entry stays true however the rows are sorted. Names
// compare as the system's file manager compares them (digits as numbers,
// without case, in the user's language): each name gets a key once, and
// the keys compare as bytes.
func Sort(entries []Entry, view []int32, o Order) {
	fillKeys(entries)
	var by func(a, b *Entry) int
	switch o.By {
	case BySize:
		by = func(a, b *Entry) int { return cmp.Compare(a.Size, b.Size) }
	case ByModified:
		by = func(a, b *Entry) int { return cmp.Compare(a.ModTime, b.ModTime) }
	case ByCreated:
		by = func(a, b *Entry) int { return cmp.Compare(a.Created, b.Created) }
	case ByType:
		by = func(a, b *Entry) int { return cmp.Compare(a.Ext(), b.Ext()) }
	}
	sign := 1
	if o.Descending {
		sign = -1
	}
	slices.SortFunc(view, func(i, j int32) int {
		a, b := &entries[i], &entries[j]
		if o.DirsFirst {
			if ad, bd := a.IsDir(), b.IsDir(); ad != bd {
				if ad {
					return -1
				}
				return 1
			}
		}
		if by != nil {
			if c := by(a, b); c != 0 {
				return sign * c
			}
		}
		return sign * cmp.Compare(a.key, b.key)
	})
}

// All is the view of every entry, in the order read.
func All(entries []Entry) []int32 {
	view := make([]int32, len(entries))
	for i := range view {
		view[i] = int32(i)
	}
	return view
}

func fillKeys(entries []Entry) {
	fill := func(part []Entry) {
		var k keyer
		for i := range part {
			if part[i].key == "" {
				part[i].key = k.key(part[i].Name)
			}
		}
	}
	if len(entries) < parallelKeys {
		fill(entries)
		return
	}
	workers := min(runtime.GOMAXPROCS(0), 8)
	chunk := (len(entries) + workers - 1) / workers
	var wg sync.WaitGroup
	for at := 0; at < len(entries); at += chunk {
		part := entries[at:min(at+chunk, len(entries))]
		wg.Add(1)
		go func() {
			defer wg.Done()
			fill(part)
		}()
	}
	wg.Wait()
}

// naturalKey is a key that sorts names in natural order without case,
// where the system has no collation to ask: digit runs compare as numbers
// and letters by their lower case.
func naturalKey(b []byte, name string) []byte {
	for i := 0; i < len(name); {
		c := name[i]
		if c >= '0' && c <= '9' {
			j := i
			for j < len(name) && name[j] == '0' {
				j++
			}
			k := j
			for k < len(name) && name[k] >= '0' && name[k] <= '9' {
				k++
			}
			// A digit, then the count of significant digits, then
			// the digits: longer numbers are larger.
			n := k - j
			b = append(b, '0', byte(n>>8), byte(n))
			b = append(b, name[j:k]...)
			i = k
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b = append(b, c)
		i++
	}
	return b
}

package app

import "testing"

// The last window closed, the program ends: once the indexes are written,
// which it does not wait for on the main thread.
func TestLeaveEndsTheProgram(t *testing.T) {
	h := newHarness(t, "a.txt")
	quits := 0
	h.a.quit = func() { quits++ }
	h.a.startIndex()
	h.a.remove(h.w)
	h.a.leave()
	if !h.a.leaving || h.a.finder != nil {
		t.Fatalf("leaving %v with the manager %v; want it leaving, and the manager no more the program's", h.a.leaving, h.a.finder)
	}
	if quits != 0 {
		t.Fatal("the program ended before the indexes were written")
	}
	// The window closing once more changes nothing.
	h.a.leave()
	h.wait("the program to end", func() bool { return quits > 0 })
	if quits != 1 || h.a.leaving {
		t.Errorf("ended %d times, leaving %v; want once, and no more leaving", quits, h.a.leaving)
	}
}

// Started again before the indexes are written, the program stays: with a
// manager anew, which the window that came meanwhile searched with.
func TestLeaveStaysStartedAgain(t *testing.T) {
	h := newHarness(t, "a.txt")
	quits := 0
	h.a.quit = func() { quits++ }
	h.a.startIndex()
	old := h.a.finder
	h.a.remove(h.w)
	h.a.leave()

	// What the program started again does: a window, and a search in it.
	h.a.windows = append(h.a.windows, h.w)
	h.a.startIndex()
	anew := h.a.finder
	if anew == nil || anew == old {
		t.Fatalf("a search while leaving has the manager %p, the one closing %p; want one of its own", anew, old)
	}
	h.wait("the indexes to be written", func() bool { return !h.a.leaving })
	if quits != 0 {
		t.Fatalf("the program ended %d times with a window open", quits)
	}
	if h.a.finder != anew {
		t.Errorf("the manager is %p; want the one made meanwhile, %p", h.a.finder, anew)
	}

	// Its window closed in turn, it ends as any.
	h.a.remove(h.w)
	h.a.leave()
	h.wait("the program to end", func() bool { return quits > 0 })
	if quits != 1 {
		t.Errorf("ended %d times; want once", quits)
	}
}

// Started again and closed again, all before the indexes are written: the
// program ends, once, with what came meanwhile closed too.
func TestLeaveTwice(t *testing.T) {
	h := newHarness(t, "a.txt")
	quits := 0
	h.a.quit = func() { quits++ }
	h.a.startIndex()
	h.a.remove(h.w)
	h.a.leave()

	h.a.windows = append(h.a.windows, h.w)
	h.a.startIndex()
	h.a.remove(h.w)
	h.a.leave()

	h.wait("the program to end", func() bool { return quits > 0 })
	if quits != 1 || h.a.finder != nil || h.a.leaving {
		t.Errorf("ended %d times, the manager %v, leaving %v; want once, with no manager left open", quits, h.a.finder, h.a.leaving)
	}
}

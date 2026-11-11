package shell

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWorkerRunsInOrderOnOneThread(t *testing.T) {
	w := NewWorker()
	const n = 1000
	var got []int
	threads := map[uint32]bool{}
	for i := range n {
		w.Do(func() {
			got = append(got, i)
			threads[windows.GetCurrentThreadId()] = true
		})
	}
	// Close lets what is queued run, and Do after it does nothing.
	w.Close()
	w.Do(func() { t.Error("ran after Close") })
	select {
	case <-w.w.t.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker did not end")
	}
	if len(got) != n {
		t.Fatalf("ran %d of %d", len(got), n)
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("call %d ran at %d", v, i)
		}
	}
	if len(threads) != 1 {
		t.Errorf("ran on %d threads", len(threads))
	}
}

func TestWorkerIsAnApartment(t *testing.T) {
	onWorker(t, func() {
		// An object of the shell's that wants an apartment can be made.
		o, err := coCreate(&clsidFileOperation, &iidFileOperation)
		if err != nil {
			t.Fatal(err)
		}
		o.release()
	})
}

func TestDoDoesNotWait(t *testing.T) {
	w := NewWorker()
	defer w.Close()
	release := make(chan struct{})
	w.Do(func() { <-release })
	start := time.Now()
	for range 10000 {
		w.Do(func() {})
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("queueing behind a busy worker took %v", d)
	}
	close(release)
}

func TestErrors(t *testing.T) {
	for hr, want := range map[hresult]error{
		0:          nil,
		1:          nil,
		0x80270000: ErrCanceled,
		0x800704C7: ErrCanceled,
	} {
		if got := hr.err(); got != want {
			t.Errorf("hresult %#x is %v, want %v", uint32(hr), got, want)
		}
	}
	err := hresult(0x80070002).err()
	if err == nil || err.Error() == "" {
		t.Fatalf("no error for a failure: %v", err)
	}
	t.Logf("0x80070002: %v", err)
	t.Logf("0x80270027: %v", hresult(0x80270027).err())
	t.Logf("0x8000FFFF: %v", hresult(0x8000FFFF).err())
	t.Logf("0x8ABC1234: %v", hresult(0x8ABC1234).err())
}

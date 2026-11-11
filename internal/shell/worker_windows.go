package shell

import (
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procMsgWaitForMultipleObjectsEx = user32.NewProc("MsgWaitForMultipleObjectsEx")
	procPeekMessageW                = user32.NewProc("PeekMessageW")
	procTranslateMessage            = user32.NewProc("TranslateMessage")
	procDispatchMessageW            = user32.NewProc("DispatchMessageW")
)

// worker is a thread of the package's own behind shell.Worker.
type worker struct {
	t *thread
}

func newWorker() worker { return worker{startThread(false)} }

func (w worker) do(fn func()) { w.t.do(fn) }

func (w worker) close() { w.t.close() }

// thread is an OS thread that is a single-threaded COM apartment and runs
// the functions queued for it, in order. Between them, and while it has
// none, it dispatches the window messages sent to it, as an apartment
// must: the system's objects made on it are called through them.
type thread struct {
	mu     sync.Mutex
	queue  []func()
	closed bool
	// wake is an event set when the queue gets its first function, or
	// the thread is closed.
	wake windows.Handle
	// done is closed when the thread has ended.
	done chan struct{}
}

// startThread starts a thread: with ole, one that can own the clipboard
// (see enterApartment).
func startThread(ole bool) *thread {
	wake, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		// Out of handles: nothing else would work either.
		panic("shell: " + err.Error())
	}
	t := &thread{wake: wake, done: make(chan struct{})}
	go t.loop(ole)
	return t
}

// do queues fn and returns at once. Once the thread is closed it does
// nothing.
func (t *thread) do(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.queue = append(t.queue, fn)
	if len(t.queue) == 1 {
		windows.SetEvent(t.wake)
	}
}

// close ends the thread once what is queued has run. It does not wait.
func (t *thread) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		windows.SetEvent(t.wake)
	}
}

func (t *thread) loop(ole bool) {
	defer close(t.done)
	// Never unlocked: the thread ends with the goroutine.
	runtime.LockOSThread()
	if enterApartment(ole) {
		defer leaveApartment(ole)
	}
	var batch []func()
	for {
		t.mu.Lock()
		batch, t.queue = t.queue, batch[:0]
		closed := t.closed
		t.mu.Unlock()
		for i, fn := range batch {
			fn()
			batch[i] = nil
		}
		dispatchMessages()
		if len(batch) > 0 {
			continue
		}
		if closed {
			// Nothing sets the event any more: do and close hold the
			// lock, and saw closed.
			windows.CloseHandle(t.wake)
			return
		}
		waitForWork(t.wake)
	}
}

// message is the system's MSG.
type message struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

// point is the system's POINT.
type point struct {
	x, y int32
}

// dispatchMessages hands the calling thread's waiting window messages to
// their windows.
func dispatchMessages() {
	const pmRemove = 0x1
	var m message
	for {
		if r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove); r == 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// waitForWork sleeps until the event is set or a window message waits.
func waitForWork(event windows.Handle) {
	const (
		qsAllInput         = 0x04FF
		mwmoInputAvailable = 0x0004
	)
	procMsgWaitForMultipleObjectsEx.Call(1, uintptr(unsafe.Pointer(&event)), windows.INFINITE, qsAllInput, mwmoInputAvailable)
}

// run runs fn on the thread and waits for it.
func (t *thread) run(fn func()) {
	ran := make(chan struct{})
	t.do(func() {
		defer close(ran)
		fn()
	})
	<-ran
}

// hosts are the threads that outlive the calls made on them.
var hosts struct {
	mu   sync.Mutex
	idle []*thread
}

// onHost runs fn on a host thread and returns what it returns. A host is
// an apartment with OLE that never ends and dispatches messages whenever
// it runs nothing, which is what the shell's lasting effects need of the
// thread that asked for them: a property sheet talks to it for as long as
// it is open, and files cut from the system's menu are on the clipboard
// only while it lives. A host busy with a call (a command of the system's
// menu may take as long as the user does) is not waited for: another is
// started, and both are kept.
func onHost(fn func() error) error {
	hosts.mu.Lock()
	var t *thread
	if n := len(hosts.idle); n > 0 {
		t, hosts.idle = hosts.idle[n-1], hosts.idle[:n-1]
	}
	hosts.mu.Unlock()
	if t == nil {
		t = startThread(true)
	}
	var err error
	t.run(func() { err = fn() })
	hosts.mu.Lock()
	hosts.idle = append(hosts.idle, t)
	hosts.mu.Unlock()
	return err
}

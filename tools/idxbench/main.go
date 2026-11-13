//go:build windows

// Command idxbench measures the index of file names: how long reading a
// drive takes, how large the index is, and how long searches take.
//
//	idxbench -root D:\ [-save dir] [-scope D:\Code] [-n 20] query...
//
// With -load it maps an index a run with -save wrote instead of reading
// the drive.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"myfiles/internal/index"
	"myfiles/internal/search"
)

func main() {
	root := flag.String("root", `D:\`, "the drive or folder to index")
	save := flag.String("save", "", "a folder to write the index to")
	load := flag.Bool("load", false, "map the index written to -save, not read the drive")
	scope := flag.String("scope", "", "a folder to search in")
	rounds := flag.Int("n", 15, "how often each query runs")
	limit := flag.Int("limit", 10000, "the most results kept")
	show := flag.Int("show", 5, "how many results to print")
	table := flag.Bool("mft", false, "read the drive's file table, as administrator")
	hidden := flag.Bool("hidden", false, "show hidden files")
	profile := flag.String("cpuprofile", "", "write a CPU profile of the searches to a file")
	cold := flag.Bool("rest", false, "give the index's memory back before each search, as after a while without one")
	noIDs := flag.Bool("paths", false, "walk as a drive is walked whose file system does not name its files")
	folder := flag.Bool("folder", false, "search -root as a folder no index covers: read now, as the program does, by its manager")
	flag.Parse()
	go watch()
	if *folder {
		searchFolder(*root, *save, flag.Args(), *limit)
		return
	}

	peak()
	rules := index.NewRules(index.DefaultRules(), os.ExpandEnv)
	var ix *index.Index
	store := index.Store{Dir: *save}
	key := strings.ToLower(strings.Trim(strings.ReplaceAll(*root, ":", ""), `\`))
	start := time.Now()
	if *load {
		var err error
		if ix, err = store.Load(key); err != nil {
			fmt.Println("load:", err)
			os.Exit(1)
		}
		fmt.Printf("mapped %d rows in %v\n", ix.Len(), time.Since(start).Round(time.Microsecond))
	} else {
		var scans []*index.Scan
		var rootID uint64
		var err error
		// As the program reads a drive: into files, not its own memory.
		sc := index.NewScratch(*save)
		if *table {
			scans, rootID, err = index.ScanVolume(context.Background(), *root, nil, sc)
		} else {
			scans, rootID, err = index.Walk(context.Background(), *root, index.WalkOptions{IDs: !*noIDs, Scratch: sc})
		}
		if err != nil {
			fmt.Println("read:", err)
			os.Exit(1)
		}
		read := time.Since(start)
		n := 0
		for _, s := range scans {
			n += s.Len()
		}
		start = time.Now()
		if *save != "" {
			ix, err = store.Build(key, *root, rootID, scans, !*noIDs, index.Meta{}, sc)
			if err != nil {
				fmt.Println("build:", err)
				os.Exit(1)
			}
		} else {
			ix = index.Build(*root, rootID, scans, !*noIDs)
		}
		fmt.Printf("read %d files in %v, built %d rows in %v\n", n, read.Round(time.Millisecond), ix.Len(), time.Since(start).Round(time.Millisecond))
		peak()
		// The scans are the scratch's, which goes with it.
		scans = nil
		sc.Close()
		peak()
	}
	start = time.Now()
	v := index.NewView(ix, rules)
	fmt.Printf("marked by the rules in %v\n", time.Since(start).Round(time.Millisecond))
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	fmt.Printf("heap in use %d MB\n", ms.HeapInuse>>20)
	peak()

	if *profile != "" {
		f, err := os.Create(*profile)
		if err == nil {
			pprof.StartCPUProfile(f)
			defer pprof.StopCPUProfile()
		}
	}
	opts := search.DefaultOptions()
	for _, text := range flag.Args() {
		q := search.ParseQuery(text, opts, ':', nil)
		req := &index.Request{Query: q, Scope: *scope, Limit: *limit, ShowHidden: *hidden}
		parts := map[string]time.Duration{}
		index.Trace = func(part string, took time.Duration) { parts[part] += took }
		var times []time.Duration
		var res []index.Result
		var total int
		for range *rounds {
			if *cold {
				ix.Rest()
			}
			t := time.Now()
			res, total = v.Search(context.Background(), req)
			times = append(times, time.Since(t))
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		fmt.Printf("%-28q %8d matches  median %8v  best %8v  worst %8v\n", text, total,
			times[len(times)/2].Round(10*time.Microsecond), times[0].Round(10*time.Microsecond), times[len(times)-1].Round(10*time.Microsecond))
		for part, took := range parts {
			fmt.Println("   ", part, (took / time.Duration(*rounds)).Round(10*time.Microsecond))
		}
		for i := 0; i < len(res) && i < *show; i++ {
			fmt.Printf("    %s\n", res[i].Path)
		}
	}
}

// peak prints the most memory of its own the program has held, and what
// it holds now: what the system counts against it, not what it maps of
// files.
// counters is PROCESS_MEMORY_COUNTERS_EX2.
type counters struct {
	cb                                       uint32
	faults                                   uint32
	peakWorking, working                     uintptr
	peakPaged, paged, peakNonPaged, nonPaged uintptr
	pagefile, peakPagefile, private          uintptr
	privateWorking                           uintptr
	sharedCommit                             uint64
}

var (
	memoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
	// mostPrivate is the most private memory in use that watch saw.
	mostPrivate atomic.Uintptr
)

func memory() (c counters, ok bool) {
	c.cb = uint32(unsafe.Sizeof(c))
	r, _, _ := memoryInfo.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&c)), uintptr(c.cb))
	return c, r != 0
}

// watch keeps the most private memory in use, which the system does not.
func watch() {
	for {
		if c, ok := memory(); ok && c.privateWorking > mostPrivate.Load() {
			mostPrivate.Store(c.privateWorking)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func peak() {
	c, ok := memory()
	if r := 1; ok && r != 0 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		fmt.Printf("memory: %d MB private in use (what Task Manager shows), %d MB at most; %d MB committed, %d MB at most; working set %d MB, %d MB at most; the runtime's heap %d MB\n",
			c.privateWorking>>20, mostPrivate.Load()>>20, c.private>>20, c.peakPagefile>>20, c.working>>20, c.peakWorking>>20, (ms.HeapSys-ms.HeapReleased)>>20)
	}
}

// searchFolder searches a folder as the program does one no index
// covers, and tells what memory that took and what is left of it.
func searchFolder(root, dir string, queries []string, limit int) {
	peak()
	m := index.NewManager(index.Config{Dir: dir})
	defer m.Close()
	for _, text := range queries {
		q := search.ParseQuery(text, search.DefaultOptions(), ':', nil)
		start := time.Now()
		f, err := m.SearchFolder(context.Background(), &index.Request{Query: q, Scope: root, Limit: limit})
		fmt.Printf("%q: %d of %d in %v, %v\n", text, len(f.Results), f.Total, time.Since(start).Round(time.Millisecond), err)
		peak()
	}
	time.Sleep(3 * time.Second)
	fmt.Println("three seconds later:")
	peak()
}

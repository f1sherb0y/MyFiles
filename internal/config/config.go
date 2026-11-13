// Package config keeps what the user set and what was open, in files of
// the user's configuration directory. It knows nothing of the interface.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"myfiles/internal/workspace"
)

// Settings are the user's preferences.
type Settings struct {
	// Theme is "system", "light" or "dark".
	Theme string `json:"theme"`
	// RowHeight is the height of the rows of files, in DIPs.
	RowHeight float32 `json:"rowHeight"`
	// FontSize is the size of the text, in DIPs.
	FontSize float32 `json:"fontSize"`
	// ShowHidden shows the files the system hides.
	ShowHidden bool `json:"showHidden"`
	// DirsFirst lists directories before files.
	DirsFirst bool `json:"dirsFirst"`
	// Sidebar shows the places and drives on the left, SidebarWidth wide.
	Sidebar      bool    `json:"sidebar"`
	SidebarWidth float32 `json:"sidebarWidth"`
	// ConfirmTrash asks before moving files to the trash; deleting for
	// good always asks.
	ConfirmTrash bool `json:"confirmTrash"`
	// RestoreTabs opens the tabs of the last session when the program
	// starts without a folder to show.
	RestoreTabs bool `json:"restoreTabs"`
	// Pinned are the folders pinned to the sidebar, in its order, and the
	// system's places among them, by the names package shell has for
	// them; PinsImported is set once they were taken from the system's
	// file manager. Expanded are the nodes of the sidebar's tree that
	// show what they hold.
	Pinned       []string `json:"pinned,omitempty"`
	PinsImported bool     `json:"pinsImported,omitempty"`
	Expanded     []string `json:"expanded,omitempty"`
	// AutoUpdate looks for newer versions as the program runs, and
	// downloads them.
	AutoUpdate bool `json:"autoUpdate"`
	// View is how new tabs show their folders, as Tab.View.
	View string `json:"view"`
	// SortBy and SortDescending are how new tabs sort.
	SortBy         string `json:"sortBy"`
	SortDescending bool   `json:"sortDescending"`
	// Columns are the widths the user gave the columns of files, and
	// ColumnOrder their order.
	Columns     map[string]float32 `json:"columns,omitempty"`
	ColumnOrder []string           `json:"columnOrder,omitempty"`
	// OpenWith are the programs the user chose here to open the files of
	// a type with, by its extension (with its dot, in lower case): each
	// a shell.App's ID. An explicit "always" selection also updates the
	// system default; this keeps the choice usable if that update fails.
	OpenWith map[string]string `json:"openWith,omitempty"`
	// Search is how searches read what is typed and what is indexed.
	Search Search `json:"search"`
}

// Search are the settings of searching.
type Search struct {
	// Fuzzy lets a word match its letters scattered in a name, Pinyin
	// reads Chinese names by their pinyin, and OrFirst makes | bind
	// tighter than a space.
	Fuzzy   bool `json:"fuzzy"`
	Pinyin  bool `json:"pinyin"`
	OrFirst bool `json:"orFirst,omitempty"`
	// History puts the files opened from results before the others.
	History bool `json:"history"`
	// Index keeps an index of the names of the drives' files. Off are
	// the roots of the drives left out of it, and Extra those of the
	// drives and folders indexed beside the fixed drives.
	Index bool     `json:"index"`
	Off   []string `json:"off,omitempty"`
	Extra []string `json:"extra,omitempty"`
	// Rules are the rules that leave files out of results or order them,
	// one a line as the settings show them; RulesSet tells an empty list
	// the user made from one never touched.
	Rules    []string `json:"rules,omitempty"`
	RulesSet bool     `json:"rulesSet,omitempty"`
	// Filters are kinds of files the user named, by the keyword a query
	// asks for them with, as ":@keyword".
	Filters map[string]string `json:"filters,omitempty"`
	// NoPrograms leaves programs and the system's settings out of what a
	// search of the whole computer finds.
	NoPrograms bool `json:"noPrograms,omitempty"`
	// ContentOff switches the search inside files off; ContentKeyword is
	// what a search of them starts with, "cs" when empty, ContentExts the
	// kinds of files read, Lertaro's when empty, and ContentMaxMB the
	// largest file read, 10 MB when 0.
	ContentOff     bool   `json:"contentOff,omitempty"`
	ContentKeyword string `json:"contentKeyword,omitempty"`
	ContentExts    string `json:"contentExts,omitempty"`
	ContentMaxMB   int    `json:"contentMaxMB,omitempty"`
	// Custom are the kinds the user named in the settings: each with the
	// keyword a query names it by.
	Custom []CustomFilter `json:"customFilters,omitempty"`
}

// Defaults are the settings of a first run.
func Defaults() Settings {
	return Settings{
		Theme:        "system",
		RowHeight:    22,
		FontSize:     12,
		DirsFirst:    true,
		Sidebar:      true,
		SidebarWidth: 168,
		RestoreTabs:  true,
		AutoUpdate:   true,
		SortBy:       "name",
		View:         "details",
		Search:       Search{Fuzzy: true, Pinyin: true, History: true, Index: true},
	}
}

// Tab is a tab as a session keeps it.
type Tab struct {
	ID   workspace.TabID `json:"id"`
	Path string          `json:"path"`
	// View is how the tab shows its folder: "details", "list", "tiles",
	// "icons", "large" or "huge".
	View string `json:"view,omitempty"`
}

// Session is what a window showed when it closed.
type Session struct {
	Layout *workspace.Layout `json:"layout"`
	Tabs   []Tab             `json:"tabs"`
}

// Dir is the directory the files are kept in, made when missing.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "MyFiles")
	return dir, os.MkdirAll(dir, 0o755)
}

func read(name string, v any) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// write replaces a file whole: a crash while writing leaves the old one.
func write(name string, v any) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

// Load reads the settings, the defaults where the file says nothing or is
// missing.
func Load() Settings {
	s := Defaults()
	_ = read("settings.json", &s)
	if s.RowHeight < 16 || s.RowHeight > 40 {
		s.RowHeight = 22
	}
	if s.FontSize < 9 || s.FontSize > 20 {
		s.FontSize = 12
	}
	if s.SidebarWidth < 80 || s.SidebarWidth > 600 {
		s.SidebarWidth = 168
	}
	return s
}

// Save writes the settings.
func (s *Settings) Save() error { return write("settings.json", s) }

// RecentFolder is a folder the user showed, how often, and when last, in
// seconds since 1970: what the sidebar's recent folders are chosen from.
type RecentFolder struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
	Last  int64  `json:"last"`
}

// LoadRecent reads the folders shown in earlier runs, and SaveRecent
// writes them.
func LoadRecent() []RecentFolder {
	var r []RecentFolder
	_ = read("recent.json", &r)
	return r
}

func SaveRecent(r []RecentFolder) error { return write("recent.json", r) }

// Opened is a file opened from the results of a search: how much it
// counts, 10 at most, and when it last was, in seconds since 1970.
type Opened struct {
	Score float64 `json:"score"`
	Last  int64   `json:"last"`
}

// LoadOpened reads the files opened from results, by their paths, and
// SaveOpened writes them.
func LoadOpened() map[string]Opened {
	m := map[string]Opened{}
	_ = read("search-history.json", &m)
	return m
}

func SaveOpened(m map[string]Opened) error { return write("search-history.json", m) }

// CustomFilter is a kind of file the user named: Keyword is what a query
// asks for it by, as in ":@code", Label what a button of it says, and
// Rule what it lets through. Row has a button of it show above results,
// and Off switches it off without forgetting it.
type CustomFilter struct {
	Keyword string `json:"keyword"`
	Label   string `json:"label,omitempty"`
	Rule    string `json:"rule"`
	Row     bool   `json:"row,omitempty"`
	Off     bool   `json:"off,omitempty"`
}

// Learned is a file the user opened from the results of a query: how
// often, of their own choosing, and when last, in seconds since 1970. A
// file opened often enough for a query is found by it whatever its name.
type Learned struct {
	Path  string `json:"path"`
	Time  int64  `json:"time"`
	Opens int    `json:"organicOpens"`
}

// LoadLearned reads the files opened for each query, by the query as it
// is told from others, and SaveLearned writes them.
func LoadLearned() map[string][]Learned {
	m := map[string][]Learned{}
	_ = read("search-query-associations.json", &m)
	return m
}

func SaveLearned(m map[string][]Learned) error {
	return write("search-query-associations.json", m)
}

// LoadSession reads the last session, nil when there is none that holds
// together.
func LoadSession() *Session {
	var s Session
	if read("session.json", &s) != nil || !s.Layout.Valid() || len(s.Tabs) == 0 {
		return nil
	}
	known := map[workspace.TabID]bool{}
	for _, t := range s.Tabs {
		known[t.ID] = true
	}
	for _, id := range s.Layout.Tabs() {
		if !known[id] {
			return nil
		}
	}
	return &s
}

// Save writes the session.
func (s *Session) Save() error { return write("session.json", s) }

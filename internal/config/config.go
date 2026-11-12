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
	// View is how new tabs show their folders, as Tab.View.
	View string `json:"view"`
	// SortBy and SortDescending are how new tabs sort.
	SortBy         string `json:"sortBy"`
	SortDescending bool   `json:"sortDescending"`
	// Columns are the widths the user gave the columns of files, and
	// ColumnOrder their order.
	Columns     map[string]float32 `json:"columns,omitempty"`
	ColumnOrder []string           `json:"columnOrder,omitempty"`
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
		SortBy:       "name",
		View:         "details",
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

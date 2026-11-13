package app

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"myfiles/cmd/myfiles/locales"
	"myfiles/internal/config"
	"myfiles/internal/content"
	"myfiles/internal/index"
)

// searchSettings are the settings of searching and of the index: how
// queries are read, which drives are indexed and how they are doing, and
// the rules that leave files out of results or order them.
func (w *Window) searchSettings(c *ui.Context) (changed bool) {
	a := w.app
	th := c.Theme()
	set := &a.set.Search

	section(c, locales.SettingsSearch())
	again := false
	again = ui.Checkbox(c, &set.Fuzzy, locales.SettingsSearchFuzzy()).Changed() || again
	hint(c, locales.SettingsSearchFuzzyHint())
	again = ui.Checkbox(c, &set.Pinyin, locales.SettingsSearchPinyin()).Changed() || again
	again = ui.Checkbox(c, &set.OrFirst, locales.SettingsSearchOrFirst()).Changed() || again
	hint(c, locales.SettingsSearchOrFirstHint())
	changed = ui.Checkbox(c, &set.History, locales.SettingsSearchHistory()).Changed() || changed
	if again {
		changed = true
		a.searchAgain()
	}

	section(c, locales.SettingsIndex())
	if ui.Checkbox(c, &set.Index, locales.SettingsIndexOn()).Changed() {
		changed = true
		// Each drive follows: those left out stay out.
		a.startIndex()
		if set.Index {
			a.finder.Start()
		}
		for _, d := range a.finder.Drives() {
			if d.Fixed || listHas(set.Extra, d.Root) {
				a.finder.SetEnabled(d.Root, set.Index && !listHas(set.Off, d.Root))
			}
		}
	}
	hint(c, locales.SettingsIndexHint())
	if a.finder == nil || !set.Index {
		return changed
	}
	table := false
	for _, d := range a.finder.Drives() {
		on := d.State != index.StateOff
		table = table || d.Table
		ui.Row(c.Key("drive:" + d.Root)).Gap(10).AlignItems(ui.Center).Children(func() {
			label := strings.TrimSuffix(d.Root, `\`)
			if d.FileSystem != "" {
				label += "  " + d.FileSystem
			}
			if ui.Checkbox(c, &on, label).Width(150).Changed() {
				changed = true
				set.Off = removeFold(set.Off, d.Root)
				set.Extra = removeFold(set.Extra, d.Root)
				switch {
				case !on:
					set.Off = append(set.Off, d.Root)
				case !d.Fixed:
					set.Extra = append(set.Extra, d.Root)
				}
				root := d.Root
				go a.finder.SetEnabled(root, on)
			}
			ui.Text(c, driveState(d)).SingleLine().Grow(1).MinWidth(0).TextColor(th.TextMuted).FontSize(th.FontSize - 1)
			if on && d.State == index.StateReady && !d.Refreshing {
				if ui.Link(c, locales.SettingsIndexRebuild(), "").FontSize(th.FontSize - 1).Clicked() {
					a.finder.Rebuild(d.Root)
				}
			}
		})
	}
	ui.Row(c).Gap(8).MarginY(2).Children(func() {
		if ui.Button(c, locales.SettingsIndexRebuildAll()).Clicked() {
			a.finder.Rebuild("")
		}
		if !table && !index.CanReadTables() {
			if ui.Button(c, locales.SettingsIndexFast()).Disabled(a.elevating).Clicked() {
				a.elevating = true
				go func() {
					err := a.finder.Elevate(a.exe)
					a.desk.post(func() {
						a.elevating = false
						if err == index.ErrDeclined {
							w.say(locales.SettingsIndexDeclined())
						} else if err != nil {
							w.say(err.Error())
						}
						a.invalidate()
					})
				}()
			}
		}
	})
	if !table && !index.CanReadTables() {
		hint(c, locales.SettingsIndexFastHint())
	}

	section(c, locales.SettingsRules())
	if !a.rulesShown {
		a.rulesShown = true
		a.rulesText = strings.Join(formatRules(a.rules()), "\n")
	}
	ui.TextArea(c, &a.rulesText).Lines(6, 12).FillWidth().Font("monospace").FontSize(th.FontSize - 1).Label(locales.SettingsRules())
	ui.Row(c).Gap(8).Children(func() {
		lines := strings.Split(strings.ReplaceAll(a.rulesText, "\r", ""), "\n")
		same := strings.Join(formatRules(parseRules(lines)), "\n") == strings.Join(formatRules(a.rules()), "\n")
		if ui.Button(c, locales.SettingsRulesApply()).Disabled(same).Clicked() {
			set.Rules, set.RulesSet = formatRules(parseRules(lines)), true
			changed = true
			a.finder.SetRules(a.rules())
			a.rulesText = strings.Join(set.Rules, "\n")
		}
		if ui.Button(c, locales.SettingsRulesDefaults()).Disabled(!set.RulesSet).Clicked() {
			set.Rules, set.RulesSet = nil, false
			changed = true
			a.finder.SetRules(a.rules())
			a.rulesText = strings.Join(formatRules(a.rules()), "\n")
		}
	})
	hint(c, locales.SettingsRulesHint())

	programs := !set.NoPrograms
	if ui.Checkbox(c, &programs, locales.SettingsSearchPrograms()).Changed() {
		set.NoPrograms = !programs
		changed = true
		a.searchAgain()
	}

	section(c, locales.SettingsContent())
	on := !set.ContentOff
	if ui.Checkbox(c, &on, locales.SettingsContentOn()).Changed() {
		set.ContentOff = !on
		changed = true
	}
	if on {
		ui.Row(c).Gap(8).AlignItems(ui.Center).MarginY(2).Children(func() {
			ui.Text(c, locales.SettingsContentKeyword()).Shrink(0)
			keyword := set.ContentKeyword
			if keyword == "" {
				keyword = "cs"
			}
			if ui.TextInput(c.Key("content-keyword"), &keyword).Width(80).Label(locales.SettingsContentKeyword()).Changed() {
				set.ContentKeyword = strings.TrimSpace(keyword)
				changed = true
			}
			ui.Text(c, locales.SettingsContentExts()).Shrink(0).MarginStart(8)
			exts := set.ContentExts
			if exts == "" {
				exts = content.DefaultExts
			}
			if ui.TextInput(c.Key("content-exts"), &exts).Grow(1).MinWidth(120).Label(locales.SettingsContentExts()).Changed() {
				set.ContentExts = strings.TrimSpace(exts)
				changed = true
			}
		})
	}
	hint(c, locales.SettingsContentHint())

	section(c, locales.SettingsFilters())
	if !a.filtersShown {
		a.filtersShown = true
		a.filtersText = strings.Join(formatFilters(set.Custom), "\n")
	}
	ui.TextArea(c, &a.filtersText).Lines(3, 10).FillWidth().Font("monospace").FontSize(th.FontSize - 1).Label(locales.SettingsFilters())
	ui.Row(c).Gap(8).Children(func() {
		lines := strings.Split(strings.ReplaceAll(a.filtersText, "\r", ""), "\n")
		parsed := parseFilters(lines)
		same := strings.Join(formatFilters(parsed), "\n") == strings.Join(formatFilters(set.Custom), "\n")
		if ui.Button(c, locales.SettingsRulesApply()).Disabled(same).Clicked() {
			set.Custom = parsed
			changed = true
			a.filtersText = strings.Join(formatFilters(parsed), "\n")
			a.searchAgain()
		}
	})
	hint(c, locales.SettingsFiltersHint())
	return changed
}

// parseFilters reads the kinds of files the user named, a line each:
//
//	keyword = rule
//	+keyword (Label) = rule     shown among the buttons above results
//	#keyword = rule             switched off
func parseFilters(lines []string) []config.CustomFilter {
	var out []config.CustomFilter
	for _, line := range lines {
		line = strings.TrimSpace(line)
		var f config.CustomFilter
		for len(line) > 0 && (line[0] == '#' || line[0] == '+') {
			if line[0] == '#' {
				f.Off = true
			} else {
				f.Row = true
			}
			line = strings.TrimSpace(line[1:])
		}
		name, rule, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name, f.Rule = strings.TrimSpace(name), strings.TrimSpace(rule)
		if open := strings.IndexByte(name, '('); open > 0 && strings.HasSuffix(name, ")") {
			f.Label = strings.TrimSpace(name[open+1 : len(name)-1])
			name = strings.TrimSpace(name[:open])
		}
		f.Keyword = strings.TrimPrefix(name, "@")
		if f.Keyword == "" || f.Rule == "" || strings.ContainsAny(f.Keyword, " |:@") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// formatFilters writes them as parseFilters reads them.
func formatFilters(filters []config.CustomFilter) []string {
	out := make([]string, 0, len(filters))
	for _, f := range filters {
		line := ""
		if f.Off {
			line += "#"
		}
		if f.Row {
			line += "+"
		}
		line += f.Keyword
		if f.Label != "" {
			line += " (" + f.Label + ")"
		}
		out = append(out, line+" = "+f.Rule)
	}
	return out
}

// filters are the rules of the kinds of files the user named, by their
// keywords: what a query's ":@keyword" and the buttons of kinds ask by.
func (a *App) filters() map[string]string {
	s := &a.set.Search
	if len(s.Custom) == 0 {
		return s.Filters
	}
	out := make(map[string]string, len(s.Filters)+len(s.Custom))
	for k, rule := range s.Filters {
		out[k] = rule
	}
	for _, f := range s.Custom {
		if !f.Off {
			out[f.Keyword] = f.Rule
		}
	}
	return out
}

// kind is a kind of file a search can be narrowed to: what its button
// says, and the rule of it.
type kind struct {
	title func() string
	rule  string
}

// kindList are the kinds a search can be narrowed to: the built-in ones,
// then those of the user's that ask for a button.
func (a *App) kindList() []kind {
	out := make([]kind, 0, len(kinds)+2)
	for _, k := range kinds {
		out = append(out, kind{k.title, k.rule})
	}
	for _, f := range a.set.Search.Custom {
		if f.Row && !f.Off {
			title := f.Label
			if title == "" {
				title = f.Keyword
			}
			out = append(out, kind{func() string { return title }, "@" + f.Keyword})
		}
	}
	return out
}

// driveState tells how a drive's index is doing.
func driveState(d index.DriveStatus) string {
	switch d.State {
	case index.StateOff:
		return locales.IndexOff()
	case index.StateLoading:
		return locales.IndexLoading()
	case index.StateReading:
		return locales.IndexReading(int(d.Seen))
	case index.StateFailed:
		return locales.IndexFailed(d.Err)
	}
	text := locales.IndexReady(d.Files + d.Dirs)
	if d.Refreshing {
		text = locales.IndexRefreshing(d.Files + d.Dirs)
	}
	how := locales.IndexWalked()
	if d.Table {
		how = locales.IndexTable()
	}
	text += "  ·  " + how
	if d.Took > 0 {
		text += ", " + locales.IndexTook(strconv.FormatFloat(d.Took.Seconds(), 'f', 1, 64))
	}
	if d.Live {
		text += "  ·  " + locales.IndexLive()
	}
	return text
}

func listHas(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func removeFold(list []string, s string) []string {
	out := list[:0]
	for _, x := range list {
		if !strings.EqualFold(x, s) {
			out = append(out, x)
		}
	}
	return out
}

// searchAgain runs the searches showing again, as after how queries are
// read changed.
func (a *App) searchAgain() {
	for _, w := range a.windows {
		for _, t := range w.tabs {
			if t.searching() {
				w.search(t, true)
			}
		}
	}
}

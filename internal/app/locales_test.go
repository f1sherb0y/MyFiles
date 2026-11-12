package app

import (
	"testing"

	"myfiles/cmd/myfiles/locales"
)

// TestMessages checks a few messages in the languages the program is
// written in: the counts, which each language says its own way, and that
// a language says its own words.
func TestMessages(t *testing.T) {
	en, zh := locales.In("en"), locales.In("zh-Hans")
	for _, c := range []struct{ got, want string }{
		{en.Items(1), "1 item"},
		{en.Items(26254), "26,254 items"},
		{en.StatusSelected(3), "3 items selected"},
		{en.StatusItemsOf(2, 40), "2 items of 40"},
		{en.TypeOfExtension("TXT"), "TXT File"},
		{en.DeleteForeverTitle("“a.txt”"), "Delete “a.txt” permanently?"},
		{zh.Items(1), "1 个项目"},
		{zh.StatusSelected(3), "已选择 3 个项目"},
		{zh.StatusItemsOf(2, 40), "2 个项目，共 40 个"},
		{zh.MenuOpenWith(), "打开方式"},
		{zh.DriveFreeOf("28.3 GB", "476 GB"), "28.3 GB 可用，共 476 GB"},
		// A reader of Chinese as written in Taiwan gets no English.
		{locales.In("zh-CN").Settings(), "设置"},
		// A language the program is not written in reads the source's.
		{locales.In("fr").Settings(), "Settings"},
	} {
		if c.got != c.want {
			t.Errorf("said %q, want %q", c.got, c.want)
		}
	}
	// The table's columns take the language's titles.
	var kept []string
	for _, title := range fileTitles {
		kept = append(kept, title())
	}
	if len(kept) != len(fileColumns) || kept[0] != "Name" {
		t.Errorf("the columns are titled %q", kept)
	}
}

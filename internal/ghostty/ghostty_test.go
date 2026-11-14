package ghostty

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The library in the program is the build the terminal plugin binds.
func TestLibraryIsThePlugins(t *testing.T) {
	if len(library) == 0 {
		t.Skip("the program carries no library on this system")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "third_party", "mygo", "plugins", "terminal", "mygo-plugin.json"))
	if err != nil {
		t.Skip("the plugin's sources are not here: ", err)
	}
	var m struct {
		Libraries []struct {
			Files map[string]struct{ Name, SHA256 string }
		}
	}
	if err := json.Unmarshal(raw, &m); err != nil || len(m.Libraries) != 1 {
		t.Fatalf("the plugin's list of libraries: %v", err)
	}
	want := m.Libraries[0].Files[runtime.GOOS+"-"+runtime.GOARCH]
	sum := sha256.Sum256(library)
	if got := hex.EncodeToString(sum[:]); got != want.SHA256 || name != want.Name {
		t.Errorf("the program carries %s %s, the plugin binds %s %s: run the script setup-mygo.ps1 in tools", name, got, want.Name, want.SHA256)
	}
	if !strings.Contains(License, "Ghostty") {
		t.Error("the license is not Ghostty's")
	}
}

// The library is written once where the plugin looks, and not at all when
// it is beside the program.
func TestEnsure(t *testing.T) {
	if len(library) == 0 {
		t.Skip("the program carries no library on this system")
	}
	beside, cache := t.TempDir(), t.TempDir()
	if err := ensure(beside, cache); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(library)
	dir := filepath.Join(cache, "mygo", "natives", hex.EncodeToString(sum[:]))
	path := filepath.Join(dir, name)
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, library) {
		t.Fatalf("the library written: %d bytes, %v", len(got), err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 1 {
		t.Errorf("%d files where the library is", len(left))
	}
	// There already: left as it is.
	before, _ := os.Stat(path)
	if err := ensure(beside, cache); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(path); !after.ModTime().Equal(before.ModTime()) {
		t.Error("the library was written again")
	}
	// Cut short, as by a full disk: written anew.
	if err := os.WriteFile(path, library[:100], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensure(beside, cache); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, library) {
		t.Error("a library cut short was left")
	}
	// Beside the program: nothing is written.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(beside, name), library, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensure(beside, other); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(other); len(left) != 0 {
		t.Error("the library was written though it is beside the program")
	}
}

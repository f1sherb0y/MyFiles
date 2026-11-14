// Package ghostty carries the terminal's emulator in the program itself:
// Ghostty's libghostty-vt, a native library MyGo's terminal plugin loads
// at run time. The installer puts the library beside the program; the
// program carried about as a single file has only what is in it. Either
// way a terminal opens without the network.
//
// The library is not in the repository: tools\setup-mygo.ps1 puts the
// build the plugin names (its mygo-plugin.json) beside this file, where
// it is embedded from.
package ghostty

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"os"
	"path/filepath"
)

// License is Ghostty's.
//
//go:embed LICENSE
var License string

// Ensure has the library where the terminal plugin looks for it, and
// reports why it could not. With the library beside the program, as the
// installer leaves it, there is nothing to do; otherwise the one in the
// program is written, once, into the user's cache, under the name the
// plugin knows it by there. Where the program carries none, as on other
// systems, finding one is left to the plugin.
func Ensure() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	return ensure(filepath.Dir(exe), cache)
}

func ensure(beside, cache string) error {
	if len(library) == 0 {
		return nil
	}
	if info, err := os.Stat(filepath.Join(beside, name)); err == nil && !info.IsDir() {
		return nil
	}
	// The plugin's place for it: <cache>/mygo/natives/<sha256>/<name>.
	sum := sha256.Sum256(library)
	path := filepath.Join(cache, "mygo", "natives", hex.EncodeToString(sum[:]), name)
	whole := func() bool {
		info, err := os.Stat(path)
		return err == nil && info.Size() == int64(len(library))
	}
	if whole() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Under another name first: the path only ever names the whole file.
	tmp, err := os.CreateTemp(filepath.Dir(path), "*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(library)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil && !whole() {
		// Not put there by another copy of the program meanwhile.
		return err
	}
	return nil
}

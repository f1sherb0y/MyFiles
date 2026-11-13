//go:build !windows

package index

import (
	"errors"
	"os"
)

// Other systems build in ordinary memory for now.
type scratchFile struct{}

func (f *scratchFile) grow(string, int) ([]byte, error) { return nil, errors.ErrUnsupported }
func (f *scratchFile) close()                           {}

// createMapped gives ordinary memory, written to the file when done.
func createMapped(path string, size int64) (data []byte, done func(keep bool) error, err error) {
	data = make([]byte, size)
	return data, func(keep bool) error {
		if !keep {
			return nil
		}
		return os.WriteFile(path, data, 0o644)
	}, nil
}

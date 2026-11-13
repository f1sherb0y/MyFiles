//go:build !windows

package index

import "os"

func rest([]byte) {}

func warm(...[]byte) {}

// mapFile reads a file whole, where it is not mapped.
func mapFile(path string) ([]byte, func(), error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	// Columns of 8 bytes must start at a multiple of 8, which what the
	// runtime gives for this many bytes does.
	return data, func() {}, nil
}

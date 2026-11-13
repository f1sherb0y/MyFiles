//go:build !windows

package search

// simplifiedTable is empty where no system is asked for it: Traditional
// and Simplified characters are then told apart.
func simplifiedTable() []uint16 { return nil }

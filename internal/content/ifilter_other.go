//go:build !windows

package content

// extractor reads documents by the system's readers, where there are
// any: here there are none.
type extractor struct{}

func newExtractor() *extractor { return &extractor{} }

func (e *extractor) close() {}

func (e *extractor) text(string) (string, bool) { return "", false }

func ansi(b []byte) string { return string(b) }

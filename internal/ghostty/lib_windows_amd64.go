package ghostty

import _ "embed"

// library is the build of libghostty-vt for this system, and name what the
// plugin calls it.
//
//go:embed ghostty-vt-windows-amd64.dll
var library []byte

const name = "ghostty-vt.dll"

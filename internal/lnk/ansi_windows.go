package lnk

import "golang.org/x/sys/windows"

// ansi reads bytes in the system's code page, which is what a shortcut
// keeps the paths in that it does not keep in UTF-16.
func ansi(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	ascii := true
	for _, c := range b {
		ascii = ascii && c < 0x80
	}
	if ascii {
		return string(b)
	}
	n, err := windows.MultiByteToWideChar(0 /* the system's code page */, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n <= 0 {
		return string(b)
	}
	u := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(0 /* the system's code page */, 0, &b[0], int32(len(b)), &u[0], n); err != nil {
		return string(b)
	}
	return windows.UTF16ToString(u)
}

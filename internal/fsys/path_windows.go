package fsys

import (
	"strings"
)

const pathSep = '\\'

// SameName reports whether two names, or paths, are the same to the file
// system, which ignores case.
func SameName(a, b string) bool { return strings.EqualFold(a, b) }

// Clean makes a path the user typed, or another program passed, the one
// form the app keeps: backslashes, an upper case drive letter, no "." or
// ".." components and no trailing separator but a root's. It returns false
// for what is no absolute path.
func Clean(path string) (string, bool) {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, `\\?\UNC`)
	if strings.HasPrefix(path, `\\?\`) {
		path = path[4:]
	} else if strings.HasPrefix(path, `\`) && !strings.HasPrefix(path, `\\`) && len(path) > 1 {
		// What is left of \\?\UNC\server\share.
		path = `\` + path
	}
	path = strings.ReplaceAll(path, "/", `\`)
	var root, rest string
	switch {
	case len(path) >= 2 && path[1] == ':' && isLetter(path[0]):
		root = strings.ToUpper(path[:1]) + `:\`
		rest = strings.TrimLeft(path[2:], `\`)
	case strings.HasPrefix(path, `\\`):
		// \\server\share is the root.
		parts := strings.FieldsFunc(path[2:], func(r rune) bool { return r == '\\' })
		if len(parts) < 2 {
			return "", false
		}
		root = `\\` + parts[0] + `\` + parts[1] + `\`
		rest = strings.Join(parts[2:], `\`)
	default:
		return "", false
	}
	var out []string
	for _, p := range strings.Split(rest, `\`) {
		switch p {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return root, true
	}
	return root + strings.Join(out, `\`), true
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// Split is the components of a clean path, the root first: "C:" for a
// drive, `\\server\share` for a share.
func Split(path string) []Part {
	var root string
	switch {
	case len(path) >= 2 && path[1] == ':':
		root = path[:2]
	case strings.HasPrefix(path, `\\`):
		i := strings.IndexByte(path[2:], '\\')
		if i < 0 {
			return []Part{{Name: path, Path: path}}
		}
		j := strings.IndexByte(path[2+i+1:], '\\')
		if j < 0 {
			root = path
		} else {
			root = path[:2+i+1+j]
		}
	default:
		return nil
	}
	parts := []Part{{Name: root, Path: root + `\`}}
	rest := strings.TrimLeft(path[len(root):], `\`)
	at := len(root) + 1
	for rest != "" {
		name := rest
		if i := strings.IndexByte(rest, '\\'); i >= 0 {
			name = rest[:i]
			rest = rest[i+1:]
		} else {
			rest = ""
		}
		end := at + len(name)
		parts = append(parts, Part{Name: name, Path: path[:min(end, len(path))]})
		at = end + 1
	}
	return parts
}

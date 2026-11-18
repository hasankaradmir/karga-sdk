package renderer

import (
	"path"
	"strings"
)

// Files exposes the package's files/ directory to templates as .Files.
// Keys are slash-separated paths relative to files/; nothing outside it is reachable.
type Files map[string][]byte

// Get returns the content of name, or "" if it does not exist.
func (f Files) Get(name string) string {
	return string(f[cleanName(name)])
}

// Glob returns every file matching pattern (path.Match syntax) keyed by path.
// Templates range over the result in sorted key order.
func (f Files) Glob(pattern string) (map[string]string, error) {
	pattern = cleanName(pattern)
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, data := range f {
		if ok, _ := path.Match(pattern, name); ok {
			out[name] = string(data)
		}
	}
	return out, nil
}

func cleanName(name string) string {
	return strings.TrimPrefix(path.Clean("/"+name), "/")
}

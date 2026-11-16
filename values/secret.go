package values

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Redacted replaces writeOnly values wherever values are stored or shown.
const Redacted = "<redacted>"

// SecretPaths returns the paths of every property the schema marks "writeOnly": true. Such values
// are used to render but never stored or printed (they are typically passwords).
// shortcut: properties under array items are not followed; mark the array itself if needed.
func SecretPaths(schema []byte) ([][]string, error) {
	if len(strings.TrimSpace(string(schema))) == 0 {
		return nil, nil
	}
	var root any
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil, fmt.Errorf("values.schema.json: %w", err)
	}
	var out [][]string
	following := map[string]bool{} // $refs on the current path, so recursive schemas stop
	var walk func(node any, path []string)
	walk = func(node any, path []string) {
		m, ok := node.(map[string]any)
		if !ok {
			return
		}
		if ref, ok := m["$ref"].(string); ok && !following[ref] {
			following[ref] = true
			walk(resolvePointer(root, ref), path)
			delete(following, ref)
		}
		if w, _ := m["writeOnly"].(bool); w && len(path) > 0 {
			out = append(out, append([]string(nil), path...))
			return
		}
		if props, ok := m["properties"].(map[string]any); ok {
			for name, sub := range props {
				walk(sub, append(path, name))
			}
		}
		for _, k := range []string{"allOf", "anyOf", "oneOf"} {
			if arr, ok := m[k].([]any); ok {
				for _, sub := range arr {
					walk(sub, path)
				}
			}
		}
	}
	following["#"] = true
	walk(root, nil)
	return out, nil
}

// resolvePointer resolves a local "#/a/b" reference; anything else resolves to nil.
func resolvePointer(root any, ref string) any {
	if !strings.HasPrefix(ref, "#") {
		return nil
	}
	cur := root
	for part := range strings.SplitSeq(strings.TrimPrefix(strings.TrimPrefix(ref, "#"), "/"), "/") {
		if part == "" {
			continue
		}
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

// Mask returns a copy of m with every present, non-nil value at paths replaced by Redacted.
func Mask(m map[string]any, paths [][]string) map[string]any {
	out, _ := deepCopy(m).(map[string]any)
	for _, p := range paths {
		cur := out
		for i, k := range p {
			if i == len(p)-1 {
				if v, ok := cur[k]; ok && v != nil {
					cur[k] = Redacted
				}
				break
			}
			next, ok := cur[k].(map[string]any)
			if !ok {
				break
			}
			cur = next
		}
	}
	return out
}

// RedactedPaths lists the paths of m whose value is Redacted, i.e. values Mask removed.
func RedactedPaths(m map[string]any) [][]string {
	var out [][]string
	var walk func(v any, path []string)
	walk = func(v any, path []string) {
		switch t := v.(type) {
		case map[string]any:
			for k, c := range t {
				walk(c, append(path, k))
			}
		case string:
			if t == Redacted {
				out = append(out, append([]string(nil), path...))
			}
		}
	}
	walk(m, nil)
	return out
}

// Drop returns a copy of m without the given paths.
func Drop(m map[string]any, paths [][]string) map[string]any {
	out, _ := deepCopy(m).(map[string]any)
	for _, p := range paths {
		cur := out
		for i, k := range p {
			if i == len(p)-1 {
				delete(cur, k)
				break
			}
			next, ok := cur[k].(map[string]any)
			if !ok {
				break
			}
			cur = next
		}
	}
	return out
}

// Has reports whether m has a non-nil value at path.
func Has(m map[string]any, path []string) bool {
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = mm[k]; !ok {
			return false
		}
	}
	return cur != nil
}

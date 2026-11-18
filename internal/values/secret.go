package values

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Any is the path element that matches every key of an object and every index of a list. Secret
// paths contain it for writeOnly values under the keywords of maps and lists (additionalProperties,
// patternProperties, items, contains, …).
const Any = "*"

// SecretPaths returns the paths of every property the schema marks "writeOnly": true. Such values
// are used to render but never stored or printed (they are typically passwords).
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
			walk(resolveRef(root, ref), path)
			delete(following, ref)
		}
		if w, _ := m["writeOnly"].(bool); w && len(path) > 0 {
			out = append(out, append([]string(nil), path...))
			return
		}
		subschemas(m, func(sub any, step string, instance bool) {
			if !instance {
				return // a definition counts where it is referenced
			}
			if step == "" {
				walk(sub, path)
			} else {
				walk(sub, append(slices.Clip(path), step))
			}
		})
	}
	following["#"] = true
	walk(root, nil)
	return out, nil
}

// resolveRef resolves a local reference: a JSON pointer ("#/$defs/a") or an anchor ("#a", a
// "$anchor" or a draft-7 "$id" of "#a"). Anything else resolves to nil.
func resolveRef(root any, ref string) any {
	name, ok := strings.CutPrefix(ref, "#")
	if !ok {
		return nil
	}
	if name == "" || strings.HasPrefix(name, "/") {
		return resolvePointer(root, name)
	}
	return findAnchor(root, name)
}

func resolvePointer(root any, pointer string) any {
	cur := root
	for part := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
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

// findAnchor returns the schema object that declares anchor name.
func findAnchor(node any, name string) any {
	switch t := node.(type) {
	case map[string]any:
		if t["$anchor"] == name || t["$id"] == "#"+name {
			return t
		}
		for _, c := range t {
			if found := findAnchor(c, name); found != nil {
				return found
			}
		}
	case []any:
		for _, c := range t {
			if found := findAnchor(c, name); found != nil {
				return found
			}
		}
	}
	return nil
}

// At returns the values of v at path; Any matches every key or index.
func At(v any, path []string) []any {
	if len(path) == 0 {
		return []any{v}
	}
	var out []any
	switch t := v.(type) {
	case map[string]any:
		if path[0] == Any {
			for _, c := range t {
				out = append(out, At(c, path[1:])...)
			}
		} else if c, ok := t[path[0]]; ok {
			out = At(c, path[1:])
		}
	case []any:
		if path[0] == Any {
			for _, c := range t {
				out = append(out, At(c, path[1:])...)
			}
		}
	}
	return out
}

// Mask returns a deep copy of v with every non-nil value at paths replaced by with; Any matches
// every key of a map and every element of a list, as in SecretPaths.
func Mask(v any, paths [][]string, with any) any {
	out := deepCopy(v)
	for _, p := range paths {
		out = maskAt(out, p, with)
	}
	return out
}

func maskAt(v any, path []string, with any) any {
	if len(path) == 0 {
		if v == nil {
			return nil
		}
		return with
	}
	switch t := v.(type) {
	case map[string]any:
		if path[0] == Any {
			for k, c := range t {
				t[k] = maskAt(c, path[1:], with)
			}
		} else if c, ok := t[path[0]]; ok {
			t[path[0]] = maskAt(c, path[1:], with)
		}
	case []any:
		if path[0] == Any {
			for i, c := range t {
				t[i] = maskAt(c, path[1:], with)
			}
		}
	}
	return v
}

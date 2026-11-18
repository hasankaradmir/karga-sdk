package values

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Any is the path element that matches every key of an object and every index of a list. Secret
// paths contain it for writeOnly values under additionalProperties, patternProperties and items.
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
		// Keys matched by a pattern or not declared at all: any key (patterns are not evaluated, so
		// this can mask more than needed, never less).
		if props, ok := m["patternProperties"].(map[string]any); ok {
			for _, sub := range props {
				walk(sub, append(path, Any))
			}
		}
		walk(m["additionalProperties"], append(path, Any))
		walk(m["items"], append(path, Any))
		for _, k := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
			if arr, ok := m[k].([]any); ok {
				for _, sub := range arr {
					if k == "prefixItems" {
						walk(sub, append(path, Any))
					} else {
						walk(sub, path)
					}
				}
			}
		}
		for _, k := range []string{"if", "then", "else", "not"} {
			walk(m[k], path)
		}
		if deps, ok := m["dependentSchemas"].(map[string]any); ok {
			for _, sub := range deps {
				walk(sub, path)
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

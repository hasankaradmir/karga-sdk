// Package values loads, merges, overrides and validates package values.
package values

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// Decode parses a YAML document into a values map. An empty document gives an empty map.
func Decode(data []byte) (map[string]any, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return map[string]any{}, nil
	}
	norm, err := normalize(raw, "")
	if err != nil {
		return nil, err
	}
	m, ok := norm.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("values root must be a map, got %T", raw)
	}
	return m, nil
}

// LoadFile reads and decodes a values file.
func LoadFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := Decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// normalize rejects non-string map keys so values stay JSON-compatible.
func normalize(v any, path string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			n, err := normalize(child, path+"."+k)
			if err != nil {
				return nil, err
			}
			t[k] = n
		}
		return t, nil
	case map[any]any:
		return nil, fmt.Errorf("%s: map keys must be strings", pathOrRoot(path))
	case []any:
		for i, child := range t {
			n, err := normalize(child, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			t[i] = n
		}
		return t, nil
	default:
		return v, nil
	}
}

func pathOrRoot(p string) string {
	if p == "" {
		return "values"
	}
	return p[1:]
}

// Merge returns base overridden by override. Maps merge recursively, everything else is replaced,
// and a nil value in override deletes the key. Neither input is modified.
func Merge(base, override map[string]any) map[string]any {
	out, _ := deepCopy(base).(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	mergeInto(out, override)
	return out
}

func mergeInto(dst, src map[string]any) {
	for k, v := range src {
		if v == nil {
			delete(dst, k)
			continue
		}
		srcMap, srcIsMap := v.(map[string]any)
		dstMap, dstIsMap := dst[k].(map[string]any)
		if srcIsMap && dstIsMap {
			mergeInto(dstMap, srcMap)
			continue
		}
		dst[k] = deepCopy(v)
	}
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, c := range t {
			m[k] = deepCopy(c)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, c := range t {
			s[i] = deepCopy(c)
		}
		return s
	default:
		return v
	}
}

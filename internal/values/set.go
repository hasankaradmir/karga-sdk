package values

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// maxListIndex bounds a[N] so a typo cannot allocate a huge list.
const maxListIndex = 10000

// Set is a --set expression ("a.b=1,c[0]=x"), or a --set-string one when String. Values of a --set
// are typed (int, bool, null); null stores nil, which Merge turns into a key deletion.
// shortcut: the {a,b} list literal is not supported; add it if users ask.
type Set struct {
	Expr   string
	String bool
}

// Apply applies s to m. A list replaces the one it overrides as a whole, so where s indexes a list
// m does not hold yet, the list starts as a copy of the one at the same path in seed (the values s
// changes): a[1]=x keeps a[0].
func (s Set) Apply(m, seed map[string]any) error {
	if m == nil {
		return errors.New("Set.Apply: nil map")
	}
	expr := s.Expr
	for _, assign := range splitUnescaped(expr, ',') {
		kv := splitUnescaped(assign, '=')
		if len(kv) < 2 {
			return fmt.Errorf("--set %q: missing '=' in %q", expr, unescape(assign))
		}
		key := kv[0]
		// The value is everything after the first '='.
		raw := assign[len(key)+1:]
		steps, err := parseKey(key)
		if err != nil {
			return fmt.Errorf("--set %q: %w", expr, err)
		}
		val := typed(unescape(raw), s.String)
		if _, err := set(m, seed, steps, val); err != nil {
			return fmt.Errorf("--set %q: %w", expr, err)
		}
	}
	return nil
}

type step struct {
	key   string
	index int
	isIdx bool
}

// parseKey turns "a.b[0][1].c" into steps.
func parseKey(key string) ([]step, error) {
	var steps []step
	for _, seg := range splitUnescaped(key, '.') {
		name := seg
		var idxs []int
		// Unescaped brackets are always at the end of a segment: name[0][1].
		for strings.HasSuffix(name, "]") && !strings.HasSuffix(name, `\]`) {
			open := strings.LastIndex(name, "[")
			if open < 0 {
				return nil, fmt.Errorf("unbalanced ']' in key %q", unescape(key))
			}
			n, err := strconv.Atoi(name[open+1 : len(name)-1])
			if err != nil || n < 0 || n > maxListIndex {
				return nil, fmt.Errorf("invalid list index in key %q", unescape(key))
			}
			idxs = append([]int{n}, idxs...)
			name = name[:open]
		}
		if name == "" {
			return nil, fmt.Errorf("empty key segment in %q", unescape(key))
		}
		steps = append(steps, step{key: unescape(name)})
		for _, n := range idxs {
			steps = append(steps, step{index: n, isIdx: true})
		}
	}
	return steps, nil
}

// set sets val at steps in cur; seed is what cur overrides at the same place.
func set(cur, seed any, steps []step, val any) (any, error) {
	if len(steps) == 0 {
		return val, nil
	}
	s := steps[0]
	if s.isIdx {
		list, ok := cur.([]any)
		seedList, _ := seed.([]any)
		if !ok && cur == nil {
			list, _ = deepCopy(seedList).([]any)
		}
		for len(list) <= s.index {
			list = append(list, nil)
		}
		var childSeed any
		if s.index < len(seedList) {
			childSeed = seedList[s.index]
		}
		child, err := set(list[s.index], childSeed, steps[1:], val)
		if err != nil {
			return nil, err
		}
		list[s.index] = child
		return list, nil
	}
	m, ok := cur.(map[string]any)
	if !ok {
		// a.b=c replaces a scalar or list at a with a map, like a values file would.
		m = map[string]any{}
	}
	seedMap, _ := seed.(map[string]any)
	child, err := set(m[s.key], seedMap[s.key], steps[1:], val)
	if err != nil {
		return nil, err
	}
	m[s.key] = child
	return m, nil
}

func typed(s string, forceString bool) any {
	if forceString {
		return s
	}
	switch s {
	case "null":
		return nil
	case "true":
		return true
	case "false":
		return false
	}
	// Only integers become numbers: decimals stay strings, so --set image.tag=7.4 is the tag "7.4".
	// A number-typed decimal comes from a values file (-f).
	// Leading zeros stay strings so "007" is not silently turned into 7.
	if len(s) > 1 && (s[0] == '0' || strings.HasPrefix(s, "-0") || s[0] == '+') {
		return s
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return s
}

// splitUnescaped splits s on sep, ignoring separators preceded by a backslash. Escapes are kept.
func splitUnescaped(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case sep:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

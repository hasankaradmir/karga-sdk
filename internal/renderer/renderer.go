// Package renderer renders package templates with Go text/template and a deterministic sprig subset.
package renderer

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"go.yaml.in/yaml/v3"
)

// Limits that keep an untrusted package from exhausting the server that renders it.
const (
	// maxIncludeDepth stops runaway recursion through include and tpl, e.g. a template that includes itself.
	maxIncludeDepth = 100
	// maxOutput caps the bytes one Render may write, nested include and tpl output included.
	maxOutput = 16 << 20
	// maxItems caps the lists until, untilStep and seq may build.
	maxItems = 10000
	// renderTimeout bounds one Render; checked whenever output is written or a template is entered.
	renderTimeout = 10 * time.Second
)

// budget is shared by everything one Render executes.
type budget struct {
	depth    int
	written  int
	deadline time.Time
}

func (b *budget) check() error {
	if b.written > maxOutput {
		return fmt.Errorf("rendered output exceeds %d bytes", maxOutput)
	}
	if time.Now().After(b.deadline) {
		return fmt.Errorf("rendering took longer than %s", renderTimeout)
	}
	return nil
}

// limitedBuffer is a bytes.Buffer that charges every write to the render budget.
type limitedBuffer struct {
	bytes.Buffer
	b *budget
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	w.b.written += len(p)
	if err := w.b.check(); err != nil {
		return 0, err
	}
	return w.Buffer.Write(p)
}

// Context is the data available to templates as the dot.
type Context struct {
	Values  map[string]any
	Release Release
	Package PackageInfo
	Files   Files
}

// Release identifies the release being rendered.
type Release struct {
	Name     string
	Revision int
}

// PackageInfo is the package metadata exposed to templates.
type PackageInfo struct {
	Name    string
	Version string
}

// Output is one rendered template file.
type Output struct {
	// Path is relative to templates/.
	Path    string
	Content string
}

// Render parses every template, then executes the non-partial ones in path order.
// Files whose base name starts with "_" only contribute named templates (define blocks).
func Render(templates map[string][]byte, ctx Context) ([]Output, error) {
	names := make([]string, 0, len(templates))
	for n := range templates {
		names = append(names, n)
	}
	sort.Strings(names)

	b := &budget{deadline: time.Now().Add(renderTimeout)}
	root := template.New("").Option("missingkey=default")
	root.Funcs(funcMap(root, b))
	for _, n := range names {
		if _, err := root.New(n).Parse(string(templates[n])); err != nil {
			return nil, err
		}
	}

	var out []Output
	for _, n := range names {
		if strings.HasPrefix(path.Base(n), "_") {
			continue
		}
		buf := limitedBuffer{b: b}
		if err := root.ExecuteTemplate(&buf, n, ctx); err != nil {
			return nil, err
		}
		out = append(out, Output{Path: n, Content: clean(buf.String())})
	}
	return out, nil
}

// clean drops the "<no value>" text/template prints for missing map keys, so optional values render empty.
func clean(s string) string { return strings.ReplaceAll(s, "<no value>", "") }

// denied lists sprig functions that read the host or are non-deterministic.
var denied = map[string]string{
	"env": "reads the host environment", "expandenv": "reads the host environment",
	"getHostByName": "performs network lookups",
}

var nonDeterministic = []string{
	"now", "date", "dateInZone", "date_in_zone", "dateModify", "date_modify", "mustDateModify",
	"ago", "htmlDate", "htmlDateInZone", "unixEpoch",
	"randAlpha", "randAlphaNum", "randAscii", "randNumeric", "randBytes", "randInt", "uuidv4", "shuffle",
	"genPrivateKey", "genCA", "genCAWithKey", "genSelfSignedCert", "genSelfSignedCertWithKey",
	"genSignedCert", "genSignedCertWithKey", "encryptAES", "bcrypt", "htpasswd",
}

func init() {
	for _, n := range nonDeterministic {
		denied[n] = "non-deterministic"
	}
}

func funcMap(root *template.Template, b *budget) template.FuncMap {
	fm := sprig.TxtFuncMap()
	for name, reason := range denied {
		fm[name] = deniedFunc(name, reason)
	}
	// sprig's keys/values follow map iteration order; sort them so output is deterministic.
	fm["keys"] = sortedKeys
	fm["values"] = sortedValues
	limitSizes(fm)

	// nest runs fn one level deeper; include and tpl share the depth so they cannot recurse through each other.
	nest := func(what string, fn func(*limitedBuffer) error) (string, error) {
		if b.depth >= maxIncludeDepth {
			return "", fmt.Errorf("%s: nested too deeply (limit %d)", what, maxIncludeDepth)
		}
		if err := b.check(); err != nil {
			return "", err
		}
		b.depth++
		defer func() { b.depth-- }()
		buf := limitedBuffer{b: b}
		if err := fn(&buf); err != nil {
			return "", err
		}
		return clean(buf.String()), nil
	}
	fm["include"] = func(name string, data any) (string, error) {
		return nest(fmt.Sprintf("include %q", name), func(buf *limitedBuffer) error {
			return root.ExecuteTemplate(buf, name, data)
		})
	}
	fm["tpl"] = func(text string, data any) (string, error) {
		return nest("tpl", func(buf *limitedBuffer) error {
			t, err := root.Clone()
			if err != nil {
				return err
			}
			if t, err = t.New("\x00tpl").Parse(text); err != nil {
				return fmt.Errorf("tpl: %w", err)
			}
			if err := t.Execute(buf, data); err != nil {
				return fmt.Errorf("tpl: %w", err)
			}
			return nil
		})
	}
	fm["required"] = func(msg string, v any) (any, error) {
		if v == nil {
			return nil, fmt.Errorf("%s", msg)
		}
		if s, ok := v.(string); ok && s == "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return v, nil
	}
	fm["toYaml"] = func(v any) (string, error) {
		b, err := ToYAML(v)
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(string(b), "\n"), nil
	}
	fm["fromYaml"] = func(s string) (map[string]any, error) {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(s), &m); err != nil {
			return nil, err
		}
		return m, nil
	}
	return fm
}

// ToYAML marshals v with 2-space indentation and sorted map keys.
func ToYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func deniedFunc(name, reason string) func(...any) (string, error) {
	return func(...any) (string, error) {
		return "", fmt.Errorf("function %q is not allowed: %s", name, reason)
	}
}

func sortedKeys(dicts ...map[string]any) []string {
	var ks []string
	for _, d := range dicts {
		for k := range d {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	return ks
}

func sortedValues(dict map[string]any) []any {
	ks := sortedKeys(dict)
	vs := make([]any, len(ks))
	for i, k := range ks {
		vs[i] = dict[k]
	}
	return vs
}

// limitSizes wraps the sprig functions whose result size an argument controls.
func limitSizes(fm template.FuncMap) {
	tooLarge := func(name string, n int) error {
		return fmt.Errorf("%s: result too large (%d)", name, n)
	}
	until := fm["until"].(func(int) []int)
	fm["until"] = func(n int) ([]int, error) {
		if abs(n) > maxItems {
			return nil, tooLarge("until", n)
		}
		return until(n), nil
	}
	untilStep := fm["untilStep"].(func(int, int, int) []int)
	fm["untilStep"] = func(start, stop, step int) ([]int, error) {
		if step != 0 && (stop-start)/step > maxItems {
			return nil, tooLarge("untilStep", (stop-start)/step)
		}
		return untilStep(start, stop, step), nil
	}
	seq := fm["seq"].(func(...int) string)
	fm["seq"] = func(p ...int) (string, error) {
		if n := seqLen(p); n > maxItems {
			return "", tooLarge("seq", n)
		}
		return seq(p...), nil
	}
	repeat := fm["repeat"].(func(int, string) string)
	fm["repeat"] = func(n int, s string) (string, error) {
		if n > 0 && n*max(len(s), 1) > maxOutput {
			return "", tooLarge("repeat", n)
		}
		return repeat(n, s), nil
	}
	for _, name := range []string{"indent", "nindent"} {
		indent := fm[name].(func(int, string) string)
		fm[name] = func(n int, s string) (string, error) {
			if n > 0 && n*(strings.Count(s, "\n")+1) > maxOutput {
				return "", tooLarge(name, n)
			}
			return indent(n, s), nil
		}
	}
}

// seqLen is how many numbers seq prints for its 1 to 3 arguments (end; start end; start step end).
func seqLen(p []int) int {
	switch len(p) {
	case 1:
		return abs(p[0])
	case 2:
		return abs(p[1]-p[0]) + 1
	case 3:
		if p[1] == 0 {
			return 0
		}
		return abs((p[2]-p[0])/p[1]) + 1
	}
	return 0
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

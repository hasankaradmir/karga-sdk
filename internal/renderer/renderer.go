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
	// maxIterations caps the iterations of every range of one Render together.
	maxIterations = 1_000_000
	// renderTimeout bounds one Render; checked whenever output is written, a template is entered or
	// a range iterates.
	renderTimeout = 10 * time.Second
)

// budget is shared by everything one Render executes.
type budget struct {
	depth      int
	written    int
	iterations int
	deadline   time.Time
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
	instrument(root)

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
// shortcut: the same text written in a template is dropped too (as in Helm); a template can print it
// with {{ "<no" }} value>.
func clean(s string) string { return strings.ReplaceAll(s, "<no value>", "") }

// denied lists sprig functions that read the host or are non-deterministic, with the reason.
var denied = func() map[string]string {
	d := map[string]string{
		"env": "reads the host environment", "expandenv": "reads the host environment",
		"getHostByName": "performs network lookups",
	}
	for _, n := range []string{
		"now", "date", "dateInZone", "date_in_zone", "dateModify", "date_modify", "mustDateModify",
		"ago", "htmlDate", "htmlDateInZone", "unixEpoch",
		"randAlpha", "randAlphaNum", "randAscii", "randNumeric", "randBytes", "randInt", "uuidv4", "shuffle",
		"genPrivateKey", "genCA", "genCAWithKey", "genSelfSignedCert", "genSelfSignedCertWithKey",
		"genSignedCert", "genSignedCertWithKey", "encryptAES", "bcrypt", "htpasswd",
	} {
		d[n] = "non-deterministic"
	}
	return d
}()

func funcMap(root *template.Template, b *budget) template.FuncMap {
	fm := sprig.TxtFuncMap()
	for name, reason := range denied {
		fm[name] = deniedFunc(name, reason)
	}
	// sprig's keys/values follow map iteration order; sort them so output is deterministic.
	fm["keys"] = sortedKeys
	fm["values"] = sortedValues
	for name, fn := range sizeFuncs {
		fm[name] = fn
	}
	fm[tickFunc] = b.tick

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
			instrument(t)
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

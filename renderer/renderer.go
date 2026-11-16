// Package renderer renders package templates with Go text/template and a deterministic sprig subset.
package renderer

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"go.yaml.in/yaml/v3"
)

// maxIncludeDepth stops runaway recursion such as a template that includes itself.
const maxIncludeDepth = 100

// Context is the data available to templates as the dot.
type Context struct {
	Values  map[string]any
	Release Release
	Package PackageInfo
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

	root := template.New("").Option("missingkey=default")
	root.Funcs(funcMap(root))
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
		var buf bytes.Buffer
		if err := root.ExecuteTemplate(&buf, n, ctx); err != nil {
			return nil, err
		}
		out = append(out, Output{Path: n, Content: clean(buf.String())})
	}
	return out, nil
}

// clean drops the "<no value>" text/template prints for missing map keys, so optional values render empty.
func clean(s string) string { return strings.ReplaceAll(s, "<no value>", "") }

// denied lists sprig functions that read the host or are non-deterministic (tech design §5.4, §17.3).
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

func funcMap(root *template.Template) template.FuncMap {
	fm := sprig.TxtFuncMap()
	for name, reason := range denied {
		fm[name] = deniedFunc(name, reason)
	}
	// sprig's keys/values follow map iteration order; sort them so output is deterministic.
	fm["keys"] = sortedKeys
	fm["values"] = sortedValues

	depth := 0
	fm["include"] = func(name string, data any) (string, error) {
		if depth >= maxIncludeDepth {
			return "", fmt.Errorf("include %q: nested too deeply (limit %d)", name, maxIncludeDepth)
		}
		depth++
		defer func() { depth-- }()
		var buf bytes.Buffer
		if err := root.ExecuteTemplate(&buf, name, data); err != nil {
			return "", err
		}
		return clean(buf.String()), nil
	}
	fm["tpl"] = func(text string, data any) (string, error) {
		t, err := root.Clone()
		if err != nil {
			return "", err
		}
		t, err = t.New("\x00tpl").Parse(text)
		if err != nil {
			return "", fmt.Errorf("tpl: %w", err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return "", fmt.Errorf("tpl: %w", err)
		}
		return clean(buf.String()), nil
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
		b, err := yaml.Marshal(v)
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

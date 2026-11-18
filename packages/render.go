package packages

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"

	"github.com/hasankaradmir/karga-sdk/internal/renderer"
	"github.com/hasankaradmir/karga-sdk/internal/values"
	"github.com/hasankaradmir/karga-sdk/resource"
)

// Options controls how a package is rendered.
type Options struct {
	ReleaseName string
	// Revision defaults to 1.
	Revision int
	Values   ValueSources
	// Strict rejects values the schema does not declare.
	Strict bool
}

// ValueSources are the values a user gives a package, applied in this order onto its defaults.
type ValueSources struct {
	// Base is applied first (e.g. values reused from the previous revision).
	Base map[string]any
	// Files are values files read here, then Data values files a client read and sent.
	Files []string
	Data  [][]byte
	// Sets are --set and --set-string expressions, in the order they were given.
	Sets []Set
}

// Set is a --set expression ("a.b=1,c[0]=x"), or a --set-string one when String.
type Set struct {
	Expr   string `json:"expr"`
	String bool   `json:"string,omitempty"`
}

// Sets makes --set expressions (no --set-string) into Sets, in order.
func Sets(exprs ...string) []Set {
	out := make([]Set, len(exprs))
	for i, e := range exprs {
		out[i] = Set{Expr: e}
	}
	return out
}

// Document is one rendered resource.
type Document struct {
	// Source is the template path relative to templates/.
	Source   string
	Content  string
	Resource resource.Resource
}

// Manifest is the result of rendering a package.
type Manifest struct {
	Package Metadata
	// Overrides are the user-supplied values (BaseOverrides, -f, --set, --set-string); Values is the
	// result of merging them onto the package defaults.
	Overrides map[string]any
	Values    map[string]any
	Documents []Document
	// SecretPaths are the writeOnly value paths from values.schema.json: never store or print them.
	SecretPaths [][]string
	// Notes is the rendered NOTES.txt with writeOnly values replaced by <redacted>.
	Notes string
}

var docSeparator = regexp.MustCompile(`(?m)^---[ \t]*$`)

// Render merges values, validates them, renders templates and decodes the resources.
func Render(p *Package, opts Options) (*Manifest, error) {
	if err := resource.ValidateReleaseName(opts.ReleaseName); err != nil {
		return nil, err
	}
	if opts.Revision == 0 {
		opts.Revision = 1
	}
	if err := p.Metadata.Validate(); err != nil {
		return nil, prefixed(metadataFile, err)
	}
	overrides, err := Overrides(opts.Values, p.Values)
	if err != nil {
		return nil, err
	}
	vals := values.Merge(p.Values, overrides)
	if err := values.Validate(p.Schema, vals, opts.Strict); err != nil {
		return nil, err
	}

	tpls := p.Templates
	if p.Notes != nil {
		tpls = maps.Clone(p.Templates)
		tpls[notesTemplate] = p.Notes
	}
	outs, err := renderer.Render(tpls, renderer.Context{
		Values:  vals,
		Release: renderer.Release{Name: opts.ReleaseName, Revision: opts.Revision},
		Package: renderer.PackageInfo{Name: p.Metadata.Name, Version: p.Metadata.Version},
		Files:   renderer.Files(p.Files),
	})
	if err != nil {
		return nil, err
	}

	secretPaths, err := values.SecretPaths(p.Schema)
	if err != nil {
		return nil, err
	}
	m := &Manifest{Package: p.Metadata, Overrides: overrides, Values: vals, SecretPaths: secretPaths}
	var errs []error
	for _, o := range outs {
		if o.Path == notesTemplate {
			m.Notes = strings.TrimSpace(o.Content)
			continue
		}
		docs := docSeparator.Split(o.Content, -1)
		line := 1 // first line of the current document in the rendered output
		for i, doc := range docs {
			start := line
			line += strings.Count(doc, "\n") // the next document starts with the newline ending "---"
			r, err := resource.Decode([]byte(doc))
			if errors.Is(err, resource.ErrEmpty) {
				continue
			}
			if err != nil {
				where := "templates/" + o.Path
				if len(docs) > 1 {
					where += fmt.Sprintf(" (document %d, from line %d of the rendered output; see karga template)", i+1, start+leadingBlank(doc))
				}
				errs = append(errs, fmt.Errorf("%s: %w", where, err))
				continue
			}
			m.Documents = append(m.Documents, Document{Source: o.Path, Content: strings.TrimSpace(doc), Resource: r})
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	rs := make([]resource.Resource, len(m.Documents))
	for i, d := range m.Documents {
		rs[i] = d.Resource
	}
	if err := resource.ValidateSet(rs); err != nil {
		return nil, err
	}
	if err := m.checkWriteOnly(); err != nil {
		return nil, err
	}
	m.Notes = m.Redact(m.Notes)
	return m, nil
}

// leadingBlank counts the empty lines a document starts with.
func leadingBlank(doc string) int {
	n := 0
	for strings.HasPrefix(doc, "\n") {
		doc, n = doc[1:], n+1
	}
	return n
}

// notesTemplate is the name NOTES.txt is rendered under, next to the templates.
const notesTemplate = "../" + notesFile

// Overrides stacks the user-supplied values v, keeping nulls so they delete defaults later. A set
// that indexes a list edits the list it overrides, from defaults or v's files (a[1]=x keeps a[0]).
func Overrides(v ValueSources, defaults map[string]any) (map[string]any, error) {
	vals := values.Overlay(nil, v.Base)
	for _, f := range v.Files {
		fv, err := values.LoadFile(f)
		if err != nil {
			return nil, err
		}
		vals = values.Overlay(vals, fv)
	}
	for i, data := range v.Data {
		fv, err := values.Decode(data)
		if err != nil {
			return nil, fmt.Errorf("values file %d: %w", i+1, err)
		}
		vals = values.Overlay(vals, fv)
	}
	seed := values.Merge(defaults, vals)
	sets := map[string]any{}
	for _, s := range v.Sets {
		if err := (values.Set{Expr: s.Expr, String: s.String}).Apply(sets, seed); err != nil {
			return nil, err
		}
	}
	return values.Overlay(vals, sets), nil
}

// Warnings lists what the manifest does that is allowed but worth knowing (resource.Warnings).
func (m *Manifest) Warnings() []string { return resource.Warnings(m.Resources()) }

// Resources returns the decoded resources of the manifest in document order.
func (m *Manifest) Resources() []resource.Resource {
	rs := make([]resource.Resource, len(m.Documents))
	for i, d := range m.Documents {
		rs[i] = d.Resource
	}
	return rs
}

// Redacted replaces writeOnly and Secret values wherever karga shows or stores them.
const Redacted = "<redacted>"

// MaskValues returns a copy of vals with every value at paths (Manifest.SecretPaths, which may hold
// values.Any for every key or element) replaced by Redacted, for storing or showing them.
func MaskValues(vals map[string]any, paths [][]string) map[string]any {
	out, _ := values.Mask(vals, paths, Redacted).(map[string]any)
	return out
}

// Secrets returns every secret value of the manifest: writeOnly values and Secret resource values,
// longest first so a secret containing another is masked whole.
func (m *Manifest) Secrets() []string {
	seen := map[string]bool{}
	var add func(v any)
	add = func(v any) {
		switch t := v.(type) {
		case string:
			if t != "" {
				seen[t] = true
			}
		case map[string]any:
			for _, c := range t {
				add(c)
			}
		case []any:
			for _, c := range t {
				add(c)
			}
		}
	}
	for _, p := range m.SecretPaths {
		for _, v := range values.At(m.Values, p) {
			add(v)
		}
	}
	for _, d := range m.Documents {
		if s, ok := d.Resource.Spec.(*resource.SecretSpec); ok {
			for _, v := range s.Values {
				add(v)
			}
		}
	}
	// A template may pass a secret through b64enc (a Kubernetes habit); its encoding is as secret.
	for v := range maps.Clone(seen) {
		if len(v) >= minWriteOnlyLen {
			seen[base64.StdEncoding.EncodeToString([]byte(v))] = true
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) || len(out[i]) == len(out[j]) && out[i] < out[j] })
	return out
}

// minWriteOnlyLen is the length from which a writeOnly value found outside a Secret is refused.
// shortcut: shorter values are not looked for, so "1" or "on" never trips over unrelated text; a
// real secret is longer.
const minWriteOnlyLen = 4

// checkWriteOnly refuses writeOnly values in resources other than Secrets: karga stores and shows
// the specs of other resources (state, plans, diffs), and protects only Secret values.
func (m *Manifest) checkWriteOnly() error {
	var errs []error
	for _, p := range m.SecretPaths {
		for _, v := range values.At(m.Values, p) {
			sv, ok := v.(string)
			if !ok || len(sv) < minWriteOnlyLen {
				continue
			}
			encoded := base64.StdEncoding.EncodeToString([]byte(sv))
			for _, d := range m.Documents {
				if d.Resource.Kind == resource.KindSecret || !strings.Contains(d.Content, sv) && !strings.Contains(d.Content, encoded) {
					continue
				}
				errs = append(errs, fmt.Errorf("templates/%s: %s %q uses the writeOnly value %s: put it in a Secret and mount it, or pass it with a *_FILE variable",
					d.Source, d.Resource.Kind, d.Resource.Metadata.Name, strings.Join(p, ".")))
			}
		}
	}
	return errors.Join(errs...)
}

// Redact replaces every secret value that occurs in s with Redacted.
// shortcut: plain substring match, so a short secret also masks unrelated text that contains it.
func (m *Manifest) Redact(s string) string { return m.redactor().Replace(s) }

// redactor replaces every secret value with Redacted, longest first (Secrets' order) so a secret
// containing another is masked whole.
func (m *Manifest) redactor() *strings.Replacer {
	secrets := m.Secrets()
	pairs := make([]string, 0, 2*len(secrets))
	for _, v := range secrets {
		pairs = append(pairs, v, Redacted)
	}
	return strings.NewReplacer(pairs...)
}

// RedactedDocuments returns the documents with every secret value replaced by Redacted: Secret
// resources are re-encoded with masked values, other documents have secret values masked wherever
// they occur.
func (m *Manifest) RedactedDocuments() ([]Document, error) {
	out := make([]Document, len(m.Documents))
	r := m.redactor()
	for i, d := range m.Documents {
		if s, ok := d.Resource.Spec.(*resource.SecretSpec); ok {
			masked, err := maskSecret(d.Resource, s)
			if err != nil {
				return nil, err
			}
			d.Content = masked
		} else {
			d.Content = r.Replace(d.Content)
		}
		out[i] = d
	}
	return out, nil
}

func maskSecret(r resource.Resource, s *resource.SecretSpec) (string, error) {
	masked := &resource.SecretSpec{Values: map[string]string{}}
	for k := range s.Values {
		masked.Values[k] = Redacted
	}
	r.Spec = masked
	// JSON first so field names follow the json tags, then YAML with sorted keys.
	js, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	var generic map[string]any
	if err := json.Unmarshal(js, &generic); err != nil {
		return "", err
	}
	out, err := renderer.ToYAML(generic)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

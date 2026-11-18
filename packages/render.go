package packages

import (
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
	// BaseOverrides are applied before ValueFiles (e.g. values reused from the previous revision).
	BaseOverrides map[string]any
	// ValueFiles are applied in order, then ValueData (values files a client read and sent), then
	// Sets, then SetStrings.
	ValueFiles []string
	ValueData  [][]byte
	Sets       []string
	SetStrings []string
	// Strict rejects values the schema does not declare.
	Strict bool
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
	overrides, err := Overrides(opts)
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

// Overrides stacks the user-supplied values of opts, keeping nulls so they delete defaults later.
func Overrides(opts Options) (map[string]any, error) {
	vals := values.Overlay(nil, opts.BaseOverrides)
	for _, f := range opts.ValueFiles {
		fv, err := values.LoadFile(f)
		if err != nil {
			return nil, err
		}
		vals = values.Overlay(vals, fv)
	}
	for i, data := range opts.ValueData {
		fv, err := values.Decode(data)
		if err != nil {
			return nil, fmt.Errorf("values file %d: %w", i+1, err)
		}
		vals = values.Overlay(vals, fv)
	}
	sets := map[string]any{}
	for _, s := range opts.Sets {
		if err := values.ApplySet(sets, s, false); err != nil {
			return nil, err
		}
	}
	for _, s := range opts.SetStrings {
		if err := values.ApplySet(sets, s, true); err != nil {
			return nil, err
		}
	}
	return values.Overlay(vals, sets), nil
}

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
			for _, d := range m.Documents {
				if d.Resource.Kind == resource.KindSecret || !strings.Contains(d.Content, sv) {
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
func (m *Manifest) Redact(s string) string {
	for _, v := range m.Secrets() {
		s = strings.ReplaceAll(s, v, Redacted)
	}
	return s
}

// RedactedDocuments returns the documents with every secret value replaced by Redacted: Secret
// resources are re-encoded with masked values, other documents have secret values masked wherever
// they occur.
func (m *Manifest) RedactedDocuments() ([]Document, error) {
	out := make([]Document, len(m.Documents))
	for i, d := range m.Documents {
		if s, ok := d.Resource.Spec.(*resource.SecretSpec); ok {
			masked, err := maskSecret(d.Resource, s)
			if err != nil {
				return nil, err
			}
			d.Content = masked
		} else {
			d.Content = m.Redact(d.Content)
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

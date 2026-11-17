package packages

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"strings"

	"github.com/hasankaradmir/karga-sdk/pkg"
	"github.com/hasankaradmir/karga-sdk/resource"
	"github.com/hasankaradmir/karga-sdk/renderer"
	"github.com/hasankaradmir/karga-sdk/values"
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
	Package pkg.Metadata
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
func Render(p *pkg.Package, opts Options) (*Manifest, error) {
	if !pkg.IsDNSLabel(opts.ReleaseName) {
		return nil, fmt.Errorf("release name %q must be a DNS label (lowercase letters, digits and '-')", opts.ReleaseName)
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
			m.Notes = redactValues(strings.TrimSpace(o.Content), vals, secretPaths)
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

// redactValues replaces the values at secret paths that occur in s.
func redactValues(s string, vals map[string]any, paths [][]string) string {
	for _, p := range paths {
		var cur any = vals
		for _, k := range p {
			m, ok := cur.(map[string]any)
			if !ok {
				cur = nil
				break
			}
			cur = m[k]
		}
		if v, ok := cur.(string); ok && v != "" {
			s = strings.ReplaceAll(s, v, redacted)
		}
	}
	return s
}

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

const redacted = "<redacted>"

// Print writes the manifest as a multi-document YAML stream.
// Secret values are replaced with "<redacted>" unless showSecrets is set.
func (m *Manifest) Print(w io.Writer, showSecrets bool) error {
	for _, d := range m.Documents {
		content := d.Content
		if s, ok := d.Resource.Spec.(*resource.SecretSpec); ok && !showSecrets {
			masked, err := maskSecret(d.Resource, s)
			if err != nil {
				return err
			}
			content = masked
		}
		if _, err := fmt.Fprintf(w, "---\n# Source: %s/templates/%s\n%s\n", m.Package.Name, d.Source, content); err != nil {
			return err
		}
	}
	return nil
}

func maskSecret(r resource.Resource, s *resource.SecretSpec) (string, error) {
	masked := &resource.SecretSpec{Values: map[string]string{}}
	for k := range s.Values {
		masked.Values[k] = redacted
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

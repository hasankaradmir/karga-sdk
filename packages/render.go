package packages

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/hasankaradmir/karga/internal/domain/pkg"
	"github.com/hasankaradmir/karga/internal/domain/resource"
	"github.com/hasankaradmir/karga/internal/infrastructure/renderer"
	"github.com/hasankaradmir/karga/internal/infrastructure/values"
)

// Options controls how a package is rendered.
type Options struct {
	ReleaseName string
	// Revision defaults to 1.
	Revision int
	// BaseOverrides are applied before ValueFiles (e.g. values reused from the previous revision).
	BaseOverrides map[string]any
	// ValueFiles are applied in order, then Sets, then SetStrings.
	ValueFiles []string
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

	outs, err := renderer.Render(p.Templates, renderer.Context{
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
		for _, doc := range docSeparator.Split(o.Content, -1) {
			r, err := resource.Decode([]byte(doc))
			if errors.Is(err, resource.ErrEmpty) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("templates/%s: %w", o.Path, err))
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

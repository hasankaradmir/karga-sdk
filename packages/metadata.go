package packages

import (
	"errors"
	"fmt"

	"github.com/Masterminds/semver/v3"

	"github.com/hasankaradmir/karga-sdk/resource"
)

// APIVersion is the only Package.yaml apiVersion accepted.
const APIVersion = "karga.io/v1alpha1"

// supportedRuntimes are the runtimes karga can run packages on.
var supportedRuntimes = map[string]bool{"docker": true}

// Metadata is the content of Package.yaml.
type Metadata struct {
	APIVersion  string       `yaml:"apiVersion" json:"apiVersion"`
	Name        string       `yaml:"name" json:"name"`
	Version     string       `yaml:"version" json:"version"`
	Description string       `yaml:"description,omitempty" json:"description,omitempty"`
	Maintainers []Maintainer `yaml:"maintainers,omitempty" json:"maintainers,omitempty"`
	Runtime     Runtime      `yaml:"runtime" json:"runtime"`
}

// Maintainer is a package maintainer.
type Maintainer struct {
	Name  string `yaml:"name" json:"name"`
	Email string `yaml:"email,omitempty" json:"email,omitempty"`
}

// Runtime declares runtime compatibility. It is a claim, not a guarantee.
type Runtime struct {
	Supported []string `yaml:"supported" json:"supported"`
}

// Validate returns every problem found in m, joined.
func (m Metadata) Validate() error {
	var errs []error
	if m.APIVersion != APIVersion {
		errs = append(errs, fmt.Errorf("apiVersion: must be %q, got %q", APIVersion, m.APIVersion))
	}
	if !resource.IsDNSLabel(m.Name) {
		errs = append(errs, fmt.Errorf("name: %q is not a valid DNS label", m.Name))
	}
	if _, err := semver.StrictNewVersion(m.Version); err != nil {
		errs = append(errs, fmt.Errorf("version: %q is not a valid semantic version", m.Version))
	}
	if len(m.Runtime.Supported) == 0 {
		errs = append(errs, errors.New("runtime.supported: must list at least one runtime"))
	}
	for _, r := range m.Runtime.Supported {
		if !supportedRuntimes[r] {
			errs = append(errs, fmt.Errorf("runtime.supported: unknown runtime %q (karga runs docker)", r))
		}
	}
	return errors.Join(errs...)
}

// Package is a loaded package: metadata plus its files.
type Package struct {
	Metadata Metadata
	// Values are the package defaults from values.yaml.
	Values map[string]any
	// RawValues is values.yaml as written (comments included), nil if absent.
	RawValues []byte
	// Schema is the raw values.schema.json, nil if absent.
	Schema []byte
	Readme []byte
	// Notes is NOTES.txt, a template shown after install and upgrade; nil if absent.
	Notes []byte
	// Templates maps paths relative to templates/ to their content.
	Templates map[string][]byte
	// Files maps paths relative to files/ to their content.
	Files map[string][]byte
}

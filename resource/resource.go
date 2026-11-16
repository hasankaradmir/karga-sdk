// Package resource defines karga resources (tech design §6) and their validation.
package resource

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/hasankaradmir/karga/internal/domain/pkg"
)

// APIVersion is the only resource apiVersion accepted.
const APIVersion = "karga.io/v1alpha1"

// Resource kinds.
const (
	KindWorkload = "Workload"
	KindVolume   = "Volume"
	KindNetwork  = "Network"
	KindConfig   = "Config"
	KindSecret   = "Secret"
)

// Metadata identifies a resource inside a release.
type Metadata struct {
	Name string `json:"name"`
}

// Resource is one decoded manifest document. Spec is one of *WorkloadSpec, *VolumeSpec,
// *NetworkSpec, *ConfigSpec or *SecretSpec, matching Kind.
type Resource struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   Metadata `json:"metadata"`
	Spec       spec     `json:"spec"`
}

type spec interface {
	validate() error
	setDefaults()
}

// ErrEmpty is returned by Decode for a document with no content (e.g. a template wrapped in a false if).
var ErrEmpty = errors.New("empty document")

var newSpec = map[string]func() spec{
	KindWorkload: func() spec { return &WorkloadSpec{} },
	KindVolume:   func() spec { return &VolumeSpec{} },
	KindNetwork:  func() spec { return &NetworkSpec{} },
	KindConfig:   func() spec { return &ConfigSpec{} },
	KindSecret:   func() spec { return &SecretSpec{} },
}

func kinds() string {
	ks := make([]string, 0, len(newSpec))
	for k := range newSpec {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

// Decode parses and validates one YAML document. Unknown fields are errors.
// YAML 1.2 (yaml.v3) is used, so "no" or "n" stay strings instead of becoming booleans.
func Decode(doc []byte) (Resource, error) {
	var raw any
	if err := yaml.Unmarshal(doc, &raw); err != nil {
		return Resource{}, err
	}
	if raw == nil {
		return Resource{}, ErrEmpty
	}
	js, err := json.Marshal(raw)
	if err != nil {
		return Resource{}, fmt.Errorf("document is not JSON-compatible (map keys must be strings): %w", err)
	}
	var h struct {
		APIVersion string          `json:"apiVersion"`
		Kind       string          `json:"kind"`
		Metadata   Metadata        `json:"metadata"`
		Spec       json.RawMessage `json:"spec"`
	}
	if err := strictJSON(js, &h); err != nil {
		return Resource{}, err
	}
	if h.APIVersion != APIVersion {
		return Resource{}, fmt.Errorf("apiVersion: must be %q, got %q", APIVersion, h.APIVersion)
	}
	mk, ok := newSpec[h.Kind]
	if !ok {
		return Resource{}, fmt.Errorf("kind: unknown kind %q (supported: %s)", h.Kind, kinds())
	}
	r := Resource{APIVersion: h.APIVersion, Kind: h.Kind, Metadata: h.Metadata, Spec: mk()}
	if len(h.Spec) > 0 && string(h.Spec) != "null" {
		if err := strictJSON(h.Spec, r.Spec); err != nil {
			return Resource{}, fmt.Errorf("%s %q: spec: %w", r.Kind, r.Metadata.Name, err)
		}
	}
	r.Spec.setDefaults()
	if err := r.Spec.validate(); err != nil {
		return Resource{}, fmt.Errorf("%s %q: %w", r.Kind, r.Metadata.Name, err)
	}
	return r, nil
}

func strictJSON(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// ValidateSet checks rules that span resources: names, uniqueness, references and host ports.
func ValidateSet(rs []Resource) error {
	var errs []error
	seen := map[string]map[string]bool{}
	for _, r := range rs {
		if !pkg.IsDNSLabel(r.Metadata.Name) {
			errs = append(errs, fmt.Errorf("%s %q: metadata.name must be a DNS label", r.Kind, r.Metadata.Name))
		}
		if seen[r.Kind] == nil {
			seen[r.Kind] = map[string]bool{}
		}
		if seen[r.Kind][r.Metadata.Name] {
			errs = append(errs, fmt.Errorf("%s %q: defined more than once", r.Kind, r.Metadata.Name))
		}
		seen[r.Kind][r.Metadata.Name] = true
	}

	hostPorts := map[string]string{}
	for _, r := range rs {
		w, ok := r.Spec.(*WorkloadSpec)
		if !ok {
			continue
		}
		for _, m := range w.Mounts {
			kind, name := m.Target()
			if !seen[kind][name] {
				errs = append(errs, fmt.Errorf("%s %q: mount %s: %s %q is not defined", KindWorkload, r.Metadata.Name, m.MountPath, kind, name))
			}
		}
		for _, n := range w.Networks {
			if !seen[KindNetwork][n] {
				errs = append(errs, fmt.Errorf("%s %q: network %q is not defined", KindWorkload, r.Metadata.Name, n))
			}
		}
		for _, p := range w.Ports {
			if p.HostPort == 0 {
				continue
			}
			key := fmt.Sprintf("%d/%s", p.HostPort, p.Protocol)
			if other, dup := hostPorts[key]; dup {
				errs = append(errs, fmt.Errorf("%s %q: hostPort %s is already used by %s %q", KindWorkload, r.Metadata.Name, key, KindWorkload, other))
			}
			hostPorts[key] = r.Metadata.Name
		}
	}
	return errors.Join(errs...)
}

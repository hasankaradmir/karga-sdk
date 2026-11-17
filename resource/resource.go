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
	KindService  = "Service"
)

// Metadata identifies a resource inside a release.
type Metadata struct {
	Name string `json:"name"`
}

// Resource is one decoded manifest document. Spec is one of *WorkloadSpec, *VolumeSpec,
// *NetworkSpec, *ConfigSpec, *SecretSpec or *ServiceSpec, matching Kind.
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

// ReservedPrefix starts the names karga gives its own objects and aliases (e.g. the proxy).
const ReservedPrefix = "karga-"

// ErrEmpty is returned by Decode for a document with no content (e.g. a template wrapped in a false if).
var ErrEmpty = errors.New("empty document")

var newSpec = map[string]func() spec{
	KindWorkload: func() spec { return &WorkloadSpec{} },
	KindVolume:   func() spec { return &VolumeSpec{} },
	KindNetwork:  func() spec { return &NetworkSpec{} },
	KindConfig:   func() spec { return &ConfigSpec{} },
	KindSecret:   func() spec { return &SecretSpec{} },
	KindService:  func() spec { return &ServiceSpec{} },
}

// moved names Workload fields that exposure moved to Services, for a clearer decode error.
var moved = []string{"hostPort", "hostIP", "routes"}

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
			for _, f := range moved {
				if r.Kind == KindWorkload && strings.Contains(err.Error(), fmt.Sprintf("unknown field %q", f)) {
					err = fmt.Errorf("%w: a Workload no longer exposes ports; a Service does (see USAGE.md, \"Expose services\")", err)
					break
				}
			}
			return Resource{}, fmt.Errorf("%s %q: spec: %w", r.Kind, r.Metadata.Name, err)
		}
	}
	r.Spec.setDefaults()
	if err := r.Spec.validate(); err != nil {
		return Resource{}, fmt.Errorf("%s %q: %w", r.Kind, r.Metadata.Name, err)
	}
	return r, nil
}

// DecodeStored parses a spec karga stored itself (JSON, already validated when it was applied).
// Fields an older karga wrote and this one no longer knows are ignored, so old state stays readable.
func DecodeStored(doc []byte) (Resource, error) {
	var h struct {
		APIVersion string          `json:"apiVersion"`
		Kind       string          `json:"kind"`
		Metadata   Metadata        `json:"metadata"`
		Spec       json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(doc, &h); err != nil {
		return Resource{}, err
	}
	mk, ok := newSpec[h.Kind]
	if !ok {
		return Resource{}, fmt.Errorf("kind: unknown kind %q", h.Kind)
	}
	r := Resource{APIVersion: h.APIVersion, Kind: h.Kind, Metadata: h.Metadata, Spec: mk()}
	if len(h.Spec) > 0 && string(h.Spec) != "null" {
		if err := json.Unmarshal(h.Spec, r.Spec); err != nil {
			return Resource{}, fmt.Errorf("%s %q: spec: %w", r.Kind, r.Metadata.Name, err)
		}
	}
	r.Spec.setDefaults()
	return r, nil
}

func strictJSON(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// ValidateSet checks rules that span resources: names, uniqueness, references, and the ports and
// exposures of Services.
func ValidateSet(rs []Resource) error {
	var errs []error
	seen := map[string]map[string]bool{}
	for _, r := range rs {
		if !pkg.IsDNSLabel(r.Metadata.Name) {
			errs = append(errs, fmt.Errorf("%s %q: metadata.name must be a DNS label", r.Kind, r.Metadata.Name))
		}
		if strings.HasPrefix(r.Metadata.Name, ReservedPrefix) {
			errs = append(errs, fmt.Errorf("%s %q: names starting with %q are reserved for karga", r.Kind, r.Metadata.Name, ReservedPrefix))
		}
		if seen[r.Kind] == nil {
			seen[r.Kind] = map[string]bool{}
		}
		if seen[r.Kind][r.Metadata.Name] {
			errs = append(errs, fmt.Errorf("%s %q: defined more than once", r.Kind, r.Metadata.Name))
		}
		seen[r.Kind][r.Metadata.Name] = true
	}

	workloads := map[string]*WorkloadSpec{}
	for _, r := range rs {
		w, ok := r.Spec.(*WorkloadSpec)
		if !ok {
			continue
		}
		workloads[r.Metadata.Name] = w
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
	}

	// One release proxy serves every Service of the release on one address, so a port number
	// belongs to one Service; a listener's host and path belong to one Service too.
	ports, exposed := map[int]string{}, map[string]string{}
	for _, r := range rs {
		s, ok := r.Spec.(*ServiceSpec)
		if !ok {
			continue
		}
		at := fmt.Sprintf("%s %q", KindService, r.Metadata.Name)
		w := workloads[s.Workload]
		switch {
		case w == nil:
			errs = append(errs, fmt.Errorf("%s: workload %q is not defined", at, s.Workload))
		case len(w.Networks) == 0:
			errs = append(errs, fmt.Errorf("%s: workload %q joins no network, so the release proxy cannot reach it", at, s.Workload))
		}
		for _, p := range s.Ports {
			if w != nil {
				if wp, ok := w.PortNamed(p.TargetPort); !ok {
					errs = append(errs, fmt.Errorf("%s: port %s: targetPort %q is not a port of workload %q", at, p.Name, p.TargetPort, s.Workload))
				} else if wp.Protocol != "tcp" {
					errs = append(errs, fmt.Errorf("%s: port %s: targetPort %q is %s; services carry tcp only", at, p.Name, p.TargetPort, wp.Protocol))
				}
			}
			if other, dup := ports[p.Port]; dup {
				errs = append(errs, fmt.Errorf("%s: port %d is already used by %s %q (one release proxy serves every service of the release)", at, p.Port, KindService, other))
			}
			ports[p.Port] = r.Metadata.Name
		}
		for _, e := range s.Expose {
			key := e.Listener + " " + e.Host + e.Path
			if other, dup := exposed[key]; dup && other != r.Metadata.Name {
				errs = append(errs, fmt.Errorf("%s: listener %s %s is already used by %s %q", at, e.Listener, e.Host+e.Path, KindService, other))
			}
			exposed[key] = r.Metadata.Name
		}
		for _, name := range s.TLSSecrets() {
			sec := secretSpec(rs, name)
			switch {
			case sec == nil:
				errs = append(errs, fmt.Errorf("%s: tls secret %q is not defined", at, name))
			case sec.Values[TLSCertKey] == "" || sec.Values[TLSKeyKey] == "":
				errs = append(errs, fmt.Errorf("%s: tls secret %q needs %s and %s", at, name, TLSCertKey, TLSKeyKey))
			}
		}
	}
	return errors.Join(errs...)
}

func secretSpec(rs []Resource, name string) *SecretSpec {
	for _, r := range rs {
		if s, ok := r.Spec.(*SecretSpec); ok && r.Metadata.Name == name {
			return s
		}
	}
	return nil
}

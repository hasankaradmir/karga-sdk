// Package resource defines karga resources and their validation.
package resource

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
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
	Spec       Spec     `json:"spec"`
}

// Spec is the spec of one kind of resource. Only this package implements it: a new kind is a new
// Spec type added to specs, and its validation and defaults live with it.
type Spec interface {
	// Kind is the kind the spec belongs to (KindWorkload, …).
	Kind() string
	validate() error
	setDefaults()
}

// ReservedPrefix starts the names karga gives its own objects and aliases (e.g. the proxy).
const ReservedPrefix = "karga-"

// ErrEmpty is returned by Decode for a document with no content (e.g. a template wrapped in a false if).
var ErrEmpty = errors.New("empty document")

// specs makes a new, empty Spec of every kind.
var specs = []func() Spec{
	func() Spec { return &WorkloadSpec{} },
	func() Spec { return &VolumeSpec{} },
	func() Spec { return &NetworkSpec{} },
	func() Spec { return &ConfigSpec{} },
	func() Spec { return &SecretSpec{} },
	func() Spec { return &ServiceSpec{} },
}

// newSpec makes a new, empty Spec by kind.
var newSpec = func() map[string]func() Spec {
	m := map[string]func() Spec{}
	for _, mk := range specs {
		m[mk().Kind()] = mk
	}
	return m
}()

// moved names fields of Workload ports that exposure moved to Services, for a clearer decode error.
var moved = []string{"hostPort", "hostIP", "routes"}

// header is a document before its spec is decoded.
type header struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   Metadata        `json:"metadata"`
	Spec       json.RawMessage `json:"spec"`
}

// noSpec reports whether the document has no spec.
func (h header) noSpec() bool { return len(h.Spec) == 0 || string(h.Spec) == "null" }

// movedField returns the first field of a Workload's raw spec ports that exposure moved to Services.
func movedField(spec json.RawMessage) string {
	var w struct {
		Ports []map[string]json.RawMessage `json:"ports"`
	}
	if json.Unmarshal(spec, &w) != nil {
		return ""
	}
	for _, p := range w.Ports {
		for _, f := range moved {
			if _, ok := p[f]; ok {
				return f
			}
		}
	}
	return ""
}

// kinds lists the kinds Decode knows, for messages.
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
	var h header
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
	if !h.noSpec() {
		if err := strictJSON(h.Spec, r.Spec); err != nil {
			if f := movedField(h.Spec); r.Kind == KindWorkload && f != "" {
				err = fmt.Errorf("%w: a Workload's ports no longer take %s; a Service does (see the karga CLI's USAGE.md, \"Expose services\")", err, f)
			}
			return Resource{}, fmt.Errorf("%s %q: spec: %w", r.Kind, r.Metadata.Name, err)
		}
	}
	r.Spec.setDefaults()
	err = r.Spec.validate()
	if !IsDNSLabel(r.Metadata.Name) {
		err = errors.Join(fmt.Errorf("metadata.name: %q must be a DNS label (lowercase letters, digits and '-')", r.Metadata.Name), err)
	}
	if err != nil {
		return Resource{}, fmt.Errorf("%s %q: %w", r.Kind, r.Metadata.Name, err)
	}
	return r, nil
}

// DecodeStored parses a spec karga stored itself (JSON, already validated when it was applied).
// Fields an older karga wrote and this one no longer knows are ignored, so old state stays readable.
func DecodeStored(doc []byte) (Resource, error) {
	var h header
	if err := json.Unmarshal(doc, &h); err != nil {
		return Resource{}, err
	}
	mk, ok := newSpec[h.Kind]
	if !ok {
		return Resource{}, fmt.Errorf("kind: unknown kind %q", h.Kind)
	}
	r := Resource{APIVersion: h.APIVersion, Kind: h.Kind, Metadata: h.Metadata, Spec: mk()}
	if !h.noSpec() {
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

// Warnings lists what in rs is allowed but worth knowing before it is applied.
func Warnings(rs []Resource) []string {
	var out []string
	for _, r := range rs {
		w, ok := r.Spec.(*WorkloadSpec)
		if !ok {
			continue
		}
		for _, e := range w.EnvironmentFrom {
			out = append(out, fmt.Sprintf("Workload %q sets %s from Secret %q as an environment variable: anyone who can inspect the container (docker inspect) reads the value; mount the Secret as a file if the application can read one",
				r.Metadata.Name, e.Name, e.Secret))
		}
	}
	return out
}

// ValidateSet checks rules that span resources: names, uniqueness, references, and the ports and
// exposures of Services.
func ValidateSet(rs []Resource) error {
	defined, err := checkNames(rs)
	return errors.Join(err, checkWorkloadRefs(rs, defined), checkServices(rs))
}

// checkNames checks that names are valid, not karga's, and unique per kind; it returns them by kind.
func checkNames(rs []Resource) (map[string]map[string]bool, error) {
	var errs []error
	defined := map[string]map[string]bool{}
	for _, r := range rs {
		if !IsDNSLabel(r.Metadata.Name) {
			errs = append(errs, fmt.Errorf("%s %q: metadata.name must be a DNS label", r.Kind, r.Metadata.Name))
		}
		if strings.HasPrefix(r.Metadata.Name, ReservedPrefix) {
			errs = append(errs, fmt.Errorf("%s %q: names starting with %q are reserved for karga", r.Kind, r.Metadata.Name, ReservedPrefix))
		}
		if defined[r.Kind] == nil {
			defined[r.Kind] = map[string]bool{}
		}
		if defined[r.Kind][r.Metadata.Name] {
			errs = append(errs, fmt.Errorf("%s %q: defined more than once", r.Kind, r.Metadata.Name))
		}
		defined[r.Kind][r.Metadata.Name] = true
	}
	return defined, errors.Join(errs...)
}

// checkWorkloadRefs checks that what workloads mount, join, depend on and take variables from is defined.
func checkWorkloadRefs(rs []Resource, defined map[string]map[string]bool) error {
	var errs []error
	for _, r := range rs {
		w, ok := r.Spec.(*WorkloadSpec)
		if !ok {
			continue
		}
		at := fmt.Sprintf("%s %q", KindWorkload, r.Metadata.Name)
		for _, m := range w.Mounts {
			if kind, name := m.Target(); !defined[kind][name] {
				errs = append(errs, fmt.Errorf("%s: mount %s: %s %q is not defined", at, m.MountPath, kind, name))
			}
		}
		for _, e := range w.EnvironmentFrom {
			if e.Secret == "" || e.Key == "" {
				continue // reported by the workload's own validation
			}
			if sec := secretSpec(rs, e.Secret); sec == nil {
				errs = append(errs, fmt.Errorf("%s: environmentFrom %s: Secret %q is not defined", at, e.Name, e.Secret))
			} else if _, ok := sec.Values[e.Key]; !ok {
				errs = append(errs, fmt.Errorf("%s: environmentFrom %s: Secret %q has no key %q", at, e.Name, e.Secret, e.Key))
			}
		}
		for _, n := range w.Networks {
			if !defined[KindNetwork][n] {
				errs = append(errs, fmt.Errorf("%s: network %q is not defined", at, n))
			}
		}
		for _, d := range w.DependsOn {
			if !defined[KindService][d] {
				errs = append(errs, fmt.Errorf("%s: dependsOn: Service %q is not defined", at, d))
			}
		}
	}
	return errors.Join(errs...)
}

// checkServices checks Services against their workloads and each other. One release proxy serves
// every Service of the release on one address, so a port number belongs to one Service; a
// listener's route belongs to one Service too.
func checkServices(rs []Resource) error {
	workloads := map[string]*WorkloadSpec{}
	for _, r := range rs {
		if w, ok := r.Spec.(*WorkloadSpec); ok {
			workloads[r.Metadata.Name] = w
		}
	}
	var errs []error
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
				if wp, ok := w.TargetPort(p.TargetPort); !ok {
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
			key := e.Listener + " " + e.route()
			if other, dup := exposed[key]; dup && other != r.Metadata.Name {
				errs = append(errs, fmt.Errorf("%s: listener %s %s is already used by %s %q", at, e.Listener, e.route(), KindService, other))
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

package resource

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// WorkloadSpec runs one container (tech design §6.1). Replicas are intentionally unsupported.
type WorkloadSpec struct {
	Image         string            `json:"image"`
	RestartPolicy string            `json:"restartPolicy,omitempty"`
	Command       []string          `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
	Ports         []Port            `json:"ports,omitempty"`
	// Networks lists Network resources the workload joins.
	Networks    []string     `json:"networks,omitempty"`
	Mounts      []Mount      `json:"mounts,omitempty"`
	HealthCheck *HealthCheck `json:"healthCheck,omitempty"`
	Resources   *Resources   `json:"resources,omitempty"`
}

// Port publishes a container port, optionally on the host.
type Port struct {
	ContainerPort int    `json:"containerPort"`
	HostPort      int    `json:"hostPort,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
}

// Mount attaches exactly one of a Volume, Config or Secret at MountPath. Config and Secret are read-only.
type Mount struct {
	Volume    string `json:"volume,omitempty"`
	Config    string `json:"config,omitempty"`
	Secret    string `json:"secret,omitempty"`
	MountPath string `json:"mountPath"`
}

// Target returns the kind and name of the mounted resource.
func (m Mount) Target() (kind, name string) {
	switch {
	case m.Volume != "":
		return KindVolume, m.Volume
	case m.Config != "":
		return KindConfig, m.Config
	default:
		return KindSecret, m.Secret
	}
}

// HealthCheck probes the workload. Only exec checks exist in v1alpha1.
type HealthCheck struct {
	Type     string   `json:"type"`
	Command  []string `json:"command"`
	Interval string   `json:"interval,omitempty"`
	Timeout  string   `json:"timeout,omitempty"`
	Retries  int      `json:"retries,omitempty"`
}

// Resources limits the workload.
type Resources struct {
	Memory Quantity `json:"memory,omitempty"`
	CPU    Quantity `json:"cpu,omitempty"`
}

// Quantity accepts both YAML numbers (cpu: 1) and strings (memory: 512Mi).
type Quantity string

// UnmarshalJSON implements json.Unmarshaler.
func (q *Quantity) UnmarshalJSON(b []byte) error {
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*q = Quantity(n.String())
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("quantity must be a number or string, got %s", b)
	}
	*q = Quantity(s)
	return nil
}

var restartPolicies = map[string]bool{"always": true, "on-failure": true, "unless-stopped": true, "never": true}

func (s *WorkloadSpec) setDefaults() {
	if s.RestartPolicy == "" {
		s.RestartPolicy = "always"
	}
	for i := range s.Ports {
		if s.Ports[i].Protocol == "" {
			s.Ports[i].Protocol = "tcp"
		}
	}
}

func (s *WorkloadSpec) validate() error {
	var errs []error
	if s.Image == "" {
		errs = append(errs, errors.New("spec.image is required"))
	}
	if !restartPolicies[s.RestartPolicy] {
		errs = append(errs, fmt.Errorf("spec.restartPolicy: %q must be one of always, on-failure, unless-stopped, never", s.RestartPolicy))
	}
	for k := range s.Environment {
		if k == "" || strings.Contains(k, "=") {
			errs = append(errs, fmt.Errorf("spec.environment: invalid variable name %q", k))
		}
	}
	for i, p := range s.Ports {
		if p.ContainerPort < 1 || p.ContainerPort > 65535 {
			errs = append(errs, fmt.Errorf("spec.ports[%d].containerPort: %d out of range 1-65535", i, p.ContainerPort))
		}
		if p.HostPort < 0 || p.HostPort > 65535 {
			errs = append(errs, fmt.Errorf("spec.ports[%d].hostPort: %d out of range 1-65535", i, p.HostPort))
		}
		if p.Protocol != "tcp" && p.Protocol != "udp" {
			errs = append(errs, fmt.Errorf("spec.ports[%d].protocol: %q must be tcp or udp", i, p.Protocol))
		}
	}
	for i, m := range s.Mounts {
		set := 0
		for _, v := range []string{m.Volume, m.Config, m.Secret} {
			if v != "" {
				set++
			}
		}
		if set != 1 {
			errs = append(errs, fmt.Errorf("spec.mounts[%d]: exactly one of volume, config or secret is required", i))
		}
		if !path.IsAbs(m.MountPath) {
			errs = append(errs, fmt.Errorf("spec.mounts[%d].mountPath: %q must be an absolute path", i, m.MountPath))
		}
	}
	if hc := s.HealthCheck; hc != nil {
		if hc.Type != "exec" {
			errs = append(errs, fmt.Errorf("spec.healthCheck.type: %q is not supported (want exec)", hc.Type))
		}
		if len(hc.Command) == 0 {
			errs = append(errs, errors.New("spec.healthCheck.command is required"))
		}
		for name, d := range map[string]string{"interval": hc.Interval, "timeout": hc.Timeout} {
			if d == "" {
				continue
			}
			if v, err := time.ParseDuration(d); err != nil || v <= 0 {
				errs = append(errs, fmt.Errorf("spec.healthCheck.%s: %q is not a positive duration", name, d))
			}
		}
		if hc.Retries < 0 {
			errs = append(errs, errors.New("spec.healthCheck.retries must not be negative"))
		}
	}
	if r := s.Resources; r != nil {
		if r.Memory != "" {
			if _, err := ParseMemory(string(r.Memory)); err != nil {
				errs = append(errs, fmt.Errorf("spec.resources.memory: %w", err))
			}
		}
		if r.CPU != "" {
			if _, err := ParseCPU(string(r.CPU)); err != nil {
				errs = append(errs, fmt.Errorf("spec.resources.cpu: %w", err))
			}
		}
	}
	return errors.Join(errs...)
}

// VolumeSpec is persistent storage (tech design §6.2).
type VolumeSpec struct {
	Driver       string   `json:"driver,omitempty"`
	MountOptions []string `json:"mountOptions,omitempty"`
	// Retention decides what uninstall does: retain (default) or delete.
	Retention string `json:"retention,omitempty"`
}

func (s *VolumeSpec) setDefaults() {
	if s.Driver == "" {
		s.Driver = "local"
	}
	if s.Retention == "" {
		s.Retention = "retain"
	}
}

func (s *VolumeSpec) validate() error {
	if s.Retention != "retain" && s.Retention != "delete" {
		return fmt.Errorf("spec.retention: %q must be retain or delete", s.Retention)
	}
	return nil
}

// NetworkSpec is a container network (tech design §6.3).
type NetworkSpec struct {
	Driver   string `json:"driver,omitempty"`
	Internal bool   `json:"internal,omitempty"`
}

func (s *NetworkSpec) setDefaults() {
	if s.Driver == "" {
		s.Driver = "bridge"
	}
}

func (s *NetworkSpec) validate() error { return nil }

// ConfigSpec holds configuration files mounted read-only (tech design §6.4).
type ConfigSpec struct {
	Files map[string]string `json:"files"`
}

func (s *ConfigSpec) setDefaults() {}

func (s *ConfigSpec) validate() error { return validateFileNames("spec.files", s.Files) }

// SecretSpec holds sensitive values mounted as read-only files (tech design §6.5).
type SecretSpec struct {
	Values map[string]string `json:"values"`
}

func (s *SecretSpec) setDefaults() {}

func (s *SecretSpec) validate() error { return validateFileNames("spec.values", s.Values) }

// validateFileNames checks keys that become file names in a mount.
func validateFileNames(field string, m map[string]string) error {
	if len(m) == 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	var errs []error
	for k := range m {
		if k == "" || k == "." || k == ".." || strings.ContainsAny(k, `/\`) {
			errs = append(errs, fmt.Errorf("%s: %q is not a valid file name", field, k))
		}
	}
	return errors.Join(errs...)
}

package resource

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hasankaradmir/karga/internal/domain/pkg"
)

// WorkloadSpec runs one container (tech design §6.1). Replicas are intentionally unsupported.
type WorkloadSpec struct {
	Image         string `json:"image"`
	RestartPolicy string `json:"restartPolicy,omitempty"`
	// PullPolicy is always, if-not-present (default) or never.
	PullPolicy  string            `json:"pullPolicy,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Ports       []Port            `json:"ports,omitempty"`
	// Networks lists Network resources the workload joins.
	Networks    []string     `json:"networks,omitempty"`
	Mounts      []Mount      `json:"mounts,omitempty"`
	HealthCheck *HealthCheck `json:"healthCheck,omitempty"`
	Resources   *Resources   `json:"resources,omitempty"`
	// Logging rotates the container's log so it cannot fill the disk.
	Logging *Logging `json:"logging,omitempty"`
	// Security hardens the container; no-new-privileges is on unless turned off.
	Security *Security `json:"security,omitempty"`
	// UpdateStrategy controls how an upgrade replaces the container. It changes deploy behaviour
	// only, so it is not part of the workload's digest.
	UpdateStrategy *UpdateStrategy `json:"updateStrategy,omitempty"`
	// StopTimeout is how long the container gets to shut down after SIGTERM before it is killed
	// (default DefaultStopTimeout). Not written as a default, so adding it to karga recreated nothing.
	StopTimeout string `json:"stopTimeout,omitempty"`
	// AnonymousVolumes are the image VOLUME paths the current container keeps in anonymous volumes
	// (no Volume is mounted there). It is found in the runtime before planning, not written in a
	// package, and is not part of the spec.
	AnonymousVolumes []string `json:"-"`
}

// Update strategies (tech design §11).
const (
	StrategyRecreate = "recreate"
	StrategyReplace  = "replace"
)

// DefaultStopTimeout is how long a container gets to shut down when no stopTimeout is set.
const DefaultStopTimeout = 10 * time.Second

// MaxStopTimeout bounds stopTimeout.
const MaxStopTimeout = time.Hour

// StopGrace returns the stop timeout, with the default applied.
func (s *WorkloadSpec) StopGrace() time.Duration {
	if d, err := time.ParseDuration(s.StopTimeout); err == nil && d > 0 {
		return d
	}
	return DefaultStopTimeout
}

// DefaultDrainTimeout is how long the old container gets to stop gracefully after a replace.
const DefaultDrainTimeout = 10 * time.Second

// UpdateStrategy selects recreate (stop, then start) or replace (start, then stop).
type UpdateStrategy struct {
	Type string `json:"type,omitempty"`
	// DrainTimeout is how long a replace waits for the old container's connections to finish
	// (behind karga's proxies) and then its graceful stop timeout.
	DrainTimeout string `json:"drainTimeout,omitempty"`
}

// Strategy returns the requested update strategy and drain timeout, with defaults applied.
func (s *WorkloadSpec) Strategy() (string, time.Duration) {
	typ, drain := StrategyRecreate, DefaultDrainTimeout
	if u := s.UpdateStrategy; u != nil {
		if u.Type != "" {
			typ = u.Type
		}
		if d, err := time.ParseDuration(u.DrainTimeout); err == nil && d > 0 {
			drain = d
		}
	}
	return typ, drain
}

// Port declares a port the container listens on. It is reachable only through a Service; nothing
// is published on the host.
type Port struct {
	// Name is how a Service's targetPort refers to the port.
	Name          string `json:"name"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

// PortNamed returns the port called name, or the port whose containerPort is the number in name.
func (s *WorkloadSpec) PortNamed(name string) (Port, bool) {
	for _, p := range s.Ports {
		if p.Name == name || strconv.Itoa(p.ContainerPort) == name {
			return p, true
		}
	}
	return Port{}, false
}

// Mount attaches exactly one of a Volume, Config or Secret at MountPath. Config and Secret are read-only.
type Mount struct {
	Volume    string `json:"volume,omitempty"`
	Config    string `json:"config,omitempty"`
	Secret    string `json:"secret,omitempty"`
	MountPath string `json:"mountPath"`
	// ReadOnly mounts a volume read-only. Config and Secret mounts are always read-only.
	ReadOnly bool `json:"readOnly,omitempty"`
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
	// Type is exec (Command runs inside the container), tcp (a connection to Port opens) or http
	// (GET Path on Port answers 2xx/3xx). tcp and http need no tool in the image: karga checks them.
	Type    string   `json:"type"`
	Command []string `json:"command,omitempty"`
	// Port defaults to the first tcp containerPort; Path to "/".
	Port     int    `json:"port,omitempty"`
	Path     string `json:"path,omitempty"`
	Interval string `json:"interval,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
	Retries  int    `json:"retries,omitempty"`
	// StartPeriod is a grace period after start during which failed checks do not count.
	StartPeriod string `json:"startPeriod,omitempty"`
}

// Health check types.
const (
	HealthExec = "exec"
	HealthTCP  = "tcp"
	HealthHTTP = "http"
)

// Resources limits the workload.
type Resources struct {
	Memory Quantity `json:"memory,omitempty"`
	CPU    Quantity `json:"cpu,omitempty"`
	// Pids caps the number of processes (default DefaultPids; 0 removes the limit).
	Pids *int64 `json:"pids,omitempty"`
}

// Security restricts what the container's processes may do.
type Security struct {
	// NoNewPrivileges blocks gaining privileges through setuid binaries (default true).
	NoNewPrivileges *bool `json:"noNewPrivileges,omitempty"`
	// ReadOnlyRootFilesystem mounts the image read-only; /tmp stays writable (tmpfs).
	ReadOnlyRootFilesystem bool `json:"readOnlyRootFilesystem,omitempty"`
	// CapDrop and CapAdd change Linux capabilities, e.g. [ALL] and [NET_BIND_SERVICE].
	CapDrop []string `json:"capDrop,omitempty"`
	CapAdd  []string `json:"capAdd,omitempty"`
	// User runs the process as "uid[:gid]" or a user of the image instead of its default.
	User string `json:"user,omitempty"`
}

var (
	capability = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	userSpec   = regexp.MustCompile(`^[A-Za-z0-9_.-]+(:[A-Za-z0-9_.-]+)?$`)
)

// Logging is the container log rotation: at most MaxFiles files of MaxSize (e.g. 10m).
type Logging struct {
	MaxSize  string `json:"maxSize,omitempty"`
	MaxFiles int    `json:"maxFiles,omitempty"`
}

// Default log rotation: at most 30MB per container.
const (
	DefaultLogMaxSize  = "10m"
	DefaultLogMaxFiles = 3
)

var logSize = regexp.MustCompile(`^[1-9][0-9]*[kmg]$`)

// DefaultPids keeps a fork bomb in one container from exhausting the host.
const DefaultPids int64 = 1024

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

var (
	restartPolicies = map[string]bool{"always": true, "on-failure": true, "unless-stopped": true, "never": true}
	pullPolicies    = map[string]bool{"always": true, "if-not-present": true, "never": true}
)

var routeHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

func (s *WorkloadSpec) setDefaults() {
	if s.RestartPolicy == "" {
		s.RestartPolicy = "always"
	}
	if s.PullPolicy == "" {
		s.PullPolicy = "if-not-present"
	}
	if s.Resources == nil {
		s.Resources = &Resources{}
	}
	if s.Resources.Pids == nil {
		p := DefaultPids
		s.Resources.Pids = &p
	}
	if hc := s.HealthCheck; hc != nil && (hc.Type == HealthTCP || hc.Type == HealthHTTP) {
		if hc.Port == 0 {
			for _, p := range s.Ports {
				if p.Protocol == "tcp" || p.Protocol == "" {
					hc.Port = p.ContainerPort
					break
				}
			}
		}
		if hc.Type == HealthHTTP && hc.Path == "" {
			hc.Path = "/"
		}
	}
	if s.Security == nil {
		s.Security = &Security{}
	}
	if s.Security.NoNewPrivileges == nil {
		on := true
		s.Security.NoNewPrivileges = &on
	}
	for _, caps := range [][]string{s.Security.CapDrop, s.Security.CapAdd} {
		for i, c := range caps {
			caps[i] = strings.TrimPrefix(strings.ToUpper(c), "CAP_")
		}
	}
	if s.Logging == nil {
		s.Logging = &Logging{}
	}
	if s.Logging.MaxSize == "" {
		s.Logging.MaxSize = DefaultLogMaxSize
	}
	if s.Logging.MaxFiles == 0 {
		s.Logging.MaxFiles = DefaultLogMaxFiles
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
	if !pullPolicies[s.PullPolicy] {
		errs = append(errs, fmt.Errorf("spec.pullPolicy: %q must be one of always, if-not-present, never", s.PullPolicy))
	}
	for k := range s.Environment {
		if k == "" || strings.Contains(k, "=") {
			errs = append(errs, fmt.Errorf("spec.environment: invalid variable name %q", k))
		}
	}
	names := map[string]bool{}
	for i, p := range s.Ports {
		if !pkg.IsDNSLabel(p.Name) {
			errs = append(errs, fmt.Errorf("spec.ports[%d].name: %q must be a DNS label (a Service refers to the port by it)", i, p.Name))
		}
		if names[p.Name] {
			errs = append(errs, fmt.Errorf("spec.ports[%d].name: %q is used twice", i, p.Name))
		}
		names[p.Name] = true
		if p.ContainerPort < 1 || p.ContainerPort > 65535 {
			errs = append(errs, fmt.Errorf("spec.ports[%d].containerPort: %d out of range 1-65535", i, p.ContainerPort))
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
	if s.StopTimeout != "" {
		if d, err := time.ParseDuration(s.StopTimeout); err != nil || d < time.Second || d > MaxStopTimeout {
			errs = append(errs, fmt.Errorf("spec.stopTimeout: %q must be a duration from 1s to %s", s.StopTimeout, MaxStopTimeout))
		}
	}
	if u := s.UpdateStrategy; u != nil {
		if u.Type != "" && u.Type != StrategyRecreate && u.Type != StrategyReplace {
			errs = append(errs, fmt.Errorf("spec.updateStrategy.type: %q must be recreate or replace", u.Type))
		}
		if u.DrainTimeout != "" {
			if d, err := time.ParseDuration(u.DrainTimeout); err != nil || d <= 0 {
				errs = append(errs, fmt.Errorf("spec.updateStrategy.drainTimeout: %q is not a positive duration", u.DrainTimeout))
			}
		}
	}
	if hc := s.HealthCheck; hc != nil {
		switch hc.Type {
		case HealthExec:
			if len(hc.Command) == 0 {
				errs = append(errs, errors.New("spec.healthCheck.command is required"))
			}
		case HealthTCP, HealthHTTP:
			if len(hc.Command) > 0 {
				errs = append(errs, fmt.Errorf("spec.healthCheck.command: not used by type %s", hc.Type))
			}
			if !slices.ContainsFunc(s.Ports, func(p Port) bool { return p.ContainerPort == hc.Port && p.Protocol == "tcp" }) {
				errs = append(errs, fmt.Errorf("spec.healthCheck.port: %d is not a tcp containerPort of the workload", hc.Port))
			}
			if hc.Type == HealthHTTP && !strings.HasPrefix(hc.Path, "/") {
				errs = append(errs, fmt.Errorf("spec.healthCheck.path: %q must start with /", hc.Path))
			}
		default:
			errs = append(errs, fmt.Errorf("spec.healthCheck.type: %q is not supported (want exec, tcp or http)", hc.Type))
		}
		for name, d := range map[string]string{"interval": hc.Interval, "timeout": hc.Timeout, "startPeriod": hc.StartPeriod} {
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
		if r.Pids != nil && *r.Pids < 0 {
			errs = append(errs, fmt.Errorf("spec.resources.pids: %d must be 0 (no limit) or positive", *r.Pids))
		}
	}
	if sec := s.Security; sec != nil {
		for field, caps := range map[string][]string{"capDrop": sec.CapDrop, "capAdd": sec.CapAdd} {
			for _, c := range caps {
				if !capability.MatchString(c) {
					errs = append(errs, fmt.Errorf("spec.security.%s: %q is not a capability name", field, c))
				}
			}
		}
		if sec.User != "" && !userSpec.MatchString(sec.User) {
			errs = append(errs, fmt.Errorf("spec.security.user: %q must be uid[:gid] or a user name", sec.User))
		}
	}
	if l := s.Logging; l != nil {
		if !logSize.MatchString(l.MaxSize) {
			errs = append(errs, fmt.Errorf("spec.logging.maxSize: %q must be a size like 10m (k, m or g)", l.MaxSize))
		}
		if l.MaxFiles < 1 {
			errs = append(errs, fmt.Errorf("spec.logging.maxFiles: %d must be at least 1", l.MaxFiles))
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

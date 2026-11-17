package resource

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/hasankaradmir/karga-sdk/pkg"
)

// ServiceSpec gives one workload a stable name inside the release, served by the release proxy,
// and can expose its ports on the host's gateway listeners. A Workload alone is reachable by no one.
type ServiceSpec struct {
	// Workload is the name of the Workload the service sends traffic to.
	Workload string        `json:"workload"`
	Ports    []ServicePort `json:"ports"`
	// Expose publishes service ports on listeners of the host gateway, which the host's
	// administrator defines (karga gateway listener add); a package cannot open a listener.
	Expose []Exposure `json:"expose,omitempty"`
}

// ServicePort is a port of the service's name. TargetPort is a port name or containerPort of the
// workload; it defaults to Name.
type ServicePort struct {
	Name       string `json:"name"`
	Port       int    `json:"port"`
	TargetPort string `json:"targetPort,omitempty"`
}

// Exposure publishes a service port on a gateway listener. On an http or https listener Host (and
// Path, a prefix) select the service; a tcp listener belongs to one service.
type Exposure struct {
	Port     string `json:"port"`
	Listener string `json:"listener"`
	Host     string `json:"host,omitempty"`
	Path     string `json:"path,omitempty"`
	// TLS names the Secret with the certificate (tls.crt) and key (tls.key); https listeners need it.
	TLS *ExposureTLS `json:"tls,omitempty"`
}

// ExposureTLS names a Secret holding a certificate chain and its private key.
type ExposureTLS struct {
	Secret string `json:"secret"`
}

// TLS secret keys.
const (
	TLSCertKey = "tls.crt"
	TLSKeyKey  = "tls.key"
)

// HTTP reports whether the exposure is a route (selected by host and path) rather than a tcp port.
func (e Exposure) HTTP() bool { return e.Host != "" }

// PortNamed returns the service port called name.
func (s *ServiceSpec) PortNamed(name string) (ServicePort, bool) {
	i := slices.IndexFunc(s.Ports, func(p ServicePort) bool { return p.Name == name })
	if i < 0 {
		return ServicePort{}, false
	}
	return s.Ports[i], true
}

// TLSSecrets returns the Secrets the service's exposures use, without duplicates.
func (s *ServiceSpec) TLSSecrets() []string {
	var out []string
	for _, e := range s.Expose {
		if e.TLS != nil && !slices.Contains(out, e.TLS.Secret) {
			out = append(out, e.TLS.Secret)
		}
	}
	return out
}

func (s *ServiceSpec) setDefaults() {
	for i := range s.Ports {
		if s.Ports[i].TargetPort == "" {
			s.Ports[i].TargetPort = s.Ports[i].Name
		}
	}
	for i := range s.Expose {
		if s.Expose[i].Host != "" && s.Expose[i].Path == "" {
			s.Expose[i].Path = "/"
		}
	}
}

func (s *ServiceSpec) validate() error {
	var errs []error
	if !pkg.IsDNSLabel(s.Workload) {
		errs = append(errs, fmt.Errorf("spec.workload: %q must name a Workload", s.Workload))
	}
	if len(s.Ports) == 0 {
		errs = append(errs, errors.New("spec.ports: at least one port is required"))
	}
	names, numbers := map[string]bool{}, map[int]bool{}
	for i, p := range s.Ports {
		if !pkg.IsDNSLabel(p.Name) {
			errs = append(errs, fmt.Errorf("spec.ports[%d].name: %q must be a DNS label", i, p.Name))
		}
		if names[p.Name] {
			errs = append(errs, fmt.Errorf("spec.ports[%d].name: %q is used twice", i, p.Name))
		}
		names[p.Name] = true
		if p.Port < 1 || p.Port > 65535 {
			errs = append(errs, fmt.Errorf("spec.ports[%d].port: %d out of range 1-65535", i, p.Port))
		}
		if numbers[p.Port] {
			errs = append(errs, fmt.Errorf("spec.ports[%d].port: %d is used twice", i, p.Port))
		}
		numbers[p.Port] = true
		if n, err := strconv.Atoi(p.TargetPort); err == nil && (n < 1 || n > 65535) {
			errs = append(errs, fmt.Errorf("spec.ports[%d].targetPort: %d out of range 1-65535", i, n))
		}
	}
	seen := map[string]bool{}
	for i, e := range s.Expose {
		at := fmt.Sprintf("spec.expose[%d]", i)
		if !names[e.Port] {
			errs = append(errs, fmt.Errorf("%s.port: %q is not a port of the service", at, e.Port))
		}
		if !pkg.IsDNSLabel(e.Listener) {
			errs = append(errs, fmt.Errorf("%s.listener: %q must name a gateway listener", at, e.Listener))
		}
		if e.Host == "" {
			if e.Path != "" || e.TLS != nil {
				errs = append(errs, fmt.Errorf("%s: path and tls need a host (a tcp listener takes neither)", at))
			}
		} else {
			if !routeHost.MatchString(e.Host) {
				errs = append(errs, fmt.Errorf("%s.host: %q is not a lowercase host name", at, e.Host))
			}
			if !strings.HasPrefix(e.Path, "/") {
				errs = append(errs, fmt.Errorf("%s.path: %q must start with /", at, e.Path))
			}
		}
		if e.TLS != nil && !pkg.IsDNSLabel(e.TLS.Secret) {
			errs = append(errs, fmt.Errorf("%s.tls.secret: %q must name a Secret", at, e.TLS.Secret))
		}
		key := e.Listener + " " + e.Host + e.Path
		if seen[key] {
			errs = append(errs, fmt.Errorf("%s: listener %s already serves %s", at, e.Listener, strings.TrimSpace(e.Host+e.Path)))
		}
		seen[key] = true
	}
	return errors.Join(errs...)
}

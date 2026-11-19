package resource

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
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

// Exposure publishes a service port on a gateway listener. On an http or https listener Host and
// Path select the service; a tcp listener belongs to one service.
type Exposure struct {
	Port     string `json:"port"`
	Listener string `json:"listener"`
	Host     string `json:"host,omitempty"`
	// Path selects the requests of the host the service gets (default "/"), by PathType: "prefix"
	// (the default: the path and below it, by segment, so /api is not /apix) or "exact".
	Path     string `json:"path,omitempty"`
	PathType string `json:"pathType,omitempty"`
	// To (with a prefix) replaces the path's prefix in what the service receives: to "/" serves
	// /api/orders of path /api as /orders.
	To string `json:"to,omitempty"`
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

// Path types.
const (
	PathPrefix = "prefix"
	PathExact  = "exact"
)

// MatchPath is the exposure's path as it is matched: a prefix without its trailing "/" ("/" stays
// "/").
func (e Exposure) MatchPath() string {
	if e.PathType == PathExact || e.Path == "/" {
		return e.Path
	}
	return strings.TrimSuffix(e.Path, "/")
}

// RouteKey is what an exposure takes on its listener: its host, path and path type; two exposures
// of a listener may not take the same (none for a tcp listener).
func (e Exposure) RouteKey() string {
	if e.Host == "" {
		return ""
	}
	return e.Host + e.MatchPath() + " (" + e.PathType + ")"
}

// validatePath checks an exposure's path, path type and to.
func validatePath(at string, e Exposure) error {
	switch {
	case !strings.HasPrefix(e.Path, "/"):
		return fmt.Errorf("%s.path: %q must start with /", at, e.Path)
	case e.PathType != PathPrefix && e.PathType != PathExact:
		return fmt.Errorf("%s.pathType: %q must be prefix or exact", at, e.PathType)
	case e.To != "" && e.PathType == PathExact:
		return fmt.Errorf("%s.to: %q needs pathType prefix", at, e.To)
	case e.To != "" && !strings.HasPrefix(e.To, "/"):
		return fmt.Errorf("%s.to: %q must start with /", at, e.To)
	}
	return nil
}

// route is RouteKey, for the messages.
func (e Exposure) route() string { return e.RouteKey() }

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

// Kind implements Spec.
func (*ServiceSpec) Kind() string { return KindService }

func (s *ServiceSpec) setDefaults() {
	for i := range s.Ports {
		if s.Ports[i].TargetPort == "" {
			s.Ports[i].TargetPort = s.Ports[i].Name
		}
	}
	for i := range s.Expose {
		if s.Expose[i].Host == "" {
			continue
		}
		if s.Expose[i].Path == "" {
			s.Expose[i].Path = "/"
		}
		if s.Expose[i].PathType == "" {
			s.Expose[i].PathType = PathPrefix
		}
	}
}

func (s *ServiceSpec) validate() error {
	var errs []error
	if !IsDNSLabel(s.Workload) {
		errs = append(errs, fmt.Errorf("spec.workload: %q must name a Workload", s.Workload))
	}
	if len(s.Ports) == 0 {
		errs = append(errs, errors.New("spec.ports: at least one port is required"))
	}
	names, numbers := map[string]bool{}, map[int]bool{}
	for i, p := range s.Ports {
		if !IsDNSLabel(p.Name) {
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
		if !IsDNSLabel(e.Listener) {
			errs = append(errs, fmt.Errorf("%s.listener: %q must name a gateway listener", at, e.Listener))
		}
		if e.Host == "" {
			if e.Path != "" || e.PathType != "" || e.To != "" || e.TLS != nil {
				errs = append(errs, fmt.Errorf("%s: path, pathType, to and tls need a host (a tcp listener takes none)", at))
			}
		} else {
			if !routeHost.MatchString(e.Host) {
				errs = append(errs, fmt.Errorf("%s.host: %q is not a lowercase host name", at, e.Host))
			}
			errs = append(errs, validatePath(at, e))
		}
		if e.TLS != nil && !IsDNSLabel(e.TLS.Secret) {
			errs = append(errs, fmt.Errorf("%s.tls.secret: %q must name a Secret", at, e.TLS.Secret))
		}
		key := e.Listener + " " + e.route()
		if seen[key] {
			errs = append(errs, fmt.Errorf("%s: listener %s already serves %s", at, e.Listener, e.route()))
		}
		seen[key] = true
	}
	return errors.Join(errs...)
}

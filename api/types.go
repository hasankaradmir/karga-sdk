package api

import (
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"time"

	"github.com/hasankaradmir/karga-sdk/packages"
	"github.com/hasankaradmir/karga-sdk/pkg"
	"github.com/hasankaradmir/karga-sdk/plan"
	"github.com/hasankaradmir/karga-sdk/release"
)

// DefaultHistoryMax is how many revisions a release keeps unless told otherwise.
const DefaultHistoryMax = 10

// DefaultTimeout bounds how long an operation waits for workloads to become ready.
const DefaultTimeout = 5 * time.Minute

// KindProxy is the kind of the release proxy in plans and status reports; packages do not write it.
const KindProxy = "Proxy"

// Listener protocols.
const (
	ProtocolTCP   = "tcp"
	ProtocolHTTP  = "http"
	ProtocolHTTPS = "https"
)

// DefaultListen is where the gateway listens until the administrator sets addresses.
const DefaultListen = "127.0.0.1"

// CommonOptions apply to every mutating command.
type CommonOptions struct {
	DryRun     bool
	Timeout    time.Duration
	HistoryMax int
	// Force aborts an interrupted operation (whose process is gone) instead of refusing to run.
	Force bool
	// DeleteVolumes allows a plan to delete volumes with retention delete, and their data.
	DeleteVolumes bool
	// Progress, if set, receives what the deployment is doing as it happens.
	Progress func(plan.Event) `json:"-"`
}

// Result describes what a mutating command did.
type Result struct {
	Release  string         `json:"release"`
	Revision int            `json:"revision"`
	From     int            `json:"from"`
	Status   release.Status `json:"status"`
	DryRun   bool           `json:"dryRun,omitempty"`
	// NoOp is set when an install found the release already deployed with the same package and values.
	NoOp bool      `json:"noop,omitempty"`
	Plan plan.Plan `json:"plan"`
	// Warnings are problems that did not stop the operation.
	Warnings []string `json:"warnings,omitempty"`
	// Notes is the package's NOTES.txt for this revision, shown after a successful apply.
	Notes string `json:"notes,omitempty"`
}

// InstallRequest installs a package as a new release.
type InstallRequest struct {
	CommonOptions
	Name    string
	Package string
	Values  packages.Options
}

// UpgradeRequest moves a release to a new package version or new values.
type UpgradeRequest struct {
	CommonOptions
	Name    string
	Package string
	Values  packages.Options
	// ReuseValues starts from the current revision's overrides (A9).
	ReuseValues bool
	// Install installs the release if it does not exist.
	Install bool
	// Strategy overrides the workloads' updateStrategy (recreate or replace); empty uses the spec.
	Strategy string
}

// RollbackRequest re-applies an earlier revision as a new revision.
type RollbackRequest struct {
	CommonOptions
	Name string
	// Revision 0 selects the last deployed revision before the current state.
	Revision int
	// Strategy overrides the workloads' updateStrategy; empty uses the spec.
	Strategy string
}

// UninstallRequest removes a release's runtime objects.
type UninstallRequest struct {
	Force         bool
	Name          string
	DeleteVolumes bool
	KeepHistory   bool
}

// LogsRequest selects containers to show logs of: the workloads of a release (all, or the named
// ones), its proxy, or the host
type LogsRequest struct {
	Release   string
	Workloads []string
	Proxy     bool
	Gateway   bool
	// Tail is the number of lines per container from the end (negative: all).
	Tail   int
	Follow bool
}

// VolumeBackupRequest copies a volume's files out through a workload that mounts it. Stop stops
// the workloads mounting it for a consistent copy; otherwise the copy is taken live.
type VolumeBackupRequest struct {
	Release, Volume string
	Stop            bool
	// W receives the backup (a gzip-compressed tar). The caller owns the file it may write to.
	W io.Writer `json:"-"`
}

// VolumeRestoreRequest writes a backup into a volume, overwriting files with the same names; the
// workloads mounting it are stopped meanwhile. Force restores a backup of another volume.
type VolumeRestoreRequest struct {
	Release, Volume string
	Force           bool
	// Name names the backup in messages (its file name); R reads it.
	Name string
	R    io.Reader `json:"-"`
}

// ResourceStatus compares one applied resource with what the runtime reports.
type ResourceStatus struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	RuntimeName string `json:"runtimeName"`
	Revision    int    `json:"revision"`
	// Desired is "applied", or "failed" when the last attempt to apply it failed.
	Desired string `json:"desired"`
	// Actual is the runtime state: running/healthy, exited, present or missing.
	Actual string `json:"actual"`
	Health string `json:"health,omitempty"`
	// Backend is the alias a proxy routes to.
	Backend string `json:"backend,omitempty"`
	// Restarts counts restarts by the runtime; OOMKilled says the last run hit the memory limit.
	Restarts  int  `json:"restarts,omitempty"`
	OOMKilled bool `json:"oomKilled,omitempty"`
}

// StatusReport is the desired vs actual state of a release (tech design §10.3).
type StatusReport struct {
	Release   release.Release  `json:"release"`
	Resources []ResourceStatus `json:"resources"`
	// Orphans are runtime objects labelled with the release that karga does not track.
	Orphans []string `json:"orphans,omitempty"`
	// Interrupted describes an operation that is still marked running.
	Interrupted string `json:"interrupted,omitempty"`
	// Reconcile is the last reconciliation and what karga gave up repairing.
	Reconcile *ReconcileStatus `json:"reconcile,omitempty"`
}

// ReconcileStatus is the reconciliation part of a status report.
type ReconcileStatus struct {
	Last   time.Time         `json:"last,omitempty"`
	Status string            `json:"status,omitempty"`
	Error  string            `json:"error,omitempty"`
	GaveUp map[string]string `json:"gaveUp,omitempty"`
}

// RevisionDiff is how two revisions of a release differ.
type RevisionDiff struct {
	Release   string              `json:"release"`
	From      int                 `json:"from"`
	To        int                 `json:"to"`
	Package   []plan.FieldChange  `json:"package,omitempty"`
	Values    []plan.FieldChange  `json:"values,omitempty"`
	Resources []plan.ResourceDiff `json:"resources"`
}

// ReconcileResult is what one reconciliation of a release did.
type ReconcileResult struct {
	Release string `json:"release"`
	// Skipped says why nothing was looked at.
	Skipped string         `json:"skipped,omitempty"`
	Actions []release.Step `json:"actions,omitempty"`
	// GaveUp lists the resources karga stopped repairing, with their last error.
	GaveUp map[string]string `json:"gaveUp,omitempty"`
}

// Listener is a port the gateway serves on every listen address. A tcp listener belongs to one
// service; http and https listeners are shared by host and path.
type Listener struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

// Settings are the host administrator's gateway: where it listens and on which listeners. They
// outlive the gateway container. Packages cannot change them.
type Settings struct {
	// Listen are the host addresses every listener binds (default 127.0.0.1: nothing is reachable
	// from other machines until the administrator says so).
	Listen    []string   `json:"listen,omitempty"`
	Listeners []Listener `json:"listeners"`
	AccessLog bool       `json:"accessLog"`
}

// State is the health part of `karga gateway status`.
type State struct {
	// Container is the gateway container's state, or "missing" when no release needs it.
	Container string   `json:"container"`
	Ready     bool     `json:"ready"`
	Version   string   `json:"version,omitempty"`
	Image     string   `json:"image,omitempty"`
	Listen    []string `json:"listen"`
	AccessLog bool     `json:"accessLog"`
	// Error says why the admin API could not be read, if it could not.
	Error string `json:"error,omitempty"`
}

// Exposed is one line of `karga gateway status`: a service port served on a listener.
type Exposed struct {
	Listener string `json:"listener"` // name:port
	Route    string `json:"route"`    // "tcp" or host+path
	Release  string `json:"release"`
	Service  string `json:"service"` // name:port
	Upstream string `json:"upstream"`
}

// Listener returns the listener called name.
func (s Settings) Listener(name string) (Listener, bool) {
	i := slices.IndexFunc(s.Listeners, func(l Listener) bool { return l.Name == name })
	if i < 0 {
		return Listener{}, false
	}
	return s.Listeners[i], true
}

// Validate checks the settings.
func (s Settings) Validate() error {
	var errs []error
	for _, ip := range s.Listen {
		if net.ParseIP(ip) == nil {
			errs = append(errs, fmt.Errorf("listen: %q is not an IP address", ip))
		}
	}
	names, portsUsed := map[string]bool{}, map[int]string{}
	for _, l := range s.Listeners {
		switch {
		case !pkg.IsDNSLabel(l.Name):
			errs = append(errs, fmt.Errorf("listener %q: the name must be a DNS label", l.Name))
		case names[l.Name]:
			errs = append(errs, fmt.Errorf("listener %q is defined twice", l.Name))
		}
		names[l.Name] = true
		if l.Port < 1 || l.Port > 65535 {
			errs = append(errs, fmt.Errorf("listener %q: port %d out of range 1-65535", l.Name, l.Port))
		} else if other, dup := portsUsed[l.Port]; dup {
			errs = append(errs, fmt.Errorf("listener %q: port %d is used by listener %q", l.Name, l.Port, other))
		}
		portsUsed[l.Port] = l.Name
		if l.Protocol != ProtocolTCP && l.Protocol != ProtocolHTTP && l.Protocol != ProtocolHTTPS {
			errs = append(errs, fmt.Errorf("listener %q: protocol %q must be tcp, http or https", l.Name, l.Protocol))
		}
	}
	return errors.Join(errs...)
}

// DefaultListeners are the listeners a new host starts with.
func DefaultListeners() []Listener {
	return []Listener{{Name: "web", Port: 80, Protocol: ProtocolHTTP}, {Name: "websecure", Port: 443, Protocol: ProtocolHTTPS}}
}

// ExecOptions describe a command run in a workload. With TTY the output is one stream (Stdout) and
// Width/Height and Resize set the terminal size; without it Stdout and Stderr are separate.
type ExecOptions struct {
	Cmd            []string
	TTY            bool
	Stdin          io.Reader // nil: no input
	Stdout, Stderr io.Writer
	Width, Height  uint
	Resize         <-chan [2]uint // width, height
}

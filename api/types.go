package api

import (
	"io"
	"time"
)

// KindProxy is the kind of the release proxy in plans and status reports; packages do not write it.
const KindProxy = "Proxy"

// Listener protocols.
const (
	ProtocolTCP   = "tcp"
	ProtocolHTTP  = "http"
	ProtocolHTTPS = "https"
)

// CommonOptions apply to every mutating command. Zero values leave the choice to the server.
type CommonOptions struct {
	DryRun bool `json:"dryRun,omitempty"`
	// Timeout bounds how long the operation waits for workloads to become ready (0: the server's default).
	Timeout Duration `json:"timeout,omitempty"`
	// HistoryMax is how many revisions the release keeps (nil: the server's default; 0: unlimited).
	HistoryMax *int `json:"historyMax,omitempty"`
	// Force aborts an interrupted operation (whose process is gone) instead of refusing to run.
	Force bool `json:"force,omitempty"`
	// DeleteVolumes allows a plan to delete volumes with retention delete, and their data.
	DeleteVolumes bool `json:"deleteVolumes,omitempty"`
	// Confirmed says the user agreed to what the request's Risk describes (see Risk).
	Confirmed bool `json:"confirmed,omitempty"`
}

// Duration is a time.Duration that travels as text such as "5m0s".
type Duration time.Duration

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	*d = Duration(v)
	return err
}

// Set is a --set expression, or a --set-string one when String.
type Set struct {
	Expr   string `json:"expr"`
	String bool   `json:"string,omitempty"`
}

// ValuesInput are the values a user gives an install or upgrade, applied in this order onto the
// package defaults.
type ValuesInput struct {
	// Files are the contents of values files (-f).
	Files [][]byte `json:"files,omitempty"`
	// Sets are --set and --set-string expressions such as a.b=1, in the order they were given.
	Sets []Set `json:"sets,omitempty"`
	// Strict rejects values the schema does not declare.
	Strict bool `json:"strict,omitempty"`
}

// Result describes what a mutating command did.
type Result struct {
	Release  string `json:"release"`
	Revision int    `json:"revision"`
	From     int    `json:"from"`
	Status   string `json:"status"`
	DryRun   bool   `json:"dryRun,omitempty"`
	// NoOp is set when an install found the release already deployed with the same package and values.
	NoOp bool `json:"noop,omitempty"`
	Plan Plan `json:"plan"`
	// Warnings are problems that did not stop the operation.
	Warnings []string `json:"warnings,omitempty"`
	// Notes is the package's NOTES.txt for this revision, shown after a successful apply.
	Notes string `json:"notes,omitempty"`
}

// InstallRequest installs a package as a new release. Package is a directory or .tgz archive
// where the call runs: the client sends its content, the server reads it from a temporary file.
type InstallRequest struct {
	CommonOptions
	Name    string      `json:"name"`
	Package string      `json:"-"`
	Values  ValuesInput `json:"values"`
}

// UpgradeRequest moves a release to a new package version or new values.
type UpgradeRequest struct {
	CommonOptions
	Name    string      `json:"name"`
	Package string      `json:"-"` // as in InstallRequest
	Values  ValuesInput `json:"values"`
	// ReuseValues starts from the current revision's overrides.
	ReuseValues bool `json:"reuseValues,omitempty"`
	// Install installs the release if it does not exist.
	Install bool `json:"install,omitempty"`
	// Strategy overrides the workloads' updateStrategy (recreate or replace); empty uses the spec.
	Strategy string `json:"strategy,omitempty"`
}

// RollbackRequest re-applies an earlier revision as a new revision.
type RollbackRequest struct {
	CommonOptions
	Name string `json:"name"`
	// Revision 0 selects the last deployed revision before the current state.
	Revision int `json:"revision,omitempty"`
	// Strategy overrides the workloads' updateStrategy; empty uses the spec.
	Strategy string `json:"strategy,omitempty"`
}

// UninstallRequest removes a release's runtime objects.
type UninstallRequest struct {
	Name          string `json:"name"`
	Force         bool   `json:"force,omitempty"`
	DeleteVolumes bool   `json:"deleteVolumes,omitempty"`
	KeepHistory   bool   `json:"keepHistory,omitempty"`
	DryRun        bool   `json:"dryRun,omitempty"`
	Confirmed     bool   `json:"confirmed,omitempty"` // see Risk
}

// LogsRequest selects containers to show logs of: the workloads of a release (all, or the named
// ones), its proxy, or the host
type LogsRequest struct {
	Release   string   `json:"release,omitempty"`
	Workloads []string `json:"workloads,omitempty"`
	Proxy     bool     `json:"proxy,omitempty"`
	Gateway   bool     `json:"gateway,omitempty"`
	// Tail is the number of lines per container from the end (negative: all).
	Tail   int  `json:"tail,omitempty"`
	Follow bool `json:"follow,omitempty"`
}

// VolumeBackupRequest copies a volume's files out, as a gzip-compressed tar, through a workload that mounts it. Stop stops
// the workloads mounting it for a consistent copy; otherwise the copy is taken live.
type VolumeBackupRequest struct {
	Release string `json:"release"`
	Volume  string `json:"volume"`
	Stop    bool   `json:"stop,omitempty"`
}

// VolumeRestoreRequest writes a backup into a volume, overwriting files with the same names; the
// workloads mounting it are stopped meanwhile. Force restores a backup of another volume.
type VolumeRestoreRequest struct {
	Release string `json:"release"`
	Volume  string `json:"volume"`
	Force   bool   `json:"force,omitempty"`
	// Name names the backup in messages (its file name).
	Name      string `json:"name,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"` // see Risk
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

// What ResourceStatus says. Desired is what the state records; Actual what the runtime has (a
// container's state for workloads and proxies); Health a workload's health check.
const (
	DesiredApplied = "applied"
	DesiredFailed  = "failed" // the resource's last apply failed

	ActualCreated   = "created"
	ActualRunning   = "running"
	ActualExited    = "exited"
	ActualMissing   = "missing"
	ActualPresent   = "present"    // a Network or Volume exists
	ActualRouted    = "routed"     // a Service's ports go to a backend
	ActualNotRouted = "not routed" // a Service's ports go nowhere yet
	ActualFile      = "file"       // a Config or Secret, kept as files

	HealthNone      = "none" // no health check
	HealthStarting  = "starting"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
)

// Healthy reports whether the resource is applied and runs well: present, not exited, routed to a
// backend (a Service), and not failing or still waiting for its health check.
func (s ResourceStatus) Healthy() bool {
	return s.Desired == DesiredApplied && s.Actual != ActualMissing && s.Actual != ActualExited && s.Actual != ActualNotRouted &&
		s.Health != HealthUnhealthy && s.Health != HealthStarting
}

// Healthy reports whether the release is deployed, no operation on it was interrupted, and every
// resource is healthy. karga status exits 6 when it is not.
func (r *StatusReport) Healthy() bool {
	if r.Release.Status != StatusDeployed || r.Interrupted != "" {
		return false
	}
	for _, x := range r.Resources {
		if !x.Healthy() {
			return false
		}
	}
	return true
}

// StatusReport is the desired vs actual state of a release.
type StatusReport struct {
	Release   Release          `json:"release"`
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
	Release   string         `json:"release"`
	From      int            `json:"from"`
	To        int            `json:"to"`
	Package   []FieldChange  `json:"package,omitempty"`
	Values    []FieldChange  `json:"values,omitempty"`
	Resources []ResourceDiff `json:"resources"`
}

// ReconcileResult is what one reconciliation of a release did.
type ReconcileResult struct {
	Release string `json:"release"`
	// Skipped says why nothing was looked at.
	Skipped string `json:"skipped,omitempty"`
	Actions []Step `json:"actions,omitempty"`
	// GaveUp lists the resources karga stopped repairing, with their last error.
	GaveUp map[string]string `json:"gaveUp,omitempty"`
}

// Listener is a port the gateway serves on every listen address. A tcp listener belongs to one
// service; http and https listeners are shared by host and path.
type Listener struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	ListenerOptions
}

// ListenerOptions are the administrator's limits of a listener, shared by every route on it. The
// server parses and checks them; empty means none (IdleTimeout: 5m).
type ListenerOptions struct {
	// Timeout (http, https) bounds a whole request, e.g. "30s": the gateway answers 504 after it.
	Timeout string `json:"timeout,omitempty"`
	// IdleTimeout closes a request or a tcp connection that sends nothing either way for this long.
	IdleTimeout string `json:"idleTimeout,omitempty"`
	// MaxBody (http, https) refuses larger request bodies with 413, e.g. "10Mi".
	MaxBody string `json:"maxBody,omitempty"`
	// RateLimit (http, https) is the listener's requests per second or minute, e.g. "100/s"; the
	// gateway answers 429 beyond it.
	RateLimit string `json:"rateLimit,omitempty"`
	// ProxyProtocol (tcp) sends the client's address to the backend first: "v1" or "v2".
	ProxyProtocol string `json:"proxyProtocol,omitempty"`
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
	// Revision is the gateway settings revision in force (0: none applied, the defaults).
	Revision int `json:"revision,omitempty"`
	// Error says why the admin API could not be read, if it could not.
	Error string `json:"error,omitempty"`
}

// GatewayPlan is what applying a gateway document would change. Problems refuse it; Risk (when
// not empty) needs the user's confirmation.
type GatewayPlan struct {
	Changes  []GatewayChange `json:"changes"`
	Problems []string        `json:"problems,omitempty"`
	Risk     string          `json:"risk,omitempty"`
}

// GatewayRevision is one applied revision of the gateway settings: who applied it, and how
// (Source: "apply", "listener add pg", "rollback 3").
type GatewayRevision struct {
	Revision int       `json:"revision"`
	Time     time.Time `json:"time"`
	Actor    string    `json:"actor,omitempty"`
	Source   string    `json:"source"`
}

// GatewayChange is one difference between gateway.yaml and the running settings. Kind is
// "listener", "listen" or "accessLog"; Action "add", "remove" or "change"; Fields say what
// changes ("timeout: 30s -> 60s").
type GatewayChange struct {
	Kind   string   `json:"kind"`
	Name   string   `json:"name,omitempty"`
	Action string   `json:"action"`
	Fields []string `json:"fields,omitempty"`
}

// Exposed is one line of `karga gateway status`: a service port served on a listener.
type Exposed struct {
	Listener string `json:"listener"` // name:port
	Route    string `json:"route"`    // "tcp" or host+path
	Release  string `json:"release"`
	Service  string `json:"service"` // name:port
	Upstream string `json:"upstream"`
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

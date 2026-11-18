// Package api carries karga's release operations between the karga CLI and karga-server over HTTP
// on a local unix socket. The server owns the state and the runtime; the CLI holds none of it.
package api

import (
	"context"
	"io"
	"strconv"
	"strings"

	"github.com/hasankaradmir/karga-sdk/resource"
)

// Backend is what the CLI asks of karga-server: the server implements it, Client calls it over the
// socket. It is the sum of the smaller interfaces below; code that needs only some of it takes the
// one it uses. progress, if not nil, receives what a deployment is doing as it happens.
type Backend interface {
	Deployer
	Reader
	Streamer
	Reconciler
	GatewayAdmin
}

// Deployer changes what a release runs.
type Deployer interface {
	Install(ctx context.Context, req InstallRequest, progress func(Event)) (*Result, error)
	Upgrade(ctx context.Context, req UpgradeRequest, progress func(Event)) (*Result, error)
	Rollback(ctx context.Context, req RollbackRequest, progress func(Event)) (*Result, error)
	Uninstall(ctx context.Context, req UninstallRequest) (*Result, error)
}

// Reader answers questions about releases and changes nothing.
type Reader interface {
	List(ctx context.Context) ([]Release, error)
	History(ctx context.Context, req ReleaseRequest) ([]Revision, error)
	Values(ctx context.Context, req ValuesRequest) (map[string]any, error)
	Notes(ctx context.Context, req RevisionRequest) (string, error)
	Manifest(ctx context.Context, req RevisionRequest) ([]resource.Resource, error)
	Status(ctx context.Context, req ReleaseRequest) (*StatusReport, error)
	Diff(ctx context.Context, req DiffRequest) (*RevisionDiff, error)
	Resources(ctx context.Context, req ReleaseRequest) ([]AppliedResource, error)
	Events(ctx context.Context, req EventsRequest) ([]Operation, error)
}

// Streamer moves data in and out of a release's containers.
type Streamer interface {
	Logs(ctx context.Context, req LogsRequest, w io.Writer) error
	Exec(ctx context.Context, req ExecRequest) (int, error)
	BackupVolume(ctx context.Context, req VolumeBackupRequest, w io.Writer) error
	RestoreVolume(ctx context.Context, req VolumeRestoreRequest, r io.Reader) error
}

// Reconciler controls the repairs of a release.
type Reconciler interface {
	Reconcile(ctx context.Context, req ReconcileRequest) (*ReconcileResult, error)
	Pause(ctx context.Context, req ReleaseRequest) error
	Resume(ctx context.Context, req ReleaseRequest) error
}

// GatewayAdmin reads and changes the host gateway.
type GatewayAdmin interface {
	GatewayRoutes(ctx context.Context) ([]Exposed, error)
	GatewayHealth(ctx context.Context) (State, error)
	GatewaySettings(ctx context.Context) (Settings, map[string][]string, error)
	SetGatewayAccessLog(ctx context.Context, req GatewayAccessLogRequest) error
	SetGatewayListen(ctx context.Context, req GatewayListenRequest) error
	AddGatewayListener(ctx context.Context, l Listener) error
	RemoveGatewayListener(ctx context.Context, req GatewayListenerRequest) error
}

// Remote is a Backend reached over the socket: it also knows the server's version.
type Remote interface {
	Backend
	Version(ctx context.Context) (ServerVersion, error)
}

// DefaultSocket is where karga-server listens unless --socket or KARGA_SOCKET say otherwise.
const DefaultSocket = "/run/karga/karga.sock"

// Request and response bodies of the endpoints that need more than the releases types.
type (
	// deployRequest is the body of an install, upgrade or rollback; Package is the package as a .tgz.
	deployRequest[Req any] struct {
		Request Req    `json:"request"`
		Package []byte `json:"package,omitempty"`
	}
	// deployLine is one line of a deploy response: progress events, then the outcome.
	deployLine struct {
		Event  *Event  `json:"event,omitempty"`
		Result *Result `json:"result,omitempty"`
		Error  string  `json:"error,omitempty"`
		Code   string  `json:"code,omitempty"`
		Done   bool    `json:"done,omitempty"`
	}
	execRequest struct {
		Release  string   `json:"release"`
		Workload string   `json:"workload"`
		Cmd      []string `json:"cmd"`
		TTY      bool     `json:"tty,omitempty"`
		Stdin    bool     `json:"stdin,omitempty"`
		Width    uint     `json:"width,omitempty"`
		Height   uint     `json:"height,omitempty"`
	}
	gatewaySettings struct {
		Settings Settings            `json:"settings"`
		Users    map[string][]string `json:"users"`
	}
	errorResponse struct {
		Error string `json:"error"`
		Code  string `json:"code,omitempty"`
	}
)

// trailerError and trailerCode carry an error that happened after a streamed response started:
// its message and its kind.
const (
	trailerError = "X-Karga-Error"
	trailerCode  = "X-Karga-Error-Code"
)

// The API version is Major.Minor. A karga CLI and a karga-server work together when their Major is
// the same, whatever their release versions. A minor version only adds: new endpoints, and new
// request fields whose zero value keeps the old behaviour. A newer client still works with an older
// server of the same Major; what the server lacks fails with a message to upgrade it.
const (
	Major = 3
	Minor = 0
)

// Version is the API version this package speaks: Major.Minor.
const Version = "3.0"

const apiHeader = "Karga-Api"

// ServerVersion is what karga-server reports about itself.
type ServerVersion struct {
	Version string `json:"version"`
	// API is the server's API version, Major.Minor.
	API string `json:"api"`
}

// Compatible reports whether a client speaking API version client works with this server.
func Compatible(client string) bool {
	major, _, _ := strings.Cut(client, ".")
	return major == strconv.Itoa(Major)
}

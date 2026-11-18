// Package api carries karga's release operations between the karga CLI and karga-server over HTTP
// on a local unix socket. The server owns the state and the runtime; the CLI holds none of it.
package api

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/hasankaradmir/karga-sdk/resource"
)

// Backend is what the CLI asks of karga-server: the server implements it, Client calls it over the
// socket. progress, if not nil, receives what a deployment is doing as it happens.
type Backend interface {
	Install(ctx context.Context, req InstallRequest, progress func(Event)) (*Result, error)
	Upgrade(ctx context.Context, req UpgradeRequest, progress func(Event)) (*Result, error)
	Rollback(ctx context.Context, req RollbackRequest, progress func(Event)) (*Result, error)
	Uninstall(ctx context.Context, req UninstallRequest) (*Result, error)

	List(ctx context.Context) ([]Release, error)
	History(ctx context.Context, name string) ([]Revision, error)
	GetValues(ctx context.Context, name string, revision int, all bool) (map[string]any, error)
	GetNotes(ctx context.Context, name string, revision int) (string, error)
	GetManifest(ctx context.Context, name string, revision int) ([]resource.Resource, error)
	Status(ctx context.Context, name string) (*StatusReport, error)
	Diff(ctx context.Context, name string, from, to int) (*RevisionDiff, error)
	Resources(ctx context.Context, name string) ([]AppliedResource, error)

	Logs(ctx context.Context, req LogsRequest, w io.Writer) error
	Exec(ctx context.Context, rel, workload string, opts ExecOptions) (int, error)
	BackupVolume(ctx context.Context, req VolumeBackupRequest, w io.Writer) error
	RestoreVolume(ctx context.Context, req VolumeRestoreRequest, r io.Reader) error
	BackupState(ctx context.Context, w io.Writer) error

	Reconcile(ctx context.Context, name string, force bool) (*ReconcileResult, error)
	Pause(ctx context.Context, name string, paused bool) error
	Events(ctx context.Context, name string, afterID int64, limit int) ([]Operation, error)

	Gateway(ctx context.Context) ([]Exposed, error)
	GatewayHealth(ctx context.Context) (State, error)
	GatewaySettings(ctx context.Context) (Settings, map[string][]string, error)
	SetGatewayAccessLog(ctx context.Context, on bool) error
	SetGatewayListen(ctx context.Context, addrs []string) error
	AddGatewayListener(ctx context.Context, l Listener) error
	RemoveGatewayListener(ctx context.Context, name string) error
}

// DefaultSocket is where karga-server listens unless --socket or KARGA_SOCKET say otherwise.
const DefaultSocket = "/run/karga/karga.sock"

// Request and response bodies of the endpoints that need more than the releases types.
type (
	nameRequest struct {
		Name     string `json:"name"`
		Force    bool   `json:"force,omitempty"`
		Paused   bool   `json:"paused,omitempty"`
		After    int64  `json:"after,omitempty"`
		Limit    int    `json:"limit,omitempty"`
		Revision int    `json:"revision,omitempty"`
		All      bool   `json:"all,omitempty"`
		From     int    `json:"from,omitempty"`
		To       int    `json:"to,omitempty"`
	}
	// deployRequest carries an install, upgrade or rollback; Package is the package as a .tgz.
	deployRequest struct {
		Install  *InstallRequest  `json:"install,omitempty"`
		Upgrade  *UpgradeRequest  `json:"upgrade,omitempty"`
		Rollback *RollbackRequest `json:"rollback,omitempty"`
		Package  []byte           `json:"package,omitempty"`
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
		TTY      bool     `json:"tty"`
		Stdin    bool     `json:"stdin"`
		Width    uint     `json:"width,omitempty"`
		Height   uint     `json:"height,omitempty"`
	}
	gatewaySettings struct {
		Settings Settings            `json:"settings"`
		Users    map[string][]string `json:"users"`
	}
	gatewayChange struct {
		AccessLog *bool     `json:"accessLog,omitempty"`
		Listen    []string  `json:"listen,omitempty"`
		Add       *Listener `json:"add,omitempty"`
		Remove    string    `json:"remove,omitempty"`
	}
	errorResponse struct {
		Error string `json:"error"`
		Code  string `json:"code,omitempty"`
	}
)

// trailerError carries an error that happened after a streamed response started.
const trailerError = "X-Karga-Error"

// The API version is Major.Minor. A karga CLI and a karga-server work together when their Major is
// the same, whatever their release versions. A minor version only adds: new endpoints, and new
// request fields whose zero value keeps the old behaviour. A newer client still works with an older
// server of the same Major; what the server lacks fails with a message to upgrade it.
const (
	Major = 2
	Minor = 0
)

// Version is the API version this package speaks, e.g. "2.0".
var Version = fmt.Sprintf("%d.%d", Major, Minor)

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

// Package api carries karga's release operations between the karga CLI and karga-server over HTTP
// on a local unix socket. The server owns the state and the runtime; the CLI holds none of it.
package api

import (
	"context"
	"encoding/json"
	"io"

	"github.com/hasankaradmir/karga/internal/application/deployment/gateway"
	"github.com/hasankaradmir/karga/internal/application/releases"
	"github.com/hasankaradmir/karga-sdk/release"
	"github.com/hasankaradmir/karga-sdk/resource"
	"github.com/hasankaradmir/karga/internal/ports"
)

// Backend is what the CLI asks of karga: *releases.Service on the server, *Client in the CLI.
// Install and Upgrade read req.Package and req.Values.ValueFiles where they run: the client packs
// them and sends their content.
type Backend interface {
	Install(ctx context.Context, req releases.InstallRequest) (*releases.Result, error)
	Upgrade(ctx context.Context, req releases.UpgradeRequest) (*releases.Result, error)
	Rollback(ctx context.Context, req releases.RollbackRequest) (*releases.Result, error)
	Uninstall(ctx context.Context, req releases.UninstallRequest) error

	List(ctx context.Context) ([]release.Release, error)
	History(ctx context.Context, name string) ([]release.Revision, error)
	GetValues(ctx context.Context, name string, revision int, all bool) (map[string]any, error)
	GetNotes(ctx context.Context, name string, revision int) (string, error)
	GetManifest(ctx context.Context, name string, revision int) ([]resource.Resource, error)
	Status(ctx context.Context, name string) (*releases.StatusReport, error)
	Diff(ctx context.Context, name string, from, to int) (*releases.RevisionDiff, error)
	Resources(ctx context.Context, name string) ([]release.AppliedResource, error)

	Logs(ctx context.Context, req releases.LogsRequest, w io.Writer) error
	Exec(ctx context.Context, rel, workload string, opts ports.ExecOptions) (int, error)
	BackupVolume(ctx context.Context, req releases.VolumeBackupRequest) error
	RestoreVolume(ctx context.Context, req releases.VolumeRestoreRequest) error
	BackupState(ctx context.Context, w io.Writer) error

	Reconcile(ctx context.Context, name string, force bool) (*releases.ReconcileResult, error)
	Pause(ctx context.Context, name string, paused bool) error
	Events(ctx context.Context, name string, afterID int64, limit int) ([]release.Operation, error)

	Gateway(ctx context.Context) ([]gateway.Exposed, error)
	GatewayHealth(ctx context.Context) (gateway.State, error)
	GatewaySettings(ctx context.Context) (gateway.Settings, map[string][]string, error)
	SetGatewayAccessLog(ctx context.Context, on bool) error
	SetGatewayListen(ctx context.Context, addrs []string) error
	AddGatewayListener(ctx context.Context, l gateway.Listener) error
	RemoveGatewayListener(ctx context.Context, name string) error
}

var _ Backend = (*releases.Service)(nil)

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
		Install  *releases.InstallRequest  `json:"install,omitempty"`
		Upgrade  *releases.UpgradeRequest  `json:"upgrade,omitempty"`
		Rollback *releases.RollbackRequest `json:"rollback,omitempty"`
		Package  []byte                    `json:"package,omitempty"`
	}
	// deployLine is one line of a deploy response: progress events, then the outcome.
	deployLine struct {
		Event  *eventJSON       `json:"event,omitempty"`
		Result *releases.Result `json:"result,omitempty"`
		Error  string           `json:"error,omitempty"`
		Done   bool             `json:"done,omitempty"`
	}
	eventJSON struct {
		Kind, Name, Phase, Detail string
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
	volumeRequest struct {
		Release string `json:"release"`
		Volume  string `json:"volume"`
		Stop    bool   `json:"stop,omitempty"`
	}
	gatewaySettings struct {
		Settings gateway.Settings    `json:"settings"`
		Users    map[string][]string `json:"users"`
	}
	gatewayChange struct {
		AccessLog *bool             `json:"accessLog,omitempty"`
		Listen    []string          `json:"listen,omitempty"`
		Add       *gateway.Listener `json:"add,omitempty"`
		Remove    string            `json:"remove,omitempty"`
	}
	// revisionJSON carries the fields of a revision that its own JSON leaves out (history --diff
	// compares them).
	revisionJSON struct {
		release.Revision
		Values    map[string]any  `json:"values"`
		ValuesAll map[string]any  `json:"valuesAll"`
		Manifest  json.RawMessage `json:"manifest"`
	}
	versionResponse struct {
		Version string `json:"version"`
	}
	errorResponse struct {
		Error string `json:"error"`
	}
)

// trailerError carries an error that happened after a streamed response started.
const trailerError = "X-Karga-Error"

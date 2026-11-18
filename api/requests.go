package api

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/hasankaradmir/karga-sdk/resource"
)

// Every endpoint takes its own request type: a request carries only what its endpoint reads, so
// the server's check of unknown fields covers every endpoint. Validate checks what does not need
// the server's state; the server calls it before anything else and answers ErrInvalid.

// ReleaseRequest names one release.
type ReleaseRequest struct {
	Name string `json:"name"`
}

// Validate checks the release name.
func (r ReleaseRequest) Validate() error { return resource.ValidateReleaseName(r.Name) }

// RevisionRequest names a revision of a release (0: the current one).
type RevisionRequest struct {
	Name     string `json:"name"`
	Revision int    `json:"revision,omitempty"`
}

// Validate checks the release name and the revision.
func (r RevisionRequest) Validate() error {
	return errors.Join(resource.ValidateReleaseName(r.Name), nonNegative("revision", r.Revision))
}

// ValuesRequest asks for the values of a revision: the ones given (All false) or all of them with
// the package's defaults.
type ValuesRequest struct {
	Name     string `json:"name"`
	Revision int    `json:"revision,omitempty"`
	All      bool   `json:"all,omitempty"`
}

// Validate checks the release name and the revision.
func (r ValuesRequest) Validate() error {
	return RevisionRequest{Name: r.Name, Revision: r.Revision}.Validate()
}

// DiffRequest compares two revisions of a release (To 0: the current one).
type DiffRequest struct {
	Name string `json:"name"`
	From int    `json:"from"`
	To   int    `json:"to,omitempty"`
}

// Validate checks the release name and the revisions.
func (r DiffRequest) Validate() error {
	err := errors.Join(resource.ValidateReleaseName(r.Name), nonNegative("to", r.To))
	if r.From < 1 {
		err = errors.Join(err, fmt.Errorf("from: revision %d must be at least 1", r.From))
	}
	return err
}

// ReconcileRequest asks for a repair of a release now. Force also repairs what karga gave up on.
type ReconcileRequest struct {
	Name  string `json:"name"`
	Force bool   `json:"force,omitempty"`
}

// Validate checks the release name.
func (r ReconcileRequest) Validate() error { return resource.ValidateReleaseName(r.Name) }

// EventsRequest asks for the operations of a release (every release when Name is empty), newest
// first: those after the operation AfterID, at most Limit of them (0: the server's default).
type EventsRequest struct {
	Name    string `json:"name,omitempty"`
	AfterID int64  `json:"afterId,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// Validate checks the release name, if any, and the bounds.
func (r EventsRequest) Validate() error {
	var err error
	if r.Name != "" {
		err = resource.ValidateReleaseName(r.Name)
	}
	if r.AfterID < 0 {
		err = errors.Join(err, fmt.Errorf("afterId: %d must not be negative", r.AfterID))
	}
	return errors.Join(err, nonNegative("limit", r.Limit))
}

// ExecRequest runs a command in a workload of a release.
type ExecRequest struct {
	Release, Workload string
	ExecOptions
}

// Validate checks the names and the command.
func (r ExecRequest) Validate() error {
	err := errors.Join(resource.ValidateReleaseName(r.Release), dnsLabel("workload", r.Workload))
	if len(r.Cmd) == 0 {
		err = errors.Join(err, errors.New("a command is required"))
	}
	return err
}

// GatewayAccessLogRequest turns the host gateway's access log on or off.
type GatewayAccessLogRequest struct {
	Enabled bool `json:"enabled,omitempty"`
}

// GatewayListenRequest sets the addresses the host gateway listens on (none: the default).
type GatewayListenRequest struct {
	Addresses []string `json:"addresses,omitempty"`
	Confirmed bool     `json:"confirmed,omitempty"` // see Risk
}

// Validate checks the addresses, and the confirmation.
func (r GatewayListenRequest) Validate() error {
	var err error
	for _, a := range r.Addresses {
		if net.ParseIP(a) == nil {
			err = errors.Join(err, fmt.Errorf("addresses: %q is not an IP address", a))
		}
	}
	return errors.Join(err, confirmed(r.Risk(), r.Confirmed))
}

// Risk says what setting the addresses exposes: another machine can reach an address that is not
// a loopback one.
func (r GatewayListenRequest) Risk() string {
	if !slices.ContainsFunc(r.Addresses, func(a string) bool { ip := net.ParseIP(a); return ip != nil && !ip.IsLoopback() }) {
		return ""
	}
	return fmt.Sprintf("every exposed service becomes reachable on %s, from other machines too", strings.Join(r.Addresses, ", "))
}

// GatewayListenerRequest names a listener of the host gateway, to remove.
type GatewayListenerRequest struct {
	Name      string `json:"name"`
	Confirmed bool   `json:"confirmed,omitempty"` // see Risk
}

// Validate checks the listener name, and the confirmation.
func (r GatewayListenerRequest) Validate() error {
	return errors.Join(dnsLabel("listener", r.Name), confirmed(r.Risk(), r.Confirmed))
}

// Risk says what removing the listener does.
func (r GatewayListenerRequest) Risk() string {
	return fmt.Sprintf("this removes listener %s from the gateway", r.Name)
}

// Validate checks the release name.
func (r InstallRequest) Validate() error { return resource.ValidateReleaseName(r.Name) }

// Validate checks the release name and the strategy, and the confirmation.
func (r UpgradeRequest) Validate() error {
	return errors.Join(resource.ValidateReleaseName(r.Name), ValidateStrategy(r.Strategy), confirmed(r.Risk(), r.Confirmed))
}

// Risk says what data the upgrade may delete.
func (r UpgradeRequest) Risk() string { return r.risk(r.Name) }

// Validate checks the release name, the revision and the strategy, and the confirmation.
func (r RollbackRequest) Validate() error {
	return errors.Join(resource.ValidateReleaseName(r.Name), nonNegative("revision", r.Revision), ValidateStrategy(r.Strategy),
		confirmed(r.Risk(), r.Confirmed))
}

// Risk says what data the rollback may delete.
func (r RollbackRequest) Risk() string { return r.risk(r.Name) }

func (o CommonOptions) risk(release string) string {
	if !o.DeleteVolumes || o.DryRun {
		return ""
	}
	return fmt.Sprintf("%s deletes the volumes the new revision no longer has, and their data (a dry run shows which)", release)
}

// confirmed refuses a request with a risk its user did not agree to. Requests that change what
// cannot be changed back (data, what the host exposes) have a Risk: a client asks its user, then
// sets Confirmed.
func confirmed(risk string, ok bool) error {
	if risk == "" || ok {
		return nil
	}
	return fmt.Errorf("confirmation needed: %s", risk)
}

// ValidateStrategy checks a strategy that overrides every workload's updateStrategy: recreate,
// replace, or empty (each workload's own).
func ValidateStrategy(s string) error {
	if s != "" && s != resource.StrategyRecreate && s != resource.StrategyReplace {
		return fmt.Errorf("strategy: %q must be %s or %s", s, resource.StrategyRecreate, resource.StrategyReplace)
	}
	return nil
}

// Validate checks the release name, and the confirmation.
func (r UninstallRequest) Validate() error {
	return errors.Join(resource.ValidateReleaseName(r.Name), confirmed(r.Risk(), r.Confirmed))
}

// Risk says what the uninstall removes.
func (r UninstallRequest) Risk() string {
	switch {
	case r.DryRun:
		return ""
	case r.DeleteVolumes:
		return fmt.Sprintf("this removes release %s, its containers and ALL ITS VOLUMES AND THEIR DATA", r.Name)
	}
	return fmt.Sprintf("this removes release %s and its containers", r.Name)
}

// Validate checks what the logs come from: a release (and its workloads), or the host gateway.
func (r LogsRequest) Validate() error {
	if r.Gateway {
		if r.Release != "" || len(r.Workloads) > 0 || r.Proxy {
			return errors.New("the gateway's logs are asked for without a release")
		}
		return nil
	}
	err := resource.ValidateReleaseName(r.Release)
	for _, w := range r.Workloads {
		err = errors.Join(err, dnsLabel("workload", w))
	}
	return err
}

// Validate checks the names.
func (r VolumeBackupRequest) Validate() error {
	return errors.Join(resource.ValidateReleaseName(r.Release), dnsLabel("volume", r.Volume))
}

// Validate checks the names.
func (r VolumeRestoreRequest) Validate() error {
	err := errors.Join(resource.ValidateReleaseName(r.Release), dnsLabel("volume", r.Volume))
	if len(r.Name) > 255 || strings.ContainsAny(r.Name, "\x00\n\r") {
		err = errors.Join(err, errors.New("name: the backup's name must be one line of at most 255 bytes"))
	}
	return errors.Join(err, confirmed(r.Risk(), r.Confirmed))
}

// Risk says what the restore overwrites.
func (r VolumeRestoreRequest) Risk() string {
	return fmt.Sprintf("this stops the workloads mounting volume %s of %s and overwrites its files with %s", r.Volume, r.Release, cmp.Or(r.Name, "the backup"))
}

// Validate checks the listener.
func (l Listener) Validate() error {
	err := dnsLabel("listener", l.Name)
	if l.Port < 1 || l.Port > 65535 {
		err = errors.Join(err, fmt.Errorf("port: %d must be between 1 and 65535", l.Port))
	}
	return err
}

func nonNegative(field string, n int) error {
	if n < 0 {
		return fmt.Errorf("%s: %d must not be negative", field, n)
	}
	return nil
}

func dnsLabel(field, s string) error {
	if !resource.IsDNSLabel(s) {
		return fmt.Errorf("%s: %q must be a DNS label (lowercase letters, digits and '-')", field, s)
	}
	return nil
}

// validator is a request the server checks before passing it on.
type validator interface{ Validate() error }

// validate checks req if it is a validator; the error is ErrInvalid.
func validate(req any) error {
	if v, ok := req.(validator); ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	return nil
}

package api

import "time"

// The views of releases, revisions, operations and plans that travel between the CLI and the
// server. The server keeps its own state model and maps it to these; they change only with the API.

// Release statuses.
const (
	StatusPending      = "pending"
	StatusDeploying    = "deploying"
	StatusDeployed     = "deployed"
	StatusUpgrading    = "upgrading"
	StatusFailed       = "failed"
	StatusUninstalling = "uninstalling"
	StatusUninstalled  = "uninstalled"
	// StatusUnreadable is a release whose state file karga-server cannot read; its status says why.
	StatusUnreadable = "unreadable"
)

// Release is an installed instance of a package.
type Release struct {
	Name           string `json:"name"`
	PackageName    string `json:"package"`
	PackageVersion string `json:"version"`
	// CurrentRevision is the last successfully deployed revision; 0 if none.
	CurrentRevision int       `json:"revision"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	// Paused releases are left alone by the reconciler.
	Paused bool `json:"paused,omitempty"`
}

// Revision statuses.
const (
	RevisionPending  = "pending"
	RevisionDeployed = "deployed"
	RevisionFailed   = "failed"
	// RevisionSuperseded is a deployed revision a later deployment replaced.
	RevisionSuperseded = "superseded"
)

// Revision is one immutable desired configuration of a release.
type Revision struct {
	Release        string    `json:"release"`
	Number         int       `json:"revision"`
	PackageName    string    `json:"package"`
	PackageVersion string    `json:"version"`
	PackageDigest  string    `json:"packageDigest"`
	ValuesDigest   string    `json:"valuesDigest"`
	ManifestDigest string    `json:"manifestDigest"`
	Status         string    `json:"status"`
	Description    string    `json:"description"`
	CreatedAt      time.Time `json:"createdAt"`
}

// AppliedResource is one resource of a release as it is applied on the runtime.
type AppliedResource struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Revision    int    `json:"revision"`
	RuntimeName string `json:"runtimeName"`
}

// Operation statuses.
const (
	OperationRunning   = "running"
	OperationSucceeded = "succeeded"
	OperationFailed    = "failed"
	OperationAborted   = "aborted"
)

// Operation is a mutating command on a release and what it did.
type Operation struct {
	ID       int64  `json:"id"`
	Release  string `json:"release"`
	Type     string `json:"type"`
	Revision int    `json:"revision"`
	Status   string `json:"status"`
	// Actor is who asked for it: a user of the karga CLI, or karga-server (repairs, resumes).
	Actor string `json:"actor,omitempty"`
	Steps []Step `json:"steps,omitempty"`
	Error string `json:"error,omitempty"`
	// Downtime is how long the operation kept workloads stopped (volume backups and restores).
	Downtime   Duration  `json:"downtime,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
}

// Step is one runtime action of an operation.
type Step struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Actions in a plan or a diff.
const (
	ActionCreate    = "create"
	ActionUpdate    = "update"
	ActionDelete    = "delete"
	ActionUnchanged = "unchanged"
)

// Plan is the ordered set of changes from the applied state to a manifest.
type Plan struct {
	Changes []Change `json:"changes"`
}

// Change is what a plan does to one resource.
type Change struct {
	Kind   string        `json:"kind"`
	Name   string        `json:"name"`
	Action string        `json:"action"`
	Fields []FieldChange `json:"fields,omitempty"`
	// Strategy is how a workload update is applied (recreate or replace); StrategyReason says why
	// a requested replace fell back to recreate.
	Strategy       string `json:"strategy,omitempty"`
	StrategyReason string `json:"strategyReason,omitempty"`
	// Notice says what deleting a volume does to its data ("" for other changes).
	Notice string `json:"notice,omitempty"`
}

// FieldChange is one changed field, as JSON values. Secret values show only that they changed.
type FieldChange struct {
	Path string `json:"path"`
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
}

// ResourceDiff is how one resource differs between two revisions.
type ResourceDiff struct {
	Kind   string        `json:"kind"`
	Name   string        `json:"name"`
	Action string        `json:"action"` // create, update or delete (from the first to the second)
	Fields []FieldChange `json:"fields,omitempty"`
}

// HasChanges reports whether applying the plan would touch the runtime.
func (p Plan) HasChanges() bool {
	for _, c := range p.Changes {
		if c.Action != ActionUnchanged {
			return true
		}
	}
	return false
}

// Label returns the strategy shown next to a change, e.g. "replace" or "recreate: mounts volume x read-write".
func (c Change) Label() string {
	if c.StrategyReason != "" {
		return c.Strategy + ": " + c.StrategyReason
	}
	return c.Strategy
}

// Progress phases of an Event.
const (
	PhasePull   = "pull"
	PhaseCreate = "create"
	PhaseStart  = "start"
	PhaseWait   = "wait"
	PhaseSwitch = "switch"
	PhaseDrain  = "drain"
	PhaseStop   = "stop"
	PhaseRemove = "remove"
	PhaseDone   = "done"
	PhaseFailed = "failed"
)

// Event reports what karga-server is doing during an operation, for a live progress display.
type Event struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Phase  string `json:"phase"`
	Detail string `json:"detail,omitempty"`
}

package plan

// Event reports what karga-server is doing during an operation, for a live progress display.
type Event struct {
	Kind, Name string
	// Phase is one of pull, create, start, wait, switch, drain, stop, remove, done, failed.
	Phase  string
	Detail string
}

// Progress phases.
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

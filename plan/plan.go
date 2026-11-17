// Package plan is what karga-server plans and the CLI shows: changes, their fields and the
// differences between stored revisions.
package plan

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/hasankaradmir/karga-sdk/resource"
)

// Actions in a plan.
const (
	ActionCreate    = "create"
	ActionUpdate    = "update"
	ActionDelete    = "delete"
	ActionUnchanged = "unchanged"
)

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

// FieldChange is one changed field, as JSON values.
type FieldChange struct {
	Path string `json:"path"`
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
}

// Plan is the ordered set of changes from the applied state to a manifest.
type Plan struct {
	Changes []Change `json:"changes"`
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

// FieldChanges lists leaf differences between two resource JSON documents. Lists compare as a whole.
func FieldChanges(kind string, oldJS, newJS json.RawMessage) []FieldChange {
	var o, n any
	_ = json.Unmarshal(oldJS, &o)
	_ = json.Unmarshal(newJS, &n)
	flatOld, flatNew := map[string]string{}, map[string]string{}
	flatten("", o, flatOld)
	flatten("", n, flatNew)
	paths := map[string]bool{}
	for p := range flatOld {
		paths[p] = true
	}
	for p := range flatNew {
		paths[p] = true
	}
	var out []FieldChange
	for p := range paths {
		if flatOld[p] == flatNew[p] {
			continue
		}
		fc := FieldChange{Path: p, Old: flatOld[p], New: flatNew[p]}
		if kind == resource.KindSecret && strings.HasPrefix(p, "spec.values.") {
			fc.Old, fc.New = "", "(changed)"
			if strings.HasPrefix(flatOld[p], `"sha256:`) && strings.HasPrefix(flatNew[p], `"hmac-sha256:`) {
				fc.New = "(digest scheme upgraded)"
			}
		}
		out = append(out, fc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func flatten(prefix string, v any, out map[string]string) {
	if m, ok := v.(map[string]any); ok {
		for k, c := range m {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			flatten(p, c, out)
		}
		return
	}
	js, _ := json.Marshal(v)
	out[prefix] = string(js)
}

// Counts returns how many changes create, update and delete resources, and how many are unchanged.
func (p Plan) Counts() (create, update, del, unchanged int) {
	for _, c := range p.Changes {
		switch c.Action {
		case ActionCreate:
			create++
		case ActionUpdate:
			update++
		case ActionDelete:
			del++
		default:
			unchanged++
		}
	}
	return
}

var actionSymbol = map[string]string{ActionCreate: "+", ActionUpdate: "~", ActionDelete: "-"}

// Summary writes one line per change (+ create, ~ update, - delete; unchanged resources are left
// out) and the counts. Print shows the changed fields too.
func (p Plan) Summary(w io.Writer, releaseName string, from, to int) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Deployment Plan\n\nRelease:  %s\n", releaseName)
	if from == 0 {
		fmt.Fprintf(&b, "Revision: %d\n\n", to)
	} else {
		fmt.Fprintf(&b, "Revision: %d -> %d\n\n", from, to)
	}
	for _, c := range p.Changes {
		sym, ok := actionSymbol[c.Action]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "  %s %s %s", sym, strings.ToUpper(c.Kind), c.Name)
		if c.Strategy != "" {
			fmt.Fprintf(&b, " (%s)", c.Label())
		}
		if n := c.Notice; n != "" {
			fmt.Fprintf(&b, " (%s)", n)
		}
		b.WriteString("\n")
	}
	create, update, del, unchanged := p.Counts()
	if !p.HasChanges() {
		b.WriteString("  No changes.\n")
	}
	fmt.Fprintf(&b, "\n%d to create, %d to update, %d to delete, %d unchanged.", create, update, del, unchanged)
	if update > 0 {
		b.WriteString(" --verbose shows the changed fields.")
	}
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// Print writes the plan in the tech design §10.2 format.
func (p Plan) Print(w io.Writer, releaseName string, from, to int) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Deployment Plan\n\nRelease: %s\n", releaseName)
	if from == 0 {
		fmt.Fprintf(&b, "Revision: %d\n", to)
	} else {
		fmt.Fprintf(&b, "Revision: %d -> %d\n", from, to)
	}
	b.WriteString("\nChanges:\n")
	for _, c := range p.Changes {
		if c.Strategy != "" {
			fmt.Fprintf(&b, "\n  %s %s (%s)\n", strings.ToUpper(c.Kind), c.Name, c.Label())
		} else {
			fmt.Fprintf(&b, "\n  %s %s\n", strings.ToUpper(c.Kind), c.Name)
		}
		switch c.Action {
		case ActionUpdate:
			for _, f := range c.Fields {
				switch {
				case f.Old == "" && f.New == "":
					fmt.Fprintf(&b, "    %s\n", f.Path)
				case f.Old == "":
					fmt.Fprintf(&b, "    %s: %s\n", f.Path, f.New)
				default:
					fmt.Fprintf(&b, "    %s: %s -> %s\n", f.Path, f.Old, f.New)
				}
			}
		default:
			if n := c.Notice; n != "" {
				fmt.Fprintf(&b, "    %s (%s)\n", c.Action, n)
			} else {
				fmt.Fprintf(&b, "    %s\n", c.Action)
			}
		}
	}
	if !p.HasChanges() {
		b.WriteString("\nNo changes.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

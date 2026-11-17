package plan

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hasankaradmir/karga-sdk/resource"
)

// ResourceDiff is how one resource differs between two stored manifests.
type ResourceDiff struct {
	Kind   string        `json:"kind"`
	Name   string        `json:"name"`
	Action string        `json:"action"` // create, update or delete (from the first to the second)
	Fields []FieldChange `json:"fields,omitempty"`
}

// CompareManifests diffs two stored manifests (JSON arrays of resources, secret values already
// digests). Unchanged resources are left out; Secret values show as "(changed)".
func CompareManifests(from, to json.RawMessage) ([]ResourceDiff, error) {
	a, err := manifestDocs(from)
	if err != nil {
		return nil, err
	}
	b, err := manifestDocs(to)
	if err != nil {
		return nil, err
	}
	var out []ResourceDiff
	for k, doc := range b {
		old, ok := a[k]
		switch {
		case !ok:
			out = append(out, ResourceDiff{Kind: k.Kind, Name: k.Name, Action: ActionCreate})
		default:
			if fields := FieldChanges(k.Kind, old, doc); len(fields) > 0 {
				out = append(out, ResourceDiff{Kind: k.Kind, Name: k.Name, Action: ActionUpdate, Fields: fields})
			}
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, ResourceDiff{Kind: k.Kind, Name: k.Name, Action: ActionDelete})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if kindRank[out[i].Kind] != kindRank[out[j].Kind] {
			return kindRank[out[i].Kind] < kindRank[out[j].Kind]
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// CompareValues diffs two values documents field by field.
func CompareValues(from, to map[string]any) []FieldChange {
	a, _ := json.Marshal(from)
	b, _ := json.Marshal(to)
	return FieldChanges("", a, b)
}

func manifestDocs(js json.RawMessage) (map[key]json.RawMessage, error) {
	var docs []json.RawMessage
	if len(js) > 0 {
		if err := json.Unmarshal(js, &docs); err != nil {
			return nil, err
		}
	}
	out := map[key]json.RawMessage{}
	for _, d := range docs {
		var head struct {
			Kind     string            `json:"kind"`
			Metadata resource.Metadata `json:"metadata"`
		}
		if err := json.Unmarshal(d, &head); err != nil {
			return nil, fmt.Errorf("stored manifest: %w", err)
		}
		out[key{head.Kind, head.Metadata.Name}] = d
	}
	return out, nil
}

type key struct{ Kind, Name string }

// kindRank orders resources of different kinds: dependencies first.
var kindRank = map[string]int{
	resource.KindNetwork: 0, resource.KindVolume: 1, resource.KindConfig: 2, resource.KindSecret: 3,
	resource.KindWorkload: 4, resource.KindService: 5,
}

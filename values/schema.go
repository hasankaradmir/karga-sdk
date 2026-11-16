package values

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaURL = "karga:///values.schema.json"

// noLoader refuses every external $ref, so a package schema cannot read host files or the network.
type noLoader struct{}

func (noLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema references are not allowed: %s", url)
}

// Validate checks vals against a JSON Schema. A nil or empty schema accepts everything.
// strict closes every object schema that lists properties but says nothing about additionalProperties.
func Validate(schema []byte, vals map[string]any, strict bool) error {
	if len(bytes.TrimSpace(schema)) == 0 {
		return nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return fmt.Errorf("values.schema.json: %w", err)
	}
	if strict {
		closeObjects(doc)
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(noLoader{})
	if err := c.AddResource(schemaURL, doc); err != nil {
		return fmt.Errorf("values.schema.json: %w", err)
	}
	sch, err := c.Compile(schemaURL)
	if err != nil {
		return fmt.Errorf("values.schema.json: %w", err)
	}

	raw, err := json.Marshal(vals)
	if err != nil {
		return fmt.Errorf("values are not JSON-compatible: %w", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if err := sch.Validate(inst); err != nil {
		// Drop the "validation failed with 'karga:///…'" header line; keep the per-path lines.
		msg := err.Error()
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		return fmt.Errorf("values do not match values.schema.json:\n%s", msg)
	}
	return nil
}

// Keywords whose value is a single subschema, a map of subschemas, or a list of subschemas.
var (
	schemaKeys    = []string{"items", "additionalProperties", "not", "if", "then", "else", "contains", "propertyNames", "unevaluatedProperties", "additionalItems", "unevaluatedItems"}
	schemaMapKeys = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}
	schemaArrKeys = []string{"allOf", "anyOf", "oneOf", "prefixItems"}
)

// closeObjects walks schema keywords (not arbitrary keys, so a property named "properties" is safe).
// shortcut: like any additionalProperties-based strict mode, it rejects valid input split across allOf branches.
func closeObjects(node any) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	if _, hasProps := m["properties"]; hasProps {
		if _, set := m["additionalProperties"]; !set {
			m["additionalProperties"] = false
		}
	}
	for _, k := range schemaKeys {
		closeObjects(m[k])
	}
	for _, k := range schemaMapKeys {
		if sub, ok := m[k].(map[string]any); ok {
			for _, s := range sub {
				closeObjects(s)
			}
		}
	}
	for _, k := range schemaArrKeys {
		if arr, ok := m[k].([]any); ok {
			for _, s := range arr {
				closeObjects(s)
			}
		}
	}
	if arr, ok := m["items"].([]any); ok {
		for _, s := range arr {
			closeObjects(s)
		}
	}
}

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

// subschemas calls fn with every subschema of the schema object m. step is the instance the
// subschema applies to, relative to m's: "" the same instance, Any every key or element, or a
// property name. instance is false for subschemas that apply to no instance by themselves
// (definitions, property names). Both walks of a schema use it, so they see the same keywords.
func subschemas(m map[string]any, fn func(sub any, step string, instance bool)) {
	if props, ok := m["properties"].(map[string]any); ok {
		for name, sub := range props {
			fn(sub, name, true)
		}
	}
	for _, k := range []string{"patternProperties"} {
		if subs, ok := m[k].(map[string]any); ok {
			for _, sub := range subs {
				fn(sub, Any, true) // patterns are not evaluated: this may mask more than needed, never less
			}
		}
	}
	for _, k := range []string{"additionalProperties", "unevaluatedProperties", "additionalItems", "unevaluatedItems", "contains"} {
		fn(m[k], Any, true)
	}
	// items is a schema, or a list of them (draft 4 to 2019-09); prefixItems is a list.
	for _, k := range []string{"items", "prefixItems"} {
		if arr, ok := m[k].([]any); ok {
			for _, sub := range arr {
				fn(sub, Any, true)
			}
		} else {
			fn(m[k], Any, true)
		}
	}
	for _, k := range []string{"allOf", "anyOf", "oneOf"} {
		if arr, ok := m[k].([]any); ok {
			for _, sub := range arr {
				fn(sub, "", true)
			}
		}
	}
	for _, k := range []string{"if", "then", "else", "not"} {
		fn(m[k], "", true)
	}
	// dependencies (draft 7) maps a property to a schema or to a list of names.
	for _, k := range []string{"dependentSchemas", "dependencies"} {
		if subs, ok := m[k].(map[string]any); ok {
			for _, sub := range subs {
				fn(sub, "", true)
			}
		}
	}
	for _, k := range []string{"$defs", "definitions"} {
		if subs, ok := m[k].(map[string]any); ok {
			for _, sub := range subs {
				fn(sub, "", false)
			}
		}
	}
	fn(m["propertyNames"], "", false)
}

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
	subschemas(m, func(sub any, _ string, _ bool) { closeObjects(sub) })
}

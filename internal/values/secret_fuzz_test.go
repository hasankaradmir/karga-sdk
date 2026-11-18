package values

import "testing"

// FuzzSecretPaths checks that no schema panics the walk, recursive ones included.
func FuzzSecretPaths(f *testing.F) {
	f.Add([]byte(`{"properties":{"db":{"properties":{"password":{"type":"string","writeOnly":true}}}}}`))
	f.Add([]byte(`{"$defs":{"n":{"properties":{"next":{"$ref":"#/$defs/n"},"k":{"writeOnly":true}}}},"$ref":"#/$defs/n"}`))
	f.Add([]byte(`{"items":{"additionalProperties":{"writeOnly":true}}}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, schema []byte) {
		paths, err := SecretPaths(schema)
		if err != nil {
			return
		}
		for _, p := range paths {
			if len(p) == 0 {
				t.Fatalf("empty path from %s", schema)
			}
		}
	})
}

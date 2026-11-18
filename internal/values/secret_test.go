package values

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestSecretPaths(t *testing.T) {
	schema := `{
	  "$defs": {"secret": {"type": "string", "writeOnly": true}},
	  "properties": {
	    "database": {"properties": {
	      "password": {"type": "string", "writeOnly": true},
	      "user": {"type": "string"}
	    }},
	    "token": {"$ref": "#/$defs/secret"},
	    "auth": {"allOf": [{"properties": {"key": {"writeOnly": true}}}]},
	    "loop": {"$ref": "#"},
	    "passwords": {"type": "object", "additionalProperties": {"type": "string", "writeOnly": true}},
	    "users": {"type": "array", "items": {"properties": {"token": {"writeOnly": true}}}},
	    "keys": {"patternProperties": {"^k": {"writeOnly": true}}},
	    "tls": {"if": {"properties": {"on": {"const": true}}}, "then": {"properties": {"key": {"writeOnly": true}}}}
	  }
	}`
	paths, err := SecretPaths([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range paths {
		got = append(got, strings.Join(p, "."))
	}
	sort.Strings(got)
	if want := []string{"auth.key", "database.password", "keys.*", "passwords.*", "tls.key", "token", "users.*.token"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if p, err := SecretPaths(nil); err != nil || p != nil {
		t.Fatalf("no schema: %v %v", p, err)
	}
}

func TestAt(t *testing.T) {
	v := map[string]any{"users": []any{map[string]any{"token": "a"}, map[string]any{"token": "b"}}, "db": map[string]any{"password": "p"}}
	got := At(v, []string{"users", Any, "token"})
	if !reflect.DeepEqual(got, []any{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	if got := At(v, []string{"db", "password"}); !reflect.DeepEqual(got, []any{"p"}) {
		t.Fatalf("got %v", got)
	}
	if got := At(v, []string{"db", "missing"}); got != nil {
		t.Fatalf("got %v", got)
	}
}

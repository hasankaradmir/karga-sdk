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
	    "loop": {"$ref": "#"}
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
	if want := []string{"auth.key", "database.password", "token"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if p, err := SecretPaths(nil); err != nil || p != nil {
		t.Fatalf("no schema: %v %v", p, err)
	}
}

func TestMaskDropHas(t *testing.T) {
	m := mustDecode(t, "database: {password: hunter2, user: app}\ntoken: null\n")
	paths := [][]string{{"database", "password"}, {"token"}, {"missing", "x"}}
	masked := Mask(m, paths)
	want := mustDecode(t, "database: {password: '<redacted>', user: app}\ntoken: null\n")
	if !reflect.DeepEqual(masked, want) {
		t.Fatalf("mask: %#v", masked)
	}
	if m["database"].(map[string]any)["password"] != "hunter2" {
		t.Fatal("Mask modified its input")
	}
	if rp := RedactedPaths(masked); len(rp) != 1 || strings.Join(rp[0], ".") != "database.password" {
		t.Fatalf("redacted paths: %v", rp)
	}
	dropped := Drop(masked, RedactedPaths(masked))
	if Has(dropped, []string{"database", "password"}) || !Has(dropped, []string{"database", "user"}) || Has(m, []string{"token"}) {
		t.Fatalf("drop/has: %#v", dropped)
	}
}

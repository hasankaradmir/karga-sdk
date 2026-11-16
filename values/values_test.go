package values

import (
	"reflect"
	"testing"
)

func mustDecode(t *testing.T, s string) map[string]any {
	t.Helper()
	m, err := Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDecode(t *testing.T) {
	m := mustDecode(t, "port: 6379\ntag: \"7.4\"\nbig: 1000000\nlist: [1, a]\n")
	want := map[string]any{"port": 6379, "tag": "7.4", "big": 1000000, "list": []any{1, "a"}}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got %#v", m)
	}
	if m := mustDecode(t, ""); len(m) != 0 {
		t.Fatalf("empty doc: got %#v", m)
	}
}

func TestDecodeErrors(t *testing.T) {
	for _, in := range []string{"- a\n- b\n", "a:\n  1: x\n", "a: [b: {1: x}]\n", "a: b: c"} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("Decode(%q): expected error", in)
		}
	}
}

func TestMergePrecedence(t *testing.T) {
	defaults := mustDecode(t, `
image: {repository: redis, tag: "7.4"}
network: {enabled: true, port: 6379}
storage: {enabled: true, size: 5Gi}
ports: [1, 2]
`)
	file1 := mustDecode(t, `
image: {tag: "7.2"}
ports: [3]
storage: null
`)
	file2 := mustDecode(t, `
image: {tag: "7.4.2"}
network: {port: null}
`)
	got := Merge(Merge(defaults, file1), file2)
	want := mustDecode(t, `
image: {repository: redis, tag: "7.4.2"}
network: {enabled: true}
ports: [3]
`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	base := mustDecode(t, "a: {b: 1, l: [1]}\nn: null\n")
	over := mustDecode(t, "a: {b: 2, c: {d: 1}}\n")
	baseCopy := mustDecode(t, "a: {b: 1, l: [1]}\nn: null\n")
	overCopy := mustDecode(t, "a: {b: 2, c: {d: 1}}\n")

	out := Merge(base, over)
	out["a"].(map[string]any)["l"].([]any)[0] = 99
	out["a"].(map[string]any)["c"].(map[string]any)["d"] = 99

	if !reflect.DeepEqual(base, baseCopy) || !reflect.DeepEqual(over, overCopy) {
		t.Fatal("Merge mutated its inputs")
	}
	if _, ok := out["n"]; !ok || out["n"] != nil {
		t.Fatalf("nil default should be kept as nil, got %#v", out["n"])
	}
}

func TestMergeNilBase(t *testing.T) {
	got := Merge(nil, map[string]any{"a": 1})
	if !reflect.DeepEqual(got, map[string]any{"a": 1}) {
		t.Fatalf("got %#v", got)
	}
}

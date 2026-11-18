package values

import (
	"reflect"
	"testing"
)

func TestApplySet(t *testing.T) {
	tests := []struct {
		expr        string
		forceString bool
		want        map[string]any
	}{
		{"a=b", false, map[string]any{"a": "b"}},
		{"image.tag=7.4.2", false, map[string]any{"image": map[string]any{"tag": "7.4.2"}}},
		{"tag=7.4", false, map[string]any{"tag": "7.4"}},
		{"port=6379", false, map[string]any{"port": 6379}},
		{"n=-5", false, map[string]any{"n": -5}},
		{"zip=007", false, map[string]any{"zip": "007"}},
		{"on=true,off=false", false, map[string]any{"on": true, "off": false}},
		{"gone=null", false, map[string]any{"gone": nil}},
		{"port=6379", true, map[string]any{"port": "6379"}},
		{"empty=", false, map[string]any{"empty": ""}},
		{"a.b=1,a.c=2", false, map[string]any{"a": map[string]any{"b": 1, "c": 2}}},
		{"list[1]=x", false, map[string]any{"list": []any{nil, "x"}}},
		{"m[0][1]=x", false, map[string]any{"m": []any{[]any{nil, "x"}}}},
		{"ports[0].host=80", false, map[string]any{"ports": []any{map[string]any{"host": 80}}}},
		{`csv=a\,b`, false, map[string]any{"csv": "a,b"}},
		{`dotted\.key=v`, false, map[string]any{"dotted.key": "v"}},
		{`eq=a=b`, false, map[string]any{"eq": "a=b"}},
		{`esc\=key=v`, false, map[string]any{"esc=key": "v"}},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			m := map[string]any{}
			if err := ApplySet(m, tt.expr, tt.forceString); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(m, tt.want) {
				t.Fatalf("got %#v, want %#v", m, tt.want)
			}
		})
	}
}

func TestApplySetOverwritesScalar(t *testing.T) {
	m := map[string]any{"a": "scalar", "l": []any{1, 2, 3}}
	if err := ApplySet(m, "a.b=1,l[1]=9", false); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"a": map[string]any{"b": 1}, "l": []any{1, 9, 3}}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got %#v", m)
	}
}

func TestApplySetNullDeletesOnMerge(t *testing.T) {
	sets := map[string]any{}
	if err := ApplySet(sets, "storage=null", false); err != nil {
		t.Fatal(err)
	}
	got := Merge(map[string]any{"storage": map[string]any{"size": "5Gi"}, "keep": 1}, sets)
	if !reflect.DeepEqual(got, map[string]any{"keep": 1}) {
		t.Fatalf("got %#v", got)
	}
}

func TestApplySetErrors(t *testing.T) {
	for _, expr := range []string{"novalue", "a..b=1", "=1", "a[x]=1", "a[-1]=1", "a[99999999]=1", "a]=1", "[0]=1"} {
		if err := ApplySet(map[string]any{}, expr, false); err == nil {
			t.Errorf("ApplySet(%q): expected error", expr)
		}
	}
}

func FuzzApplySet(f *testing.F) {
	for _, s := range []string{"a=b", "a.b[0]=1,c=d", `a\,b=c`, "x[3][2].y=null"} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, expr string) {
		_ = ApplySet(map[string]any{}, expr, false)
	})
}

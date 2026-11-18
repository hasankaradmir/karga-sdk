package renderer

import (
	"reflect"
	"strings"
	"testing"
)

func ctx() Context {
	return Context{
		Values: map[string]any{
			"image":  map[string]any{"repository": "redis", "tag": "7.4"},
			"port":   6379,
			"big":    1000000,
			"labels": map[string]any{"b": "2", "a": "1", "c": "3"},
			"extra":  "name: {{ .Release.Name }}",
		},
		Release: Release{Name: "my-redis", Revision: 1},
		Package: PackageInfo{Name: "redis", Version: "1.0.0"},
	}
}

func render(t *testing.T, tpls map[string]string) ([]Output, error) {
	t.Helper()
	in := map[string][]byte{}
	for k, v := range tpls {
		in[k] = []byte(v)
	}
	return Render(in, ctx())
}

func renderOne(t *testing.T, tpl string) string {
	t.Helper()
	out, err := render(t, map[string]string{"x.yaml": tpl})
	if err != nil {
		t.Fatal(err)
	}
	return out[0].Content
}

func TestRenderBasics(t *testing.T) {
	tests := []struct{ tpl, want string }{
		{`{{ .Values.image.repository }}:{{ .Values.image.tag }}`, "redis:7.4"},
		{`{{ .Values.port }} {{ .Values.big }}`, "6379 1000000"},
		{`{{ .Release.Name }}/{{ .Release.Revision }}`, "my-redis/1"},
		{`{{ .Package.Name }}-{{ .Package.Version }}`, "redis-1.0.0"},
		{`[{{ .Values.missing }}]`, "[]"},
		{`{{ if .Values.missing }}yes{{ else }}no{{ end }}`, "no"},
		{`{{ range $k, $v := .Values.labels }}{{ $k }}{{ end }}`, "abc"},
		{`{{ keys .Values.labels | join "," }}`, "a,b,c"},
		{`{{ values .Values.labels | join "," }}`, "1,2,3"},
		{`{{ .Values.labels | toYaml }}`, "a: \"1\"\nb: \"2\"\nc: \"3\""},
		{`{{ dict "a" (dict "b" 1) | toYaml }}`, "a:\n  b: 1"},
		{`{{ (fromYaml "a: 1").a }}`, "1"},
		{`{{ "hello" | upper | quote }}`, `"HELLO"`},
		{`{{ tpl .Values.extra . }}`, "name: my-redis"},
		{`{{ required "need tag" .Values.image.tag }}`, "7.4"},
	}
	for _, tt := range tests {
		if got := renderOne(t, tt.tpl); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.tpl, got, tt.want)
		}
	}
}

func TestNamedTemplatesAndOrder(t *testing.T) {
	out, err := render(t, map[string]string{
		"_common.tpl": `{{ define "redis.name" }}{{ .Release.Name }}-redis{{ end }}`,
		"b.yaml":      `b: {{ include "redis.name" . | upper }}`,
		"a.yaml":      `a: {{ include "redis.name" . }}`,
		"sub/_x.tpl":  `{{ define "x" }}X{{ end }}`,
		"sub/c.yaml":  `c: {{ include "x" . }}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Output{
		{Path: "a.yaml", Content: "a: my-redis-redis"},
		{Path: "b.yaml", Content: "b: MY-REDIS-REDIS"},
		{Path: "sub/c.yaml", Content: "c: X"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %+v", out)
	}
}

func TestRenderErrors(t *testing.T) {
	tests := []struct{ tpl, want string }{
		{`{{ required "database.password is required" .Values.missing }}`, "database.password is required"},
		{`{{ now }}`, `function "now" is not allowed: non-deterministic`},
		{`{{ randAlphaNum 8 }}`, "non-deterministic"},
		{`{{ uuidv4 }}`, "non-deterministic"},
		{`{{ env "HOME" }}`, "reads the host environment"},
		{`{{ getHostByName "example.com" }}`, "network"},
		{`{{ define "loop" }}{{ include "loop" . }}{{ end }}{{ include "loop" . }}`, "nested too deeply"},
		{`{{ include "nope" . }}`, "nope"},
		{`{{ .Values.image.tag`, "x.yaml"},
		{`{{ tpl "{{ .Bad" . }}`, "tpl"},
	}
	for _, tt := range tests {
		_, err := render(t, map[string]string{"x.yaml": tt.tpl})
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: want error containing %q, got %v", tt.tpl, tt.want, err)
		}
	}
}

func TestRenderDeterministic(t *testing.T) {
	tpls := map[string]string{
		"a.yaml": `{{ .Values | toYaml }}{{ range $k, $v := .Values.labels }}{{ $k }}={{ $v }}{{ end }}{{ keys .Values | join "," }}`,
	}
	first, err := render(t, tpls)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		again, err := render(t, tpls)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("output changed between runs:\n%q\n%q", first, again)
		}
	}
}

func TestRenderLimits(t *testing.T) {
	cases := map[string]struct{ tpl, want string }{
		"self tpl":     {`{{ tpl .Values.self . }}`, "nested too deeply"},
		"self include": {`{{ define "a" }}{{ include "a" . }}{{ end }}{{ include "a" . }}`, "nested too deeply"},
		"tpl include":  {`{{ define "a" }}{{ tpl "{{ include \"a\" . }}" . }}{{ end }}{{ include "a" . }}`, "nested too deeply"},
		"until":        {`{{ range until 100000000 }}{{ end }}`, "too large"},
		"untilStep":    {`{{ range untilStep 0 100000000 1 }}{{ end }}`, "too large"},
		"seq":          {`{{ seq 100000000 }}`, "too large"},
		"repeat":       {`{{ repeat 100000000 "x" }}`, "too large"},
		"indent":       {`{{ indent 100000000 "x" }}`, "too large"},
		"output":       {`{{ range until 10000 }}{{ $.Values.kb }}{{ end }}`, "output exceeds"},
		// Loops that write nothing never reach the output limit; their iterations are counted.
		"range int":       {`{{ range 100000 }}{{ range 100000 }}{{ end }}{{ end }}`, "iterations"},
		"nested until":    {`{{ range until 10000 }}{{ range until 10000 }}{{ range until 10000 }}{{ end }}{{ end }}{{ end }}`, "iterations"},
		"range values":    {`{{ range .Values.n }}{{ range $.Values.n }}{{ end }}{{ end }}`, "iterations"},
		"range in define": {`{{ define "a" }}{{ range 100000 }}{{ range 100000 }}{{ end }}{{ end }}{{ end }}{{ include "a" . }}`, "iterations"},
		"range in tpl":    {`{{ tpl "{{ range 100000 }}{{ range 100000 }}{{ end }}{{ end }}" . }}`, "iterations"},
		// Sizes that overflow int must not get past the limits.
		"until MinInt":     {`{{ until -9223372036854775808 }}`, "too large"},
		"untilStep span":   {`{{ untilStep -9223372036854775807 9223372036854775807 1 }}`, "too large"},
		"untilStep MaxInt": {`{{ untilStep 0 9223372036854775807 1 }}`, "too large"},
		"seq MinInt":       {`{{ seq -9223372036854775808 }}`, "too large"},
		"seq MaxInt":       {`{{ seq 9223372036854775807 }}`, "too large"},
		"seq span":         {`{{ seq -9223372036854775807 9223372036854775807 }}`, "too large"},
		"seq step span":    {`{{ seq -9223372036854775807 1 9223372036854775807 }}`, "too large"},
		"repeat overflow":  {`{{ repeat 4611686018427387904 "xxxx" }}`, "too large"},
		"indent overflow":  {`{{ indent 4611686018427387904 "a\nb\nc\nd" }}`, "too large"},
		"nindent overflow": {`{{ nindent 4611686018427387904 "a\nb\nc\nd" }}`, "too large"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cx := ctx()
			cx.Values["self"] = "{{ tpl .Values.self . }}"
			cx.Values["kb"] = strings.Repeat("x", 4096)
			cx.Values["n"] = 1000000
			_, err := Render(map[string][]byte{"x.yaml": []byte(c.tpl)}, cx)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	// Normal use stays allowed.
	if got := renderOne(t, `{{ range until 3 }}{{ . }}{{ end }}{{ repeat 2 "ab" }}{{ indent 2 "x" }}`); got != "012abab  x" {
		t.Fatalf("got %q", got)
	}
	// Steps that would overflow int stop at the last value in range instead of wrapping around.
	if got := renderOne(t, `{{ untilStep 9223372036854775807 -9223372036854775807 -9223372036854775808 }}`); got != "[9223372036854775807 -1]" {
		t.Fatalf("untilStep near the limits: %q", got)
	}
}

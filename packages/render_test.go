package packages

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hasankaradmir/karga-sdk/resource"
)

func renderDemo(t *testing.T, opts Options) (*Manifest, error) {
	t.Helper()
	p, err := Load("testdata/demo")
	if err != nil {
		t.Fatal(err)
	}
	if opts.ReleaseName == "" {
		opts.ReleaseName = "web"
	}
	return Render(p, opts)
}

func kinds(m *Manifest) []string {
	var out []string
	for _, d := range m.Documents {
		out = append(out, d.Resource.Kind+"/"+d.Resource.Metadata.Name)
	}
	return out
}

func TestRenderDemo(t *testing.T) {
	m, err := renderDemo(t, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Sorted template order: storage.yaml (Volume, Config), workload.yaml. secret.yaml renders empty.
	if got, want := strings.Join(kinds(m), ","), "Volume/web-data,Config/web-web,Workload/web-web"; got != want {
		t.Fatalf("documents: %s, want %s", got, want)
	}
	w := m.Documents[2].Resource.Spec.(*resource.WorkloadSpec)
	if w.Image != "nginx:1.27" || w.Ports[0].ContainerPort != 8080 {
		t.Fatalf("workload: %+v", w)
	}
	cfg := m.Documents[1].Resource.Spec.(*resource.ConfigSpec)
	if cfg.Files["default.conf"] != "server { listen 80; }\n" {
		t.Fatalf("config: %q", cfg.Files["default.conf"])
	}
}

func TestRenderValuePrecedence(t *testing.T) {
	f := filepath.Join(t.TempDir(), "prod.yaml")
	if err := os.WriteFile(f, []byte("image: {tag: '1.26'}\nport: 9000\nstorage: {enabled: false}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := renderDemo(t, Options{
		ValueFiles: []string{f},
		Sets:       []string{"port=9100"},
		SetStrings: []string{"image.tag=1.28"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(kinds(m), ","); got != "Config/web-web,Workload/web-web" {
		t.Fatalf("storage.enabled=false from file not applied: %s", got)
	}
	w := m.Documents[1].Resource.Spec.(*resource.WorkloadSpec)
	if w.Image != "nginx:1.28" || w.Ports[0].ContainerPort != 9100 {
		t.Fatalf("precedence wrong: %+v", w)
	}
}

func TestRenderErrors(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"bad release name", Options{ReleaseName: "Web_1"}, "DNS label"},
		{"schema type", Options{Sets: []string{"image.tag=1"}}, "/image/tag"},
		{"strict unknown", Options{Sets: []string{"typo=x"}, Strict: true}, "typo"},
		{"missing values file", Options{ValueFiles: []string{"nope.yaml"}}, "nope.yaml"},
		{"bad set", Options{Sets: []string{"novalue"}}, "missing '='"},
		{"invalid resource", Options{Sets: []string{"port=70000"}}, "templates/workload.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := renderDemo(t, tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
	if _, err := renderDemo(t, Options{Sets: []string{"typo=x"}}); err != nil {
		t.Fatalf("unknown value must pass without --strict: %v", err)
	}
}

func TestRedactedDocuments(t *testing.T) {
	m, err := renderDemo(t, Options{Sets: []string{"password=hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	var masked, plain bytes.Buffer
	docs, err := m.RedactedDocuments()
	if err != nil {
		t.Fatal(err)
	}
	printDocs(&masked, m.Package.Name, docs)
	printDocs(&plain, m.Package.Name, m.Documents)
	if strings.Contains(masked.String(), "hunter2") || !strings.Contains(masked.String(), "password: <redacted>") {
		t.Fatalf("secret not masked:\n%s", masked.String())
	}
	if !strings.Contains(plain.String(), "hunter2") {
		t.Fatalf("--show-secrets lost the value:\n%s", plain.String())
	}
	if !strings.Contains(masked.String(), "# Source: demo/templates/secret.yaml") {
		t.Fatalf("source header missing:\n%s", masked.String())
	}
}

func TestLint(t *testing.T) {
	if issues, _ := Lint("testdata/demo", Options{}); issues != nil {
		t.Fatalf("demo should lint clean: %v", issues)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Package.yaml"), []byte("apiVersion: v1\nname: Bad\nversion: x\nruntime: {supported: []}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	issues, _ := Lint(dir, Options{})
	if len(issues) != 4 {
		t.Fatalf("want 4 separate issues, got %d: %q", len(issues), issues)
	}
	for _, i := range issues {
		if !strings.HasPrefix(i, "Package.yaml: ") {
			t.Fatalf("issue without prefix: %q", i)
		}
	}
}

func TestRenderOverrides(t *testing.T) {
	m, err := renderDemo(t, Options{
		BaseOverrides: map[string]any{"port": 9000, "storage": map[string]any{"enabled": false}},
		Sets:          []string{"image.tag=1.28", "password=null"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"port": 9000, "storage": map[string]any{"enabled": false}, "image": map[string]any{"tag": "1.28"}, "password": nil}
	if !reflect.DeepEqual(m.Overrides, want) {
		t.Fatalf("overrides: %#v", m.Overrides)
	}
	if _, ok := m.Values["password"]; ok || m.Values["port"] != 9000 {
		t.Fatalf("values: %#v", m.Values)
	}
	if len(m.Resources()) != len(m.Documents) {
		t.Fatal("Resources length")
	}
}

func TestRenderErrorSaysWhichDocument(t *testing.T) {
	p := &Package{Metadata: Metadata{APIVersion: "karga.io/v1alpha1", Name: "p", Version: "1.0.0", Runtime: Runtime{Supported: []string{"docker"}}}, Values: map[string]any{},
		Templates: map[string][]byte{"all.yaml": []byte("apiVersion: karga.io/v1alpha1\nkind: Network\nmetadata: {name: n}\n---\napiVersion: karga.io/v1alpha1\nkind: Workload\nmetadata: {name: w}\nspec: {image: a, bogus: 1}\n")}}
	_, err := Render(p, Options{ReleaseName: "r"})
	if err == nil || !strings.Contains(err.Error(), "templates/all.yaml (document 2, from line 5 of the rendered output") || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("want the document and line, got %v", err)
	}
}

func TestNotesRenderedAndRedacted(t *testing.T) {
	p := &Package{Metadata: Metadata{APIVersion: "karga.io/v1alpha1", Name: "p", Version: "1.0.0", Runtime: Runtime{Supported: []string{"docker"}}},
		Values:    map[string]any{"password": "s3cret", "port": 8080},
		Schema:    []byte(`{"type":"object","properties":{"password":{"type":"string","writeOnly":true},"port":{"type":"integer"}}}`),
		Notes:     []byte("{{ .Release.Name }} on {{ .Values.port }} with {{ .Values.password }}\n"),
		Templates: map[string][]byte{"n.yaml": []byte("apiVersion: karga.io/v1alpha1\nkind: Network\nmetadata: {name: n}\n")}}
	m, err := Render(p, Options{ReleaseName: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Notes != "r on 8080 with <redacted>" {
		t.Fatalf("notes: %q", m.Notes)
	}
}

// printDocs writes documents the way karga template does.
func printDocs(w io.Writer, pkgName string, docs []Document) {
	for _, d := range docs {
		_, _ = fmt.Fprintf(w, "---\n# Source: %s/templates/%s\n%s\n", pkgName, d.Source, d.Content)
	}
}

func TestWriteOnlyOnlyInSecrets(t *testing.T) {
	m := &Manifest{
		Values:      map[string]any{"db": map[string]any{"password": "hunter22"}, "pin": "1"},
		SecretPaths: [][]string{{"db", "password"}, {"pin"}},
		Documents: []Document{
			{Source: "s.yaml", Content: "values:\n  password: hunter22", Resource: resource.Resource{Kind: resource.KindSecret}},
			{Source: "w.yaml", Content: "env:\n  REPLICAS: 1", Resource: resource.Resource{Kind: resource.KindWorkload, Metadata: resource.Metadata{Name: "web"}}},
		},
	}
	if err := m.checkWriteOnly(); err != nil {
		t.Fatalf("a Secret may hold writeOnly values, short values are not looked for: %v", err)
	}
	for _, content := range []string{"env:\n  DB_PASSWORD: hunter22", "files:\n  pw: aHVudGVyMjI="} { // as written, and through b64enc
		m.Documents[1].Content = content
		if err := m.checkWriteOnly(); err == nil || !strings.Contains(err.Error(), `Workload "web" uses the writeOnly value db.password`) {
			t.Fatalf("%q: want a refusal, got %v", content, err)
		}
	}
	if got := m.Redact("a hunter22 b aHVudGVyMjI= c"); got != "a <redacted> b <redacted> c" {
		t.Fatalf("redact: %q", got)
	}
}

func TestRedactWriteOnlyEverywhere(t *testing.T) {
	m := &Manifest{
		Values:      map[string]any{"db": map[string]any{"password": "hunter22"}, "keys": map[string]any{"a": "k-one", "b": "k-two"}},
		SecretPaths: [][]string{{"db", "password"}, {"keys", "*"}},
		Documents: []Document{{Source: "w.yaml", Content: "env:\n  DB_PASSWORD: hunter22\n  A: k-one\n  B: \"k-two\"\n  USER: app",
			Resource: resource.Resource{Kind: resource.KindWorkload, Spec: &resource.WorkloadSpec{}}}},
	}
	docs, err := m.RedactedDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if want := "env:\n  DB_PASSWORD: <redacted>\n  A: <redacted>\n  B: \"<redacted>\"\n  USER: app"; docs[0].Content != want {
		t.Fatalf("got:\n%s", docs[0].Content)
	}
	if m.Documents[0].Content == docs[0].Content {
		t.Fatal("RedactedDocuments changed the manifest")
	}
}

func TestMaskValues(t *testing.T) {
	vals := map[string]any{
		"db":    map[string]any{"password": "pw-123456", "user": "app"},
		"users": map[string]any{"alice": map[string]any{"password": "alice-123456"}},
		"keys":  []any{map[string]any{"token": "tok-123456"}, map[string]any{"token": nil}},
	}
	got := MaskValues(vals, [][]string{{"db", "password"}, {"users", "*", "password"}, {"keys", "*", "token"}, {"absent", "x"}})
	want := map[string]any{
		"db":    map[string]any{"password": Redacted, "user": "app"},
		"users": map[string]any{"alice": map[string]any{"password": Redacted}},
		"keys":  []any{map[string]any{"token": Redacted}, map[string]any{"token": nil}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("masked %v", got)
	}
	if vals["db"].(map[string]any)["password"] != "pw-123456" {
		t.Fatal("the input was changed")
	}
}

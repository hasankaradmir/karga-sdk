package packages

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hasankaradmir/karga/internal/domain/resource"
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
	if w.Image != "nginx:1.27" || w.Ports[0].HostPort != 8080 {
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
	if w.Image != "nginx:1.28" || w.Ports[0].HostPort != 9100 {
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

func TestPrintMasksSecrets(t *testing.T) {
	m, err := renderDemo(t, Options{Sets: []string{"password=hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	var masked, plain bytes.Buffer
	if err := m.Print(&masked, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Print(&plain, true); err != nil {
		t.Fatal(err)
	}
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
	if issues := Lint("testdata/demo", Options{}); issues != nil {
		t.Fatalf("demo should lint clean: %v", issues)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Package.yaml"), []byte("apiVersion: v1\nname: Bad\nversion: x\nruntime: {supported: []}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	issues := Lint(dir, Options{})
	if len(issues) != 4 {
		t.Fatalf("want 4 separate issues, got %d: %q", len(issues), issues)
	}
	for _, i := range issues {
		if !strings.HasPrefix(i, "Package.yaml: ") {
			t.Fatalf("issue without prefix: %q", i)
		}
	}
}

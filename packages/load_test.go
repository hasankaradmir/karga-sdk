package packages

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadMinimal(t *testing.T) {
	p, err := Load("testdata/minimal")
	if err != nil {
		t.Fatal(err)
	}
	if p.Metadata.Name != "minimal" || p.Metadata.Version != "0.1.0" {
		t.Fatalf("metadata: %+v", p.Metadata)
	}
	if !reflect.DeepEqual(p.Values, map[string]any{"image": map[string]any{"tag": "1.0"}}) {
		t.Fatalf("values: %#v", p.Values)
	}
	if _, ok := p.Templates["volume.yaml"]; !ok || len(p.Templates) != 1 {
		t.Fatalf("templates: %v", keys(p.Templates))
	}
	if string(p.Files["greeting.txt"]) != "hello\n" || len(p.Files) != 1 {
		t.Fatalf("files: %v", keys(p.Files))
	}
	if p.Schema != nil || p.Readme != nil {
		t.Fatal("schema and readme should be nil when absent")
	}
}

func TestLoadErrors(t *testing.T) {
	empty := t.TempDir()
	linked := t.TempDir()
	if err := os.WriteFile(filepath.Join(linked, "Package.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(linked, "hosts")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path, want string
	}{
		{empty, "Package.yaml not found"},
		{"testdata/unknownfield", "field chart not found"},
		{"testdata/withdeps", "dependencies are not supported"},
		{linked, "only regular files"},
		{"testdata/minimal/Package.yaml", "not a package directory"},
		{"testdata/missing", "no such file"},
	}
	for _, tt := range tests {
		_, err := Load(tt.path)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Load(%s): want error containing %q, got %v", tt.path, tt.want, err)
		}
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

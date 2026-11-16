package pkg

import (
	"strings"
	"testing"
)

func valid() Metadata {
	return Metadata{
		APIVersion: APIVersion,
		Name:       "redis",
		Version:    "1.0.0",
		Runtime:    Runtime{Supported: []string{"docker", "podman"}},
		Dependencies: []Dependency{
			{Name: "postgres", Repository: "stable", Version: "^1.0.0"},
		},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Metadata)
		wantErr string
	}{
		{"valid", func(*Metadata) {}, ""},
		{"bad apiVersion", func(m *Metadata) { m.APIVersion = "karga.io/v1" }, "apiVersion"},
		{"uppercase name", func(m *Metadata) { m.Name = "Redis" }, "name"},
		{"empty name", func(m *Metadata) { m.Name = "" }, "name"},
		{"non-semver version", func(m *Metadata) { m.Version = "1.0" }, "version"},
		{"no runtimes", func(m *Metadata) { m.Runtime.Supported = nil }, "runtime.supported"},
		{"unknown runtime", func(m *Metadata) { m.Runtime.Supported = []string{"containerd"} }, "containerd"},
		{"bad dependency constraint", func(m *Metadata) { m.Dependencies[0].Version = "not-a-version" }, "dependencies[0].version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := valid()
			tt.mutate(&m)
			err := m.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateReportsAllErrors(t *testing.T) {
	err := Metadata{}.Validate()
	for _, want := range []string{"apiVersion", "name", "version", "runtime.supported"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %v", want, err)
		}
	}
}

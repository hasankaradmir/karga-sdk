package packages

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hasankaradmir/karga-sdk/internal/archive"
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
		{"testdata/withdeps", "field dependencies not found"},
		{linked, "only regular files"},
		{"testdata/minimal/Package.yaml", "not a package directory or .tgz"},
		{"testdata/missing", "no such file"},
	}
	for _, tt := range tests {
		_, err := Load(tt.path)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Load(%s): want error containing %q, got %v", tt.path, tt.want, err)
		}
	}
}

func TestLoadArchive(t *testing.T) {
	var buf bytes.Buffer
	if err := archive.Pack("testdata/minimal", &buf, archive.DefaultLimits); err != nil {
		t.Fatal(err)
	}
	tgz := filepath.Join(t.TempDir(), "minimal-0.1.0.tgz")
	if err := os.WriteFile(tgz, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	fromTgz, err := Load(tgz)
	if err != nil {
		t.Fatal(err)
	}
	fromDir, err := Load("testdata/minimal")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromTgz, fromDir) {
		t.Fatalf("archive and directory differ:\n%+v\n%+v", fromTgz, fromDir)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The digest is the package's content: its directory and its archive have the same one, however
// the archive was compressed, and any change to a file changes it.
func TestDigest(t *testing.T) {
	dir, err := Load("testdata/minimal")
	if err != nil {
		t.Fatal(err)
	}
	files, err := archive.ReadDir("testdata/minimal", archive.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir.Digest, "sha256:") || len(dir.Digest) != len("sha256:")+64 {
		t.Fatalf("digest: %s", dir.Digest)
	}
	for _, level := range []int{gzip.DefaultCompression, gzip.BestCompression, gzip.NoCompression} {
		var buf bytes.Buffer
		gz, _ := gzip.NewWriterLevel(&buf, level)
		tw := tar.NewWriter(gz)
		for _, name := range slices.Sorted(maps.Keys(files)) {
			_ = tw.WriteHeader(&tar.Header{Name: "minimal/" + name, Mode: 0o644, Size: int64(len(files[name])), Typeflag: tar.TypeReg})
			_, _ = tw.Write(files[name])
		}
		_ = tw.Close()
		_ = gz.Close()
		tgz := filepath.Join(t.TempDir(), "m.tgz")
		if err := os.WriteFile(tgz, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := Load(tgz)
		if err != nil || p.Digest != dir.Digest {
			t.Fatalf("gzip level %d: archive digest %s, directory digest %s (%v)", level, p.Digest, dir.Digest, err)
		}
	}
	changed := maps.Clone(files)
	changed["values.yaml"] = append(slices.Clone(changed["values.yaml"]), '\n')
	if archive.ContentDigest(changed) == dir.Digest {
		t.Fatal("a changed file kept the digest")
	}
	// Lengths are part of what is hashed: moving bytes between a path and its content changes it.
	if archive.ContentDigest(map[string][]byte{"ab": []byte("c")}) == archive.ContentDigest(map[string][]byte{"a": []byte("bc")}) {
		t.Fatal("ambiguous digest")
	}
}

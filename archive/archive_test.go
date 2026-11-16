package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "redis")
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPackDeterministicAndRoundTrip(t *testing.T) {
	tree := map[string]string{"Package.yaml": "name: redis\n", "templates/a.yaml": "a", "files/x/y.conf": "y"}
	dir := writeTree(t, tree)

	var a, b bytes.Buffer
	if err := Pack(dir, &a); err != nil {
		t.Fatal(err)
	}
	// Different mtimes must not change the archive.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "templates/a.yaml"), later, later); err != nil {
		t.Fatal(err)
	}
	if err := Pack(dir, &b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("Pack is not reproducible")
	}
	da, _ := Digest(bytes.NewReader(a.Bytes()))
	if !strings.HasPrefix(da, "sha256:") || len(da) != len("sha256:")+64 {
		t.Fatalf("digest: %s", da)
	}

	got, err := Unpack(bytes.NewReader(a.Bytes()), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{}
	for k, v := range tree {
		want[k] = []byte(v)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: %v", got)
	}
}

func TestPackRejectsSymlink(t *testing.T) {
	dir := writeTree(t, map[string]string{"Package.yaml": ""})
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "hosts")); err != nil {
		t.Fatal(err)
	}
	if err := Pack(dir, &bytes.Buffer{}); err == nil {
		t.Fatal("expected symlink error")
	}
}

type entry struct {
	name     string
	typeflag byte
	body     string
}

func makeTar(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if e.typeflag == 0 {
			e.typeflag = tar.TypeReg
		}
		size := int64(len(e.body))
		if e.typeflag != tar.TypeReg {
			size = 0
		}
		hdr := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: 0o644, Size: size, Linkname: "/etc/passwd"}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUnpackRejectsHarmfulArchives(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		limits  Limits
		wantErr string
	}{
		{"absolute", makeTar(t, entry{name: "/etc/passwd", body: "x"}), DefaultLimits, "invalid path"},
		{"dotdot", makeTar(t, entry{name: "redis/../../x", body: "x"}), DefaultLimits, "escapes"},
		{"symlink", makeTar(t, entry{name: "redis/link", typeflag: tar.TypeSymlink}), DefaultLimits, "only regular files"},
		{"hardlink", makeTar(t, entry{name: "redis/link", typeflag: tar.TypeLink}), DefaultLimits, "only regular files"},
		{"no top dir", makeTar(t, entry{name: "Package.yaml", body: "x"}), DefaultLimits, "single top-level"},
		{"two top dirs", makeTar(t, entry{name: "a/x", body: "x"}, entry{name: "b/y", body: "y"}), DefaultLimits, "more than one top-level"},
		{"duplicate", makeTar(t, entry{name: "a/x", body: "x"}, entry{name: "a/./x", body: "y"}), DefaultLimits, "duplicate"},
		{"too big", makeTar(t, entry{name: "a/x", body: strings.Repeat("x", 100)}), Limits{MaxBytes: 50, MaxFiles: 10}, "larger than"},
		{"too many files", makeTar(t, entry{name: "a/x", body: "1"}, entry{name: "a/y", body: "2"}), Limits{MaxBytes: 50, MaxFiles: 1}, "more than 1 files"},
		{"not gzip", []byte("plain text"), DefaultLimits, "not a gzip archive"},
	}
	for _, tt := range tests {
		_, err := Unpack(bytes.NewReader(tt.data), tt.limits)
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: want error containing %q, got %v", tt.name, tt.wantErr, err)
		}
	}
}

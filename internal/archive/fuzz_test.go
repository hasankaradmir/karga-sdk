package archive

import (
	"bytes"
	"path"
	"strings"
	"testing"
)

// FuzzUnpack checks that no archive, however malformed, panics or yields a file outside the package
// or beyond the limits.
func FuzzUnpack(f *testing.F) {
	var packed bytes.Buffer
	if err := Pack(writeTree(f, map[string]string{"karga.yaml": "name: redis\n", "templates/a.yaml": "kind: Workload\n"}), &packed, DefaultLimits); err != nil {
		f.Fatal(err)
	}
	f.Add(packed.Bytes())
	f.Add([]byte{})
	f.Add([]byte("not gzip"))
	lim := Limits{MaxBytes: 1 << 16, MaxFiles: 64}
	f.Fuzz(func(t *testing.T, data []byte) {
		files, err := Unpack(bytes.NewReader(data), lim)
		if err != nil {
			return
		}
		if len(files) > lim.MaxFiles {
			t.Fatalf("%d files, limit %d", len(files), lim.MaxFiles)
		}
		var total int64
		for name, content := range files {
			total += int64(len(content))
			if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, `\`) {
				t.Fatalf("unsafe path %q", name)
			}
		}
		if total > lim.MaxBytes {
			t.Fatalf("%d bytes, limit %d", total, lim.MaxBytes)
		}
	})
}

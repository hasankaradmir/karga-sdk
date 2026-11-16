package renderer

import (
	"reflect"
	"testing"
)

func TestFiles(t *testing.T) {
	f := Files{
		"redis.conf":     []byte("maxmemory 256mb\n"),
		"conf/a.conf":    []byte("a"),
		"conf/b.conf":    []byte("b"),
		"conf/notes.txt": []byte("n"),
	}
	if got := f.Get("redis.conf"); got != "maxmemory 256mb\n" {
		t.Fatalf("Get: %q", got)
	}
	for _, name := range []string{"./redis.conf", "/redis.conf", "conf/../redis.conf"} {
		if got := f.Get(name); got != "maxmemory 256mb\n" {
			t.Errorf("Get(%q) = %q", name, got)
		}
	}
	for _, name := range []string{"missing", "../Package.yaml", "../../etc/passwd"} {
		if got := f.Get(name); got != "" {
			t.Errorf("Get(%q) = %q, want empty", name, got)
		}
	}
	got, err := f.Glob("conf/*.conf")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"conf/a.conf": "a", "conf/b.conf": "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Glob: %v", got)
	}
	if _, err := f.Glob("[bad"); err == nil {
		t.Fatal("expected bad pattern error")
	}
}

func TestFilesInTemplate(t *testing.T) {
	c := ctx()
	c.Files = Files{"redis.conf": []byte("a\nb"), "x/1": []byte("one"), "x/2": []byte("two")}
	out, err := Render(map[string][]byte{
		"cfg.yaml": []byte(`conf: |{{ .Files.Get "redis.conf" | nindent 2 }}
{{ range $p, $c := .Files.Glob "x/*" }}{{ $p }}={{ $c }};{{ end }}`),
	}, c)
	if err != nil {
		t.Fatal(err)
	}
	if want := "conf: |\n  a\n  b\nx/1=one;x/2=two;"; out[0].Content != want {
		t.Fatalf("got %q", out[0].Content)
	}
}

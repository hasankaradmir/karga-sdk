package packages

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/hasankaradmir/karga-sdk/internal/archive"
)

var update = flag.Bool("update", false, "rewrite golden files")

var examples = []struct {
	name, release string
	opts          Options
}{
	{"redis", "my-redis", Options{}},
	{"postgres", "my-db", Options{Values: ValueSources{Sets: []Set{{Expr: "database.password=s3cret"}}}}},
}

func examplePath(name string) string { return filepath.Join("..", "examples", name) }

func renderExample(t *testing.T, name, release string, opts Options) []byte {
	t.Helper()
	p, err := Load(examplePath(name))
	if err != nil {
		t.Fatal(err)
	}
	opts.ReleaseName = release
	m, err := Render(p, opts)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := m.RedactedDocuments()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	printDocs(&buf, m.Package.Name, docs)
	return buf.Bytes()
}

func TestExamplesGolden(t *testing.T) {
	for _, ex := range examples {
		t.Run(ex.name, func(t *testing.T) {
			got := renderExample(t, ex.name, ex.release, ex.opts)
			if again := renderExample(t, ex.name, ex.release, ex.opts); !bytes.Equal(got, again) {
				t.Fatal("rendering is not deterministic")
			}
			if bytes.Contains(got, []byte("s3cret")) {
				t.Fatal("secret value leaked into printed manifest")
			}
			golden := filepath.Join("testdata", "golden", ex.name+".yaml")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test ./packages -update)", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("output differs from %s (run with -update if intended)\n--- got:\n%s", golden, got)
			}
		})
	}
}

func TestExamplesLint(t *testing.T) {
	for _, ex := range examples {
		if issues, _ := Lint(examplePath(ex.name), ex.opts); issues != nil {
			t.Errorf("%s: %v", ex.name, issues)
		}
	}
	if issues, _ := Lint(examplePath("postgres"), Options{}); len(issues) != 1 {
		t.Errorf("postgres without password: want 1 issue, got %v", issues)
	}
}

func TestExamplesPackReproducible(t *testing.T) {
	for _, ex := range examples {
		var a, b bytes.Buffer
		if err := archive.Pack(examplePath(ex.name), &a, archive.DefaultLimits); err != nil {
			t.Fatal(err)
		}
		if err := archive.Pack(examplePath(ex.name), &b, archive.DefaultLimits); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a.Bytes(), b.Bytes()) {
			t.Errorf("%s: the archive changed between packs", ex.name)
		}
	}
}

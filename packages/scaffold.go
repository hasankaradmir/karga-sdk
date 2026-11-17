package packages

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hasankaradmir/karga-sdk/pkg"
)

//go:embed all:scaffold
var scaffold embed.FS

// Create writes a new, working package named after dir's base name: a web workload with an http
// health check, replace updates and NOTES.txt. dir must not exist.
func Create(dir string) error {
	name := filepath.Base(filepath.Clean(dir))
	if !pkg.IsDNSLabel(name) {
		return fmt.Errorf("package name %q (from %s) must be a DNS label: lowercase letters, digits and '-'", name, dir)
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	}
	return fs.WalkDir(scaffold, "scaffold", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "scaffold"), "/")
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := scaffold.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, []byte(strings.ReplaceAll(string(data), "__NAME__", name)), 0o644)
	})
}

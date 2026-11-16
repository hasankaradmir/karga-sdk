// Package packages implements package use cases: load, render, lint, show.
package packages

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/hasankaradmir/karga/internal/domain/pkg"
	"github.com/hasankaradmir/karga/internal/infrastructure/archive"
	"github.com/hasankaradmir/karga/internal/infrastructure/values"
)

const (
	metadataFile = "Package.yaml"
	valuesFile   = "values.yaml"
	schemaFile   = "values.schema.json"
	readmeFile   = "README.md"
	templatesDir = "templates/"
	filesDir     = "files/"
	packagesDir  = "packages/"
)

// Load reads a package from a directory or a .tgz archive.
func Load(path string) (*pkg.Package, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var files map[string][]byte
	switch {
	case info.IsDir():
		files, err = readDir(path)
	case strings.HasSuffix(path, ".tgz") || strings.HasSuffix(path, ".tar.gz"):
		files, err = readArchive(path)
	default:
		return nil, fmt.Errorf("%s: not a package directory or .tgz archive", path)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	p, err := fromFiles(files)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

func readArchive(path string) (map[string][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only
	return archive.Unpack(f, archive.DefaultLimits)
}

// readDir returns every regular file under root keyed by slash-separated relative path.
func readDir(root string) (map[string][]byte, error) {
	fsys := os.DirFS(root)
	files := map[string][]byte{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			// Symlinks could point outside the package (e.g. files/ -> /etc).
			return fmt.Errorf("%s: only regular files are allowed in a package", p)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		files[p] = data
		return nil
	})
	return files, err
}

// fromFiles builds a Package from its files keyed by package-relative path.
func fromFiles(files map[string][]byte) (*pkg.Package, error) {
	raw, ok := files[metadataFile]
	if !ok {
		return nil, fmt.Errorf("%s not found", metadataFile)
	}
	var md pkg.Metadata
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&md); err != nil {
		return nil, fmt.Errorf("%s: %w", metadataFile, err)
	}

	p := &pkg.Package{
		Metadata:  md,
		Values:    map[string]any{},
		Schema:    files[schemaFile],
		Readme:    files[readmeFile],
		Templates: map[string][]byte{},
		Files:     map[string][]byte{},
	}
	if v, ok := files[valuesFile]; ok {
		vals, err := values.Decode(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", valuesFile, err)
		}
		p.Values = vals
		p.RawValues = v
	}

	hasVendored := false
	for name, data := range files {
		switch {
		case strings.HasPrefix(name, templatesDir):
			p.Templates[strings.TrimPrefix(name, templatesDir)] = data
		case strings.HasPrefix(name, filesDir):
			p.Files[strings.TrimPrefix(name, filesDir)] = data
		case strings.HasPrefix(name, packagesDir):
			hasVendored = true
		}
	}
	if len(md.Dependencies) > 0 || hasVendored {
		return nil, errors.New("package dependencies are not supported yet (planned for M4)")
	}
	return p, nil
}

// Digest returns the package digest: the archive file's SHA-256, or for a directory the SHA-256 of
// its reproducible archive (archive.Pack), so the same content always gives the same digest.
func Digest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer func() { _ = f.Close() }() // read-only
		return archive.Digest(f)
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(archive.Pack(path, pw)) }()
	return archive.Digest(pr)
}

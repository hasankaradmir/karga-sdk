// Package packages implements package use cases: load, render, lint, show.
package packages

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/hasankaradmir/karga-sdk/internal/archive"
	"github.com/hasankaradmir/karga-sdk/internal/values"
)

const (
	metadataFile = "Package.yaml"
	valuesFile   = "values.yaml"
	schemaFile   = "values.schema.json"
	readmeFile   = "README.md"
	notesFile    = "NOTES.txt"
	templatesDir = "templates/"
	filesDir     = "files/"
	packagesDir  = "packages/"
)

// Load reads a package from a directory or a .tgz archive.
func Load(path string) (*Package, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var files map[string][]byte
	switch {
	case info.IsDir():
		files, err = archive.ReadDir(path, archive.DefaultLimits)
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
	p.Digest = archive.ContentDigest(files)
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

// fromFiles builds a Package from its files keyed by package-relative path.
func fromFiles(files map[string][]byte) (*Package, error) {
	raw, ok := files[metadataFile]
	if !ok {
		return nil, fmt.Errorf("%s not found", metadataFile)
	}
	var md Metadata
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&md); err != nil {
		return nil, fmt.Errorf("%s: %w", metadataFile, err)
	}

	p := &Package{
		Metadata:  md,
		Values:    map[string]any{},
		Schema:    files[schemaFile],
		Readme:    files[readmeFile],
		Notes:     files[notesFile],
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
	if hasVendored {
		return nil, errors.New("packages/: package dependencies are not supported")
	}
	return p, nil
}

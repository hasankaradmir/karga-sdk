// Package archive packs and unpacks package .tgz archives.
package archive

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Limits bounds what Unpack accepts from an untrusted archive.
type Limits struct {
	MaxBytes int64
	MaxFiles int
}

// DefaultLimits is used for package archives.
var DefaultLimits = Limits{MaxBytes: 32 << 20, MaxFiles: 10000}

// Pack writes dir as a gzip-compressed tar with entries under "<base name of dir>/".
// Output is byte-for-byte reproducible: sorted entries, zero timestamps and owners, normalized modes.
func Pack(dir string, w io.Writer) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	prefix := filepath.Base(abs)

	gz := gzip.NewWriter(w) // empty Name and zero ModTime in the gzip header
	tw := tar.NewWriter(gz)
	// WalkDir visits entries in lexical order.
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(abs, p)
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: only regular files can be packaged", rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     path.Join(prefix, filepath.ToSlash(rel)),
			Mode:     mode,
			Size:     info.Size(),
			ModTime:  time.Unix(0, 0),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }() // read-only
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// Unpack reads a package archive into memory, keyed by path with the single top-level directory removed.
// It rejects anything that could escape the package or exhaust memory.
func Unpack(r io.Reader, lim Limits) (map[string][]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	files := map[string][]byte{}
	var total int64
	top := ""
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return nil, fmt.Errorf("%s: only regular files are allowed in a package archive", hdr.Name)
		}

		name := hdr.Name
		if strings.Contains(name, `\`) || path.IsAbs(name) {
			return nil, fmt.Errorf("%s: invalid path in archive", name)
		}
		clean := path.Clean(name)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("%s: path escapes the package", name)
		}
		dir, rel, ok := strings.Cut(clean, "/")
		if !ok {
			return nil, fmt.Errorf("%s: files must be inside a single top-level directory", name)
		}
		if top == "" {
			top = dir
		} else if dir != top {
			return nil, fmt.Errorf("%s: archive has more than one top-level directory", name)
		}
		if _, dup := files[rel]; dup {
			return nil, fmt.Errorf("%s: duplicate entry", name)
		}

		if len(files) >= lim.MaxFiles {
			return nil, fmt.Errorf("archive has more than %d files", lim.MaxFiles)
		}
		total += hdr.Size
		if hdr.Size < 0 || total > lim.MaxBytes {
			return nil, fmt.Errorf("archive is larger than %d bytes", lim.MaxBytes)
		}
		data, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return nil, err
		}
		files[rel] = data
	}
	return files, nil
}

// Digest returns "sha256:<hex>" of everything read from r.
func Digest(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

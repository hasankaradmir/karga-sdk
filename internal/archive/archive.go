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

// IgnoreFile lists patterns, one per line, of paths left out of a package directory. "#" starts a
// comment; a trailing "/" matches directories only. A pattern matches the slash-separated relative
// path or any single path element, as path.Match does. .git and the ignore file itself are always left out.
// shortcut: no "**" and no "!" negation; add them if packages need finer rules.
const IgnoreFile = ".kargaignore"

// ReadDir returns the regular files of a package directory keyed by slash-separated relative path.
// It applies IgnoreFile and the same limits Unpack applies, so a directory and its archive load alike.
func ReadDir(dir string, lim Limits) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := walk(dir, lim, func(rel string, _ fs.FileInfo, data []byte) error {
		files[rel] = data
		return nil
	})
	return files, err
}

// Pack writes dir as a gzip-compressed tar with entries under "<base name of dir>/".
// It packs what ReadDir reads. Output is byte-for-byte reproducible: sorted entries, zero timestamps
// and owners, normalized modes.
func Pack(dir string, w io.Writer, lim Limits) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	prefix := filepath.Base(abs)

	gz := gzip.NewWriter(w) // empty Name and zero ModTime in the gzip header
	tw := tar.NewWriter(gz)
	err = walk(abs, lim, func(rel string, info fs.FileInfo, data []byte) error {
		mode := int64(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     path.Join(prefix, rel),
			Mode:     mode,
			Size:     int64(len(data)),
			ModTime:  time.Unix(0, 0),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err := tw.Write(data)
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

// walk calls fn for every packaged file of dir in lexical order.
func walk(dir string, lim Limits, fn func(rel string, info fs.FileInfo, data []byte) error) error {
	fsys := os.DirFS(dir)
	ignore, err := readIgnore(fsys)
	if err != nil {
		return err
	}
	var count int
	var total int64
	// WalkDir visits entries in lexical order.
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if ignored(p, d.IsDir(), ignore) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			// Symlinks could point outside the package (e.g. files/ -> /etc).
			return fmt.Errorf("%s: only regular files can be packaged", p)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if count++; count > lim.MaxFiles {
			return fmt.Errorf("package has more than %d files; list unneeded ones in %s", lim.MaxFiles, IgnoreFile)
		}
		if total += info.Size(); total > lim.MaxBytes {
			return fmt.Errorf("package is larger than %d bytes; list unneeded files in %s", lim.MaxBytes, IgnoreFile)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		return fn(p, info, data)
	})
}

func readIgnore(fsys fs.FS) ([]string, error) {
	data, err := fs.ReadFile(fsys, IgnoreFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pats []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			pats = append(pats, line)
		}
	}
	return pats, nil
}

func ignored(p string, isDir bool, pats []string) bool {
	if p == IgnoreFile || (isDir && path.Base(p) == ".git") {
		return true
	}
	for _, pat := range pats {
		dirOnly := strings.HasSuffix(pat, "/")
		pat = strings.TrimSuffix(pat, "/")
		if dirOnly && !isDir {
			continue
		}
		if ok, _ := path.Match(pat, p); ok {
			return true
		}
		if ok, _ := path.Match(pat, path.Base(p)); ok {
			return true
		}
	}
	return false
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
	var entries int
	top := ""
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		// Every entry counts toward the limits, skipped ones too: tar still inflates their bodies.
		entries++
		total += hdr.Size
		if entries > lim.MaxFiles {
			return nil, fmt.Errorf("archive has more than %d files", lim.MaxFiles)
		}
		if hdr.Size < 0 || total > lim.MaxBytes {
			return nil, fmt.Errorf("archive is larger than %d bytes", lim.MaxBytes)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return nil, fmt.Errorf("%s: only regular files are allowed in a package archive", hdr.Name)
		}

		name := hdr.Name
		// macOS tar adds AppleDouble metadata entries ("._name"); they are never package content.
		if strings.HasPrefix(path.Base(name), "._") {
			continue
		}
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

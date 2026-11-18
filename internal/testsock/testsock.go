// Package testsock serves an HTTP handler on a unix socket for the length of a test.
package testsock

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Serve serves h on a new unix socket until the test ends and returns the socket's path.
func Serve(t testing.TB, h http.Handler) string {
	t.Helper()
	// Unix socket paths are short (104 bytes on macOS): t.TempDir() can be too long.
	dir, err := os.MkdirTemp("", "ksock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.Serve(l) }()
	t.Cleanup(func() { _ = hs.Close() })
	return sock
}

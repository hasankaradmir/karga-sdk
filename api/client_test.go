package api

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientDialErrors(t *testing.T) {
	// Not t.TempDir: its path is too long for a unix socket on some systems.
	dir, err := os.MkdirTemp("", "ksock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700); _ = os.RemoveAll(dir) })

	_, err = NewClient(filepath.Join(dir, "missing.sock")).Version(context.Background())
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("no socket: got %v, want ErrNotRunning", err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root is not denied by file modes")
	}
	sock := filepath.Join(dir, "karga.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	_, err = NewClient(sock).Version(context.Background())
	if errors.Is(err, ErrNotRunning) || !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), "permission denied on "+sock) {
		t.Errorf("denied socket: got %v, want permission denied on %s", err, sock)
	}
}

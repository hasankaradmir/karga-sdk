package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hasankaradmir/karga-sdk/plan"
)

// slow is a Backend whose installs wait to be released; it records whether the server canceled them.
type slow struct {
	Backend
	started  chan struct{}
	finish   chan struct{}
	canceled chan bool
}

func (s *slow) Install(ctx context.Context, req InstallRequest) (*Result, error) {
	req.Progress(plan.Event{Kind: "Workload", Name: "web", Phase: plan.PhaseCreate})
	close(s.started)
	<-s.finish
	s.canceled <- ctx.Err() != nil
	return &Result{Release: req.Name}, nil
}

func serve(t *testing.T, b Backend) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kapi") // unix socket paths are short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: NewServer(b, "dev"), ReadHeaderTimeout: time.Second}
	go func() { _ = hs.Serve(l) }()
	t.Cleanup(func() { _ = hs.Close() })
	return sock
}

func TestOperationOutlivesTheClient(t *testing.T) {
	b := &slow{started: make(chan struct{}), finish: make(chan struct{}), canceled: make(chan bool, 1)}
	c := NewClient(serve(t, b))
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.Install(ctx, InstallRequest{Name: "shop", Package: "../packages/testdata/demo"})
		errc <- err
	}()
	<-b.started
	cancel() // Ctrl-C in the CLI
	err := <-errc
	if err == nil || !strings.Contains(err.Error(), "the operation goes on in karga-server") || !strings.Contains(err.Error(), "karga events -f shop") {
		t.Fatalf("client error: %v", err)
	}
	close(b.finish)
	if <-b.canceled {
		t.Fatal("the server canceled the operation when the client went away")
	}
}

func TestAPIVersionContract(t *testing.T) {
	sock := serve(t, &slow{})
	v, err := NewClient(sock).Version(context.Background())
	if err != nil || v.API != Major || v.Version != "dev" {
		t.Fatalf("version: %+v %v", v, err)
	}
	post := func(api string) (int, string) {
		t.Helper()
		hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}}
		req, _ := http.NewRequest(http.MethodPost, "http://karga/v1/list", strings.NewReader("{}"))
		if api != "" {
			req.Header.Set(apiHeader, api)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var e errorResponse
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return resp.StatusCode, e.Error
	}
	if code, msg := post("2"); code != http.StatusBadRequest || msg != "karga speaks API 2, karga-server API 1: upgrade karga-server" {
		t.Fatalf("newer client: %d %q", code, msg)
	}
	if code, msg := post(""); code != http.StatusBadRequest || !strings.HasSuffix(msg, "upgrade karga") {
		t.Fatalf("client without a version: %d %q", code, msg)
	}
}

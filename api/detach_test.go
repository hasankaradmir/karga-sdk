package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/hasankaradmir/karga-sdk/internal/testsock"
)

// slow is a Backend whose installs wait to be released; it records whether the server canceled them.
type slow struct {
	Backend
	started  chan struct{}
	finish   chan struct{}
	canceled chan bool
}

func (s *slow) Install(ctx context.Context, req InstallRequest, progress func(Event)) (*Result, error) {
	progress(Event{Kind: "Workload", Name: "web", Phase: PhaseCreate})
	close(s.started)
	<-s.finish
	s.canceled <- ctx.Err() != nil
	return &Result{Release: req.Name}, nil
}

func (s *slow) List(context.Context) ([]Release, error) { return nil, nil }

func serve(t *testing.T, b Backend) string {
	t.Helper()
	return testsock.Serve(t, NewServer(b, "dev", Options{}))
}

func TestOperationOutlivesTheClient(t *testing.T) {
	b := &slow{started: make(chan struct{}), finish: make(chan struct{}), canceled: make(chan bool, 1)}
	c := NewClient(serve(t, b))
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	shown := make(chan struct{})
	go func() {
		_, err := c.Install(ctx, InstallRequest{Name: "shop", Package: "../packages/testdata/demo"}, func(Event) { close(shown) })
		errc <- err
	}()
	<-b.started
	<-shown
	cancel() // Ctrl-C in the CLI, once progress shows
	err := <-errc
	if !errors.Is(err, ErrDetached) || !errors.Is(err, context.Canceled) {
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
	if err != nil || v.API != Version || v.Version != "dev" {
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
	if code, msg := post("3.0"); code != http.StatusBadRequest || !strings.HasSuffix(msg, "karga speaks API 3.0, karga-server API "+Version+": upgrade karga-server") {
		t.Fatalf("newer client: %d %q", code, msg)
	}
	if code, msg := post("1"); code != http.StatusBadRequest || !strings.HasSuffix(msg, "upgrade karga") {
		t.Fatalf("older client: %d %q", code, msg)
	}
	if code, msg := post(""); code != http.StatusBadRequest || !strings.HasSuffix(msg, "upgrade karga") {
		t.Fatalf("client without a version: %d %q", code, msg)
	}
	// Another minor version of the same major works.
	if code, msg := post(fmt.Sprintf("%d.%d", Major, Minor+1)); code != http.StatusOK {
		t.Fatalf("newer minor: %d %q", code, msg)
	}
}

func TestVersionMatchesMajorMinor(t *testing.T) {
	if want := fmt.Sprintf("%d.%d", Major, Minor); Version != want {
		t.Fatalf("Version %q, want %q", Version, want)
	}
}

// What an older server lacks is reported as an incompatibility, never ignored.
func TestNewerClientFeatures(t *testing.T) {
	sock := serve(t, &slow{})
	post := func(path, body string) error {
		t.Helper()
		c := NewClient(sock)
		resp, err := c.post(context.Background(), path, strings.NewReader(body), nil)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}
	if err := post("/v1/list", `{"since":"yesterday"}`); !errors.Is(err, ErrIncompatible) || !strings.Contains(err.Error(), `"since"`) {
		t.Fatalf("unknown field: %v", err)
	}
	if err := post("/v1/something-new", `{}`); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("unknown endpoint: %v", err)
	}
}

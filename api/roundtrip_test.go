package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hasankaradmir/karga-sdk/internal/testsock"
	"github.com/hasankaradmir/karga-sdk/resource"
)

// recorder is a Backend that records each call with its request and answers with canned values or
// with err.
type recorder struct {
	mu    sync.Mutex
	calls []string
	got   any
	err   error
}

func (r *recorder) rec(name string, req any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
	r.got = req
	return r.err
}

func (r *recorder) Install(_ context.Context, req InstallRequest, p func(Event)) (*Result, error) {
	req.Package = "" // a temporary path on the server
	p(Event{Kind: "Workload", Name: "web"})
	return &Result{Release: req.Name, Revision: 1}, r.rec("Install", req)
}
func (r *recorder) Upgrade(_ context.Context, req UpgradeRequest, _ func(Event)) (*Result, error) {
	req.Package = ""
	return &Result{Release: req.Name, Revision: 2}, r.rec("Upgrade", req)
}
func (r *recorder) Rollback(_ context.Context, req RollbackRequest, _ func(Event)) (*Result, error) {
	return &Result{Release: req.Name, Revision: 3}, r.rec("Rollback", req)
}
func (r *recorder) Uninstall(_ context.Context, req UninstallRequest) (*Result, error) {
	return &Result{Release: req.Name}, r.rec("Uninstall", req)
}
func (r *recorder) List(context.Context) ([]Release, error) {
	return []Release{{Name: "shop"}}, r.rec("List", nil)
}
func (r *recorder) History(_ context.Context, req ReleaseRequest) ([]Revision, error) {
	return []Revision{{Number: 1}}, r.rec("History", req)
}
func (r *recorder) Values(_ context.Context, req ValuesRequest) (map[string]any, error) {
	return map[string]any{"a": "b"}, r.rec("Values", req)
}
func (r *recorder) Notes(_ context.Context, req RevisionRequest) (string, error) {
	return "notes", r.rec("Notes", req)
}
func (r *recorder) Manifest(_ context.Context, req RevisionRequest) ([]resource.Resource, error) {
	res, err := resource.Decode([]byte("apiVersion: karga.io/v1alpha1\nkind: Network\nmetadata: {name: net}\n"))
	return []resource.Resource{res}, errors.Join(err, r.rec("Manifest", req))
}
func (r *recorder) Status(_ context.Context, req ReleaseRequest) (*StatusReport, error) {
	return &StatusReport{Release: Release{Name: req.Name}}, r.rec("Status", req)
}
func (r *recorder) Diff(_ context.Context, req DiffRequest) (*RevisionDiff, error) {
	return &RevisionDiff{Release: req.Name, From: req.From}, r.rec("Diff", req)
}
func (r *recorder) Resources(_ context.Context, req ReleaseRequest) ([]AppliedResource, error) {
	return []AppliedResource{{Kind: "Network", Name: "net"}}, r.rec("Resources", req)
}
func (r *recorder) Events(_ context.Context, req EventsRequest) ([]Operation, error) {
	return []Operation{{ID: 7}}, r.rec("Events", req)
}
func (r *recorder) Logs(_ context.Context, req LogsRequest, w io.Writer) error {
	_, _ = io.WriteString(w, "log line\n")
	return r.rec("Logs", req)
}
func (r *recorder) Exec(_ context.Context, req ExecRequest) (int, error) {
	_, _ = io.WriteString(req.Stdout, "out")
	return 3, r.rec("Exec", ExecRequest{Release: req.Release, Workload: req.Workload, ExecOptions: ExecOptions{Cmd: req.Cmd}})
}
func (r *recorder) BackupVolume(_ context.Context, req VolumeBackupRequest, w io.Writer) error {
	_, _ = io.WriteString(w, "backup")
	return r.rec("BackupVolume", req)
}
func (r *recorder) RestoreVolume(_ context.Context, req VolumeRestoreRequest, in io.Reader) error {
	data, _ := io.ReadAll(in)
	if string(data) != "backup" {
		return fmt.Errorf("restored %q", data)
	}
	return r.rec("RestoreVolume", req)
}
func (r *recorder) Reconcile(_ context.Context, req ReconcileRequest) (*ReconcileResult, error) {
	return &ReconcileResult{Release: req.Name}, r.rec("Reconcile", req)
}
func (r *recorder) Pause(_ context.Context, req ReleaseRequest) error  { return r.rec("Pause", req) }
func (r *recorder) Resume(_ context.Context, req ReleaseRequest) error { return r.rec("Resume", req) }
func (r *recorder) GatewayRoutes(context.Context) ([]Exposed, error) {
	return []Exposed{{Release: "shop"}}, r.rec("GatewayRoutes", nil)
}
func (r *recorder) GatewayHealth(context.Context) (State, error) {
	return State{Container: "running"}, r.rec("GatewayHealth", nil)
}
func (r *recorder) GatewaySettings(context.Context) (Settings, map[string][]string, error) {
	return Settings{AccessLog: true}, map[string][]string{"web": {"shop"}}, r.rec("GatewaySettings", nil)
}
func (r *recorder) SetGatewayAccessLog(_ context.Context, req GatewayAccessLogRequest) error {
	return r.rec("SetGatewayAccessLog", req)
}
func (r *recorder) SetGatewayListen(_ context.Context, req GatewayListenRequest) error {
	return r.rec("SetGatewayListen", req)
}
func (r *recorder) AddGatewayListener(_ context.Context, l Listener) error {
	return r.rec("AddGatewayListener", l)
}

func (r *recorder) SetGatewayListener(_ context.Context, req GatewayListenerSetRequest) error {
	return r.rec("SetGatewayListener", req)
}
func (r *recorder) DiffGateway(_ context.Context, doc GatewayDocument) (GatewayPlan, error) {
	return GatewayPlan{Changes: []GatewayChange{{Kind: "listener", Name: "web", Action: "change", Fields: []string{"timeout: - -> 30s"}}}},
		r.rec("DiffGateway", doc)
}

func (r *recorder) ApplyGateway(_ context.Context, req GatewayApplyRequest) (GatewayRevision, error) {
	return GatewayRevision{Revision: 2, Source: req.Source}, r.rec("ApplyGateway", req)
}

func (r *recorder) ExportGateway(_ context.Context, req GatewayExportRequest) (GatewayDocument, error) {
	return GatewayDocument{Content: []byte("kind: Gateway\n")}, r.rec("ExportGateway", req)
}

func (r *recorder) GatewayHistory(context.Context) ([]GatewayRevision, error) {
	return []GatewayRevision{{Revision: 1, Source: "apply"}}, r.rec("GatewayHistory", nil)
}

func (r *recorder) RemoveGatewayListener(_ context.Context, req GatewayListenerRequest) error {
	return r.rec("RemoveGatewayListener", req)
}

var _ Backend = (*recorder)(nil)

const demoPkg = "../packages/testdata/demo"

// Every operation reaches the backend with its request, and its answer reaches the caller.
func TestEveryEndpointRoundTrips(t *testing.T) {
	b := &recorder{}
	c := NewClient(serve(t, b))
	ctx := context.Background()
	var out bytes.Buffer
	cases := []struct {
		name string
		call func() (any, error)
		want any // the request the backend got
	}{
		{"Install", func() (any, error) {
			var events []Event
			res, err := c.Install(ctx, InstallRequest{Name: "shop", Package: demoPkg, Values: ValuesInput{Sets: []Set{{Expr: "a=b"}}}}, func(e Event) { events = append(events, e) })
			if len(events) != 1 {
				return nil, fmt.Errorf("events %v", events)
			}
			return res.Revision, err
		}, InstallRequest{Name: "shop", Values: ValuesInput{Sets: []Set{{Expr: "a=b"}}}}},
		{"Upgrade", func() (any, error) {
			res, err := c.Upgrade(ctx, UpgradeRequest{Name: "shop", Package: demoPkg, Strategy: "replace"}, nil)
			return res.Revision, err
		}, UpgradeRequest{Name: "shop", Strategy: "replace"}},
		{"Rollback", func() (any, error) {
			res, err := c.Rollback(ctx, RollbackRequest{Name: "shop", Revision: 1}, nil)
			return res.Revision, err
		}, RollbackRequest{Name: "shop", Revision: 1}},
		{"Uninstall", func() (any, error) {
			return c.Uninstall(ctx, UninstallRequest{Name: "shop", KeepHistory: true, Confirmed: true})
		},
			UninstallRequest{Name: "shop", KeepHistory: true, Confirmed: true}},
		{"List", func() (any, error) { return c.List(ctx) }, nil},
		{"History", func() (any, error) { return c.History(ctx, ReleaseRequest{Name: "shop"}) }, ReleaseRequest{Name: "shop"}},
		{"Values", func() (any, error) { return c.Values(ctx, ValuesRequest{Name: "shop", Revision: 2, All: true}) },
			ValuesRequest{Name: "shop", Revision: 2, All: true}},
		{"Notes", func() (any, error) { return c.Notes(ctx, RevisionRequest{Name: "shop", Revision: 2}) }, RevisionRequest{Name: "shop", Revision: 2}},
		{"Manifest", func() (any, error) {
			rs, err := c.Manifest(ctx, RevisionRequest{Name: "shop"})
			if err == nil && (len(rs) != 1 || rs[0].Kind != "Network") {
				return nil, fmt.Errorf("manifest %+v", rs)
			}
			return len(rs), err
		}, RevisionRequest{Name: "shop"}},
		{"Status", func() (any, error) { return c.Status(ctx, ReleaseRequest{Name: "shop"}) }, ReleaseRequest{Name: "shop"}},
		{"Diff", func() (any, error) { return c.Diff(ctx, DiffRequest{Name: "shop", From: 1, To: 2}) }, DiffRequest{Name: "shop", From: 1, To: 2}},
		{"Resources", func() (any, error) { return c.Resources(ctx, ReleaseRequest{Name: "shop"}) }, ReleaseRequest{Name: "shop"}},
		{"Events", func() (any, error) { return c.Events(ctx, EventsRequest{AfterID: 3, Limit: 5}) }, EventsRequest{AfterID: 3, Limit: 5}},
		{"Logs", func() (any, error) {
			out.Reset()
			err := c.Logs(ctx, LogsRequest{Release: "shop", Workloads: []string{"web"}, Tail: 10}, &out)
			return out.String(), err
		}, LogsRequest{Release: "shop", Workloads: []string{"web"}, Tail: 10}},
		{"Exec", func() (any, error) {
			out.Reset()
			code, err := c.Exec(ctx, ExecRequest{Release: "shop", Workload: "web", ExecOptions: ExecOptions{Cmd: []string{"ls"}, Stdout: &out}})
			if err == nil && out.String() != "out" {
				return nil, fmt.Errorf("stdout %q", out.String())
			}
			return code, err
		}, ExecRequest{Release: "shop", Workload: "web", ExecOptions: ExecOptions{Cmd: []string{"ls"}}}},
		{"BackupVolume", func() (any, error) {
			out.Reset()
			err := c.BackupVolume(ctx, VolumeBackupRequest{Release: "shop", Volume: "data", Stop: true}, &out)
			return out.String(), err
		}, VolumeBackupRequest{Release: "shop", Volume: "data", Stop: true}},
		{"RestoreVolume", func() (any, error) {
			return nil, c.RestoreVolume(ctx, VolumeRestoreRequest{Release: "shop", Volume: "data", Force: true, Name: "data.tar.gz", Confirmed: true}, strings.NewReader("backup"))
		}, VolumeRestoreRequest{Release: "shop", Volume: "data", Force: true, Name: "data.tar.gz", Confirmed: true}},
		{"Reconcile", func() (any, error) { return c.Reconcile(ctx, ReconcileRequest{Name: "shop", Force: true}) }, ReconcileRequest{Name: "shop", Force: true}},
		{"Pause", func() (any, error) { return nil, c.Pause(ctx, ReleaseRequest{Name: "shop"}) }, ReleaseRequest{Name: "shop"}},
		{"Resume", func() (any, error) { return nil, c.Resume(ctx, ReleaseRequest{Name: "shop"}) }, ReleaseRequest{Name: "shop"}},
		{"GatewayRoutes", func() (any, error) { return c.GatewayRoutes(ctx) }, nil},
		{"GatewayHealth", func() (any, error) { return c.GatewayHealth(ctx) }, nil},
		{"DiffGateway", func() (any, error) {
			p, err := c.DiffGateway(ctx, GatewayDocument{Content: []byte("x")})
			if err == nil && (len(p.Changes) != 1 || p.Changes[0].Fields[0] != "timeout: - -> 30s") {
				return nil, fmt.Errorf("plan %+v", p)
			}
			return p, err
		}, GatewayDocument{Content: []byte("x")}},
		{"ApplyGateway", func() (any, error) {
			rev, err := c.ApplyGateway(ctx, GatewayApplyRequest{GatewayDocument: GatewayDocument{Content: []byte("x")}, Source: "rollback 1", Confirmed: true})
			if err == nil && (rev.Revision != 2 || rev.Source != "rollback 1") {
				return nil, fmt.Errorf("revision %+v", rev)
			}
			return rev, err
		}, GatewayApplyRequest{GatewayDocument: GatewayDocument{Content: []byte("x")}, Source: "rollback 1", Confirmed: true}},
		{"ExportGateway", func() (any, error) {
			d, err := c.ExportGateway(ctx, GatewayExportRequest{Revision: 1})
			if err == nil && string(d.Content) != "kind: Gateway\n" {
				return nil, fmt.Errorf("document %q", d.Content)
			}
			return d, err
		}, GatewayExportRequest{Revision: 1}},
		{"GatewayHistory", func() (any, error) { return c.GatewayHistory(ctx) }, nil},
		{"GatewaySettings", func() (any, error) {
			s, users, err := c.GatewaySettings(ctx)
			if err == nil && (!s.AccessLog || len(users["web"]) != 1) {
				return nil, fmt.Errorf("settings %+v %v", s, users)
			}
			return s, err
		}, nil},
		{"SetGatewayAccessLog", func() (any, error) { return nil, c.SetGatewayAccessLog(ctx, GatewayAccessLogRequest{Enabled: true}) },
			GatewayAccessLogRequest{Enabled: true}},
		{"SetGatewayListen", func() (any, error) { return nil, c.SetGatewayListen(ctx, GatewayListenRequest{}) }, GatewayListenRequest{}},
		{"AddGatewayListener", func() (any, error) {
			return nil, c.AddGatewayListener(ctx, Listener{Name: "web", Port: 8080, Protocol: "http"})
		},
			Listener{Name: "web", Port: 8080, Protocol: "http"}},
		{"SetGatewayListener", func() (any, error) {
			return nil, c.SetGatewayListener(ctx, GatewayListenerSetRequest{Name: "web", ListenerOptions: ListenerOptions{Timeout: "30s", RateLimit: "100/s"}})
		},
			GatewayListenerSetRequest{Name: "web", ListenerOptions: ListenerOptions{Timeout: "30s", RateLimit: "100/s"}}},
		{"RemoveGatewayListener", func() (any, error) {
			return nil, c.RemoveGatewayListener(ctx, GatewayListenerRequest{Name: "web", Confirmed: true})
		},
			GatewayListenerRequest{Name: "web", Confirmed: true}},
	}
	if n := reflect.TypeFor[Backend]().NumMethod(); n != len(cases) {
		t.Fatalf("%d cases for %d Backend methods", len(cases), n)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b.calls, b.got, b.err = nil, nil, nil
			if _, err := tc.call(); err != nil {
				t.Fatal(err)
			}
			if len(b.calls) != 1 || b.calls[0] != tc.name {
				t.Fatalf("calls %v", b.calls)
			}
			if !reflect.DeepEqual(b.got, tc.want) {
				t.Fatalf("backend got %#v, want %#v", b.got, tc.want)
			}
			// The backend's error reaches the caller with its kind.
			b.err = fmt.Errorf("shop: %w", ErrConflict)
			if _, err := tc.call(); !errors.Is(err, ErrConflict) {
				t.Fatalf("error kind lost: %v", err)
			}
		})
	}
}

// A request that names nothing valid is refused before it reaches the backend.
func TestInvalidRequestsNeverReachTheBackend(t *testing.T) {
	b := &recorder{}
	c := NewClient(serve(t, b))
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"path in a name": func() error { _, err := c.Status(ctx, ReleaseRequest{Name: "../x"}); return err },
		"empty name":     func() error { _, err := c.History(ctx, ReleaseRequest{}); return err },
		"karga's names":  func() error { _, err := c.Uninstall(ctx, UninstallRequest{Name: "karga-gateway"}); return err },
		"uppercase": func() error {
			_, err := c.Install(ctx, InstallRequest{Name: "Shop", Package: demoPkg}, nil)
			return err
		},
		"negative revision": func() error {
			_, err := c.Rollback(ctx, RollbackRequest{Name: "shop", Revision: -1}, nil)
			return err
		},
		"diff from 0":     func() error { _, err := c.Diff(ctx, DiffRequest{Name: "shop"}); return err },
		"logs of nothing": func() error { return c.Logs(ctx, LogsRequest{}, io.Discard) },
		"bad workload":    func() error { return c.Logs(ctx, LogsRequest{Release: "shop", Workloads: []string{"a/b"}}, io.Discard) },
		"bad volume": func() error {
			return c.BackupVolume(ctx, VolumeBackupRequest{Release: "shop", Volume: "../etc"}, io.Discard)
		},
		"restore bad name": func() error {
			return c.RestoreVolume(ctx, VolumeRestoreRequest{Release: "x/y", Volume: "data"}, strings.NewReader("backup"))
		},
		"exec nothing": func() error {
			_, err := c.Exec(ctx, ExecRequest{Release: "shop", Workload: "web"})
			return err
		},
		"listener port":   func() error { return c.AddGatewayListener(ctx, Listener{Name: "web", Port: 70000}) },
		"export revision": func() error { _, err := c.ExportGateway(ctx, GatewayExportRequest{Revision: -1}); return err },
		"strategy": func() error {
			_, err := c.Upgrade(ctx, UpgradeRequest{Name: "shop", Package: demoPkg, Strategy: "blue-green"}, nil)
			return err
		},
		"listen on a name": func() error { return c.SetGatewayListen(ctx, GatewayListenRequest{Addresses: []string{""}}) },
		// What cannot be changed back needs the user's agreement, whichever client sends it.
		"unconfirmed uninstall": func() error { _, err := c.Uninstall(ctx, UninstallRequest{Name: "shop"}); return err },
		"unconfirmed volume deletion": func() error {
			_, err := c.Upgrade(ctx, UpgradeRequest{CommonOptions: CommonOptions{DeleteVolumes: true}, Name: "shop", Package: demoPkg}, nil)
			return err
		},
		"unconfirmed restore": func() error {
			return c.RestoreVolume(ctx, VolumeRestoreRequest{Release: "shop", Volume: "data"}, strings.NewReader("backup"))
		},
		"unconfirmed exposure": func() error {
			return c.SetGatewayListen(ctx, GatewayListenRequest{Addresses: []string{"0.0.0.0"}})
		},
		"unconfirmed listener removal": func() error { return c.RemoveGatewayListener(ctx, GatewayListenerRequest{Name: "web"}) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
	if len(b.calls) != 0 {
		t.Fatalf("the backend got %v", b.calls)
	}
}

// required are the request fields every request carries; every other field is omitempty, so a
// newer client that leaves a new field at its zero value still talks to an older server of the
// same Major.
var required = map[string]bool{"name": true, "release": true, "workload": true, "volume": true, "from": true, "cmd": true,
	"values": true, "port": true, "protocol": true, "expr": true}

func TestOptionalRequestFieldsAreOmitted(t *testing.T) {
	for _, v := range []any{InstallRequest{}, UpgradeRequest{}, RollbackRequest{}, UninstallRequest{}, ReleaseRequest{}, RevisionRequest{},
		ValuesRequest{}, DiffRequest{}, ReconcileRequest{}, EventsRequest{}, LogsRequest{}, VolumeBackupRequest{}, VolumeRestoreRequest{},
		GatewayAccessLogRequest{}, GatewayListenRequest{}, GatewayListenerRequest{}, Listener{}, CommonOptions{}, ValuesInput{}, Set{}, execRequest{}} {
		typ := reflect.TypeOf(v)
		for f := range typ.Fields() {
			tag, ok := f.Tag.Lookup("json")
			if !ok || tag == "-" || f.Anonymous {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if !required[name] && !strings.Contains(opts, "omitempty") && !strings.Contains(opts, "omitzero") {
				t.Errorf("%s.%s: optional request fields must be omitempty", typ.Name(), f.Name)
			}
		}
	}
}

// Error messages never carry a secret the server's redactor knows, streamed or not.
func TestErrorsAreRedacted(t *testing.T) {
	b := &recorder{err: errors.New("db password hunter2 refused")}
	c := NewClient(testsock.Serve(t, NewServer(b, "dev", Options{Redact: func(s string) string { return strings.ReplaceAll(s, "hunter2", "<redacted>") }})))
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"json":   func() error { _, err := c.Status(ctx, ReleaseRequest{Name: "shop"}); return err },
		"deploy": func() error { _, err := c.Rollback(ctx, RollbackRequest{Name: "shop"}, nil); return err },
		"stream": func() error { return c.Logs(ctx, LogsRequest{Release: "shop"}, io.Discard) },
		"exec": func() error {
			_, err := c.Exec(ctx, ExecRequest{Release: "shop", Workload: "web", ExecOptions: ExecOptions{Cmd: []string{"ls"}}})
			return err
		},
	} {
		if err := call(); err == nil || strings.Contains(err.Error(), "hunter2") || !strings.Contains(err.Error(), "<redacted>") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Only what cannot be changed back has a risk: a dry run, loopback addresses and an upgrade that
// keeps volumes need no agreement.
func TestRiskFreeRequests(t *testing.T) {
	for name, r := range map[string]interface{ Risk() string }{
		"dry-run uninstall": UninstallRequest{Name: "shop", DryRun: true},
		"upgrade":           UpgradeRequest{Name: "shop"},
		"dry-run deletion":  RollbackRequest{CommonOptions: CommonOptions{DryRun: true, DeleteVolumes: true}, Name: "shop"},
		"loopback":          GatewayListenRequest{Addresses: []string{"127.0.0.1", "::1"}},
		"default listen":    GatewayListenRequest{},
	} {
		if risk := r.Risk(); risk != "" {
			t.Errorf("%s: %q", name, risk)
		}
	}
}

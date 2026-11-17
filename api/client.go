package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/hasankaradmir/karga-sdk/archive"
	"github.com/hasankaradmir/karga-sdk/packages"
	"github.com/hasankaradmir/karga-sdk/plan"
	"github.com/hasankaradmir/karga-sdk/release"
	"github.com/hasankaradmir/karga-sdk/resource"
)

// ErrNotRunning is returned when nothing listens on the socket.
var ErrNotRunning = errors.New("karga-server is not running")

// Client talks to karga-server on a unix socket. It implements Backend.
type Client struct {
	socket string
	hc     *http.Client
}

var _ Backend = (*Client)(nil)

// NewClient returns a client for the server listening on socket.
func NewClient(socket string) *Client {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}
	return &Client{socket: socket, hc: &http.Client{Transport: &http.Transport{DialContext: dial, DisableCompression: true}}}
}

func (c *Client) post(ctx context.Context, path string, body io.Reader, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://karga"+path, body)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, fmt.Errorf("%w (no server on %s): start it with 'sudo systemctl start karga-server'", ErrNotRunning, c.socket)
		}
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		var e errorResponse
		if err := json.NewDecoder(resp.Body).Decode(&e); err != nil || e.Error == "" {
			return nil, fmt.Errorf("karga-server: %s", resp.Status)
		}
		return nil, errors.New(e.Error)
	}
	return resp, nil
}

// call posts in as JSON and decodes the response into out.
func (c *Client) call(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	resp, err := c.post(ctx, path, bytes.NewReader(body), nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Version returns the server's version.
func (c *Client) Version(ctx context.Context) (string, error) {
	var v versionResponse
	err := c.call(ctx, "/v1/version", struct{}{}, &v)
	return v.Version, err
}

// packPackage reads a package directory or archive for the server.
func packPackage(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return os.ReadFile(path)
	}
	var buf bytes.Buffer
	if err := archive.Pack(path, &buf); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return buf.Bytes(), nil
}

// readValues moves values files into the request as content: the server cannot read the client's files.
func readValues(opts *packages.Options) error {
	for _, f := range opts.ValueFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		opts.ValueData = append(opts.ValueData, data)
	}
	opts.ValueFiles = nil
	return nil
}

func (c *Client) deploy(ctx context.Context, req deployRequest, progress func(plan.Event)) (*Result, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.post(ctx, "/v1/deploy", bytes.NewReader(body), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var l deployLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("karga-server: %w", err)
		}
		if l.Event != nil && progress != nil {
			progress(plan.Event{Kind: l.Event.Kind, Name: l.Event.Name, Phase: l.Event.Phase, Detail: l.Event.Detail})
		}
		if l.Done {
			if l.Error != "" {
				return l.Result, errors.New(l.Error)
			}
			return l.Result, nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("karga-server closed the connection before the operation ended")
}

// Install implements Backend.
func (c *Client) Install(ctx context.Context, req InstallRequest) (*Result, error) {
	pkg, err := packPackage(req.Package)
	if err != nil {
		return nil, err
	}
	if err := readValues(&req.Values); err != nil {
		return nil, err
	}
	progress := req.Progress
	req.Progress, req.Package = nil, ""
	return c.deploy(ctx, deployRequest{Install: &req, Package: pkg}, progress)
}

// Upgrade implements Backend.
func (c *Client) Upgrade(ctx context.Context, req UpgradeRequest) (*Result, error) {
	pkg, err := packPackage(req.Package)
	if err != nil {
		return nil, err
	}
	if err := readValues(&req.Values); err != nil {
		return nil, err
	}
	progress := req.Progress
	req.Progress, req.Package = nil, ""
	return c.deploy(ctx, deployRequest{Upgrade: &req, Package: pkg}, progress)
}

// Rollback implements Backend.
func (c *Client) Rollback(ctx context.Context, req RollbackRequest) (*Result, error) {
	progress := req.Progress
	req.Progress = nil
	return c.deploy(ctx, deployRequest{Rollback: &req}, progress)
}

// Uninstall implements Backend.
func (c *Client) Uninstall(ctx context.Context, req UninstallRequest) error {
	return c.call(ctx, "/v1/uninstall", req, nil)
}

// List implements Backend.
func (c *Client) List(ctx context.Context) ([]release.Release, error) {
	var out []release.Release
	return out, c.call(ctx, "/v1/list", struct{}{}, &out)
}

// History implements Backend.
func (c *Client) History(ctx context.Context, name string) ([]release.Revision, error) {
	var in []revisionJSON
	if err := c.call(ctx, "/v1/history", nameRequest{Name: name}, &in); err != nil {
		return nil, err
	}
	out := make([]release.Revision, len(in))
	for i, r := range in {
		out[i] = r.Revision
		out[i].Values, out[i].ValuesAll, out[i].Manifest = r.Values, r.ValuesAll, r.Manifest
	}
	return out, nil
}

// GetValues implements Backend.
func (c *Client) GetValues(ctx context.Context, name string, revision int, all bool) (map[string]any, error) {
	var out map[string]any
	return out, c.call(ctx, "/v1/values", nameRequest{Name: name, Revision: revision, All: all}, &out)
}

// GetNotes implements Backend.
func (c *Client) GetNotes(ctx context.Context, name string, revision int) (string, error) {
	var out string
	return out, c.call(ctx, "/v1/notes", nameRequest{Name: name, Revision: revision}, &out)
}

// GetManifest implements Backend.
func (c *Client) GetManifest(ctx context.Context, name string, revision int) ([]resource.Resource, error) {
	var docs []json.RawMessage
	if err := c.call(ctx, "/v1/manifest", nameRequest{Name: name, Revision: revision}, &docs); err != nil {
		return nil, err
	}
	out := make([]resource.Resource, 0, len(docs))
	for _, d := range docs {
		r, err := resource.DecodeStored(d)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Status implements Backend.
func (c *Client) Status(ctx context.Context, name string) (*StatusReport, error) {
	var out StatusReport
	return &out, c.call(ctx, "/v1/status", nameRequest{Name: name}, &out)
}

// Diff implements Backend.
func (c *Client) Diff(ctx context.Context, name string, from, to int) (*RevisionDiff, error) {
	var out RevisionDiff
	return &out, c.call(ctx, "/v1/diff", nameRequest{Name: name, From: from, To: to}, &out)
}

// Resources implements Backend.
func (c *Client) Resources(ctx context.Context, name string) ([]release.AppliedResource, error) {
	var out []release.AppliedResource
	return out, c.call(ctx, "/v1/resources", nameRequest{Name: name}, &out)
}

// download copies a streamed response to w and returns the error the server put in its trailer.
func (c *Client) download(ctx context.Context, path string, in any, w io.Writer) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	resp, err := c.post(ctx, path, bytes.NewReader(body), nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(w, resp.Body); err != nil {
		if ctx.Err() != nil {
			return nil // the caller stopped (e.g. logs -f interrupted)
		}
		return err
	}
	if msg := resp.Trailer.Get(trailerError); msg != "" {
		return errors.New(msg)
	}
	return nil
}

// Logs implements Backend.
func (c *Client) Logs(ctx context.Context, req LogsRequest, w io.Writer) error {
	return c.download(ctx, "/v1/logs", req, w)
}

// BackupVolume implements Backend.
func (c *Client) BackupVolume(ctx context.Context, req VolumeBackupRequest) error {
	return c.download(ctx, "/v1/volume/backup", volumeRequest{Release: req.Release, Volume: req.Volume, Stop: req.Stop}, req.W)
}

// RestoreVolume implements Backend.
func (c *Client) RestoreVolume(ctx context.Context, req VolumeRestoreRequest) error {
	q := url.Values{"release": {req.Release}, "volume": {req.Volume}, "name": {req.Name}, "force": {strconv.FormatBool(req.Force)}}
	resp, err := c.post(ctx, "/v1/volume/restore?"+q.Encode(), req.R, http.Header{"Content-Type": {"application/gzip"}})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// BackupState implements Backend.
func (c *Client) BackupState(ctx context.Context, w io.Writer) error {
	return c.download(ctx, "/v1/state/backup", struct{}{}, w)
}

// Gateway implements Backend.
func (c *Client) Gateway(ctx context.Context) ([]Exposed, error) {
	var out []Exposed
	return out, c.call(ctx, "/v1/gateway/routes", struct{}{}, &out)
}

// GatewayHealth implements Backend.
func (c *Client) GatewayHealth(ctx context.Context) (State, error) {
	var out State
	return out, c.call(ctx, "/v1/gateway/health", struct{}{}, &out)
}

// GatewaySettings implements Backend.
func (c *Client) GatewaySettings(ctx context.Context) (Settings, map[string][]string, error) {
	var out gatewaySettings
	err := c.call(ctx, "/v1/gateway/settings", struct{}{}, &out)
	return out.Settings, out.Users, err
}

// SetGatewayAccessLog implements Backend.
func (c *Client) SetGatewayAccessLog(ctx context.Context, on bool) error {
	return c.call(ctx, "/v1/gateway/change", gatewayChange{AccessLog: &on}, nil)
}

// SetGatewayListen implements Backend.
func (c *Client) SetGatewayListen(ctx context.Context, addrs []string) error {
	if addrs == nil {
		addrs = []string{}
	}
	return c.call(ctx, "/v1/gateway/change", gatewayChange{Listen: addrs}, nil)
}

// AddGatewayListener implements Backend.
func (c *Client) AddGatewayListener(ctx context.Context, l Listener) error {
	return c.call(ctx, "/v1/gateway/change", gatewayChange{Add: &l}, nil)
}

// RemoveGatewayListener implements Backend.
func (c *Client) RemoveGatewayListener(ctx context.Context, name string) error {
	return c.call(ctx, "/v1/gateway/change", gatewayChange{Remove: name}, nil)
}

// Exec implements Backend: the connection is upgraded and the streams travel as frames.
func (c *Client) Exec(ctx context.Context, rel, workload string, opts ExecOptions) (int, error) {
	body, err := json.Marshal(execRequest{Release: rel, Workload: workload, Cmd: opts.Cmd, TTY: opts.TTY,
		Stdin: opts.Stdin != nil, Width: opts.Width, Height: opts.Height})
	if err != nil {
		return -1, err
	}
	resp, err := c.post(ctx, "/v1/exec", bytes.NewReader(body), http.Header{"Connection": {"Upgrade"}, "Upgrade": {"karga-exec"}})
	if err != nil {
		return -1, err
	}
	conn, ok := resp.Body.(io.ReadWriteCloser)
	if resp.StatusCode != http.StatusSwitchingProtocols || !ok {
		_ = resp.Body.Close()
		return -1, fmt.Errorf("karga-server: exec: %s", resp.Status)
	}
	defer func() { _ = conn.Close() }()
	out := &frameWriter{w: conn}
	if opts.Stdin != nil {
		go func() {
			_, _ = io.Copy(out.stream(frameStdin), opts.Stdin)
			_ = out.frame(frameStdinEOF, nil)
		}()
	}
	if opts.Resize != nil {
		go func() {
			for size := range opts.Resize {
				if out.frame(frameResize, resizePayload(size[0], size[1])) != nil {
					return
				}
			}
		}()
	}
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	stdout, stderr := opts.Stdout, opts.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	for {
		typ, p, err := readFrame(conn)
		if err != nil {
			if ctx.Err() != nil {
				return -1, ctx.Err()
			}
			return -1, fmt.Errorf("exec: %w", err)
		}
		switch typ {
		case frameStdout:
			_, _ = stdout.Write(p)
		case frameStderr:
			_, _ = stderr.Write(p)
		case frameExit:
			return strconv.Atoi(strings.TrimSpace(string(p)))
		case frameError:
			return -1, errors.New(string(p))
		}
	}
}

// Reconcile implements Backend.
func (c *Client) Reconcile(ctx context.Context, name string, force bool) (*ReconcileResult, error) {
	var out ReconcileResult
	return &out, c.call(ctx, "/v1/reconcile", nameRequest{Name: name, Force: force}, &out)
}

// Pause implements Backend.
func (c *Client) Pause(ctx context.Context, name string, paused bool) error {
	return c.call(ctx, "/v1/pause", nameRequest{Name: name, Paused: paused}, nil)
}

// Events implements Backend.
func (c *Client) Events(ctx context.Context, name string, afterID int64, limit int) ([]release.Operation, error) {
	var out []release.Operation
	return out, c.call(ctx, "/v1/events", nameRequest{Name: name, After: afterID, Limit: limit}, &out)
}

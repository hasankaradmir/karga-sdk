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

	"github.com/hasankaradmir/karga-sdk/internal/archive"
	"github.com/hasankaradmir/karga-sdk/resource"
)

// Client talks to karga-server on a unix socket. It implements Remote.
type Client struct {
	socket string
	hc     *http.Client
}

var _ Remote = (*Client)(nil)

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
	req.Header.Set(apiHeader, Version)
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
		return nil, remote(e.Error, e.Code)
	}
	return resp, nil
}

// call posts in as JSON and decodes the response into out (nothing for struct{}).
func call[Out any](ctx context.Context, c *Client, path string, in any) (Out, error) {
	var out Out
	body, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	resp, err := c.post(ctx, path, bytes.NewReader(body), nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = resp.Body.Close() }()
	if _, ok := any(out).(struct{}); ok {
		_, _ = io.Copy(io.Discard, resp.Body)
		return out, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		var zero Out
		return zero, err
	}
	return out, nil
}

// do is call for operations that answer nothing.
func do(ctx context.Context, c *Client, path string, in any) error {
	_, err := call[struct{}](ctx, c, path, in)
	return err
}

// Version returns the server's version.
func (c *Client) Version(ctx context.Context) (ServerVersion, error) {
	return call[ServerVersion](ctx, c, "/v1/version", struct{}{})
}

// maxPackageArchive bounds a package archive the client sends: it travels base64-encoded in a
// request of at most 64 MiB.
const maxPackageArchive = 32 << 20

// packPackage reads a package directory or archive for the server.
func packPackage(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		// The server takes at most 64 MiB of request; a larger archive is refused here, unread.
		if info.Size() > maxPackageArchive {
			return nil, fmt.Errorf("%s: %d bytes; a package archive may have at most %d", path, info.Size(), maxPackageArchive)
		}
		return os.ReadFile(path)
	}
	var buf bytes.Buffer
	if err := archive.Pack(path, &buf, archive.DefaultLimits); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return buf.Bytes(), nil
}

func (c *Client) deploy(ctx context.Context, path string, req any, progress func(Event)) (*Result, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.post(ctx, path, bytes.NewReader(body), nil)
	if err != nil {
		return nil, err
	}
	res, err := c.follow(resp, progress)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%w: %w", ErrDetached, ctx.Err())
	}
	return res, err
}

// follow reads the progress lines of a deploy response until its outcome.
// Canceling the request's context closes resp's body, which ends the loop.
func (c *Client) follow(resp *http.Response, progress func(Event)) (*Result, error) {
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	for sc.Scan() {
		var l deployLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("karga-server: %w", err)
		}
		if l.Event != nil && progress != nil {
			progress(*l.Event)
		}
		if l.Done {
			if l.Error != "" {
				return l.Result, remote(l.Error, l.Code)
			}
			return l.Result, nil
		}
	}
	if err := sc.Err(); errors.Is(err, bufio.ErrTooLong) {
		return nil, fmt.Errorf("karga-server: a progress line is longer than %d bytes", maxLine)
	} else if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("%w: %w", ErrDisconnected, err)
	}
	return nil, ErrDisconnected
}

// maxLine bounds one line of a deploy response.
const maxLine = 16 << 20

// Install implements Backend.
func (c *Client) Install(ctx context.Context, req InstallRequest, progress func(Event)) (*Result, error) {
	pkg, err := packPackage(req.Package)
	if err != nil {
		return nil, err
	}
	return c.deploy(ctx, "/v1/install", deployRequest[InstallRequest]{Request: req, Package: pkg}, progress)
}

// Upgrade implements Backend.
func (c *Client) Upgrade(ctx context.Context, req UpgradeRequest, progress func(Event)) (*Result, error) {
	pkg, err := packPackage(req.Package)
	if err != nil {
		return nil, err
	}
	return c.deploy(ctx, "/v1/upgrade", deployRequest[UpgradeRequest]{Request: req, Package: pkg}, progress)
}

// Rollback implements Backend.
func (c *Client) Rollback(ctx context.Context, req RollbackRequest, progress func(Event)) (*Result, error) {
	return c.deploy(ctx, "/v1/rollback", deployRequest[RollbackRequest]{Request: req}, progress)
}

// Uninstall implements Backend.
func (c *Client) Uninstall(ctx context.Context, req UninstallRequest) (*Result, error) {
	return call[*Result](ctx, c, "/v1/uninstall", req)
}

// List implements Backend.
func (c *Client) List(ctx context.Context) ([]Release, error) {
	return call[[]Release](ctx, c, "/v1/list", struct{}{})
}

// History implements Backend.
func (c *Client) History(ctx context.Context, req ReleaseRequest) ([]Revision, error) {
	return call[[]Revision](ctx, c, "/v1/history", req)
}

// Values implements Backend.
func (c *Client) Values(ctx context.Context, req ValuesRequest) (map[string]any, error) {
	return call[map[string]any](ctx, c, "/v1/values", req)
}

// Notes implements Backend.
func (c *Client) Notes(ctx context.Context, req RevisionRequest) (string, error) {
	return call[string](ctx, c, "/v1/notes", req)
}

// Manifest implements Backend.
func (c *Client) Manifest(ctx context.Context, req RevisionRequest) ([]resource.Resource, error) {
	docs, err := call[[]json.RawMessage](ctx, c, "/v1/manifest", req)
	if err != nil {
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
func (c *Client) Status(ctx context.Context, req ReleaseRequest) (*StatusReport, error) {
	return call[*StatusReport](ctx, c, "/v1/status", req)
}

// Diff implements Backend.
func (c *Client) Diff(ctx context.Context, req DiffRequest) (*RevisionDiff, error) {
	return call[*RevisionDiff](ctx, c, "/v1/diff", req)
}

// Resources implements Backend.
func (c *Client) Resources(ctx context.Context, req ReleaseRequest) ([]AppliedResource, error) {
	return call[[]AppliedResource](ctx, c, "/v1/resources", req)
}

// Events implements Backend.
func (c *Client) Events(ctx context.Context, req EventsRequest) ([]Operation, error) {
	return call[[]Operation](ctx, c, "/v1/events", req)
}

// Reconcile implements Backend.
func (c *Client) Reconcile(ctx context.Context, req ReconcileRequest) (*ReconcileResult, error) {
	return call[*ReconcileResult](ctx, c, "/v1/reconcile", req)
}

// Pause implements Backend.
func (c *Client) Pause(ctx context.Context, req ReleaseRequest) error {
	return do(ctx, c, "/v1/pause", req)
}

// Resume implements Backend.
func (c *Client) Resume(ctx context.Context, req ReleaseRequest) error {
	return do(ctx, c, "/v1/resume", req)
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
		return remote(msg, resp.Trailer.Get(trailerCode))
	}
	return nil
}

// Logs implements Backend.
func (c *Client) Logs(ctx context.Context, req LogsRequest, w io.Writer) error {
	return c.download(ctx, "/v1/logs", req, w)
}

// BackupVolume implements Backend.
func (c *Client) BackupVolume(ctx context.Context, req VolumeBackupRequest, w io.Writer) error {
	return c.download(ctx, "/v1/volume/backup", req, w)
}

// RestoreVolume implements Backend.
func (c *Client) RestoreVolume(ctx context.Context, req VolumeRestoreRequest, r io.Reader) error {
	q := url.Values{"release": {req.Release}, "volume": {req.Volume}, "name": {req.Name}, "force": {strconv.FormatBool(req.Force)},
		"confirmed": {strconv.FormatBool(req.Confirmed)}}
	resp, err := c.post(ctx, "/v1/volume/restore?"+q.Encode(), r, http.Header{"Content-Type": {"application/gzip"}})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// GatewayRoutes implements Backend.
func (c *Client) GatewayRoutes(ctx context.Context) ([]Exposed, error) {
	return call[[]Exposed](ctx, c, "/v1/gateway/routes", struct{}{})
}

// GatewayHealth implements Backend.
func (c *Client) GatewayHealth(ctx context.Context) (State, error) {
	return call[State](ctx, c, "/v1/gateway/health", struct{}{})
}

// GatewaySettings implements Backend.
func (c *Client) GatewaySettings(ctx context.Context) (Settings, map[string][]string, error) {
	out, err := call[gatewaySettings](ctx, c, "/v1/gateway/settings", struct{}{})
	return out.Settings, out.Users, err
}

// SetGatewayAccessLog implements Backend.
func (c *Client) SetGatewayAccessLog(ctx context.Context, req GatewayAccessLogRequest) error {
	return do(ctx, c, "/v1/gateway/access-log", req)
}

// SetGatewayListen implements Backend.
func (c *Client) SetGatewayListen(ctx context.Context, req GatewayListenRequest) error {
	return do(ctx, c, "/v1/gateway/listen", req)
}

// AddGatewayListener implements Backend.
func (c *Client) AddGatewayListener(ctx context.Context, l Listener) error {
	return do(ctx, c, "/v1/gateway/listeners/add", l)
}

// SetGatewayListener implements Backend.
func (c *Client) SetGatewayListener(ctx context.Context, req GatewayListenerSetRequest) error {
	return do(ctx, c, "/v1/gateway/listeners/set", req)
}

// ValidateGateway implements Backend.
func (c *Client) ValidateGateway(ctx context.Context) (GatewayPlan, error) {
	return call[GatewayPlan](ctx, c, "/v1/gateway/validate", struct{}{})
}

// ApplyGateway implements Backend.
func (c *Client) ApplyGateway(ctx context.Context, req GatewayApplyRequest) error {
	return do(ctx, c, "/v1/gateway/apply", req)
}

// RemoveGatewayListener implements Backend.
func (c *Client) RemoveGatewayListener(ctx context.Context, req GatewayListenerRequest) error {
	return do(ctx, c, "/v1/gateway/listeners/remove", req)
}

// Exec implements Backend: the connection is upgraded and the streams travel as frames.
func (c *Client) Exec(ctx context.Context, req ExecRequest) (int, error) {
	opts := req.ExecOptions
	body, err := json.Marshal(execRequest{Release: req.Release, Workload: req.Workload, Cmd: opts.Cmd, TTY: opts.TTY,
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
	// Canceling ctx ends the command's session; stop releases the watch when Exec returns.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
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
			var e errorResponse
			if json.Unmarshal(p, &e) != nil {
				return -1, errors.New(string(p))
			}
			return -1, remote(e.Error, e.Code)
		}
	}
}

package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Server serves a Backend over HTTP.
type Server struct {
	b       Backend
	version string
	opts    Options
	mux     *http.ServeMux
}

// Options tune a Server; zero values use the defaults.
type Options struct {
	// MaxRequest bounds request bodies other than volume restores (default 64 MiB: a package archive
	// is the largest).
	MaxRequest int64
	// TempDir holds uploaded packages and volume backups while they are used (default os.TempDir()).
	TempDir string
	// MaxRestore bounds an uploaded volume backup (default 16 GiB).
	MaxRestore int64
	// Classify returns the kind (ErrNotFound, ErrConflict, ErrInvalid) of an error the Backend
	// returned that does not wrap one already, or nil: the client then sees an internal error.
	Classify func(error) error
	// Redact, if set, masks secret values in every error message the server sends.
	Redact func(string) string
}

// NewServer serves b; version is reported to clients.
func NewServer(b Backend, version string, opts Options) *Server {
	if opts.MaxRequest == 0 {
		opts.MaxRequest = 64 << 20
	}
	if opts.MaxRestore == 0 {
		opts.MaxRestore = 16 << 30
	}
	if opts.Redact == nil {
		opts.Redact = func(s string) string { return s }
	}
	if opts.Classify == nil {
		opts.Classify = func(error) error { return nil }
	}
	s := &Server{b: b, version: version, opts: opts, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, ServerVersion{Version: s.version, API: Version})
	})
	s.mux.HandleFunc("POST /v1/install", deploy(s, func(ctx context.Context, req InstallRequest, pkg string, progress func(Event)) (*Result, error) {
		req.Package = pkg
		return b.Install(ctx, req, progress)
	}))
	s.mux.HandleFunc("POST /v1/upgrade", deploy(s, func(ctx context.Context, req UpgradeRequest, pkg string, progress func(Event)) (*Result, error) {
		req.Package = pkg
		return b.Upgrade(ctx, req, progress)
	}))
	s.mux.HandleFunc("POST /v1/rollback", deploy(s, func(ctx context.Context, req RollbackRequest, _ string, progress func(Event)) (*Result, error) {
		return b.Rollback(ctx, req, progress)
	}))
	s.mux.HandleFunc("POST /v1/uninstall", mutation(s, b.Uninstall))
	s.mux.HandleFunc("POST /v1/list", handle(s, func(ctx context.Context, _ struct{}) ([]Release, error) { return b.List(ctx) }))
	s.mux.HandleFunc("POST /v1/history", handle(s, b.History))
	s.mux.HandleFunc("POST /v1/values", handle(s, b.Values))
	s.mux.HandleFunc("POST /v1/notes", handle(s, b.Notes))
	s.mux.HandleFunc("POST /v1/manifest", handle(s, b.Manifest))
	s.mux.HandleFunc("POST /v1/status", handle(s, b.Status))
	s.mux.HandleFunc("POST /v1/diff", handle(s, b.Diff))
	s.mux.HandleFunc("POST /v1/resources", handle(s, b.Resources))
	s.mux.HandleFunc("POST /v1/events", handle(s, b.Events))
	s.mux.HandleFunc("POST /v1/reconcile", mutation(s, b.Reconcile))
	s.mux.HandleFunc("POST /v1/pause", mutation(s, noResult(b.Pause)))
	s.mux.HandleFunc("POST /v1/resume", mutation(s, noResult(b.Resume)))
	s.mux.HandleFunc("POST /v1/gateway/routes", handle(s, func(ctx context.Context, _ struct{}) ([]Exposed, error) { return b.GatewayRoutes(ctx) }))
	s.mux.HandleFunc("POST /v1/gateway/health", handle(s, func(ctx context.Context, _ struct{}) (State, error) { return b.GatewayHealth(ctx) }))
	s.mux.HandleFunc("POST /v1/gateway/settings", handle(s, func(ctx context.Context, _ struct{}) (gatewaySettings, error) {
		settings, users, err := b.GatewaySettings(ctx)
		return gatewaySettings{settings, users}, err
	}))
	s.mux.HandleFunc("POST /v1/gateway/access-log", mutation(s, noResult(b.SetGatewayAccessLog)))
	s.mux.HandleFunc("POST /v1/gateway/listen", mutation(s, noResult(b.SetGatewayListen)))
	s.mux.HandleFunc("POST /v1/gateway/listeners/add", mutation(s, noResult(b.AddGatewayListener)))
	s.mux.HandleFunc("POST /v1/gateway/listeners/remove", mutation(s, noResult(b.RemoveGatewayListener)))
	s.mux.HandleFunc("POST /v1/logs", s.logs)
	s.mux.HandleFunc("POST /v1/exec", s.exec)
	s.mux.HandleFunc("POST /v1/volume/backup", s.volumeBackup)
	s.mux.HandleFunc("POST /v1/volume/restore", s.volumeRestore)
	return s
}

// ServeHTTP implements http.Handler. It refuses clients of another Major API version;
// /v1/version answers every client.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get(apiHeader); !Compatible(got) && r.URL.Path != "/v1/version" {
		s.writeError(w, apiMismatch(got))
		return
	}
	if _, pattern := s.mux.Handler(r); pattern == "" {
		s.writeError(w, fmt.Errorf("%w: karga-server (API %s) does not know %s; upgrade karga-server", ErrIncompatible, Version, r.URL.Path))
		return
	}
	s.mux.ServeHTTP(w, r)
}

func apiMismatch(client string) error {
	upgrade := "karga"
	if n, _ := strconv.Atoi(strings.SplitN(client, ".", 2)[0]); n > Major {
		upgrade = "karga-server"
	}
	if client == "" {
		client = "none"
	}
	return fmt.Errorf("%w: karga speaks API %s, karga-server API %s: upgrade %s", ErrIncompatible, client, Version, upgrade)
}

type clientKey struct{}

// detach returns a context that goes on when the client goes away, carrying the client's for
// ClientContext.
func detach(ctx context.Context) context.Context {
	return context.WithValue(context.WithoutCancel(ctx), clientKey{}, ctx)
}

// ClientContext returns the context of the client's request. A Backend's mutation runs on a context
// the client's going away does not cancel, so nothing is left half done; what only waits before it
// changes anything (for a release's lock, say) can stop when the client's is done instead. For a
// context that is not a mutation's, it returns ctx.
func ClientContext(ctx context.Context) context.Context {
	if c, ok := ctx.Value(clientKey{}).(context.Context); ok {
		return c
	}
	return ctx
}

// mutation is handle for requests that change something: they go on to the end when the client
// goes away (Ctrl-C, a broken connection), so nothing is left half done because of the client.
func mutation[Req, Out any](s *Server, fn func(context.Context, Req) (Out, error)) http.HandlerFunc {
	return handle(s, func(ctx context.Context, req Req) (Out, error) { return fn(detach(ctx), req) })
}

// handle serves a JSON endpoint: it decodes and validates the request, calls fn and writes its
// answer or its error.
func handle[Req, Out any](s *Server, fn func(context.Context, Req) (Out, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Req
		if !s.decode(w, r, &req) {
			return
		}
		out, err := fn(r.Context(), req)
		if err != nil {
			s.writeError(w, err)
			return
		}
		writeJSON(w, out)
	}
}

// noResult adapts an operation that answers nothing to handle.
func noResult[Req any](fn func(context.Context, Req) error) func(context.Context, Req) (struct{}, error) {
	return func(ctx context.Context, req Req) (struct{}, error) { return struct{}{}, fn(ctx, req) }
}

// decode reads the JSON request body into v; on failure it answers the request and returns false.
// An empty body leaves v as it is.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, s.opts.MaxRequest))
	// A field this server does not know comes from a newer client: say so rather than ignore it.
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil || errors.Is(err, io.EOF) {
		if err := validate(v); err != nil {
			s.writeError(w, err)
			return false
		}
		return true
	}
	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		s.writeError(w, fmt.Errorf("%w: karga-server (API %s) does not know request field %s; upgrade karga-server",
			ErrIncompatible, Version, strings.TrimPrefix(err.Error(), "json: unknown field ")))
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = fmt.Errorf("request larger than %d bytes or cut short", s.opts.MaxRequest)
	}
	s.writeError(w, fmt.Errorf("%w: %w", ErrInvalid, err))
	return false
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// message is what the client is told of err, secret values masked.
func (s *Server) message(err error) string { return s.opts.Redact(err.Error()) }

// codeOf is the error code and HTTP status the client gets for err.
func (s *Server) codeOf(err error) (string, int) {
	code, status := kindOf(err)
	if code == "internal" {
		if kind := s.opts.Classify(err); kind != nil {
			code, status = kindOf(kind)
		}
	}
	return code, status
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	code, status := s.codeOf(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: s.message(err), Code: code})
}

// deploy serves an install, upgrade or rollback, streaming progress events as JSON lines.
func deploy[Req any](s *Server, fn func(ctx context.Context, req Req, pkg string, progress func(Event)) (*Result, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req deployRequest[Req]
		if !s.decode(w, r, &req) {
			return
		}
		if err := validate(req.Request); err != nil {
			s.writeError(w, err)
			return
		}
		var pkgPath string
		if req.Package != nil {
			dir, err := os.MkdirTemp(s.opts.TempDir, "karga-package-")
			if err != nil {
				s.writeError(w, err)
				return
			}
			defer func() { _ = os.RemoveAll(dir) }()
			pkgPath = filepath.Join(dir, "package.tgz")
			if err := os.WriteFile(pkgPath, req.Package, 0o600); err != nil {
				s.writeError(w, err)
				return
			}
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		// The client knows the operation was received once the headers arrive.
		w.WriteHeader(http.StatusOK)
		_ = http.NewResponseController(w).Flush()
		// Lines are written by one goroutine so a slow or stopped client never holds up the operation:
		// progress events are dropped when it falls behind, and a stalled write gives up after a while.
		lines := make(chan deployLine, 64)
		written := make(chan struct{})
		go func() {
			defer close(written)
			rc := http.NewResponseController(w)
			enc := json.NewEncoder(w)
			for l := range lines {
				_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
				if enc.Encode(l) == nil {
					_ = rc.Flush()
				}
			}
		}()
		progress := func(e Event) {
			select {
			case lines <- deployLine{Event: &e}:
			default: // the client is behind; it still gets the result
			}
		}
		// The operation goes on if the client goes away; only its progress reports are lost.
		ctx := detach(r.Context())
		res, err := fn(ctx, req.Request, pkgPath, progress)
		line := deployLine{Result: res, Done: true}
		if err != nil {
			line.Error = s.message(err)
			line.Code, _ = s.codeOf(err)
		}
		lines <- line
		close(lines)
		<-written
	}
}

// writeTimeout bounds one write to a client that stopped reading.
const writeTimeout = 10 * time.Second

// streamed writes a streamed response body. The status is sent with the first byte of the body, so
// an error before it (an unknown release) is answered as any other error, with its kind; an error
// after it goes in trailers, with its kind too.
func (s *Server) streamed(w http.ResponseWriter, contentType string, fn func(io.Writer) error) {
	started := false
	start := func() {
		if !started {
			started = true
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Trailer", trailerError+", "+trailerCode)
			w.WriteHeader(http.StatusOK)
		}
	}
	flusher, _ := w.(http.Flusher)
	out := writerFunc(func(p []byte) (int, error) {
		start()
		n, err := w.Write(p)
		if flusher != nil {
			flusher.Flush()
		}
		return n, err
	})
	err := fn(out)
	if err != nil && !started {
		s.writeError(w, err)
		return
	}
	start()
	if err != nil {
		code, _ := s.codeOf(err)
		w.Header().Set(trailerError, s.message(err))
		w.Header().Set(trailerCode, code)
	}
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	var req LogsRequest
	if !s.decode(w, r, &req) {
		return
	}
	s.streamed(w, "text/plain", func(out io.Writer) error {
		err := s.b.Logs(r.Context(), req, out)
		if errors.Is(err, context.Canceled) {
			return nil // the client stopped following
		}
		return err
	})
}

func (s *Server) volumeBackup(w http.ResponseWriter, r *http.Request) {
	var req VolumeBackupRequest
	if !s.decode(w, r, &req) {
		return
	}
	s.streamed(w, "application/gzip", func(out io.Writer) error {
		return s.b.BackupVolume(r.Context(), req, out)
	})
}

// volumeRestore receives the whole backup before it touches the volume, then restores it to the
// end even if the client goes away: a restore cut short would leave the volume half overwritten.
func (s *Server) volumeRestore(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	force, err := strconv.ParseBool(cmp.Or(q.Get("force"), "false"))
	confirmed, cerr := strconv.ParseBool(cmp.Or(q.Get("confirmed"), "false"))
	err = errors.Join(err, cerr)
	req := VolumeRestoreRequest{Release: q.Get("release"), Volume: q.Get("volume"), Force: force, Name: q.Get("name"), Confirmed: confirmed}
	if err == nil {
		err = validate(req)
	} else {
		err = fmt.Errorf("%w: force, confirmed: %w", ErrInvalid, err)
	}
	if err != nil {
		s.writeError(w, err)
		return
	}
	f, err := os.CreateTemp(s.opts.TempDir, "karga-restore-")
	if err != nil {
		s.writeError(w, err)
		return
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	n, err := io.Copy(f, io.LimitReader(r.Body, s.opts.MaxRestore+1))
	if err != nil {
		s.writeError(w, fmt.Errorf("receiving the backup: %w", err))
		return
	}
	if n > s.opts.MaxRestore {
		s.writeError(w, fmt.Errorf("%w: the backup is larger than %d bytes", ErrInvalid, s.opts.MaxRestore))
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.writeError(w, err)
		return
	}
	err = s.b.RestoreVolume(detach(r.Context()), req, f)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, struct{}{})
}

// exec upgrades the connection and carries the command's streams as frames.
func (s *Server) exec(w http.ResponseWriter, r *http.Request) {
	var req execRequest
	if !s.decode(w, r, &req) {
		return
	}
	if err := validate(ExecRequest{Release: req.Release, Workload: req.Workload, ExecOptions: ExecOptions{Cmd: req.Cmd}}); err != nil {
		s.writeError(w, err)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		s.writeError(w, errors.New("exec: the connection cannot be upgraded"))
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		s.writeError(w, err)
		return
	}
	defer func() { _ = conn.Close() }()
	if _, err := buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: karga-exec\r\nConnection: Upgrade\r\n\r\n"); err != nil {
		return
	}
	if err := buf.Flush(); err != nil {
		return
	}
	out := &frameWriter{w: conn}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdin *io.PipeWriter
	opts := ExecOptions{Cmd: req.Cmd, TTY: req.TTY, Width: req.Width, Height: req.Height,
		Stdout: out.stream(frameStdout), Stderr: out.stream(frameStderr)}
	if req.Stdin {
		pr, pw := io.Pipe()
		opts.Stdin, stdin = pr, pw
	}
	resize := make(chan [2]uint, 1)
	if req.TTY {
		opts.Resize = resize
	}
	go func() { // input from the client; a closed connection ends the command
		defer cancel()
		for {
			typ, p, err := readFrame(buf)
			if err != nil {
				if stdin != nil {
					_ = stdin.CloseWithError(err)
				}
				return
			}
			switch typ {
			case frameStdin:
				if stdin != nil {
					if _, err := stdin.Write(p); err != nil {
						return
					}
				}
			case frameStdinEOF:
				if stdin != nil {
					_ = stdin.Close()
				}
			case frameResize:
				if size, err := parseResize(p); err == nil && req.TTY {
					select {
					case resize <- size:
					default:
					}
				}
			}
		}
	}()
	code, err := s.b.Exec(ctx, ExecRequest{Release: req.Release, Workload: req.Workload, ExecOptions: opts})
	if err != nil {
		code, _ := s.codeOf(err)
		payload, _ := json.Marshal(errorResponse{Error: s.message(err), Code: code})
		_ = out.frame(frameError, payload)
		return
	}
	_ = out.frame(frameExit, []byte(strconv.Itoa(code)))
}

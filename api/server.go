package api

import (
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
}

// NewServer serves b; version is reported to clients.
func NewServer(b Backend, version string, opts Options) *Server {
	if opts.MaxRequest == 0 {
		opts.MaxRequest = 64 << 20
	}
	if opts.MaxRestore == 0 {
		opts.MaxRestore = 16 << 30
	}
	if opts.Classify == nil {
		opts.Classify = func(error) error { return nil }
	}
	s := &Server{b: b, version: version, opts: opts, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, ServerVersion{Version: s.version, API: Version})
	})
	s.mux.HandleFunc("POST /v1/deploy", s.deploy)
	s.mux.HandleFunc("POST /v1/uninstall", mutation(s, func(ctx context.Context, req UninstallRequest) (any, error) {
		return b.Uninstall(ctx, req)
	}))
	s.mux.HandleFunc("POST /v1/list", handle(s, func(ctx context.Context, _ struct{}) (any, error) { return b.List(ctx) }))
	s.mux.HandleFunc("POST /v1/history", handle(s, func(ctx context.Context, req nameRequest) (any, error) {
		return b.History(ctx, req.Name)
	}))
	s.mux.HandleFunc("POST /v1/values", handle(s, func(ctx context.Context, req nameRequest) (any, error) {
		return b.GetValues(ctx, req.Name, req.Revision, req.All)
	}))
	s.mux.HandleFunc("POST /v1/notes", handle(s, func(ctx context.Context, req nameRequest) (any, error) {
		return b.GetNotes(ctx, req.Name, req.Revision)
	}))
	s.mux.HandleFunc("POST /v1/manifest", handle(s, func(ctx context.Context, req nameRequest) (any, error) {
		return b.GetManifest(ctx, req.Name, req.Revision)
	}))
	s.mux.HandleFunc("POST /v1/status", handle(s, func(ctx context.Context, req nameRequest) (any, error) { return b.Status(ctx, req.Name) }))
	s.mux.HandleFunc("POST /v1/diff", handle(s, func(ctx context.Context, req nameRequest) (any, error) {
		return b.Diff(ctx, req.Name, req.From, req.To)
	}))
	s.mux.HandleFunc("POST /v1/resources", handle(s, func(ctx context.Context, req nameRequest) (any, error) {
		return b.Resources(ctx, req.Name)
	}))
	s.mux.HandleFunc("POST /v1/reconcile", mutation(s, func(ctx context.Context, req nameRequest) (any, error) {
		return b.Reconcile(ctx, req.Name, req.Force)
	}))
	s.mux.HandleFunc("POST /v1/pause", mutation(s, func(ctx context.Context, req nameRequest) (any, error) {
		return struct{}{}, b.Pause(ctx, req.Name, req.Paused)
	}))
	s.mux.HandleFunc("POST /v1/events", handle(s, func(ctx context.Context, req nameRequest) (any, error) {
		return b.Events(ctx, req.Name, req.After, req.Limit)
	}))
	s.mux.HandleFunc("POST /v1/gateway/routes", handle(s, func(ctx context.Context, _ struct{}) (any, error) { return b.Gateway(ctx) }))
	s.mux.HandleFunc("POST /v1/gateway/health", handle(s, func(ctx context.Context, _ struct{}) (any, error) { return b.GatewayHealth(ctx) }))
	s.mux.HandleFunc("POST /v1/gateway/settings", handle(s, func(ctx context.Context, _ struct{}) (any, error) {
		settings, users, err := b.GatewaySettings(ctx)
		return gatewaySettings{settings, users}, err
	}))
	s.mux.HandleFunc("POST /v1/gateway/change", mutation(s, func(ctx context.Context, req gatewayChange) (any, error) {
		switch {
		case req.AccessLog != nil:
			return struct{}{}, b.SetGatewayAccessLog(ctx, *req.AccessLog)
		case req.Listen != nil:
			return struct{}{}, b.SetGatewayListen(ctx, req.Listen)
		case req.Add != nil:
			return struct{}{}, b.AddGatewayListener(ctx, *req.Add)
		default:
			return struct{}{}, b.RemoveGatewayListener(ctx, req.Remove)
		}
	}))
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

// mutation is handle for requests that change something: they go on to the end when the client
// goes away (Ctrl-C, a broken connection), so nothing is left half done because of the client.
func mutation[Req any](s *Server, fn func(context.Context, Req) (any, error)) http.HandlerFunc {
	return handle(s, func(ctx context.Context, req Req) (any, error) { return fn(context.WithoutCancel(ctx), req) })
}

func handle[Req any](s *Server, fn func(context.Context, Req) (any, error)) http.HandlerFunc {
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

// decode reads the JSON request body into v; on failure it answers the request and returns false.
// An empty body leaves v as it is.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, s.opts.MaxRequest))
	// A field this server does not know comes from a newer client: say so rather than ignore it.
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil || errors.Is(err, io.EOF) {
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
	_ = json.NewEncoder(w).Encode(errorResponse{Error: err.Error(), Code: code})
}

// deploy runs an install, upgrade or rollback, streaming progress events as JSON lines.
func (s *Server) deploy(w http.ResponseWriter, r *http.Request) {
	var req deployRequest
	if !s.decode(w, r, &req) {
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
	ctx := context.WithoutCancel(r.Context())
	var res *Result
	var err error
	switch {
	case req.Install != nil:
		in := *req.Install
		in.Package = pkgPath
		res, err = s.b.Install(ctx, in, progress)
	case req.Upgrade != nil:
		in := *req.Upgrade
		in.Package = pkgPath
		res, err = s.b.Upgrade(ctx, in, progress)
	case req.Rollback != nil:
		res, err = s.b.Rollback(ctx, *req.Rollback, progress)
	default:
		err = fmt.Errorf("%w: no install, upgrade or rollback", ErrInvalid)
	}
	line := deployLine{Result: res, Done: true}
	if err != nil {
		line.Error = err.Error()
		line.Code, _ = s.codeOf(err)
	}
	lines <- line
	close(lines)
	<-written
}

// writeTimeout bounds one write to a client that stopped reading.
const writeTimeout = 10 * time.Second

// streamed writes a streamed response body; an error after the body started goes in a trailer.
func streamed(w http.ResponseWriter, contentType string, fn func(io.Writer) error) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Trailer", trailerError)
	flusher, _ := w.(http.Flusher)
	out := writerFunc(func(p []byte) (int, error) {
		n, err := w.Write(p)
		if flusher != nil {
			flusher.Flush()
		}
		return n, err
	})
	if err := fn(out); err != nil {
		w.Header().Set(trailerError, err.Error())
	}
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	var req LogsRequest
	if !s.decode(w, r, &req) {
		return
	}
	streamed(w, "text/plain", func(out io.Writer) error {
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
	streamed(w, "application/gzip", func(out io.Writer) error {
		return s.b.BackupVolume(r.Context(), req, out)
	})
}

// volumeRestore receives the whole backup before it touches the volume, then restores it to the
// end even if the client goes away: a restore cut short would leave the volume half overwritten.
func (s *Server) volumeRestore(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	force, _ := strconv.ParseBool(q.Get("force"))
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
	err = s.b.RestoreVolume(context.WithoutCancel(r.Context()), VolumeRestoreRequest{
		Release: q.Get("release"), Volume: q.Get("volume"), Force: force, Name: q.Get("name")}, f)
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
	code, err := s.b.Exec(ctx, req.Release, req.Workload, opts)
	if err != nil {
		_ = out.frame(frameError, []byte(err.Error()))
		return
	}
	_ = out.frame(frameExit, []byte(strconv.Itoa(code)))
}

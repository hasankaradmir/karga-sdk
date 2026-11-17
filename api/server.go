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
	"sync"

	"github.com/hasankaradmir/karga/internal/application/deployment"
	"github.com/hasankaradmir/karga/internal/application/releases"
	"github.com/hasankaradmir/karga/internal/ports"
)

// Server serves a Backend over HTTP. Errors keep their message; the status code is only a hint.
type Server struct {
	b       Backend
	version string
	mux     *http.ServeMux
}

// NewServer serves b; version is reported to clients.
func NewServer(b Backend, version string) *Server {
	s := &Server{b: b, version: version, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, versionResponse{Version: s.version})
	})
	s.mux.HandleFunc("POST /v1/deploy", s.deploy)
	s.mux.HandleFunc("POST /v1/uninstall", handle(func(ctx context.Context, req releases.UninstallRequest) (any, error) {
		return struct{}{}, b.Uninstall(ctx, req)
	}))
	s.mux.HandleFunc("POST /v1/list", handle(func(ctx context.Context, _ struct{}) (any, error) { return b.List(ctx) }))
	s.mux.HandleFunc("POST /v1/history", handle(func(ctx context.Context, req nameRequest) (any, error) {
		revs, err := b.History(ctx, req.Name)
		out := make([]revisionJSON, len(revs))
		for i, r := range revs {
			out[i] = revisionJSON{Revision: r, Values: r.Values, ValuesAll: r.ValuesAll, Manifest: r.Manifest}
		}
		return out, err
	}))
	s.mux.HandleFunc("POST /v1/values", handle(func(ctx context.Context, req nameRequest) (any, error) {
		return b.GetValues(ctx, req.Name, req.Revision, req.All)
	}))
	s.mux.HandleFunc("POST /v1/notes", handle(func(ctx context.Context, req nameRequest) (any, error) {
		return b.GetNotes(ctx, req.Name, req.Revision)
	}))
	s.mux.HandleFunc("POST /v1/manifest", handle(func(ctx context.Context, req nameRequest) (any, error) {
		return b.GetManifest(ctx, req.Name, req.Revision)
	}))
	s.mux.HandleFunc("POST /v1/status", handle(func(ctx context.Context, req nameRequest) (any, error) { return b.Status(ctx, req.Name) }))
	s.mux.HandleFunc("POST /v1/diff", handle(func(ctx context.Context, req nameRequest) (any, error) {
		return b.Diff(ctx, req.Name, req.From, req.To)
	}))
	s.mux.HandleFunc("POST /v1/resources", handle(func(ctx context.Context, req nameRequest) (any, error) {
		return b.Resources(ctx, req.Name)
	}))
	s.mux.HandleFunc("POST /v1/reconcile", handle(func(ctx context.Context, req nameRequest) (any, error) {
		return b.Reconcile(ctx, req.Name, req.Force)
	}))
	s.mux.HandleFunc("POST /v1/pause", handle(func(ctx context.Context, req nameRequest) (any, error) {
		return struct{}{}, b.Pause(ctx, req.Name, req.Paused)
	}))
	s.mux.HandleFunc("POST /v1/events", handle(func(ctx context.Context, req nameRequest) (any, error) {
		return b.Events(ctx, req.Name, req.After, req.Limit)
	}))
	s.mux.HandleFunc("POST /v1/gateway/routes", handle(func(ctx context.Context, _ struct{}) (any, error) { return b.Gateway(ctx) }))
	s.mux.HandleFunc("POST /v1/gateway/health", handle(func(ctx context.Context, _ struct{}) (any, error) { return b.GatewayHealth(ctx) }))
	s.mux.HandleFunc("POST /v1/gateway/settings", handle(func(ctx context.Context, _ struct{}) (any, error) {
		settings, users, err := b.GatewaySettings(ctx)
		return gatewaySettings{settings, users}, err
	}))
	s.mux.HandleFunc("POST /v1/gateway/change", handle(func(ctx context.Context, req gatewayChange) (any, error) {
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
	s.mux.HandleFunc("POST /v1/state/backup", s.stateBackup)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// maxRequest bounds request bodies other than volume restores (a package archive is the largest).
const maxRequest = 64 << 20

func handle[Req any](fn func(context.Context, Req) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Req
		if err := json.NewDecoder(io.LimitReader(r.Body, maxRequest)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, fmt.Errorf("bad request: %w", err))
			return
		}
		out, err := fn(r.Context(), req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, out)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func status(err error) int {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, releases.ErrLocked), errors.Is(err, releases.ErrInterrupted), errors.Is(err, releases.ErrExists),
		errors.Is(err, ports.ErrPortInUse), errors.Is(err, ports.ErrConflict):
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status(err))
	_ = json.NewEncoder(w).Encode(errorResponse{Error: err.Error()})
}

// deploy runs an install, upgrade or rollback, streaming progress events as JSON lines.
func (s *Server) deploy(w http.ResponseWriter, r *http.Request) {
	var req deployRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequest)).Decode(&req); err != nil {
		writeError(w, fmt.Errorf("bad request: %w", err))
		return
	}
	var pkgPath string
	if req.Package != nil {
		dir, err := os.MkdirTemp("", "karga-package-")
		if err != nil {
			writeError(w, err)
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		pkgPath = filepath.Join(dir, "package.tgz")
		if err := os.WriteFile(pkgPath, req.Package, 0o600); err != nil {
			writeError(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	var mu sync.Mutex
	enc := json.NewEncoder(w)
	send := func(l deployLine) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(l)
		if flusher != nil {
			flusher.Flush()
		}
	}
	progress := func(e deployment.Event) {
		send(deployLine{Event: &eventJSON{e.Kind, e.Name, e.Phase, e.Detail}})
	}
	ctx := r.Context()
	var res *releases.Result
	var err error
	switch {
	case req.Install != nil:
		in := *req.Install
		in.Package, in.Values.ValueFiles, in.Progress = pkgPath, nil, progress
		res, err = s.b.Install(ctx, in)
	case req.Upgrade != nil:
		in := *req.Upgrade
		in.Package, in.Values.ValueFiles, in.Progress = pkgPath, nil, progress
		res, err = s.b.Upgrade(ctx, in)
	case req.Rollback != nil:
		in := *req.Rollback
		in.Progress = progress
		res, err = s.b.Rollback(ctx, in)
	default:
		err = errors.New("bad request: no install, upgrade or rollback")
	}
	line := deployLine{Result: res, Done: true}
	if err != nil {
		line.Error = err.Error()
	}
	send(line)
}

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
	var req releases.LogsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequest)).Decode(&req); err != nil {
		writeError(w, fmt.Errorf("bad request: %w", err))
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
	var req volumeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequest)).Decode(&req); err != nil {
		writeError(w, fmt.Errorf("bad request: %w", err))
		return
	}
	streamed(w, "application/gzip", func(out io.Writer) error {
		return s.b.BackupVolume(r.Context(), releases.VolumeBackupRequest{Release: req.Release, Volume: req.Volume, Stop: req.Stop, W: out})
	})
}

func (s *Server) volumeRestore(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	force, _ := strconv.ParseBool(q.Get("force"))
	err := s.b.RestoreVolume(r.Context(), releases.VolumeRestoreRequest{
		Release: q.Get("release"), Volume: q.Get("volume"), Force: force, Name: q.Get("name"), R: r.Body})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, struct{}{})
}

func (s *Server) stateBackup(w http.ResponseWriter, r *http.Request) {
	streamed(w, "application/vnd.sqlite3", func(out io.Writer) error { return s.b.BackupState(r.Context(), out) })
}

// exec upgrades the connection and carries the command's streams as frames.
func (s *Server) exec(w http.ResponseWriter, r *http.Request) {
	var req execRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequest)).Decode(&req); err != nil {
		writeError(w, fmt.Errorf("bad request: %w", err))
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, errors.New("exec: the connection cannot be upgraded"))
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		writeError(w, err)
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
	opts := ports.ExecOptions{Cmd: req.Cmd, TTY: req.TTY, Width: req.Width, Height: req.Height,
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

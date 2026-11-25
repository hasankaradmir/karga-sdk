package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A volume operation that did its work but whose workloads did not get ready again reports
// ErrNotReady with its result: a backup's file is complete, a restore's files are in place.
func TestVolumeNotReadyKeepsTheResult(t *testing.T) {
	b := &recorder{err: fmt.Errorf("workload db keeps restarting: %w", ErrNotReady)}
	c := NewClient(serve(t, b))
	ctx := context.Background()

	var out bytes.Buffer
	res, err := c.BackupVolume(ctx, VolumeBackupRequest{Release: "shop", Volume: "data"}, &out)
	if !errors.Is(err, ErrNotReady) || !strings.Contains(err.Error(), "keeps restarting") || out.String() != "backup" || res == nil || len(res.Stopped) != 1 {
		t.Fatalf("backup: %v %q %+v", err, out.String(), res)
	}
	r, err := c.RestoreVolume(ctx, VolumeRestoreRequest{Release: "shop", Volume: "data", Confirmed: true}, strings.NewReader("backup"), nil)
	if !errors.Is(err, ErrNotReady) || r == nil || r.RestorePoint == "" {
		t.Fatalf("restore: %v %+v", err, r)
	}
}

// A backup the caller stops while it downloads is an error, not a complete file (the CLI then
// removes the file).
func TestBackupStoppedByTheCallerFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewClient(serve(t, cutShort{&recorder{}}))
	res, err := c.BackupVolume(ctx, VolumeBackupRequest{Release: "shop", Volume: "data"}, stopOnWrite(cancel))
	if !errors.Is(err, ErrDetached) || !errors.Is(err, context.Canceled) || res != nil {
		t.Fatalf("got %v %+v", err, res)
	}
}

// cutShort sends part of a backup, then waits for the client to go away.
type cutShort struct{ *recorder }

func (cutShort) BackupVolume(ctx context.Context, _ VolumeBackupRequest, w io.Writer) (*VolumeResult, error) {
	_, _ = w.Write([]byte("part"))
	if f, ok := w.(interface{ Flush() }); ok {
		f.Flush()
	}
	<-ctx.Done()
	return &VolumeResult{}, nil
}

// stopOnWrite stops the caller when the first bytes arrive.
type stopOnWrite context.CancelFunc

func (s stopOnWrite) Write(p []byte) (int, error) {
	s()
	return len(p), nil
}

func TestVolumeRisks(t *testing.T) {
	for _, tc := range []struct {
		r    interface{ Risk() string }
		want string
	}{
		{VolumeRestoreRequest{Release: "shop", Volume: "data", Name: "d.tgz"}, "replaces the files of volume data of shop with d.tgz; its current files are kept as a restore point"},
		{VolumeRestoreRequest{Release: "shop", Volume: "data", Merge: true}, "writes the backup over the files of volume data of shop"},
		{VolumeRestoreRequest{Release: "shop", Volume: "data", NoRestorePoint: true}, "without a restore point, its current files cannot be brought back"},
		{VolumeUndoRestoreRequest{Release: "shop", Volume: "data"}, "puts back the files it had before its last restore"},
	} {
		if risk := tc.r.Risk(); !strings.Contains(risk, tc.want) || !strings.Contains(risk, "stops the workloads") {
			t.Errorf("%+v: %q", tc.r, risk)
		}
	}
}

// A stream karga-server breaks off (it stopped) is ErrDisconnected, not a raw EOF; a request the
// caller stops (Ctrl-C) once sent is ErrDetached: it goes on in karga-server.
func TestTransportErrors(t *testing.T) {
	c := NewClient(serve(t, &aborting{&recorder{}}))
	err := c.Logs(context.Background(), LogsRequest{Release: "shop"}, io.Discard)
	if !errors.Is(err, ErrDisconnected) {
		t.Fatalf("logs: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if err := c.Pause(ctx, ReleaseRequest{Name: "shop"}); !errors.Is(err, ErrDetached) {
		t.Fatalf("pause: %v", err)
	}
}

// aborting breaks off a log stream, and holds a pause for a second.
type aborting struct{ *recorder }

func (aborting) Logs(_ context.Context, _ LogsRequest, w io.Writer) error {
	_, _ = w.Write([]byte("line\n"))
	if f, ok := w.(interface{ Flush() }); ok {
		f.Flush()
	}
	panic(http.ErrAbortHandler)
}

func (aborting) Pause(context.Context, ReleaseRequest) error {
	time.Sleep(time.Second)
	return nil
}

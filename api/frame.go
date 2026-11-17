package api

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// exec streams travel in frames over the upgraded connection: a type byte, a 4-byte big-endian
// length and the payload.
const (
	frameStdin    byte = iota // client -> server: input
	frameStdinEOF             // client -> server: no more input
	frameResize               // client -> server: width, height (uint32 each)
	frameStdout               // server -> client
	frameStderr               // server -> client
	frameExit                 // server -> client: exit code (int32); last frame
	frameError                // server -> client: error message; last frame
)

// maxFrame bounds a frame so a broken peer cannot make the other side allocate without limit.
const maxFrame = 1 << 20

type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (f *frameWriter) frame(typ byte, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var h [5]byte
	h[0] = typ
	binary.BigEndian.PutUint32(h[1:], uint32(len(payload)))
	if _, err := f.w.Write(h[:]); err != nil {
		return err
	}
	_, err := f.w.Write(payload)
	return err
}

// stream is an io.Writer that sends what it gets as frames of one type.
func (f *frameWriter) stream(typ byte) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		for rest := p; len(rest) > 0; {
			n := min(len(rest), maxFrame)
			if err := f.frame(typ, rest[:n]); err != nil {
				return len(p) - len(rest), err
			}
			rest = rest[n:]
		}
		return len(p), nil
	})
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func readFrame(r io.Reader) (byte, []byte, error) {
	var h [5]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(h[1:])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("exec: frame of %d bytes", n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	return h[0], p, nil
}

func resizePayload(w, h uint) []byte {
	var p [8]byte
	binary.BigEndian.PutUint32(p[:4], uint32(w))
	binary.BigEndian.PutUint32(p[4:], uint32(h))
	return p[:]
}

func parseResize(p []byte) ([2]uint, error) {
	if len(p) != 8 {
		return [2]uint{}, errors.New("exec: bad resize frame")
	}
	return [2]uint{uint(binary.BigEndian.Uint32(p[:4])), uint(binary.BigEndian.Uint32(p[4:]))}, nil
}

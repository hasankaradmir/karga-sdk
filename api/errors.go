package api

import (
	"errors"
	"net/http"
)

// Errors the server reports by kind. Wrap them so errors.Is finds them on both sides of the socket:
// the server sends the kind with the message and the client returns an error that wraps it.
var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid request")
	ErrIncompatible = errors.New("incompatible API version")
)

var kinds = []struct {
	err    error
	code   string
	status int
}{
	{ErrNotFound, "not_found", http.StatusNotFound},
	{ErrConflict, "conflict", http.StatusConflict},
	{ErrInvalid, "invalid", http.StatusBadRequest},
	{ErrIncompatible, "incompatible", http.StatusBadRequest},
}

// kindOf returns the error code and HTTP status of err ("internal", 500 if it has no known kind).
func kindOf(err error) (string, int) {
	for _, k := range kinds {
		if errors.Is(err, k.err) {
			return k.code, k.status
		}
	}
	return "internal", http.StatusInternalServerError
}

// remoteError is an error the server reported; it wraps the sentinel of its kind.
type remoteError struct {
	msg  string
	kind error
}

func (e *remoteError) Error() string { return e.msg }
func (e *remoteError) Unwrap() error { return e.kind }

func remote(msg, code string) error {
	for _, k := range kinds {
		if k.code == code {
			return &remoteError{msg: msg, kind: k.err}
		}
	}
	return errors.New(msg)
}

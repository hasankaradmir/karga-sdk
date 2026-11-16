// Package logger builds karga's slog logger with mandatory secret redaction.
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

const redacted = "[REDACTED]"

// Redactor removes registered secret values from every log record.
// shortcut: plain substring match, so a short secret also masks unrelated text that contains it; revisit in M2-07.
type Redactor struct {
	mu      sync.RWMutex
	secrets map[string]struct{}
}

// NewRedactor returns an empty Redactor.
func NewRedactor() *Redactor {
	return &Redactor{secrets: map[string]struct{}{}}
}

// Add registers a secret value. Empty values are ignored.
func (r *Redactor) Add(secret string) {
	if secret == "" {
		return
	}
	r.mu.Lock()
	r.secrets[secret] = struct{}{}
	r.mu.Unlock()
}

// Redact replaces every registered secret in s.
func (r *Redactor) Redact(s string) string {
	r.mu.RLock()
	list := make([]string, 0, len(r.secrets))
	for k := range r.secrets {
		list = append(list, k)
	}
	r.mu.RUnlock()
	// Longest first, so a secret that contains another one is not left half-masked.
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, sec := range list {
		s = strings.ReplaceAll(s, sec, redacted)
	}
	return s
}

// ReplaceAttr is a slog.HandlerOptions.ReplaceAttr hook. It also sees the message attribute.
func (r *Redactor) ReplaceAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(r.Redact(a.Value.String()))
	case slog.KindAny:
		s := fmt.Sprint(a.Value.Any())
		if red := r.Redact(s); red != s {
			a.Value = slog.StringValue(red)
		}
	}
	return a
}

// New builds a logger writing to w. format is "text" or "json"; debug lowers the level from warn to debug.
func New(w io.Writer, format string, debug bool, r *Redactor) (*slog.Logger, error) {
	level := slog.LevelWarn
	if debug {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: r.ReplaceAttr}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("invalid --log-format %q: must be \"text\" or \"json\"", format)
	}
}

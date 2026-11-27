package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A known path asked with another method gets 405 and the method to use; an unknown path keeps
// "upgrade karga-server", for a client newer than the server.
func TestWrongMethodIsNotAnOldServer(t *testing.T) {
	h := NewServer(&execCtx{}, "dev", Options{})
	ask := func(method, path string) (*httptest.ResponseRecorder, errorResponse) {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set(apiHeader, Version)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var e errorResponse
		_ = json.NewDecoder(w.Body).Decode(&e)
		return w, e
	}
	w, e := ask(http.MethodGet, "/v1/list")
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost ||
		!strings.Contains(e.Error, "use POST for /v1/list") || strings.Contains(e.Error, "upgrade") {
		t.Fatalf("GET /v1/list: %d %q %+v", w.Code, w.Header().Get("Allow"), e)
	}
	if _, e := ask(http.MethodPost, "/v1/no-such-thing"); !strings.Contains(e.Error, "does not know /v1/no-such-thing; upgrade karga-server") {
		t.Fatalf("unknown path: %+v", e)
	}
}

// Package apitest serves an api.Backend for tests of API clients, such as the karga CLI.
package apitest

import (
	"testing"

	"github.com/hasankaradmir/karga-sdk/api"
	"github.com/hasankaradmir/karga-sdk/internal/testsock"
)

// Serve serves b through api.NewServer on a new unix socket until the test ends and returns the
// socket's path, for api.NewClient. The server reports version "dev".
func Serve(t testing.TB, b api.Backend) string {
	t.Helper()
	return testsock.Serve(t, api.NewServer(b, "dev", api.Options{}))
}

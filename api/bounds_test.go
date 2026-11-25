package api

import (
	"strings"
	"testing"
)

// The API bounds what a request may ask for and refuses control characters in text the server
// keeps or shows (SC-15).
func TestRequestBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  interface{ Validate() error }
		want string // "" for valid
	}{
		{"events limit", EventsRequest{Limit: MaxEvents + 1}, "more than"},
		{"events limit at the bound", EventsRequest{Limit: MaxEvents}, ""},
		{"log tail", LogsRequest{Release: "shop", Tail: MaxTail + 1}, "more than"},
		{"all log lines", LogsRequest{Release: "shop", Tail: -1}, ""},
		{"exec NUL", ExecRequest{Release: "shop", Workload: "web", ExecOptions: ExecOptions{Cmd: []string{"sh", "-c", "a\x00b"}}}, "NUL"},
		{"restore name with an escape", VolumeRestoreRequest{Release: "shop", Volume: "data", Name: "b\x1b[2J.tgz", Confirmed: true}, "control characters"},
		{"apply source with an escape", GatewayApplyRequest{Source: "apply\x1b[31m"}, "control character"},
		{"apply source too long", GatewayApplyRequest{Source: strings.Repeat("a", MaxSource+1)}, "more than"},
		{"apply", GatewayApplyRequest{Source: "rollback 3"}, ""},
		{"listener protocol", Listener{Name: "x", Port: 80, Protocol: "udp"}, "tcp, http or https"},
		{"too many sets", InstallRequest{Name: "shop", Values: ValuesInput{Sets: make([]Set, MaxSets+1)}}, "--set"},
		{"too many files", UpgradeRequest{Name: "shop", Values: ValuesInput{Files: make([][]byte, MaxValueFiles+1)}}, "files"},
	} {
		err := tc.req.Validate()
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}

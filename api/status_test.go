package api

import "testing"

func TestStatusHealthy(t *testing.T) {
	ok := ResourceStatus{Desired: DesiredApplied, Actual: ActualRunning, Health: HealthHealthy}
	deployed := Release{Status: StatusDeployed}
	for name, tc := range map[string]struct {
		rep  StatusReport
		want bool
	}{
		"healthy":            {StatusReport{Release: deployed, Resources: []ResourceStatus{ok, {Desired: DesiredApplied, Actual: ActualPresent}}}, true},
		"no health check":    {StatusReport{Release: deployed, Resources: []ResourceStatus{{Desired: DesiredApplied, Actual: ActualRunning, Health: HealthNone}}}, true},
		"failed release":     {StatusReport{Release: Release{Status: StatusFailed}, Resources: []ResourceStatus{ok}}, false},
		"interrupted":        {StatusReport{Release: deployed, Interrupted: "upgrade 3", Resources: []ResourceStatus{ok}}, false},
		"failed apply":       {StatusReport{Release: deployed, Resources: []ResourceStatus{{Desired: DesiredFailed, Actual: ActualRunning}}}, false},
		"missing":            {StatusReport{Release: deployed, Resources: []ResourceStatus{{Desired: DesiredApplied, Actual: ActualMissing}}}, false},
		"exited":             {StatusReport{Release: deployed, Resources: []ResourceStatus{{Desired: DesiredApplied, Actual: ActualExited}}}, false},
		"unhealthy":          {StatusReport{Release: deployed, Resources: []ResourceStatus{{Desired: DesiredApplied, Actual: ActualRunning, Health: HealthUnhealthy}}}, false},
		"still starting":     {StatusReport{Release: deployed, Resources: []ResourceStatus{{Desired: DesiredApplied, Actual: ActualRunning, Health: HealthStarting}}}, false},
		"service not routed": {StatusReport{Release: deployed, Resources: []ResourceStatus{{Desired: DesiredApplied, Actual: ActualNotRouted}}}, false}, // its traffic goes nowhere
	} {
		if got := tc.rep.Healthy(); got != tc.want {
			t.Errorf("%s: Healthy() = %v", name, got)
		}
	}
}

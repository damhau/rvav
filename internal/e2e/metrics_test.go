package e2e

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/observe"
)

// sample is a metric's value for these labels (0 when it has none).
func sample(t *testing.T, g prometheus.Gatherer, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := g.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
	metrics:
		for _, m := range f.GetMetric() {
			for k, v := range labels {
				if !hasLabel(m, k, v) {
					continue metrics
				}
			}
			switch {
			case m.Counter != nil:
				return m.Counter.GetValue()
			case m.Gauge != nil:
				return m.Gauge.GetValue()
			case m.Histogram != nil:
				return float64(m.Histogram.GetSampleCount())
			}
		}
	}
	return 0
}

func hasLabel(m *dto.Metric, k, v string) bool {
	for _, l := range m.GetLabel() {
		if l.GetName() == k && l.GetValue() == v {
			return true
		}
	}
	return false
}

// TestMetrics: a request built moves the counters (S14.5): API calls by
// route, workflow steps by system, the request finished, the calls to
// EfficientIP; and the backlog gauges read the database.
func TestMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		before := sample(t, observe.Registry, "rvav_requests_finished_total", map[string]string{"kind": "provision", "outcome": "succeeded"})
		d := buildVM(h, model)
		_ = d.vm()
		r := observe.Registry
		for _, c := range []struct {
			name   string
			labels map[string]string
		}{
			{"rvav_http_requests_total", map[string]string{"method": "POST", "route": "/api/v1/requests", "code": "202"}},
			{"rvav_http_request_duration_seconds", map[string]string{"route": "/api/v1/deployments/{deploymentId}"}},
			{"rvav_steps_total", map[string]string{"system": "vsphere", "outcome": "done"}},
			{"rvav_steps_total", map[string]string{"system": "awx", "outcome": "done"}},
			{"rvav_step_duration_seconds", map[string]string{"system": "ipam"}},
			{"rvav_integration_calls_total", map[string]string{"system": "efficientip", "outcome": "ok"}},
		} {
			if sample(t, r, c.name, c.labels) == 0 {
				t.Errorf("%s%v not counted", c.name, c.labels)
			}
		}
		if after := sample(t, r, "rvav_requests_finished_total", map[string]string{"kind": "provision", "outcome": "succeeded"}); after != before+1 {
			t.Errorf("requests finished %v, then %v", before, after)
		}
		backlog := prometheus.NewRegistry()
		backlog.MustRegister(observe.Backlog{Pool: h.store.Pool})
		if n := sample(t, backlog, "rvav_vms", map[string]string{"sync_state": "in_sync"}); n != 1 {
			t.Errorf("VMs in sync = %v", n)
		}
		if n := sample(t, backlog, "rvav_requests_active", map[string]string{"status": "running"}); n != 0 {
			t.Errorf("requests running = %v", n)
		}
		if n := sample(t, backlog, "rvav_queue_depth", map[string]string{"queue": "events"}); n < 0 {
			t.Errorf("events waiting = %v", n)
		}
	}, model)
}

package observe

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// Backlog reports what waits in rvav's database at each scrape (S14.5):
// requests by status, approvals waiting and overdue, events, deliveries and
// notifications not sent yet, VMs drifted or missing, and how fresh each
// vCenter's inventory is. The API registers it: every replica reports the
// same figures, so dashboards take their maximum.
type Backlog struct {
	Pool *pgxpool.Pool
}

var (
	requestsActive = prometheus.NewDesc("rvav_requests_active", "Requests not finished, by status.", []string{"status"}, nil)
	approvals      = prometheus.NewDesc("rvav_approvals_pending", "Approval levels waiting for a decision; overdue: past their due time.", []string{"overdue"}, nil)
	queue          = prometheus.NewDesc("rvav_queue_depth", "What waits to go out: events (integration_events), subscription runs, notification deliveries.", []string{"queue"}, nil)
	oldest         = prometheus.NewDesc("rvav_queue_oldest_seconds", "How long the oldest item of a queue has waited.", []string{"queue"}, nil)
	vms            = prometheus.NewDesc("rvav_vms", "VMs rvav manages, by sync state (in_sync, drifted, missing).", []string{"sync_state"}, nil)
	inventoryAge   = prometheus.NewDesc("rvav_inventory_age_seconds", "Since a VM of the vCenter was last read.", []string{"vcenter"}, nil)
)

func (b Backlog) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{requestsActive, approvals, queue, oldest, vms, inventoryAge} {
		ch <- d
	}
}

func (b Backlog) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	for _, s := range []string{"pending_approval", "scheduled", "queued", "running", "cancelling"} {
		var n float64
		_ = b.Pool.QueryRow(ctx, `select count(*) from requests where status = $1`, s).Scan(&n)
		gauge(requestsActive, n, s)
	}
	var pending, overdue float64
	_ = b.Pool.QueryRow(ctx, `select count(*) filter (where due_at is null or due_at >= now()),
		count(*) filter (where due_at < now()) from approvals where status = 'pending'`).Scan(&pending, &overdue)
	gauge(approvals, pending, "false")
	gauge(approvals, overdue, "true")
	for name, sql := range map[string]string{
		"events":        `select count(*), coalesce(extract(epoch from now() - min(created_at)), 0) from integration_events where dispatched_at is null and not held`,
		"subscriptions": `select count(*), coalesce(extract(epoch from now() - min(next_attempt_at)), 0) from subscription_runs where status = 'queued' and next_attempt_at <= now()`,
		"notifications": `select count(*), coalesce(extract(epoch from now() - min(next_attempt_at)), 0) from notification_deliveries where status = 'queued' and next_attempt_at <= now()`,
	} {
		var n, age float64
		_ = b.Pool.QueryRow(ctx, sql).Scan(&n, &age)
		gauge(queue, n, name)
		gauge(oldest, age, name)
	}
	for _, s := range []string{"in_sync", "drifted", "missing"} {
		var n float64
		_ = b.Pool.QueryRow(ctx, `select count(*) from resources r join deployments d on d.id = r.deployment_id
			where r.type = 'vsphere.vm' and d.status <> 'deleted' and r.sync_state = $1`, s).Scan(&n)
		gauge(vms, n, s)
	}
	rows, err := b.Pool.Query(ctx, `select vcenter, extract(epoch from now() - max(synced_at)) from inv_vms group by vcenter`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var vc string
			var age float64
			if rows.Scan(&vc, &age) == nil {
				gauge(inventoryAge, age, vc)
			}
		}
	}
}

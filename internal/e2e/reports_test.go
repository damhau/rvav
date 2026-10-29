package e2e

import (
	"context"
	"encoding/csv"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestReports: requests over time and their success rate, provisioning
// time, approval lead time, usage against quota and leases ending soon,
// for what the caller sees, and their CSV export (S13.4).
func TestReports(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		ok := buildVM(h, model)
		bad := buildVM(h, model)
		exec := func(sql string, args ...any) {
			t.Helper()
			if _, err := h.store.Pool.Exec(ctx, sql, args...); err != nil {
				t.Fatal(err)
			}
		}
		// The first took five minutes to build; the second failed. One of
		// its levels was approved in an hour, another waits.
		exec(`update requests set started_at = submitted_at, finished_at = submitted_at + interval '300 seconds' where id = $1`, ok.built.Id)
		exec(`update requests set status = 'failed' where id = $1`, bad.built.Id)
		exec(`insert into approvals (id, request_id, seq, name, status, opened_at, decided_at, outcome) values
			('apr_r1', $1, 1, 'Project admins', 'approved', now() - interval '2 hours', now() - interval '1 hour', 'Approved by Marc Keller'),
			('apr_r2', $1, 2, 'Infrastructure leads', 'pending', now() - interval '30 minutes', null, null)`, bad.built.Id)

		var r api.Report
		h.json("GET", "/api/v1/reports", admin, nil, 200, &r)
		today := time.Now().UTC().Truncate(24 * time.Hour)
		last := r.Requests.OverTime[len(r.Requests.OverTime)-1]
		if r.Requests.Totals.Submitted != 2 || r.Requests.Totals.Succeeded != 1 || r.Requests.Totals.Failed != 1 ||
			r.Requests.Totals.SuccessRate == nil || *r.Requests.Totals.SuccessRate != 0.5 ||
			len(r.Requests.OverTime) != 30 || !last.Period.Time.Equal(today) || last.Submitted != 2 || r.Interval != api.ReportIntervalDay {
			t.Fatalf("requests = %+v, last period %+v", r.Requests.Totals, last)
		}
		if r.Provisioning.Count != 1 || r.Provisioning.P50Seconds == nil || *r.Provisioning.P50Seconds != 300 {
			t.Fatalf("provisioning = %+v", r.Provisioning)
		}
		if len(r.Requests.ByItem) != 1 || r.Requests.ByItem[0].Item.Slug != "rhel9-server" || r.Requests.ByItem[0].Submitted != 2 ||
			r.Requests.ByItem[0].Provisioning.Count != 1 {
			t.Fatalf("by item = %+v", r.Requests.ByItem)
		}
		if a := r.Approvals; a.Decided != 1 || a.Approved != 1 || a.LeadTime.P50Seconds == nil || *a.LeadTime.P50Seconds != 3600 ||
			a.Pending != 1 || a.OldestPendingSince == nil {
			t.Fatalf("approvals = %+v", a)
		}
		vcpu := slices.IndexFunc(r.Quota, func(l api.ReportQuotaLine) bool {
			return l.Project.Id == "prj_web" && l.Environment == nil && l.Resource == api.Vcpu
		})
		if vcpu < 0 || r.Quota[vcpu].Used != ok.vm().CpuCount+bad.vm().CpuCount {
			t.Fatalf("quota = %+v", r.Quota)
		}
		if !slices.ContainsFunc(r.Leases, func(l api.ReportLease) bool { return l.Deployment.Id == ok.id && l.Owner.Id == "usr_tnguyen" }) {
			t.Fatalf("leases = %+v", r.Leases)
		}

		// By week: periods start on Monday.
		h.json("GET", "/api/v1/reports?interval=week&from="+today.AddDate(0, 0, -20).Format(time.DateOnly), admin, nil, 200, &r)
		for _, p := range r.Requests.OverTime {
			if p.Period.Time.Weekday() != time.Monday {
				t.Fatalf("week starting %s", p.Period)
			}
		}

		// Others see the projects they are members of.
		h.json("GET", "/api/v1/reports", "afavre", nil, 200, &r)
		if r.Requests.Totals.Submitted != 0 || len(r.Leases) != 0 ||
			slices.ContainsFunc(r.Quota, func(l api.ReportQuotaLine) bool { return l.Project.Id != "prj_core" }) {
			t.Fatalf("afavre's report = %+v %+v", r.Requests.Totals, r.Quota)
		}
		h.json("GET", "/api/v1/reports?project_id=prj_web", "afavre", nil, 403, nil)
		h.json("GET", "/api/v1/reports?project_id=prj_web", "tnguyen", nil, 200, &r)
		if r.Requests.Totals.Submitted != 2 {
			t.Fatalf("tnguyen's report = %+v", r.Requests.Totals)
		}
		h.json("GET", "/api/v1/reports?from=2026-10-10&to=2026-10-01", admin, nil, 422, nil)
		h.json("GET", "/api/v1/reports?from=2020-01-01&to=2026-10-01", admin, nil, 422, nil)

		// CSV: a row per request, approval level, quota line or lease.
		export := func(section string) [][]string {
			t.Helper()
			code, body, hdr := h.do("GET", "/api/v1/reports/export?section="+section, admin, nil)
			if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/csv") {
				t.Fatalf("%s export = %d %s", section, code, body)
			}
			rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			return rows
		}
		if rows := export("requests"); len(rows) != 3 || rows[0][0] != "number" || rows[1][11] != "300" || rows[2][7] != "failed" {
			t.Fatalf("requests csv = %v", rows)
		}
		if rows := export("approvals"); len(rows) != 3 || rows[1][6] != "3600" || rows[2][3] != "pending" {
			t.Fatalf("approvals csv = %v", rows)
		}
		if rows := export("quota"); len(rows) < 5 || rows[0][2] != "resource" {
			t.Fatalf("quota csv = %v", rows)
		}
		if rows := export("leases"); len(rows) < 2 || rows[0][0] != "deployment" {
			t.Fatalf("leases csv = %v", rows)
		}
	}, model)
}

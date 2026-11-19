package e2e

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/seed"
)

// chainLevel is one stored approval level of a request, as the workflow
// runs it.
type chainLevel struct {
	Group     string
	Approvers []string
	Timeout   *int
	OnTimeout string
	Escalate  []string
	Policies  []string
	Status    string
}

func chainOf(t *testing.T, h *harness, ctx context.Context, requestID string) []chainLevel {
	t.Helper()
	rows, err := h.store.Pool.Query(ctx, `select coalesce(group_name, ''), approver_ids, timeout_seconds, coalesce(on_timeout, ''),
		escalate_ids, policy_ids, status from approvals where request_id = $1 order by seq`, requestID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []chainLevel
	for rows.Next() {
		var l chainLevel
		if err := rows.Scan(&l.Group, &l.Approvers, &l.Timeout, &l.OnTimeout, &l.Escalate, &l.Policies, &l.Status); err != nil {
			t.Fatal(err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestProjectApprovalJoinsGlobal: a project admin's day-2 approval adds its
// levels after a hard global approval's, never in place of them (ADR-0045).
func TestProjectApprovalJoinsGlobal(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		web := "prj_web"
		var global, project api.Day2Policy
		h.json("POST", "/api/v1/admin/day2-policies", "mkeller", api.Day2PolicyWrite{Name: "Leads approve power-off",
			Reason: strPtr("Infra leads sign off power-offs"), Priority: new(20), Actions: &[]string{"power_off"},
			Roles: &[]string{"owner"}, Effect: api.Day2PolicyWriteEffectApproval,
			Levels: &[]api.ApprovalLevel{{Name: "Infra leads", Group: strPtr("rvav-infra-leads")}}}, 201, &global)
		d := buildVM(h, nil)
		h.json("POST", "/api/v1/admin/day2-policies", "lmartin", api.Day2PolicyWrite{Name: "Léa approves power-off",
			Reason: strPtr("Léa signs off"), Priority: new(1), Actions: &[]string{"power_off"}, Roles: &[]string{"owner"},
			Effect: api.Day2PolicyWriteEffectApproval, ProjectId: &web,
			Levels: &[]api.ApprovalLevel{{Name: "Léa", Approvers: &[]string{seed.UserID("lmartin")}, Timeout: strPtr("1s"),
				OnTimeout: new(api.ApprovalLevelOnTimeoutApprove)}}}, 201, &project)

		if a := d.actions("tnguyen")["power_off"]; !a.Approval.Required ||
			!slices.Equal(a.Approval.Reasons, []string{"Infra leads sign off power-offs", "Léa signs off"}) {
			t.Fatalf("power_off's approval = %+v", a.Approval)
		}
		r := d.run("tnguyen", "power_off", nil, 202)
		chain := chainOf(t, h, ctx, r.Id)
		if len(chain) != 2 || chain[0].Group != "rvav-infra-leads" || chain[0].Timeout != nil ||
			!slices.Equal(chain[0].Policies, []string{global.Id}) ||
			!slices.Equal(chain[1].Approvers, []string{seed.UserID("lmartin")}) || !slices.Equal(chain[1].Policies, []string{project.Id}) {
			t.Fatalf("asked %+v, want the leads first, then Léa", chain)
		}

		var why api.PolicyExplanation
		h.json("POST", "/api/v1/admin/policies/explain", "lmartin", api.PolicyExplainQuery{ProjectId: web, Environment: "dev",
			Action: strPtr("power_off")}, 200, &why)
		if why.Day2 == nil || deref(why.Day2.DecidedBy) != "Leads approve power-off, Léa approves power-off" {
			t.Fatalf("explained %+v", why.Day2)
		}
	})
}

// TestProjectLevelKeepsOffGlobalLevel: a project's approval level with the
// same approvers as a global one stays a level of its own, so its timeout
// and escalation never reach the global level (ADR-0045). Run on a real
// Temporal dev server, where timeouts fire on the clock.
func TestProjectLevelKeepsOffGlobalLevel(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-project-levels"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		web := "prj_web"
		cond := `environment == "dev" && inputs.role == "cab"`
		var global, project api.ApprovalPolicy
		h.json("POST", "/api/v1/admin/approval-policies", "mkeller", api.ApprovalPolicyWrite{Name: "CAB", Reason: strPtr("The CAB signs off"),
			Condition: cond, Priority: new(5), Levels: []api.ApprovalLevel{{Name: "CAB", Group: strPtr("rvav-cab")}}}, 201, &global)
		h.json("POST", "/api/v1/admin/approval-policies", "lmartin", api.ApprovalPolicyWrite{Name: "CAB too", Reason: strPtr("Web too"),
			Condition: cond, Priority: new(1), ProjectId: &web, Levels: []api.ApprovalLevel{{Name: "CAB", Group: strPtr("rvav-cab"),
				Timeout: strPtr("2s"), OnTimeout: new(api.ApprovalLevelOnTimeoutEscalate),
				EscalateTo: &struct {
					Approvers *[]string `json:"approvers,omitempty"`
					Group     *string   `json:"group,omitempty"`
				}{Approvers: &[]string{seed.UserID("lmartin")}}}}}, 201, &project)

		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		var r api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId,
			ProjectId: web, Environment: "dev", Inputs: map[string]any{"role": "cab", "size": "S", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}},
			202, &r, "Idempotency-Key", uuid.NewString())
		chain := chainOf(t, h, ctx, r.Id)
		if len(chain) != 2 || chain[0].Timeout == nil || *chain[0].Timeout != 2 || !slices.Equal(chain[0].Policies, []string{project.Id}) ||
			chain[1].Group != "rvav-cab" || chain[1].Timeout != nil || len(chain[1].Escalate) != 0 ||
			!slices.Equal(chain[1].Policies, []string{global.Id}) {
			t.Fatalf("levels %+v, want the project's CAB level and the global one apart", chain)
		}

		lvl := h.waitInbox("lmartin", 1).Items[0]
		if !lvl.CanDecide || lvl.Escalated == nil || !*lvl.Escalated || lvl.Level.Index != 1 {
			t.Fatalf("lmartin's inbox after the escalation: %+v", lvl)
		}
		h.json("POST", "/api/v1/approvals/"+lvl.Id+"/decision", "lmartin", api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove}, 200, nil)
		deadline := time.Now().Add(30 * time.Second)
		for chain = chainOf(t, h, ctx, r.Id); chain[1].Status != "pending"; chain = chainOf(t, h, ctx, r.Id) {
			if time.Now().After(deadline) {
				t.Fatalf("the global level never opened: %+v", chain)
			}
			time.Sleep(50 * time.Millisecond)
		}
		time.Sleep(2500 * time.Millisecond)
		var got api.Request
		h.json("GET", "/api/v1/requests/"+r.Id, "tnguyen", nil, 200, &got)
		if inbox := h.waitInbox("lmartin", 0); got.Status != api.RequestStatusPendingApproval || inbox.Total != 0 {
			t.Fatalf("after Léa's own escalation the request is %s; she has %d to decide", got.Status, inbox.Total)
		}
	})
}

package e2e

import (
	"context"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/present"
)

// TestUpdateKeepsItsVMs: an update's plan is fixed on the VMs the
// deployment had when it was submitted (X1.6). While it waits for
// approval, VMs can't join the deployment; if its VMs changed anyway, it
// doesn't start, and says to update again.
func TestUpdateKeepsItsVMs(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web = "mkeller", "tnguyen", "prj_web"
		def := webFarm(false)
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "web-farm", Name: "Web farm",
			Summary: "Web servers.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/web-farm/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		v1 := item.DraftVersion.Id
		inputs := map[string]any{"app": "farm", "web_nodes": 2, "size": "S", "cluster": "GVA-DEV-CL01", "network": "dvpg-app-dev-vl310"}
		var built api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: v1, ProjectId: web,
			Environment: "dev", Inputs: inputs}, 202, &built, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(built.Id, nil)
		h.waitStatus(built.Id, owner, api.RequestStatusSucceeded)
		depID := (*built.DeploymentIds)[0]

		h.json("POST", "/api/v1/admin/approval-policies", admin, api.ApprovalPolicyWrite{Name: "Updates",
			Condition: `request.kind == "update"`,
			Levels:    []api.ApprovalLevel{{Name: "Infrastructure leads", Group: new("rvav-infra-leads")}}}, 201, nil)
		resized := maps.Clone(inputs)
		resized["size"] = "M"
		var req api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/update", owner, api.UpdateDraft{VersionId: v1, Inputs: resized}, 202, &req,
			"Idempotency-Key", uuid.NewString())
		if req.Status != api.RequestStatusPendingApproval {
			t.Fatalf("the update is %s", req.Status)
		}

		// A VM adopted into it while the update waits is refused.
		legacy := existingVM(t, ctx, h, c, model, "legacy-web01", "10.31.10.231")
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var problem api.Problem
		h.json("POST", "/api/v1/admin/onboarding/into", admin, api.OnboardingIntoDraft{DeploymentId: depID, Component: present.Ptr("web"),
			Vms: []vmRefT{{Moref: legacy, Vcenter: "vc01.corp.example"}}}, 409, &problem)
		if !strings.Contains(deref(problem.Detail), req.Number+" is running or waiting on") {
			t.Fatalf("the join = %+v", problem)
		}

		// A VM left it anyway, as one could before: the approved update
		// doesn't start, and nothing changes.
		var web02 string
		if err := h.store.Pool.QueryRow(ctx, `delete from resources where deployment_id = $1 and type = 'vsphere.vm'
			and name = 'web-dev-farmweb02' returning id`, depID).Scan(&web02); err != nil {
			t.Fatal(err)
		}
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				var inbox api.ApprovalPage
				h.json("GET", "/api/v1/approvals", admin, nil, 200, &inbox)
				for _, a := range inbox.Items {
					if a.Request.Id == req.Id {
						h.json("POST", "/api/v1/approvals/"+a.Id+"/decision", admin,
							api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove}, 200, nil)
					}
				}
				h.wf.deliver(env)
			}, time.Hour)
		})
		got := h.waitStatus(req.Id, owner, api.RequestStatusFailed)
		if got.Error == nil || !strings.Contains(got.Error.Message, "VMs changed since this update was submitted") {
			t.Fatalf("the update's error = %+v", got.Error)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+depID, owner, nil, 200, &dep)
		if dep.Status != api.DeploymentStatusReady || dep.Inputs["size"] != "S" {
			t.Fatalf("deployment = %s, inputs %v", dep.Status, dep.Inputs)
		}
		for _, r := range dep.Resources {
			if v, _ := r.ValueByDiscriminator(); v != nil {
				if vm, ok := v.(api.VmResource); ok && vm.Vm.CpuCount != 2 {
					t.Fatalf("%s was resized: %d vCPU", vm.Name, vm.Vm.CpuCount)
				}
			}
		}
	}, model)
}

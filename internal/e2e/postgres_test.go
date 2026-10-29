package e2e

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestPostgresCluster requests the seeded PostgreSQL cluster, the reference
// composite item: three nodes kept on separate hosts with their data
// disks, made a Patroni cluster by AWX, then the database's owner role, in
// one timeline and one deployment (S12.7, ADR-0024).
func TestPostgresCluster(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/postgres-cluster?project_id=prj_data", "sbrunner", nil, 200, &item)
		// Its nodes are requested through it only.
		h.json("GET", "/api/v1/catalog/items/pg-node?project_id=prj_data", "sbrunner", nil, 404, nil)

		var req api.Request
		h.json("POST", "/api/v1/requests", "sbrunner", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.Version.Id,
			ProjectId: "prj_data", Environment: "dev", Inputs: map[string]any{"name": "ledger", "pg_version": "16.6", "size": "S",
				"data_gb": 50, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-db-dev-vl320"}},
			202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, "sbrunner", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("request = %s %s", req.Status, errorOf(req))
		}
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "sbrunner", nil, 200, &steps)
		all := map[string]api.Step{}
		for _, s := range steps.Items {
			all[s.Key] = s
			for _, ch := range s.Children {
				all[ch.Key] = ch
			}
		}
		for _, key := range []string{"step:nodes", "step:nodes/vsphere.clone@data-dev-pgledger03", "step:nodes/vsphere.drs_rule@db",
			"step:nodes/awx.job:patroni-cluster@db", "step:owner"} {
			if all[key].Status != api.StepStatusDone {
				t.Fatalf("%s = %s (steps %v)", key, all[key].Status, keysOf(all))
			}
		}

		// One deployment: three nodes on three hosts, kept apart, each with
		// its data disk.
		if req.DeploymentIds == nil || len(*req.DeploymentIds) != 1 {
			t.Fatalf("deployments = %v", req.DeploymentIds)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+(*req.DeploymentIds)[0], "sbrunner", nil, 200, &dep)
		hosts := map[string]bool{}
		var rule api.DrsRuleResource
		for _, r := range dep.Resources {
			switch v, _ := r.ValueByDiscriminator(); v := v.(type) {
			case api.VmResource:
				hosts[deref(v.Vm.Host)] = true
				if len(v.Vm.Disks) != 2 || v.Vm.Disks[1].CapacityGb != 50 {
					t.Fatalf("%s disks = %+v", v.Name, v.Vm.Disks)
				}
			case api.DrsRuleResource:
				rule = v
			}
		}
		if dep.Name != "data-dev-pg-ledger" || len(hosts) != 3 || len(rule.Rule.Vms) != 3 {
			t.Fatalf("deployment %s on hosts %v, rule %+v", dep.Name, hosts, rule.Rule)
		}

		// AWX: the roles on the three nodes, then the owner on the database.
		patroni, _ := strconv.Atoi(all["step:nodes/awx.job:patroni-cluster@db"].ExternalRef.Id)
		if limit := h.awx.Launched(patroni).Limit; !slices.Equal(strings.Split(limit, ","),
			[]string{"data-dev-pgledger01", "data-dev-pgledger02", "data-dev-pgledger03"}) {
			t.Fatalf("patroni's limit = %q", limit)
		}
		owner, _ := strconv.Atoi(all["step:owner"].ExternalRef.Id)
		if vars := h.awx.Launched(owner).ExtraVars; vars["role_name"] != "ledger_owner" || vars["privileges"] != "owner" {
			t.Fatalf("owner's job = %v", vars)
		}
	})
}

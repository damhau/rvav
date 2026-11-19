package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/vra"
)

// TestVRADeploymentSharingAndEnforcement: vRA's Deployment sharing is the
// rvav project's to set, and its policies' enforcement the rvav policies'
// (G1.3, ADR-0045).
func TestVRADeploymentSharingAndEnforcement(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		e := vra.SampleEstate(time.Now())
		e.Policies = append(e.Policies, vra.Policy{ID: "pol-lease-soft", Name: "Soft leases", TypeID: vra.PolicyLease,
			EnforcementType: "SOFT", ProjectID: "vra-prj-data", Definition: json.RawMessage(`{"leaseTermMax":14}`)})
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, e, 201, nil)
		h.json("PUT", "/api/v1/admin/vra/projects/vra-prj-data", admin, api.VraProjectMapping{RvavProjectId: ptr("prj_data")}, 204, nil)
		plan := func() (map[string]api.VraProjectPlan, map[string]api.VraPolicyPlan) {
			t.Helper()
			var p api.VraPlan
			h.json("GET", "/api/v1/admin/vra/plan", admin, nil, 200, &p)
			projects, policies := map[string]api.VraProjectPlan{}, map[string]api.VraPolicyPlan{}
			for _, x := range p.Projects {
				projects[x.VraId] = x
			}
			for _, x := range p.Policies {
				policies[x.VraId] = x
			}
			return projects, policies
		}

		projects, policies := plan()
		if web := projects["vra-prj-web"]; web.DeploymentSharing != api.AllMembers || valueOf(web.RvavDeploymentSharing) != api.AllMembers {
			t.Fatalf("Web Platform = %+v", web)
		}
		if data := projects["vra-prj-data"]; data.DeploymentSharing != api.OwnerOnly || valueOf(data.RvavDeploymentSharing) != api.AllMembers {
			t.Fatalf("Data Services = %+v", data)
		}
		if legacy := projects["vra-prj-legacy"]; legacy.RvavDeploymentSharing != nil {
			t.Fatalf("Legacy Apps, mapped to nothing = %+v", legacy)
		}
		soft := policies["pol-lease-soft"]
		notes := func(p api.VraPolicyPlan, text string) bool {
			return slices.ContainsFunc(p.Notes, func(n api.VraNote) bool { return strings.Contains(n.Text, text) })
		}
		if valueOf(soft.Enforcement) != api.PolicyEnforcementSoft || !notes(soft, "its project admins can write it") || !notes(soft, "can't be soft") {
			t.Fatalf("a project's soft lease = %+v", soft)
		}
		if p := policies["pol-lease-dev"]; valueOf(p.Enforcement) != api.PolicyEnforcementHard {
			t.Fatalf("a hard lease = %+v", p)
		}
		if p := policies["pol-quota-web"]; valueOf(p.Enforcement) != api.PolicyEnforcementHard {
			t.Fatalf("a quota = %+v", p)
		}
		if p := policies["pol-limit"]; valueOf(p.Enforcement) != api.PolicyEnforcementHard || !notes(p, "vCPU per deployment: 16.") {
			t.Fatalf("a deployment limit = %+v", p)
		}

		// Set in rvav, by its project admin: the plan agrees.
		h.json("PUT", "/api/v1/projects/prj_data/deployment-sharing", "sbrunner", api.DeploymentSharingWrite{DeploymentSharing: api.OwnerOnly}, 200, nil)
		if projects, _ = plan(); valueOf(projects["vra-prj-data"].RvavDeploymentSharing) != api.OwnerOnly {
			t.Fatalf("after setting it = %+v", projects["vra-prj-data"])
		}
	})
}

func valueOf[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

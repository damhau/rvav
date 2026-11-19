package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
)

// TestTextLimits: the server counts characters, not bytes, and holds the
// limits the API states: a plan's filter, a view's filter names, and the
// reason given to approvers of a request, an update or a day-2 action.
func TestTextLimits(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web = "mkeller", "tnguyen", "prj_web"
		var problem struct{ Errors []api.FieldError }
		refused := func(what, field string) {
			t.Helper()
			if len(problem.Errors) != 1 || problem.Errors[0].Field != field || deref(problem.Errors[0].Code) != "too_long" {
				t.Fatalf("%s: %+v", what, problem.Errors)
			}
			problem.Errors = nil
		}

		var rhel api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, owner, nil, 200, &rhel)
		target := api.OnboardingTarget{ProjectId: web, Environment: present.Ptr("dev"), OwnerId: present.Ptr("usr_tnguyen"),
			CatalogItemId: present.Ptr(rhel.Id), Inputs: &map[string]any{"role": "app", "size": "M"}}
		accented := api.OnboardingPlanWrite{Name: strings.Repeat("é", 80), Description: present.Ptr(strings.Repeat("ü", 500)),
			Filter: api.OnboardingFilter{Q: present.Ptr("legacy-")}, Target: target}
		h.json("POST", "/api/v1/admin/onboarding/plans", admin, accented, 201, nil)
		for field, f := range map[string]api.OnboardingFilter{
			"/filter/q":      {Q: present.Ptr(strings.Repeat("a", 201))},
			"/filter/folder": {Folder: present.Ptr("/" + strings.Repeat("a", 500))},
			"/filter/tag":    {Tag: present.Ptr("app/" + strings.Repeat("a", 197))},
		} {
			h.json("POST", "/api/v1/admin/onboarding/plans", admin, api.OnboardingPlanWrite{Name: "Too long", Filter: f,
				Target: target}, 422, &problem)
			refused("plan filter", field)
		}

		h.json("POST", "/api/v1/me/views", owner, api.SavedViewWrite{List: api.ViewListDeployments, Name: "Long",
			Query: map[string]string{"q": strings.Repeat("é", 400)}}, 201, nil)
		h.json("POST", "/api/v1/me/views", owner, api.SavedViewWrite{List: api.ViewListDeployments, Name: "Odd",
			Query: map[string]string{strings.Repeat("k", 41): "x"}}, 422, &problem)
		refused("view filter name", "/query")

		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, owner, nil, 200, &form)
		inputs := map[string]any{"role": "app", "size": "M", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}
		long := present.Ptr(strings.Repeat("é", 2001))
		draft := api.RequestDraft{CatalogItemId: rhel.Id, VersionId: form.VersionId, ProjectId: web, Environment: "dev",
			Inputs: inputs, Justification: long}
		h.json("POST", "/api/v1/requests", owner, draft, 422, &problem, "Idempotency-Key", uuid.NewString())
		refused("request", "/justification")
		draft.Justification = present.Ptr(strings.Repeat("é", 2000))
		var built api.Request
		h.json("POST", "/api/v1/requests", owner, draft, 202, &built, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(built.Id, nil)
		h.waitStatus(built.Id, owner, api.RequestStatusSucceeded)
		dep := (*built.DeploymentIds)[0]

		h.json("POST", "/api/v1/deployments/"+dep+"/update", owner, api.UpdateDraft{VersionId: form.VersionId, Inputs: inputs,
			Justification: long}, 422, &problem, "Idempotency-Key", uuid.NewString())
		refused("update", "/justification")
		var detail api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+dep, owner, nil, 200, &detail)
		var vm string
		for _, r := range detail.Resources {
			if v, _ := r.ValueByDiscriminator(); v != nil {
				if r, ok := v.(api.VmResource); ok {
					vm = r.Id
				}
			}
		}
		h.json("POST", "/api/v1/deployments/"+dep+"/actions", owner, api.Day2Draft{Action: "power_off", ResourceId: &vm,
			Inputs: map[string]any{}, Justification: long}, 422, &problem, "Idempotency-Key", uuid.NewString())
		refused("day-2 action", "/justification")
	})
}

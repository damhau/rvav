package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestDeploymentSharing: with Web Platform's deployments shown to their
// owners only, a member, an owner, the person one was handed over to, a
// project admin, a supervisor and an administrator each see what ADR-0045
// says, in lists, details, search, VMs, requests, day-2 actions and
// updates (G1.3).
func TestDeploymentSharing(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, admin, projectAdmin, owner, given, member, supervisor = "prj_web", "mkeller", "lmartin", "tnguyen", "cdubois", "afavre", "sbrunner"
		for user, role := range map[string]string{given: "member", member: "member", supervisor: "supervisor"} {
			if err := h.store.SetMemberRoles(ctx, web, seed.UserID(user), []string{role}); err != nil {
				t.Fatal(err)
			}
		}

		// Projects keep their sharing through the migration.
		var projects api.ProjectPage
		h.json("GET", "/api/v1/projects?limit=100", admin, nil, 200, &projects)
		for _, p := range projects.Items {
			if p.DeploymentSharing == nil || *p.DeploymentSharing != api.AllMembers {
				t.Fatalf("%s's sharing = %v", p.Code, p.DeploymentSharing)
			}
		}

		kept, handed := buildVM(h, nil), buildVM(h, nil)
		var moved api.Request
		h.json("POST", "/api/v1/deployments/"+handed.id+"/actions", owner, api.Day2Draft{Action: "change_owner",
			Inputs: map[string]any{"owner": seed.UserID(given)}}, 202, &moved, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(moved.Id, nil)
		h.waitStatus(moved.Id, owner, api.RequestStatusSucceeded)
		var keptDep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+kept.id, owner, nil, 200, &keptDep)

		listed := func(user, query string) []string {
			t.Helper()
			var page api.DeploymentPage
			h.json("GET", "/api/v1/deployments?limit=200"+query, user, nil, 200, &page)
			var out []string
			for _, d := range page.Items {
				out = append(out, d.Id)
			}
			slices.Sort(out)
			return out
		}
		both := []string{kept.id, handed.id}
		slices.Sort(both)
		if got := listed(member, "&project_id="+web); !slices.Equal(got, both) {
			t.Fatalf("shared with all members, %s lists %v", member, got)
		}

		sharing := func(user, to string, want int) {
			t.Helper()
			h.json("PUT", "/api/v1/projects/"+web+"/deployment-sharing", user,
				api.DeploymentSharingWrite{DeploymentSharing: api.DeploymentSharing(to)}, want, nil)
		}
		sharing(member, "owner_only", 403)
		sharing(projectAdmin, "everyone", 422)
		sharing(projectAdmin, "owner_only", 200)
		var project api.Project
		h.json("GET", "/api/v1/projects/"+web, member, nil, 200, &project)
		if project.DeploymentSharing == nil || *project.DeploymentSharing != api.OwnerOnly {
			t.Fatalf("sharing = %v", project.DeploymentSharing)
		}
		var audit api.AuditEventPage
		h.json("GET", "/api/v1/audit?action=project.sharing.set", admin, nil, 200, &audit)
		if len(audit.Items) != 1 {
			t.Fatalf("audited %+v", audit.Items)
		}

		vmsOf := func(user string) int {
			t.Helper()
			var page api.VmPage
			h.json("GET", "/api/v1/resources/vms?limit=200&project_id="+web, user, nil, 200, &page)
			return len(page.Items)
		}
		requestsOf := func(user string) []string {
			t.Helper()
			var page api.RequestPage
			h.json("GET", "/api/v1/requests?limit=200&project_id="+web, user, nil, 200, &page)
			var out []string
			for _, r := range page.Items {
				out = append(out, r.Id)
			}
			return out
		}
		for _, c := range []struct {
			user string
			sees []string
		}{
			{member, nil}, {owner, []string{kept.id}}, {given, []string{handed.id}},
			{projectAdmin, both}, {supervisor, both}, {admin, both},
		} {
			if got := listed(c.user, "&project_id="+web); !slices.Equal(got, c.sees) {
				t.Errorf("%s lists %v, want %v", c.user, got, c.sees)
			}
			if got := listed(c.user, ""); !slices.Equal(got, c.sees) {
				t.Errorf("%s lists %v in every project, want %v", c.user, got, c.sees)
			}
			// Search by name finds only what is seen.
			found := listed(c.user, "&q="+keptDep.Name)
			if want := slices.Contains(c.sees, kept.id); slices.Contains(found, kept.id) != want {
				t.Errorf("%s finds %s: %v, want %v", c.user, keptDep.Name, found, want)
			}
			if got := vmsOf(c.user); got != len(c.sees) {
				t.Errorf("%s lists %d VMs, want %d", c.user, got, len(c.sees))
			}
			var facets api.DeploymentFacets
			h.json("GET", "/api/v1/deployments/facets?project_id="+web, c.user, nil, 200, &facets)
			if (len(facets.Items) > 0) != (len(c.sees) > 0) {
				t.Errorf("%s's facets = %+v", c.user, facets)
			}
			for _, d := range []*vmDeployment{kept, handed} {
				want, actionsWant := 404, 404
				if slices.Contains(c.sees, d.id) {
					want, actionsWant = 200, 200
				}
				h.json("GET", "/api/v1/deployments/"+d.id, c.user, nil, want, nil)
				h.json("GET", "/api/v1/deployments/"+d.id+"/history", c.user, nil, want, nil)
				h.json("GET", "/api/v1/deployments/"+d.id+"/actions", c.user, nil, actionsWant, nil)
				// A request shows to who made it and whom it was for.
				reqWant := 404
				if len(c.sees) == 2 || c.user == owner {
					reqWant = 200
				}
				h.json("GET", "/api/v1/requests/"+d.built.Id, c.user, nil, reqWant, nil)
			}
			reqs := requestsOf(c.user)
			if slices.Contains(reqs, kept.built.Id) != (len(c.sees) == 2 || c.user == owner) {
				t.Errorf("%s lists requests %v", c.user, reqs)
			}
		}

		// Who doesn't see a deployment can't act on it, nor update it.
		h.json("POST", "/api/v1/deployments/"+kept.id+"/actions", member, api.Day2Draft{Action: "power_off", ResourceId: &kept.resource},
			404, nil, "Idempotency-Key", uuid.NewString())
		h.json("POST", "/api/v1/deployments/"+kept.id+"/update/preview", member,
			api.UpdateDraft{VersionId: keptDep.Version.Id, Inputs: map[string]any{}}, 404, nil)
		h.json("POST", "/api/v1/deployments/"+kept.id+"/update", member,
			api.UpdateDraft{VersionId: keptDep.Version.Id, Inputs: map[string]any{}}, 404, nil, "Idempotency-Key", uuid.NewString())
		h.json("POST", "/api/v1/deployments/"+kept.id+"/update/preview", owner,
			api.UpdateDraft{VersionId: keptDep.Version.Id, Inputs: map[string]any{}}, 200, nil)
		kept.succeed(owner, "power_off", nil)

		// An owner who no longer sees the project still lists their own.
		h.json("DELETE", "/api/v1/projects/"+web+"/members/"+seed.UserID(given), projectAdmin, nil, 204, nil)
		if got := listed(given, ""); !slices.Equal(got, []string{handed.id}) {
			t.Fatalf("outside the project, %s lists %v", given, got)
		}

		sharing(projectAdmin, "all_members", 200)
		if got := listed(member, "&project_id="+web); !slices.Equal(got, both) {
			t.Fatalf("shared again, %s lists %v", member, got)
		}
	})
}

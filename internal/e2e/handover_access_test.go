package e2e

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestHandoverInOwnerOnlyProject: in a project whose members see their own
// deployments only, handing a deployment over ends the old owner's stream
// of it, and the new owner opens every request of its history (ADR-0045).
func TestHandoverInOwnerOnlyProject(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const web, oldOwner, newOwner = "prj_web", "tnguyen", "afavre"
		if err := h.store.SetMemberRoles(ctx, web, seed.UserID(newOwner), []string{"member"}); err != nil {
			t.Fatal(err)
		}
		d := buildVM(h, nil)
		h.json("PUT", "/api/v1/projects/"+web+"/deployment-sharing", "lmartin",
			api.DeploymentSharingWrite{DeploymentSharing: api.OwnerOnly}, 200, nil)

		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		follow := "/api/v1/events?topics=deployment:" + d.id
		out := make(chan string, 256)
		go readSSE(sctx, h.srv.URL+follow, oldOwner, "", out)
		time.Sleep(500 * time.Millisecond)

		var r api.Request
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", oldOwner, api.Day2Draft{Action: "change_owner",
			Inputs: map[string]any{"owner": seed.UserID(newOwner)}}, 202, &r, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, oldOwner, api.RequestStatusSucceeded)
		if status, _, _ := h.do("GET", "/api/v1/deployments/"+d.id, oldOwner, nil); status != 404 {
			t.Fatalf("the old owner reads the deployment after the handover: %d", status)
		}
		got := drain(out, 2*time.Second)
		i := slices.Index(got, " resync")
		if i < 0 || i != len(got)-1 {
			t.Fatalf("the old owner's stream after the handover: %v, want it to end with resync", got)
		}
		last := ""
		for _, e := range got {
			if id, _, _ := strings.Cut(e, " "); id != "" {
				last = id
			}
		}

		// Reconnected, the stream no longer carries the deployment.
		again := make(chan string, 256)
		go readSSE(sctx, h.srv.URL+follow, oldOwner, last, again)
		drain(again, time.Second)
		after, err := h.store.LatestEventID(ctx)
		if err != nil {
			t.Fatal(err)
		}
		h.svc.Events.Deployment(ctx, d.id)
		for _, e := range drain(again, 2*time.Second) {
			id, typ, _ := strings.Cut(e, " ")
			if n, _ := strconv.ParseInt(id, 10, 64); n > after && typ == "deployment.updated" {
				t.Fatalf("the old owner still receives the deployment's events: %s", e)
			}
		}

		var history api.RequestPage
		h.json("GET", "/api/v1/deployments/"+d.id+"/history", newOwner, nil, 200, &history)
		if len(history.Items) != 2 {
			t.Fatalf("the history lists %d requests", len(history.Items))
		}
		for _, it := range history.Items {
			if status, _, _ := h.do("GET", "/api/v1/requests/"+it.Id, newOwner, nil); status != 200 {
				t.Errorf("the new owner opens %s (%s): %d", it.Number, it.Title, status)
			}
		}
		var mine api.RequestPage
		h.json("GET", "/api/v1/requests?project_id="+web, newOwner, nil, 200, &mine)
		if len(mine.Items) != 2 {
			t.Errorf("the new owner lists %d requests in %s, want the deployment's 2", len(mine.Items), web)
		}
		if status, _, _ := h.do("GET", "/api/v1/requests/"+d.built.Id, "cdubois", nil); status != 404 {
			t.Errorf("someone outside the project opens the build: %d", status)
		}
	})
}

// TestUnsharedItemDraftAndFavourite: an item not shared with the caller
// answers as one that doesn't exist when they save a draft of it or make
// it a favourite (ADR-0019).
func TestUnsharedItemDraftAndFavourite(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, member = "lmartin", "tnguyen"
		var rhel api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &rhel)
		var published api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/versions/"+rhel.PublishedVersion.Id, admin, nil, 200, &published)
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "linux-leads", Name: "Leads' Linux VM",
			Summary: "A VM for the leads.", Category: "compute", Type: api.VmBlueprint, Definition: &published.Definition}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/linux-leads/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/linux-leads/entitlements", admin, api.CatalogEntitlements{ProjectIds: []string{"prj_web"},
			Audiences: &[]api.CatalogProjectAudience{{ProjectId: "prj_web", UserIds: []string{seed.UserID("lmartin")},
				Groups: []string{}, Roles: []string{}}}}, 200, nil)

		draft := func(itemID, versionID string) api.SavedDraftWrite {
			return api.SavedDraftWrite{Draft: api.RequestDraft{CatalogItemId: itemID, VersionId: versionID, ProjectId: "prj_web",
				Environment: "dev", Inputs: map[string]any{}}}
		}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/me/drafts", member, draft("linux-leads", item.DraftVersion.Id), 422, &problem)
		if fieldCodes(problem.Errors)["/draft/catalog_item_id"] != "unknown" || fieldCodes(problem.Errors)["/draft/version_id"] != "unknown" {
			t.Errorf("a draft of an item not shared with the member: %+v", problem.Errors)
		}
		var none struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/me/drafts", member, draft("no-such-item", item.DraftVersion.Id), 422, &none)
		if len(none.Errors) != len(problem.Errors) {
			t.Errorf("an item that doesn't exist answers %+v, one not shared %+v", none.Errors, problem.Errors)
		}
		h.json("POST", "/api/v1/me/drafts", admin, draft("linux-leads", item.DraftVersion.Id), 201, nil)
		h.json("POST", "/api/v1/me/drafts", member, draft("rhel9-server", rhel.PublishedVersion.Id), 201, nil)

		h.json("PUT", "/api/v1/catalog/items/linux-leads/favourite", member, nil, 404, nil)
		h.json("DELETE", "/api/v1/catalog/items/linux-leads/favourite", member, nil, 404, nil)
		h.json("PUT", "/api/v1/catalog/items/linux-leads/favourite", admin, nil, 204, nil)
		h.json("PUT", "/api/v1/catalog/items/rhel9-server/favourite", member, nil, 204, nil)
	})
}

// fieldCodes are the code of each error, by field.
func fieldCodes(errs []api.FieldError) map[string]string {
	out := map[string]string{}
	for _, e := range errs {
		out[e.Field] = deref(e.Code)
	}
	return out
}

package e2e

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/ad/adtest"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/seed"
)

// adRig is a harness with a test directory and an OU row for prj_web.
type adRig struct {
	h   *harness
	t   *testing.T
	c   *vim25.Client
	dir *adtest.Server
	web string
}

func newADRig(t *testing.T, ctx context.Context, c *vim25.Client, ous []string, groups ...string) *adRig {
	h := setup(t, ctx, c)
	web := "OU=Web," + adtest.BaseDN
	dir := adtest.Start(t, append([]string{web}, ous...), groups...)
	h.svc.Config.AD = directoryConfig(dir)
	h.acts.Directory = func(context.Context) provision.Directory {
		return renaming{core.Directory(h.svc.Config.AD), dir}
	}
	return &adRig{h: h, t: t, c: c, dir: dir, web: web}
}

func (r *adRig) item(slug string) api.AdminCatalogItem {
	r.t.Helper()
	def := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example",
		"cloud_init": map[string]any{"user_data": "#cloud-config\n"}})
	var item api.AdminCatalogItem
	r.h.json("POST", "/api/v1/admin/catalog/items", "mkeller", api.CatalogItemCreate{Slug: slug, Name: slug, Summary: slug,
		Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
	r.h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", "mkeller", nil, 200, nil)
	r.h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", "mkeller", api.CatalogEntitlements{AllProjects: true,
		ProjectIds: []string{}}, 200, nil)
	return item
}

func (r *adRig) draft(item api.AdminCatalogItem, role string) api.RequestDraft {
	return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
		Inputs: map[string]any{"role": role, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}
}

func (r *adRig) build(d api.RequestDraft) api.Request {
	r.t.Helper()
	var req api.Request
	r.h.json("POST", "/api/v1/requests", "tnguyen", d, 202, &req, "Idempotency-Key", uuid.NewString())
	r.h.runWorkflow(req.Id, cloudInitGuest(r.h, r.c))
	return r.h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
}

func (r *adRig) deleteDeployment(id string) api.Request {
	r.t.Helper()
	var del api.Request
	r.h.json("POST", "/api/v1/deployments/"+id+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
		202, &del, "Idempotency-Key", uuid.NewString())
	r.h.runWorkflow(del.Id, nil)
	return r.h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
}

// TestActiveDirectoryNameLength: every name a request gives is held to
// the 15 characters of a computer name, not its first one only.
func TestActiveDirectoryNameLength(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		r := newADRig(t, ctx, c, nil)
		r.h.json("POST", "/api/v1/admin/ad-placements", "mkeller", api.AdPlacementWrite{ProjectId: new("prj_web"), Ou: "OU=Web"}, 201, nil)
		if _, err := r.h.store.Pool.Exec(ctx, `insert into name_counters (prefix, last_value) values ('web-dev-abcde', 98)`); err != nil {
			t.Fatal(err)
		}
		d := r.draft(r.item("ad-len"), "abcde")
		d.Count = new(2)
		var pv api.RequestPreview
		r.h.json("POST", "/api/v1/requests/preview", "tnguyen", d, 200, &pv)
		if i := slices.IndexFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "invalid_name" }); pv.Valid || i < 0 ||
			pv.Errors[i].Field != "/inputs/role" || !strings.HasPrefix(pv.Errors[i].Message, "web-dev-abcde100 is longer than the 15") {
			t.Fatalf("two VMs, the second's name too long: %+v", pv.Errors)
		}
		r.h.json("POST", "/api/v1/requests", "tnguyen", d, 422, nil, "Idempotency-Key", uuid.NewString())
		d.Count = new(1)
		r.build(d)
		if got := r.dir.Computers(); len(got) != 1 || !strings.HasPrefix(got[0], "CN=WEB-DEV-ABCDE99,") {
			t.Fatalf("computers = %v", got)
		}
	})
}

// TestActiveDirectoryRenameGone: a VM whose computer object an
// administrator deleted is renamed all the same, its addresses and
// records too, and the deployment no longer holds the object.
func TestActiveDirectoryRenameGone(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		r := newADRig(t, ctx, c, nil)
		r.h.json("POST", "/api/v1/admin/ad-placements", "mkeller", api.AdPlacementWrite{ProjectId: new("prj_web"), Ou: "OU=Web"}, 201, nil)
		req := r.build(r.draft(r.item("ad-gone"), "lin"))
		depID := (*req.DeploymentIds)[0]
		if err := core.Directory(r.h.svc.Config.AD).Delete(ctx, "CN=WEB-DEV-LIN01,"+r.web); err != nil {
			t.Fatal(err)
		}
		d := &vmDeployment{h: r.h, id: depID, built: req, viewer: "tnguyen"}
		d.resource = *d.actions("tnguyen")["power_off"].ResourceId
		ren := d.succeed("tnguyen", "rename", map[string]any{"name": "web-dev-new01"})
		steps := timelineOf(r.h, "tnguyen", ren.Id)
		if s := steps["ad.rename"]; s.Status != api.StepStatusDone ||
			deref(s.Detail) != "Active Directory no longer has WEB-DEV-LIN01$: the deployment no longer holds it" {
			t.Fatalf("ad.rename = %s %q", s.Status, deref(s.Detail))
		}
		for _, k := range []string{"ipam.rename", "dns.rename"} {
			if s, ok := steps[k]; ok && s.Status != api.StepStatusDone {
				t.Fatalf("%s = %s", k, s.Status)
			}
		}
		name, err := object.NewVirtualMachine(c, types.ManagedObjectReference{Type: "VirtualMachine", Value: d.vm().Moref}).ObjectName(ctx)
		if err != nil || name != "web-dev-new01" {
			t.Fatalf("the VM = %q, %v", name, err)
		}
		var n int
		if err := r.h.store.Pool.QueryRow(ctx, `select count(*) from resources where deployment_id = $1 and type = 'ad.computer'`, depID).
			Scan(&n); err != nil || n != 0 {
			t.Fatalf("ad.computer resources = %d, %v", n, err)
		}
		r.deleteDeployment(depID)
	})
}

// TestActiveDirectoryOwnership: rvav deletes only an object where it made
// it, with the description it wrote (docs/operations/security.md): one an
// administrator moved or described again is left, and the deployment's
// delete goes on.
func TestActiveDirectoryOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		quarantine := "OU=Quarantine," + adtest.BaseDN
		r := newADRig(t, ctx, c, []string{quarantine})
		r.h.json("POST", "/api/v1/admin/ad-placements", "mkeller", api.AdPlacementWrite{ProjectId: new("prj_web"), Ou: "OU=Web"}, 201, nil)
		item := r.item("ad-owned")
		moved, edited := r.build(r.draft(item, "mv")), r.build(r.draft(item, "ed"))

		dn, movedDN := "CN=WEB-DEV-MV01,"+r.web, "CN=WEB-DEV-MV01,"+quarantine
		attrs := maps.Clone(r.dir.Entry(dn))
		if err := core.Directory(r.h.svc.Config.AD).Delete(ctx, dn); err != nil {
			t.Fatal(err)
		}
		r.dir.Put(movedDN, attrs)
		edn := "CN=WEB-DEV-ED01," + r.web
		r.dir.Entry(edn)["description"] = []string{r.dir.Entry(edn)["description"][0] + " (owner: app team)"}

		for _, req := range []api.Request{moved, edited} {
			del := r.deleteDeployment((*req.DeploymentIds)[0])
			if s := timelineOf(r.h, "tnguyen", del.Id)["ad.computer.delete"]; s.Status != api.StepStatusDone ||
				!strings.Contains(deref(s.Detail), "no longer rvav's") {
				t.Errorf("delete of %s: %s %q", req.Number, s.Status, deref(s.Detail))
			}
		}
		if r.dir.Entry(movedDN) == nil || r.dir.Entry(edn) == nil {
			t.Fatalf("computers left = %v", r.dir.Computers())
		}
	})
}

// TestActiveDirectoryProjectPlacement: a role that manages placement in
// one project only places its rows under the OU of the row for every
// project, and names groups under its own OU or among that row's; a
// directory that can't be reached answers 502 (ADR-0064).
func TestActiveDirectoryProjectPlacement(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		servers, users := "OU=Servers,"+adtest.BaseDN, "CN=Users,"+adtest.BaseDN
		all, webServers, admins := "CN=All servers,"+servers, "CN=Web servers,OU=Web,"+servers, "CN=Domain Admins,"+users
		r := newADRig(t, ctx, c, []string{servers, "OU=Web," + servers, users}, all, webServers, admins)
		const admin, placer = "mkeller", "cdubois"
		r.h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "placer", Name: "Placer",
			Permissions: []api.Permission{"project.see", "placement.manage"}}, 201, nil)
		r.h.json("PUT", "/api/v1/projects/prj_web/members/"+seed.UserID(placer), admin,
			api.ProjectMemberWrite{Roles: &[]string{"placer"}}, 200, nil)
		save := func(user, id string, w api.AdPlacementWrite, want int) (api.AdPlacement, []string) {
			t.Helper()
			method, path := "POST", "/api/v1/admin/ad-placements"
			if id != "" {
				method, path = "PUT", path+"/"+id
			}
			var row api.AdPlacement
			var problem struct{ Errors []api.FieldError }
			code, b, _ := r.h.do(method, path, user, w)
			if code != want {
				t.Fatalf("%s %+v = %d %s, want %d", user, w, code, b, want)
			}
			_ = json.Unmarshal(b, &row)
			_ = json.Unmarshal(b, &problem)
			return row, fieldsOf(problem.Errors)
		}
		web := new("prj_web")

		// Nothing delegated yet: refused.
		if _, fields := save(placer, "", api.AdPlacementWrite{ProjectId: web, Ou: "OU=Web,OU=Servers"}, 422); !slices.Equal(fields, []string{"/ou"}) {
			t.Fatalf("no row for every project = %v", fields)
		}
		save(admin, "", api.AdPlacementWrite{Ou: "OU=Servers", Groups: &[]string{"CN=All servers,OU=Servers"}}, 201)

		// Outside the OU delegated, or a group outside its own OU: refused.
		if _, fields := save(placer, "", api.AdPlacementWrite{ProjectId: web, Ou: "CN=Users"}, 422); !slices.Equal(fields, []string{"/ou"}) {
			t.Fatalf("an OU outside = %v", fields)
		}
		if _, fields := save(placer, "", api.AdPlacementWrite{ProjectId: web, Ou: "OU=Web,OU=Servers",
			Groups: &[]string{"CN=Web servers,OU=Web,OU=Servers", "CN=Domain Admins,CN=Users"}}, 422); !slices.Equal(fields, []string{"/groups/1"}) {
			t.Fatalf("Domain Admins = %v", fields)
		}
		row, _ := save(placer, "", api.AdPlacementWrite{ProjectId: web, Ou: "OU=Web,OU=Servers",
			Groups: &[]string{"CN=Web servers,OU=Web,OU=Servers", "CN=All servers,OU=Servers"}}, 201)

		// What an administrator for all of rvav set stays when it saves.
		save(admin, row.Id, api.AdPlacementWrite{ProjectId: web, Ou: "OU=Web,OU=Servers",
			Groups: &[]string{"CN=Web servers,OU=Web,OU=Servers", "CN=Domain Admins,CN=Users"}}, 200)
		save(placer, row.Id, api.AdPlacementWrite{ProjectId: web, Ou: "OU=Web,OU=Servers",
			Groups: &[]string{"CN=Web servers,OU=Web,OU=Servers", "CN=Domain Admins,CN=Users"}, AllowBlueprintOu: new(true)}, 200)

		// A directory that can't be reached isn't the row's fault.
		r.h.svc.Config.AD.URL = "ldaps://127.0.0.1:1"
		code, b, _ := r.h.do("PUT", "/api/v1/admin/ad-placements/"+row.Id, admin, api.AdPlacementWrite{ProjectId: web, Ou: "OU=Web,OU=Servers"})
		if code != http.StatusBadGateway {
			t.Fatalf("the directory down = %d %s", code, b)
		}
	})
}

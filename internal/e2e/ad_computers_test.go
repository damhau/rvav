package e2e

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/ad"
	"github.com/damhau/rvav/internal/integrations/ad/adtest"
	"github.com/damhau/rvav/internal/provision"
)

// renaming is the directory with its renames' modify DN done in the test
// directory, which can't serve one.
type renaming struct {
	*ad.Client
	dir *adtest.Server
}

func (r renaming) Rename(ctx context.Context, dn, name, dnsHostName string) (string, error) {
	return r.Client.Rename(ctx, r.dir.RenameEntry(dn, strings.ToUpper(name)), name, dnsHostName)
}

// TestActiveDirectoryComputerObjects: a Linux VM gets its computer object
// in its project's OU before it is cloned, with its name, DNS name and a
// description naming its request and deployment, and in its groups; once,
// however often the step is tried. Names Active Directory already has are
// passed over, too long ones refused; a rename renames the object, and is
// refused to a name the directory has. A failed clone, a rollback and the
// VM's deletion remove it; a delete Active Directory refuses fails, and a
// retry finishes it (ADR-0064).
func TestActiveDirectoryComputerObjects(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, user = "mkeller", "tnguyen"
		web, linuxOU := "OU=Web,"+adtest.BaseDN, "OU=Linux,OU=Web,"+adtest.BaseDN
		group := "CN=Web servers," + web
		dir := adtest.Start(t, []string{web, linuxOU}, group)
		h.svc.Config.AD = directoryConfig(dir)
		h.acts.Directory = func(context.Context) provision.Directory {
			return renaming{core.Directory(h.svc.Config.AD), dir}
		}
		h.json("POST", "/api/v1/admin/ad-placements", admin, api.AdPlacementWrite{ProjectId: new("prj_web"), Environment: new(api.EnvironmentKey("dev")),
			Ou: "OU=Web", Groups: &[]string{"CN=Web servers,OU=Web"}, AllowBlueprintOu: new(true)}, 201, nil)

		publish := func(slug string, extra map[string]any) api.AdminCatalogItem {
			t.Helper()
			guest := map[string]any{"os_family": "linux", "domain": "corp.example", "cloud_init": map[string]any{"user_data": "#cloud-config\n"}}
			for k, v := range extra {
				guest[k] = v
			}
			def := guestItem("tpl-rhel9.4-2026.10", guest)
			if slug == "ad-rollback" {
				def.Blueprint["rollback_on_failure"] = true
			}
			var item api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: slug, Summary: slug,
				Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			return item
		}
		draft := func(item api.AdminCatalogItem, role string) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
				Inputs: map[string]any{"role": role, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}
		}
		preview := func(d api.RequestDraft) api.RequestPreview {
			t.Helper()
			var pv api.RequestPreview
			h.json("POST", "/api/v1/requests/preview", user, d, 200, &pv)
			return pv
		}
		submit := func(d api.RequestDraft) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", user, d, 202, &req, "Idempotency-Key", uuid.NewString())
			return req
		}
		computers := func(id string) []api.AdComputerResource {
			t.Helper()
			var dep struct{ Resources []api.Resource }
			h.json("GET", "/api/v1/deployments/"+id, user, nil, 200, &dep)
			var out []api.AdComputerResource
			for _, r := range dep.Resources {
				if kind, _ := r.Discriminator(); kind == "ad.computer" {
					v, _ := r.AsAdComputerResource()
					out = append(out, v)
				}
			}
			return out
		}
		cloneFails := func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(h.acts.CloneVM, mock.Anything, mock.Anything).Return("", temporal.NewNonRetryableApplicationError(
				"vCenter refused the clone", "test", nil))
		}

		// A computer of the next name is someone else's: the name is passed
		// over. One too long for Active Directory is refused.
		dir.Put("CN=WEB-DEV-LIN01,"+web, map[string][]string{"objectClass": {"computer"}, "sAMAccountName": {"WEB-DEV-LIN01$"},
			"description": {"made by hand"}})
		item := publish("ad-linux", nil)
		if pv := preview(draft(item, "lin")); pv.Names == nil || !slices.Equal(*pv.Names, []string{"web-dev-lin02"}) {
			t.Fatalf("names = %v, errors %+v", pv.Names, pv.Errors)
		}
		pv := preview(draft(item, "averylongrole"))
		if i := slices.IndexFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "invalid_name" }); i < 0 ||
			pv.Errors[i].Field != "/inputs/role" || !strings.Contains(pv.Errors[i].Message, "15 characters") {
			t.Fatalf("a name too long = %+v", pv.Errors)
		}

		// Made before the clone, once though its first try's answer is lost.
		var tries atomic.Int32
		req := submit(draft(item, "lin"))
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			cloudInitGuest(h, c)(env)
			env.OnActivity(h.acts.CreateComputer, mock.Anything, mock.Anything).Return(func(ctx context.Context, in provision.VMInput) error {
				err := h.acts.CreateComputer(ctx, in)
				if tries.Add(1) == 1 && err == nil {
					return temporal.NewApplicationError("the answer was lost", "test")
				}
				return err
			})
		})
		req = h.waitStatus(req.Id, user, api.RequestStatusSucceeded)
		depID := (*req.DeploymentIds)[0]
		dn := "CN=WEB-DEV-LIN02," + web
		e := dir.Entry(dn)
		if tries.Load() != 2 || dir.Adds() != 1 || e == nil {
			t.Fatalf("%d tries, %d adds, computers %v", tries.Load(), dir.Adds(), dir.Computers())
		}
		for attr, want := range map[string]string{"sAMAccountName": "WEB-DEV-LIN02$", "userAccountControl": "4096",
			"dNSHostName": "web-dev-lin02.corp.example", "description": "rvav: request " + req.Id + ", deployment " + depID} {
			if !slices.Equal(e[attr], []string{want}) {
				t.Errorf("%s = %v, want %s", attr, e[attr], want)
			}
		}
		if !slices.Contains(e["objectClass"], "computer") || !slices.Contains(dir.Entry(group)["member"], dn) {
			t.Fatalf("entry %v; group %v", e, dir.Entry(group))
		}
		steps := timelineOf(h, user, req.Id)
		if s := steps["ad.computer"]; s.System == nil || *s.System != api.StepSystemAd || s.Title != "Created computer object WEB-DEV-LIN02$" ||
			deref(s.Detail) != dn+", in 1 group" {
			t.Fatalf("step = %q %q %v", s.Title, deref(s.Detail), s.System)
		}
		if got := computers(depID); len(got) != 1 || got[0].Computer.Dn != dn || got[0].Computer.SamAccountName != "WEB-DEV-LIN02$" ||
			!slices.Equal(got[0].Computer.Groups, []string{group}) {
			t.Fatalf("resources = %+v", got)
		}

		// Renamed with its VM; not to a name Active Directory has, nor to
		// one too long for it.
		d := &vmDeployment{h: h, id: depID, built: req, viewer: user}
		d.resource = *d.actions(user)["power_off"].ResourceId
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", user, api.Day2Draft{Action: "rename", ResourceId: &d.resource,
			Inputs: map[string]any{"name": "web-dev-lin01"}}, 422, &problem, "Idempotency-Key", uuid.NewString())
		if len(problem.Errors) != 1 || problem.Errors[0].Message != "Active Directory already has a computer WEB-DEV-LIN01$." {
			t.Fatalf("a name the directory has = %+v", problem.Errors)
		}
		d.run(user, "rename", map[string]any{"name": "web-dev-renamed01"}, 422)
		renamed := d.succeed(user, "rename", map[string]any{"name": "web-dev-new01"})
		dn = "CN=WEB-DEV-NEW01," + web
		e = dir.Entry(dn)
		if e == nil || dir.Entry("CN=WEB-DEV-LIN02,"+web) != nil || !slices.Equal(e["sAMAccountName"], []string{"WEB-DEV-NEW01$"}) ||
			!slices.Equal(e["dNSHostName"], []string{"web-dev-new01.corp.example"}) || !slices.Contains(dir.Entry(group)["member"], dn) {
			t.Fatalf("renamed entry %v, computers %v", e, dir.Computers())
		}
		if s := timelineOf(h, user, renamed.Id)["ad.rename"]; s.Status != api.StepStatusDone || deref(s.Detail) != "WEB-DEV-LIN02$ → WEB-DEV-NEW01$" {
			t.Fatalf("rename step = %+v", s)
		}
		if got := computers(depID); len(got) != 1 || got[0].Name != "web-dev-new01" || got[0].Computer.Dn != dn {
			t.Fatalf("resources after the rename = %+v", got)
		}

		// Deleted after its VM. When Active Directory refuses, the delete
		// fails and says so, and a retry deletes it.
		dir.FailDelete(true)
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", user, api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, user, api.RequestStatusFailed)
		if s := timelineOf(h, user, del.Id)["ad.computer.delete"]; s.Status != api.StepStatusFailed || !strings.Contains(deref(s.Error), "Active Directory") {
			t.Fatalf("refused delete step = %+v", s)
		}
		if dir.Entry(dn) == nil {
			t.Fatal("the refused delete deleted the object")
		}
		dir.FailDelete(false)
		h.json("POST", "/api/v1/requests/"+del.Id+"/retry", user, nil, 202, nil)
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, user, api.RequestStatusSucceeded)
		if dir.Entry(dn) != nil || len(dir.Entry(group)["member"]) != 0 {
			t.Fatalf("after the delete: computers %v, group %v", dir.Computers(), dir.Entry(group))
		}

		// A failed clone removes it, in the OU the blueprint chose; a retry
		// makes it again.
		ou := publish("ad-ou", map[string]any{"active_directory": map[string]any{"ou": "OU=Linux,OU=Web"}})
		failed := submit(draft(ou, "bad"))
		h.runWorkflow(failed.Id, cloneFails)
		h.waitStatus(failed.Id, user, api.RequestStatusFailed)
		dn = "CN=WEB-DEV-BAD01," + linuxOU
		if s := timelineOf(h, user, failed.Id)["ad.computer"]; dir.Entry(dn) != nil || dir.Adds() != 2 ||
			s.Title != "Removed computer object WEB-DEV-BAD01$" || len(computers((*failed.DeploymentIds)[0])) != 0 {
			t.Fatalf("after the failed clone: %d adds, computers %v, step %q %q", dir.Adds(), dir.Computers(), s.Title, deref(s.Detail))
		}
		h.json("POST", "/api/v1/requests/"+failed.Id+"/retry", user, nil, 202, nil)
		h.runWorkflow(failed.Id, cloudInitGuest(h, c))
		h.waitStatus(failed.Id, user, api.RequestStatusSucceeded)
		if dir.Entry(dn) == nil || dir.Adds() != 3 {
			t.Fatalf("after the retry: %d adds, computers %v", dir.Adds(), dir.Computers())
		}

		// A rollback removes it.
		rolled := submit(draft(publish("ad-rollback", nil), "rb"))
		h.runWorkflow(rolled.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.OnActivity(h.acts.PowerOn, mock.Anything, mock.Anything).Return("", temporal.NewNonRetryableApplicationError(
				"the VM doesn't start", "test", nil))
		})
		h.waitStatus(rolled.Id, user, api.RequestStatusFailed)
		if s := timelineOf(h, user, rolled.Id)["rollback"]; s.Status != api.StepStatusDone || dir.Adds() != 4 ||
			dir.Entry("CN=WEB-DEV-RB01,"+web) != nil {
			t.Fatalf("rollback %+v; %d adds, computers %v", s, dir.Adds(), dir.Computers())
		}

		// A blueprint the row lets skip it gets none.
		skipped := submit(draft(publish("ad-skip", map[string]any{"active_directory": map[string]any{"skip": true}}), "skip"))
		h.runWorkflow(skipped.Id, cloudInitGuest(h, c))
		h.waitStatus(skipped.Id, user, api.RequestStatusSucceeded)
		if _, ok := timelineOf(h, user, skipped.Id)["ad.computer"]; ok || dir.Adds() != 4 {
			t.Fatalf("skipped: %d adds, computers %v", dir.Adds(), dir.Computers())
		}
	})
}

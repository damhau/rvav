package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
)

// TestDay2Tags: a VM's users change its vSphere tags within the categories
// its environment allows (X1.3). The new tags are what rvav expects, not
// drift; a change made in vCenter is drift until accepted; an environment
// that allows no category offers no edit.
func TestDay2Tags(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		d := buildVM(h, model)
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		resync := func() {
			t.Helper()
			if err := sync.SyncOnce(ctx); err != nil {
				t.Fatal(err)
			}
		}
		tags := func() []string {
			t.Helper()
			if p := d.vm().Tags; p != nil {
				return *p
			}
			return nil
		}
		drift := func() []string {
			t.Helper()
			var dep struct {
				Resources []struct {
					Type  string
					Drift []api.VmDrift `json:"drift"`
				}
			}
			h.json("GET", "/api/v1/deployments/"+d.id, "tnguyen", nil, 200, &dep)
			var out []string
			for _, r := range dep.Resources {
				for _, x := range r.Drift {
					out = append(out, x.Label+": "+deref(x.Rvav)+" → "+deref(x.Vcenter))
				}
			}
			return out
		}

		// Before the sync has read the VM's tags, there is nothing to edit.
		if a := d.actions("tnguyen")["tags_edit"]; a.Enabled {
			t.Fatalf("tags_edit before the tags were read = %+v", a)
		}
		resync()
		if got := tags(); got == nil || len(got) != 0 {
			t.Fatalf("tags after the sync = %v", got)
		}
		var inv api.VsphereInventory
		h.json("GET", "/api/v1/admin/inventory", admin, nil, 200, &inv)
		if i := slices.IndexFunc(inv.TagCategories, func(c api.InventoryTagCategory) bool { return c.Name == "backup" }); i < 0 ||
			!slices.Equal(inv.TagCategories[i].Tags, []string{"daily", "weekly"}) {
			t.Fatalf("inventory tag categories = %+v", inv.TagCategories)
		}

		// The seed lets Development's users change backup tags.
		a := d.actions("tnguyen")["tags_edit"]
		if !a.Enabled || a.Form == nil || a.Group != api.Configuration {
			t.Fatalf("tags_edit = %+v", a)
		}
		var offered []string
		for _, o := range *a.Form.UiSchema.Fields["tags"].Options {
			offered = append(offered, o.Value)
		}
		if !slices.Equal(offered, []string{"backup/daily", "backup/weekly"}) {
			t.Fatalf("tags offered = %v", offered)
		}
		var problem struct{ Errors []api.FieldError }
		d.h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "tags_edit", ResourceId: &d.resource,
			Inputs: map[string]any{"tags": []string{"app/shop"}}}, 422, &problem, "Idempotency-Key", uuid.NewString())
		if !slices.Contains(fieldsOf(problem.Errors), "/inputs/tags/0") {
			t.Fatalf("a tag of another category = %+v", problem.Errors)
		}
		d.run("tnguyen", "tags_edit", map[string]any{"tags": []string{}}, 422)

		r := d.succeed("tnguyen", "tags_edit", map[string]any{"tags": []string{"backup/daily"}})
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+r.Id+"/steps", "tnguyen", nil, 200, &steps)
		if !slices.ContainsFunc(steps.Items, func(s api.Step) bool {
			return s.Key == "vsphere.tags" && deref(s.Detail) == "Adds backup/daily"
		}) {
			t.Fatalf("steps = %+v", steps.Items)
		}
		if got := tags(); !slices.Equal(got, []string{"backup/daily"}) || d.syncState() != api.InSync {
			t.Fatalf("after the edit: tags %v, %s", got, d.syncState())
		}
		resync()
		if got := drift(); len(got) != 0 {
			t.Fatalf("the new tags drift: %v", got)
		}

		// Changed in vCenter: drift, until accepted.
		if err := h.vc.SetTags(ctx, d.vm().Moref, []string{"backup"}, []string{"backup/daily", "backup/weekly"}); err != nil {
			t.Fatal(err)
		}
		resync()
		if got := drift(); !slices.Equal(got, []string{"backup tags: daily → daily, weekly"}) {
			t.Fatalf("drift = %v", got)
		}
		d.succeed("tnguyen", "accept_changes", nil)
		if got := drift(); len(got) != 0 || !slices.Equal(tags(), []string{"backup/daily", "backup/weekly"}) {
			t.Fatalf("accepted: drift %v, tags %v", got, tags())
		}

		// An environment that allows no category offers no edit.
		var dev api.AdminEnvironment
		h.json("PATCH", "/api/v1/admin/environments/dev", admin, api.AdminEnvironmentUpdate{TagCategories: &[]string{}}, 200, &dev)
		if dev.TagCategories == nil || len(*dev.TagCategories) != 0 {
			t.Fatalf("dev = %+v", dev)
		}
		if a := d.actions("tnguyen")["tags_edit"]; a.Enabled ||
			deref(a.DisabledReason) != "No tag category may be changed in Development: an administrator chooses them on the environment." {
			t.Fatalf("tags_edit without categories = %+v", a)
		}
	})
}

package vsphere

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
)

// TestSetTags: within the categories named, a VM ends with exactly the
// tags wanted; its other tags stay, running it again changes nothing, and
// a tag vCenter lacks is refused. The inventory reads VMs' tags and the
// categories VMs can take, with their tags (X1.3).
func TestSetTags(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		if err := Bootstrap(ctx, c, DefaultLayout()); err != nil {
			t.Fatal(err)
		}
		d := NewFromVim("vc01.corp.example", c)
		vm, err := d.CloneVM(ctx, VMSpec{Name: "web-dev-tags01", Template: "tpl-rhel9.4-2026.10", Cluster: "GVA-DEV-CL01",
			Datastore: "vsan-gva-dev-cl01", Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/rvav/web/dev", VCPU: 2, MemoryMB: 2048, DiskGB: 50})
		if err != nil {
			t.Fatal(err)
		}
		if err := d.AttachTags(ctx, vm.Moref, []string{"app/shop"}); err != nil {
			t.Fatal(err)
		}
		tags := func(want ...string) {
			t.Helper()
			got, err := d.VMTags(ctx, vm.Moref)
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("tags = %v, %v; want %v", got, err, want)
			}
		}
		backup := []string{"backup"}
		for _, step := range []struct {
			want []string
			got  []string
		}{
			{[]string{"backup/daily"}, []string{"app/shop", "backup/daily"}},
			{[]string{"backup/weekly"}, []string{"app/shop", "backup/weekly"}},
			{[]string{"backup/weekly"}, []string{"app/shop", "backup/weekly"}},
			{[]string{"backup/daily", "backup/weekly"}, []string{"app/shop", "backup/daily", "backup/weekly"}},
			{[]string{}, []string{"app/shop"}},
		} {
			if err := d.SetTags(ctx, vm.Moref, backup, step.want); err != nil {
				t.Fatalf("set %v: %v", step.want, err)
			}
			tags(step.got...)
		}
		var refused *Refused
		if err := d.SetTags(ctx, vm.Moref, backup, []string{"backup/monthly"}); !errors.As(err, &refused) {
			t.Fatalf("a tag vCenter lacks = %v", err)
		}
		if err := d.SetTags(ctx, vm.Moref, []string{"nope"}, []string{"nope/x"}); !errors.As(err, &refused) {
			t.Fatalf("a category vCenter lacks = %v", err)
		}
		tags("app/shop")

		if err := d.SetTags(ctx, vm.Moref, backup, []string{"backup/daily"}); err != nil {
			t.Fatal(err)
		}
		inv, err := d.Inventory(ctx)
		if err != nil || inv.TagsError != nil {
			t.Fatal(err, inv.TagsError)
		}
		i := slices.IndexFunc(inv.VMs, func(v InventoryVM) bool { return v.Moref == vm.Moref })
		if i < 0 || !slices.Equal(inv.VMs[i].Tags, []string{"app/shop", "backup/daily"}) {
			t.Fatalf("inventory VM = %+v", inv.VMs[i])
		}
		var names []string
		for _, c := range inv.TagCategories {
			names = append(names, c.Name)
			if c.Name == "backup" && (c.Single || !slices.Equal(c.Tags, []string{"daily", "weekly"})) {
				t.Errorf("category backup = %+v", c)
			}
		}
		if !slices.Equal(names, []string{"app", "backup"}) {
			t.Errorf("categories for VMs = %v (the others are for clusters, datastores and networks)", names)
		}
	})
}

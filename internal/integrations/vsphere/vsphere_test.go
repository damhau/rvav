package vsphere

import (
	"context"
	"slices"
	"testing"
	"time"

	_ "github.com/vmware/govmomi/pbm/simulator" // storage policies
	"github.com/vmware/govmomi/simulator"
	_ "github.com/vmware/govmomi/vapi/simulator" // the tagging service and content libraries
	"github.com/vmware/govmomi/vim25"
)

func TestBootstrapCloneCustomizeDestroy(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		if err := Bootstrap(ctx, c, DefaultLayout()); err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		// Running it again must not fail or duplicate anything.
		if err := Bootstrap(ctx, c, DefaultLayout()); err != nil {
			t.Fatalf("second bootstrap: %v", err)
		}
		d := NewFromVim("vc01.corp.example", c)

		inv, err := d.Inventory(ctx)
		if err != nil {
			t.Fatalf("inventory: %v", err)
		}
		cluster := findCluster(inv, "GVA-DEV-CL01")
		if cluster == nil || cluster.HostCount != 3 || cluster.Datacenter != "GVA" {
			t.Fatalf("cluster GVA-DEV-CL01 = %+v", cluster)
		}
		ds := findDatastore(inv, "vsan-gva-dev-cl01")
		if ds == nil || len(ds.Clusters) != 1 || ds.Clusters[0] != "GVA-DEV-CL01" {
			t.Fatalf("datastore vsan-gva-dev-cl01 = %+v", ds)
		}
		// Tags, for placement by tag, once each though the bootstrap ran twice.
		if inv.TagsError != nil || !contains(cluster.Tags, "rvav-tier/bronze") || len(cluster.Tags) != 1 ||
			!contains(ds.Tags, "rvav-tier/gold") || len(ds.Tags) != 1 {
			t.Fatalf("tags: cluster %v, datastore %v, error %v", cluster.Tags, ds.Tags, inv.TagsError)
		}
		pg := findNetworkInv(inv, "dvpg-app-dev-vl310")
		if pg == nil || !contains(pg.Clusters, "GVA-DEV-CL01") {
			t.Fatalf("port group dvpg-app-dev-vl310 = %+v", pg)
		}
		// Three VM templates, the content library's item (ADR-0024) and the
		// VM template vcsim keeps for it; the storage policies vcsim has.
		var library []string
		for _, tpl := range inv.Templates {
			if tpl.Library != "" {
				library = append(library, tpl.Library+"/"+tpl.Name)
			}
		}
		if len(inv.Templates) != 5 || len(library) != 1 || library[0] != "rvav-templates/rhel9-library" || inv.LibrariesError != nil {
			t.Fatalf("templates = %+v (%v)", inv.Templates, inv.LibrariesError)
		}
		if inv.PoliciesError != nil || !slices.ContainsFunc(inv.StoragePolicies, func(p StoragePolicy) bool { return p.Name == "vSAN Default Storage Policy" }) {
			t.Fatalf("storage policies = %+v (%v)", inv.StoragePolicies, inv.PoliciesError)
		}

		spec := VMSpec{
			Name: "web-dev-app007", Template: "tpl-rhel9.4-2026.10", Cluster: "GVA-DEV-CL01",
			Datastore: "vsan-gva-dev-cl01", Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/rvav/web/dev",
			VCPU: 4, MemoryMB: 16384, DiskGB: 80, ExtraConfig: map[string]string{"rvav.deployment": "dep_test"},
		}
		vm, err := d.CloneVM(ctx, spec)
		if err != nil {
			t.Fatalf("clone: %v", err)
		}
		if vm.CPUCount != 4 || vm.MemoryMB != 16384 || vm.DiskGB != 80 || vm.Folder != "/GVA/vm/rvav/web/dev" {
			t.Fatalf("cloned vm = %+v", vm)
		}
		if len(vm.NICs) != 1 || vm.NICs[0].Network != "dvpg-app-dev-vl310" {
			t.Fatalf("nics = %+v", vm.NICs)
		}
		again, err := d.CloneVM(ctx, spec)
		if err != nil || again.Moref != vm.Moref {
			t.Fatalf("second clone should return the same VM: %v %+v", err, again)
		}

		if err := d.Customize(ctx, vm.Moref, Customization{OSFamily: "linux", Hostname: "web-dev-app007",
			Domain: "corp.example", IP: "10.31.10.57", PrefixLength: 24, Gateway: "10.31.10.1"}); err != nil {
			t.Fatalf("customize: %v", err)
		}
		if err := d.PowerOn(ctx, vm.Moref); err != nil {
			t.Fatalf("power on: %v", err)
		}
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		ip, err := d.WaitForIP(wctx, vm.Moref)
		if err != nil || ip != "10.31.10.57" {
			t.Fatalf("wait for ip = %q, %v", ip, err)
		}

		if err := d.Destroy(ctx, vm.Moref); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		if gone, err := d.FindVM(ctx, "web-dev-app007"); err != nil || gone != nil {
			t.Fatalf("vm still there: %+v %v", gone, err)
		}
		if err := d.Destroy(ctx, vm.Moref); err != nil {
			t.Fatalf("destroying a deleted VM should succeed: %v", err)
		}
	})
}

func findCluster(inv *Inventory, name string) *Cluster {
	for i := range inv.Clusters {
		if inv.Clusters[i].Name == name {
			return &inv.Clusters[i]
		}
	}
	return nil
}

func findDatastore(inv *Inventory, name string) *Datastore {
	for i := range inv.Datastores {
		if inv.Datastores[i].Name == name {
			return &inv.Datastores[i]
		}
	}
	return nil
}

func findNetworkInv(inv *Inventory, name string) *Network {
	for i := range inv.Networks {
		if inv.Networks[i].Name == name {
			return &inv.Networks[i]
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

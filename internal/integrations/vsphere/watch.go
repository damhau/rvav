package vsphere

import (
	"context"
	"slices"

	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/types"
)

// Change is something vCenter says changed: a VM (Gone when deleted), or
// the rest of the inventory rvav caches (clusters, hosts, datastores,
// networks, templates).
type Change struct {
	VM        string
	Gone      bool
	Inventory bool
}

// watched are the properties followed, by kind: what rvav shows of a VM
// (its quick stats are read with the full sync), and what the inventory
// cache is made of.
var watched = map[string][]string{
	"VirtualMachine": {"name", "parent", "runtime.powerState", "runtime.host", "guest.ipAddress", "guest.hostName",
		"guest.toolsRunningStatus", "guest.net", "config.guestFullName", "config.hardware.numCPU", "config.hardware.memoryMB",
		"config.hardware.device", "config.cpuHotAddEnabled", "config.memoryHotAddEnabled", "config.template", "snapshot"},
	"HostSystem":             {"name", "parent"},
	"ClusterComputeResource": {"name", "host"},
	"Datastore":              {"name", "host"},
	"Network":                {"name", "host"},
}

// keepAlive is how long one wait for updates lasts: vCenter answers with
// nothing after it, which keeps the connection in use.
const keepAlive = 60

// Watch follows the vCenter's changes with the property collector
// (WaitForUpdatesEx) until ctx is done, calling changed for each batch. The
// first updates, everything as it is, aren't passed on: once they are in,
// it says the inventory changed instead, since what changed before it
// followed may have been missed. It returns when the session breaks; the
// caller watches again.
func (cl *Client) Watch(ctx context.Context, changed func([]Change)) error {
	c, err := cl.client(ctx)
	if err != nil {
		return err
	}
	kinds := make([]string, 0, len(watched))
	for k := range watched {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	v, err := view.NewManager(c).CreateContainerView(ctx, c.ServiceContent.RootFolder, kinds, true)
	if err != nil {
		return err
	}
	defer func() { _ = v.Destroy(context.Background()) }()
	pc, err := property.DefaultCollector(c).Create(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = pc.Destroy(context.Background()) }()
	spec := types.PropertyFilterSpec{ObjectSet: []types.ObjectSpec{{Obj: v.Reference(), Skip: types.NewBool(true),
		SelectSet: []types.BaseSelectionSpec{&types.TraversalSpec{Type: "ContainerView", Path: "view"}}}}}
	for _, k := range kinds {
		spec.PropSet = append(spec.PropSet, types.PropertySpec{Type: k, PathSet: watched[k]})
	}
	if _, err := pc.CreateFilter(ctx, types.CreateFilter{Spec: spec, PartialUpdates: true}); err != nil {
		return err
	}
	req := types.WaitForUpdatesEx{This: pc.Reference(), Options: &types.WaitOptions{MaxWaitSeconds: types.NewInt32(keepAlive)}}
	baseline := true
	for {
		res, err := methods.WaitForUpdatesEx(ctx, c, &req)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		set := res.Returnval
		if set == nil {
			continue // nothing changed while waiting
		}
		req.Version = set.Version
		var changes []Change
		for _, fs := range set.FilterSet {
			for _, ou := range fs.ObjectSet {
				changes = append(changes, changesOf(ou)...)
			}
		}
		switch {
		case !baseline:
			if len(changes) > 0 {
				changed(changes)
			}
		// Everything as it is may come in several parts.
		case set.Truncated == nil || !*set.Truncated:
			baseline = false
			changed([]Change{{Inventory: true}})
		}
	}
}

func changesOf(ou types.ObjectUpdate) []Change {
	if ou.Obj.Type != "VirtualMachine" {
		return []Change{{Inventory: true}}
	}
	gone := ou.Kind == types.ObjectUpdateKindLeave
	out := []Change{{VM: ou.Obj.Value, Gone: gone}}
	if gone {
		out = append(out, Change{Inventory: true}) // it may have been a template
	}
	// A template made or turned back into a VM changes the templates.
	for _, pc := range ou.ChangeSet {
		if pc.Name == "config.template" {
			out = append(out, Change{Inventory: true})
		}
	}
	return out
}

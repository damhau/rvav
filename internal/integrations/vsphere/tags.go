package vsphere

import (
	"context"
	"fmt"
	"sort"

	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vapi/tags"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// readTags puts the vSphere tags of clusters and datastores, as
// category/tag, on the inventory: placement rules can choose by them
// (ADR-0020). It needs the tagging service, through vCenter's REST API
// with the same credentials.
func (cl *Client) readTags(ctx context.Context, c *vim25.Client, inv *Inventory) error {
	if cl.login == nil {
		return fmt.Errorf("no credentials for the tagging service")
	}
	rc := rest.NewClient(c)
	if err := rc.Login(ctx, cl.login); err != nil {
		return fmt.Errorf("sign in to the tagging service: %w", err)
	}
	defer func() { _ = rc.Logout(context.Background()) }()
	m := tags.NewManager(rc)

	var refs []mo.Reference
	for _, cc := range inv.Clusters {
		refs = append(refs, types.ManagedObjectReference{Type: "ClusterComputeResource", Value: cc.Moref})
	}
	for _, d := range inv.Datastores {
		refs = append(refs, types.ManagedObjectReference{Type: "Datastore", Value: d.Moref})
	}
	if len(refs) == 0 {
		return nil
	}
	attached, err := m.GetAttachedTagsOnObjects(ctx, refs)
	if err != nil {
		return err
	}
	categories := map[string]string{}
	byObject := map[string][]string{}
	for _, a := range attached {
		for _, t := range a.Tags {
			name, ok := categories[t.CategoryID]
			if !ok {
				if cat, err := m.GetCategory(ctx, t.CategoryID); err == nil {
					name = cat.Name
				}
				categories[t.CategoryID] = name
			}
			ref := a.ObjectID.Reference()
			byObject[ref.Type+":"+ref.Value] = append(byObject[ref.Type+":"+ref.Value], name+"/"+t.Name)
		}
	}
	for i := range inv.Clusters {
		inv.Clusters[i].Tags = sorted(byObject["ClusterComputeResource:"+inv.Clusters[i].Moref])
	}
	for i := range inv.Datastores {
		inv.Datastores[i].Tags = sorted(byObject["Datastore:"+inv.Datastores[i].Moref])
	}
	return nil
}

func sorted(list []string) []string {
	sort.Strings(list)
	return list
}

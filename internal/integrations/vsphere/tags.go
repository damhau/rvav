package vsphere

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/vmware/govmomi/vapi/tags"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// readTags puts the vSphere tags of clusters, datastores and networks, as
// category/tag, on the inventory: placement rules (ADR-0020) and
// blueprints' constraints (ADR-0035) choose by them. VMs' tags and the
// categories for VMs are read too, for editing VMs' tags (X1.3). It needs
// the tagging service, through vCenter's REST API with the same
// credentials.
func (cl *Client) readTags(ctx context.Context, c *vim25.Client, inv *Inventory) error {
	m, done, err := cl.tagManager(ctx, c)
	if err != nil {
		return err
	}
	defer done()

	var refs []mo.Reference
	for _, cc := range inv.Clusters {
		refs = append(refs, types.ManagedObjectReference{Type: "ClusterComputeResource", Value: cc.Moref})
	}
	for _, d := range inv.Datastores {
		refs = append(refs, types.ManagedObjectReference{Type: "Datastore", Value: d.Moref})
	}
	for _, n := range inv.Networks {
		refs = append(refs, types.ManagedObjectReference{Type: n.Kind, Value: n.Moref})
	}
	for _, h := range inv.Hosts {
		refs = append(refs, types.ManagedObjectReference{Type: "HostSystem", Value: h.Moref})
	}
	for _, p := range inv.ResourcePools {
		refs = append(refs, types.ManagedObjectReference{Type: "ResourcePool", Value: p.Moref})
	}
	for _, p := range inv.DatastoreClusters {
		refs = append(refs, types.ManagedObjectReference{Type: "StoragePod", Value: p.Moref})
	}
	for _, v := range inv.VMs {
		refs = append(refs, vmRef(v.Moref))
	}
	cats, err := m.GetCategories(ctx)
	if err != nil {
		return err
	}
	categories := map[string]string{}
	for _, c := range cats {
		categories[c.ID] = c.Name
	}
	if err := readTagCategories(ctx, m, cats, inv); err != nil {
		return err
	}
	byObject := map[string][]string{}
	if len(refs) > 0 {
		attached, err := m.GetAttachedTagsOnObjects(ctx, refs)
		if err != nil {
			return err
		}
		for _, a := range attached {
			for _, t := range a.Tags {
				ref := a.ObjectID.Reference()
				byObject[ref.Type+":"+ref.Value] = append(byObject[ref.Type+":"+ref.Value], categories[t.CategoryID]+"/"+t.Name)
			}
		}
	}
	for i := range inv.VMs {
		inv.VMs[i].VM.Tags = sorted(append([]string{}, byObject["VirtualMachine:"+inv.VMs[i].Moref]...))
	}
	for i := range inv.Clusters {
		inv.Clusters[i].Tags = sorted(byObject["ClusterComputeResource:"+inv.Clusters[i].Moref])
	}
	for i := range inv.Datastores {
		inv.Datastores[i].Tags = sorted(byObject["Datastore:"+inv.Datastores[i].Moref])
	}
	for i := range inv.Networks {
		inv.Networks[i].Tags = sorted(byObject[inv.Networks[i].Kind+":"+inv.Networks[i].Moref])
	}
	for i := range inv.Hosts {
		inv.Hosts[i].Tags = sorted(byObject["HostSystem:"+inv.Hosts[i].Moref])
	}
	for i := range inv.ResourcePools {
		inv.ResourcePools[i].Tags = sorted(byObject["ResourcePool:"+inv.ResourcePools[i].Moref])
	}
	for i := range inv.DatastoreClusters {
		inv.DatastoreClusters[i].Tags = sorted(byObject["StoragePod:"+inv.DatastoreClusters[i].Moref])
	}
	return nil
}

// readTagCategories puts the categories VMs can take, with their tags, on
// the inventory.
func readTagCategories(ctx context.Context, m *tags.Manager, cats []tags.Category, inv *Inventory) error {
	for _, c := range cats {
		if len(c.AssociableTypes) > 0 && !slices.Contains(c.AssociableTypes, "VirtualMachine") {
			continue
		}
		// Only VMs' categories: each tag is a call of its own.
		in, err := m.GetTagsForCategory(ctx, c.ID)
		if err != nil {
			return err
		}
		tc := TagCategory{Name: c.Name, Single: c.Cardinality == "SINGLE", Tags: []string{}}
		for _, t := range in {
			tc.Tags = append(tc.Tags, t.Name)
		}
		slices.Sort(tc.Tags)
		inv.TagCategories = append(inv.TagCategories, tc)
	}
	slices.SortFunc(inv.TagCategories, func(a, b TagCategory) int { return strings.Compare(a.Name, b.Name) })
	return nil
}

// SetTags gives a VM, within the named tag categories, exactly the tags
// wanted (category/tag): the others of those categories are detached, and
// tags of other categories are left. Run again, it changes nothing. A tag
// vCenter lacks is refused: day-2 edits choose among existing tags.
func (cl *Client) SetTags(ctx context.Context, moref string, categories, want []string) error {
	c, err := cl.client(ctx)
	if err != nil {
		return err
	}
	m, done, err := cl.tagManager(ctx, c)
	if err != nil {
		return err
	}
	defer done()
	cats, err := m.GetCategories(ctx)
	if err != nil {
		return err
	}
	attached, err := m.GetAttachedTags(ctx, vmRef(moref))
	if err != nil {
		return err
	}
	catName := map[string]string{}
	for _, c := range cats {
		catName[c.ID] = c.Name
	}
	has := map[string]bool{}
	for _, t := range attached {
		name := catName[t.CategoryID] + "/" + t.Name
		has[name] = true
		if slices.Contains(categories, catName[t.CategoryID]) && !slices.Contains(want, name) {
			if err := m.DetachTag(ctx, t.ID, vmRef(moref)); err != nil {
				return fmt.Errorf("detach tag %s: %w", name, err)
			}
		}
	}
	for _, t := range want {
		if has[t] {
			continue
		}
		cat, name, _ := strings.Cut(t, "/")
		i := slices.IndexFunc(cats, func(c tags.Category) bool { return c.Name == cat })
		if i < 0 {
			return &Refused{Msg: "Tag category " + cat + " is not in vCenter."}
		}
		tag, err := m.GetTagForCategory(ctx, name, cats[i].ID)
		if err != nil {
			return &Refused{Msg: "Tag " + t + " is not in vCenter."}
		}
		if err := m.AttachTag(ctx, tag.ID, vmRef(moref)); err != nil {
			return fmt.Errorf("attach tag %s: %w", t, err)
		}
	}
	return nil
}

// tagManager signs in to the tagging service; done signs out.
func (cl *Client) tagManager(ctx context.Context, c *vim25.Client) (*tags.Manager, func(), error) {
	rc, done, err := cl.restClient(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	return tags.NewManager(rc), done, nil
}

// AttachTags attaches vSphere tags, as category/tag, to a VM (ADR-0024).
// A tag missing from its category is created there; a missing category is
// ErrNotFound: categories are an administrator's choice.
func (cl *Client) AttachTags(ctx context.Context, moref string, list []string) error {
	if len(list) == 0 {
		return nil
	}
	c, err := cl.client(ctx)
	if err != nil {
		return err
	}
	m, done, err := cl.tagManager(ctx, c)
	if err != nil {
		return err
	}
	defer done()
	cats, err := m.GetCategories(ctx)
	if err != nil {
		return err
	}
	for _, t := range list {
		catName, tagName, _ := strings.Cut(t, "/")
		i := slices.IndexFunc(cats, func(c tags.Category) bool { return c.Name == catName })
		if i < 0 {
			return fmt.Errorf("tag category %q: %w", catName, ErrNotFound)
		}
		inCat, err := m.GetTagsForCategory(ctx, cats[i].ID)
		if err != nil {
			return err
		}
		id := ""
		if j := slices.IndexFunc(inCat, func(x tags.Tag) bool { return x.Name == tagName }); j >= 0 {
			id = inCat[j].ID
		} else if id, err = m.CreateTag(ctx, &tags.Tag{Name: tagName, CategoryID: cats[i].ID}); err != nil {
			return fmt.Errorf("create tag %s: %w", t, err)
		}
		if err := m.AttachTag(ctx, id, vmRef(moref)); err != nil {
			return fmt.Errorf("attach tag %s: %w", t, err)
		}
	}
	return nil
}

// VMTags returns a VM's tags, as category/tag.
func (cl *Client) VMTags(ctx context.Context, moref string) ([]string, error) {
	c, err := cl.client(ctx)
	if err != nil {
		return nil, err
	}
	m, done, err := cl.tagManager(ctx, c)
	if err != nil {
		return nil, err
	}
	defer done()
	attached, err := m.GetAttachedTags(ctx, vmRef(moref))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range attached {
		cat, err := m.GetCategory(ctx, t.CategoryID)
		if err != nil {
			return nil, err
		}
		out = append(out, cat.Name+"/"+t.Name)
	}
	return sorted(out), nil
}

func sorted(list []string) []string {
	sort.Strings(list)
	return list
}

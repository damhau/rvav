package vsphere

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

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

// tagManager signs in to the tagging service; done signs out.
func (cl *Client) tagManager(ctx context.Context, c *vim25.Client) (*tags.Manager, func(), error) {
	if cl.login == nil {
		return nil, nil, fmt.Errorf("no credentials for the tagging service")
	}
	rc := rest.NewClient(c)
	if err := rc.Login(ctx, cl.login); err != nil {
		return nil, nil, fmt.Errorf("sign in to the tagging service: %w", err)
	}
	return tags.NewManager(rc), func() { _ = rc.Logout(context.Background()) }, nil
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

package vsphere

import (
	"context"
	"fmt"

	"github.com/vmware/govmomi/pbm"
	pbmtypes "github.com/vmware/govmomi/pbm/types"
	"github.com/vmware/govmomi/vapi/library"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vapi/vcenter"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
)

// Content library templates and storage policies (ADR-0024).

// StoragePolicy is a storage policy (SPBM) of the vCenter.
type StoragePolicy struct {
	ID   string
	Name string
}

// restClient signs in to vCenter's REST API (content libraries, tags);
// done signs out.
func (cl *Client) restClient(ctx context.Context, c *vim25.Client) (*rest.Client, func(), error) {
	if cl.login == nil {
		return nil, nil, fmt.Errorf("no credentials for vCenter's REST API")
	}
	rc := rest.NewClient(c)
	if err := rc.Login(ctx, cl.login); err != nil {
		return nil, nil, fmt.Errorf("sign in to vCenter's REST API: %w", err)
	}
	return rc, func() { _ = rc.Logout(context.Background()) }, nil
}

// readLibraries adds the content libraries' VM template and OVF items to
// the inventory's templates, with their library.
func (cl *Client) readLibraries(ctx context.Context, c *vim25.Client, inv *Inventory) error {
	rc, done, err := cl.restClient(ctx, c)
	if err != nil {
		return err
	}
	defer done()
	m := library.NewManager(rc)
	libs, err := m.GetLibraries(ctx)
	if err != nil {
		return err
	}
	for _, l := range libs {
		items, err := m.GetLibraryItems(ctx, l.ID)
		if err != nil {
			return err
		}
		for _, it := range items {
			if it.Type == library.ItemTypeVMTX || it.Type == library.ItemTypeOVF {
				inv.Templates = append(inv.Templates, Template{Moref: it.ID, Name: it.Name, Library: l.Name})
			}
		}
	}
	return nil
}

// readStoragePolicies lists the vCenter's storage policies.
func readStoragePolicies(ctx context.Context, c *vim25.Client, inv *Inventory) error {
	pc, err := pbm.NewClient(ctx, c)
	if err != nil {
		return err
	}
	ids, err := pc.QueryProfile(ctx, pbmtypes.PbmProfileResourceType{ResourceType: string(pbmtypes.PbmProfileResourceTypeEnumSTORAGE)},
		string(pbmtypes.PbmProfileCategoryEnumREQUIREMENT))
	if err != nil {
		return err
	}
	profiles, err := pc.RetrieveContent(ctx, ids)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		base := p.GetPbmProfile()
		inv.StoragePolicies = append(inv.StoragePolicies, StoragePolicy{ID: base.ProfileId.UniqueId, Name: base.Name})
	}
	return nil
}

// policyID is a storage policy's ID ("" for none).
func policyID(ctx context.Context, c *vim25.Client, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	pc, err := pbm.NewClient(ctx, c)
	if err != nil {
		return "", err
	}
	id, err := pc.ProfileIDByName(ctx, name)
	if err != nil {
		return "", fmt.Errorf("storage policy %q: %w", name, ErrNotFound)
	}
	return id, nil
}

// profile is the profile spec of a storage policy (nil for none).
func profile(id string) []types.BaseVirtualMachineProfileSpec {
	if id == "" {
		return nil
	}
	return []types.BaseVirtualMachineProfileSpec{&types.VirtualMachineDefinedProfileSpec{ProfileId: id}}
}

// deployFromLibrary deploys a content library item, a VM template or an
// OVF, where a clone would go; the VM stays powered off.
func (cl *Client) deployFromLibrary(ctx context.Context, c *vim25.Client, spec VMSpec, pool, host, folder, ds types.ManagedObjectReference,
	policy string) (types.ManagedObjectReference, error) {
	var none types.ManagedObjectReference
	rc, done, err := cl.restClient(ctx, c)
	if err != nil {
		return none, err
	}
	defer done()
	lm := library.NewManager(rc)
	lib, err := lm.GetLibraryByName(ctx, spec.Library)
	if err != nil {
		return none, fmt.Errorf("content library %q: %w", spec.Library, ErrNotFound)
	}
	ids, err := lm.FindLibraryItems(ctx, library.FindItem{LibraryID: lib.ID, Name: spec.Template})
	if err != nil {
		return none, err
	}
	if len(ids) == 0 {
		return none, fmt.Errorf("item %q of content library %q: %w", spec.Template, spec.Library, ErrNotFound)
	}
	item, err := lm.GetLibraryItem(ctx, ids[0])
	if err != nil {
		return none, err
	}
	vm := vcenter.NewManager(rc)
	var ref *types.ManagedObjectReference
	switch item.Type {
	case library.ItemTypeVMTX:
		storage := &vcenter.DiskStorage{Datastore: ds.Value}
		if policy != "" {
			storage.StoragePolicy = &vcenter.StoragePolicy{Policy: policy, Type: "USE_SPECIFIED_POLICY"}
		}
		ref, err = vm.DeployTemplateLibraryItem(ctx, item.ID, vcenter.DeployTemplate{Name: spec.Name, Description: spec.Annotation,
			Placement:     &vcenter.Placement{ResourcePool: pool.Value, Host: host.Value, Folder: folder.Value},
			VMHomeStorage: storage, DiskStorage: storage})
	case library.ItemTypeOVF:
		ref, err = vm.DeployLibraryItem(ctx, item.ID, vcenter.Deploy{
			DeploymentSpec: vcenter.DeploymentSpec{Name: spec.Name, Annotation: spec.Annotation, AcceptAllEULA: true,
				DefaultDatastoreID: ds.Value, StorageProfileID: policy},
			Target: vcenter.Target{ResourcePoolID: pool.Value, HostID: host.Value, FolderID: folder.Value}})
	default:
		return none, fmt.Errorf("item %q of content library %q is a %s, not a VM template or an OVF: %w", spec.Template, spec.Library,
			item.Type, ErrNotFound)
	}
	if err != nil {
		return none, fmt.Errorf("deploy %s from content library %s: %w", spec.Name, spec.Library, err)
	}
	return *ref, nil
}

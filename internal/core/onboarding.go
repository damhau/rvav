package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vmprops"
)

// Onboarding (S13.3): administrators adopt VMs rvav didn't build into
// deployments, with an owner, a project, an environment, a catalog item and
// a lease, and the addresses and DNS records EfficientIP has for them.
// Nothing changes in vCenter or EfficientIP.

var deploymentNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// maxAdopt is how many VMs one adoption takes.
const maxAdopt = 200

// OnboardingVMs lists the inventory's VMs no deployment has.
func (s *Service) OnboardingVMs(ctx context.Context, p *auth.Principal, f store.UnmanagedFilter) (api.OnboardingVmPage, error) {
	out := api.OnboardingVmPage{Items: []api.OnboardingVm{}}
	if !p.Everywhere(auth.ImportManage) {
		return out, ErrForbidden
	}
	page, total, err := s.Store.UnmanagedVMs(ctx, f)
	if err != nil {
		return out, err
	}
	for _, v := range page.Items {
		out.Items = append(out.Items, presentUnmanaged(v))
	}
	out.Total = total
	if page.Next != nil {
		out.NextCursor = present.Ptr(page.Next.Encode())
	}
	return out, nil
}

func presentUnmanaged(v store.UnmanagedVM) api.OnboardingVm {
	set := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	return api.OnboardingVm{Vcenter: v.VCenter, Moref: v.Moref, InstanceUuid: set(v.InstanceUUID), Name: v.Name,
		PowerState: api.VmPowerState(v.PowerState), GuestOs: set(v.GuestOS), CpuCount: v.CPUCount, MemoryMb: v.MemoryMB,
		Cluster: set(v.Cluster), Host: set(v.Host), IpAddress: set(v.IPAddress), Hostname: set(v.Hostname), Folder: set(v.Folder),
		Tags: &v.Tags}
}

// adoptee is a VM being adopted: as vCenter has it now, and what
// EfficientIP has for it.
type adoptee struct {
	inv       store.UnmanagedVM
	at        string // where the draft names it
	vcenter   string
	vm        *vsphere.VM
	fqdn      string
	addresses []efficientip.Address
	records   []efficientip.Record
}

// adoptedDeployment is one deployment of an OnboardingResult.
type adoptedDeployment = struct {
	Addresses int      `json:"addresses"`
	Id        string   `json:"id"`
	Name      string   `json:"name"`
	Records   int      `json:"records"`
	Vms       []string `json:"vms"`
}

type vmRef = struct {
	Moref   string `json:"moref"`
	Vcenter string `json:"vcenter"`
}

// AdoptVMs adopts VMs into deployments: each its own, named after it, or
// all in one. All are adopted, or none.
func (s *Service) AdoptVMs(ctx context.Context, p *auth.Principal, d api.OnboardingDraft) (api.OnboardingResult, error) {
	out := api.OnboardingResult{Deployments: []adoptedDeployment{}}
	if !p.Everywhere(auth.ImportManage) {
		return out, ErrForbidden
	}
	var errs []api.FieldError
	add := func(at, code, msg string) { errs = append(errs, fieldErr(at, code, msg)) }
	t := newTarget{projectID: d.ProjectId, environment: d.Environment, ownerID: d.OwnerId, itemID: d.CatalogItemId,
		leaseDays: d.LeaseDays, deploymentName: deref(d.DeploymentName), component: deref(d.Component)}
	if d.Inputs != nil {
		t.inputs = *d.Inputs
	}
	dest, err := s.newDestination(ctx, p.User.ID, t, "", add)
	if err != nil {
		return out, err
	}
	vms, err := s.pickVMs(ctx, d.Vms, add)
	if err != nil || len(errs) > 0 {
		return out, cmp.Or(err, error(&ValidationError{Errors: errs}))
	}
	if err := s.lookUpAll(ctx, vms); err != nil {
		return out, err
	}
	one := dest.name != ""
	shareAddresses(vms, one, add)
	if len(errs) > 0 {
		return out, &ValidationError{Errors: errs}
	}

	// Record them: one deployment each, or one for all.
	groups := [][]*adoptee{}
	if one {
		groups = append(groups, nil)
		for i := range vms {
			groups[0] = append(groups[0], &vms[i])
		}
	} else {
		for i := range vms {
			groups = append(groups, []*adoptee{&vms[i]})
		}
	}
	inputs := make([]map[string]any, len(groups))
	for i, group := range groups {
		raised, m := fit(dest.fitting(nil, group))
		if m != nil {
			add(group[m.vm].at, m.code, m.msg)
		}
		inputs[i] = dest.inputs
		if raised != nil {
			inputs[i] = raised
		}
	}
	if len(errs) > 0 {
		return out, &ValidationError{Errors: errs}
	}
	var adopted []string
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		for i, group := range groups {
			name := group[0].inv.Name
			if one {
				name = dest.name
			}
			res, err := s.createDeployment(ctx, tx, dest, name, group, inputs[i], &p.User.ID, nil)
			if err != nil {
				return err
			}
			out.Deployments = append(out.Deployments, res)
			adopted = append(adopted, res.Id)
		}
		return nil
	})
	var taken *takenError
	if errors.As(err, &taken) {
		return api.OnboardingResult{}, &ValidationError{Errors: []api.FieldError{fieldErr(taken.at(d.Vms), "taken", taken.Error())}}
	}
	if err != nil {
		return api.OnboardingResult{}, err
	}
	for _, id := range adopted {
		s.Events.Deployment(ctx, id)
	}
	return out, nil
}

// AdoptVMsInto adopts VMs into a deployment rvav has, as VMs of one of its
// components. All are adopted, or none.
func (s *Service) AdoptVMsInto(ctx context.Context, p *auth.Principal, d api.OnboardingIntoDraft) (api.OnboardingResult, error) {
	out := api.OnboardingResult{Deployments: []adoptedDeployment{}}
	if !p.Everywhere(auth.ImportManage) {
		return out, ErrForbidden
	}
	var errs []api.FieldError
	add := func(at, code, msg string) { errs = append(errs, fieldErr(at, code, msg)) }
	dest, err := s.existingDestination(ctx, d.DeploymentId, "", deref(d.Component), "", add)
	if err != nil {
		return out, err
	}
	vms, err := s.pickVMs(ctx, d.Vms, add)
	if err != nil || len(errs) > 0 {
		return out, cmp.Or(err, error(&ValidationError{Errors: errs}))
	}
	if err := s.lookUpAll(ctx, vms); err != nil {
		return out, err
	}
	shareAddresses(vms, true, add)
	group := make([]*adoptee, len(vms))
	for i := range vms {
		group[i] = &vms[i]
	}
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		res, err := s.joinDeployment(ctx, tx, dest, group, &p.User.ID, nil)
		out.Deployments = append(out.Deployments, res)
		return err
	})
	var taken *takenError
	var m *misfit
	switch {
	case errors.As(err, &taken):
		return api.OnboardingResult{}, &ValidationError{Errors: []api.FieldError{fieldErr(taken.at(d.Vms), "taken", taken.Error())}}
	case errors.As(err, &m) && m.code == "busy":
		return api.OnboardingResult{}, &ConflictError{Msg: m.msg}
	case errors.As(err, &m):
		return api.OnboardingResult{}, &ValidationError{Errors: []api.FieldError{fieldErr(group[m.vm].at, m.code, m.msg)}}
	case err != nil:
		return api.OnboardingResult{}, err
	}
	s.Events.Deployment(ctx, dest.existing.ID)
	return out, nil
}

// pickVMs finds the VMs a draft names in the inventory, rvav managing
// none of them.
func (s *Service) pickVMs(ctx context.Context, refs []vmRef, add addFunc) ([]adoptee, error) {
	if len(refs) == 0 || len(refs) > maxAdopt {
		add("/vms", "out_of_range", fmt.Sprintf("Adopt from 1 to %d VMs at once.", maxAdopt))
		return nil, nil
	}
	vms := make([]adoptee, len(refs))
	seen := map[string]bool{}
	for i, ref := range refs {
		at := fmt.Sprintf("/vms/%d", i)
		vms[i].at = at
		if seen[ref.Vcenter+"/"+ref.Moref] {
			add(at, "duplicate", ref.Moref+" is in the list twice.")
			continue
		}
		seen[ref.Vcenter+"/"+ref.Moref] = true
		v, err := s.Store.GetUnmanagedVM(ctx, ref.Vcenter, ref.Moref)
		switch {
		case errors.Is(err, store.ErrNotFound):
			add(at, "not_found", fmt.Sprintf("%s has no VM %s that rvav doesn't manage: a deployment has it, or the inventory hasn't read it yet.",
				ref.Vcenter, ref.Moref))
		case err != nil:
			return nil, err
		}
		vms[i].inv = v
	}
	return vms, nil
}

// lookUpAll reads each VM in vCenter and looks it up in EfficientIP, a few
// at a time.
func (s *Service) lookUpAll(ctx context.Context, vms []adoptee) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for i := range vms {
		g.Go(func() error { return s.lookUp(gctx, &vms[i]) })
	}
	err := g.Wait()
	var gone *goneError
	if errors.As(err, &gone) {
		return &ValidationError{Errors: []api.FieldError{fieldErr(gone.at, "not_found", gone.Error())}}
	}
	return err
}

// shareAddresses keeps an address or record two VMs share (a virtual
// address) on the first: one deployment holds it once; two can't both.
func shareAddresses(vms []adoptee, one bool, add addFunc) {
	owner := map[string]int{}
	for i := range vms {
		a := &vms[i]
		shared := func(key, what string) bool {
			j, seen := owner[key]
			if !seen {
				owner[key] = i
				return false
			}
			if !one {
				add(a.at, "shared", fmt.Sprintf("%s is %s's too: adopt them into one deployment.", what, vms[j].inv.Name))
			}
			return true
		}
		a.addresses = slices.DeleteFunc(a.addresses, func(ad efficientip.Address) bool {
			return shared("ip:"+cmp.Or(ad.ID, ad.IP), "Its address "+ad.IP)
		})
		a.records = slices.DeleteFunc(a.records, func(r efficientip.Record) bool {
			return shared("rr:"+cmp.Or(r.ID, r.Type+":"+r.FQDN), "Its DNS record "+r.Type+" "+r.FQDN)
		})
	}
}

// fitting is what fit checks of VMs joining the destination, which has
// the VMs have.
func (d *destination) fitting(have []seat, vms []*adoptee) fitting {
	f := fitting{bp: d.bp, comp: d.comp, label: d.label, schema: d.version.FormSchema, inputs: d.inputs, have: have}
	for _, a := range vms {
		f.vms = append(f.vms, seat{name: a.vm.Name, component: d.compKey, guestOS: cmp.Or(a.vm.GuestOS, a.inv.GuestOS)})
	}
	return f
}

// createDeployment records VMs as a new deployment, with the inputs they
// fit with. detail goes into the audit.
func (s *Service) createDeployment(ctx context.Context, tx pgx.Tx, dest *destination, name string, group []*adoptee,
	inputs map[string]any, actor *string, detail map[string]any) (adoptedDeployment, error) {
	expires, extensions := dest.expires()
	if inputs == nil {
		inputs = map[string]any{}
	}
	raw, _ := json.Marshal(inputs)
	dep := store.Deployment{ID: ids.New(ids.Deployment), Name: name, Status: "ready", ProjectID: dest.project.ID,
		Environment: dest.env, ItemID: dest.itemID, VersionID: dest.version.ID, OwnerID: dest.ownerID,
		LeaseExpiresAt: expires, LeaseExtensionsMax: extensions, ExpiryAction: orDefault(dest.lease.ExpiryAction, "notify_only"),
		Inputs: raw}
	if err := s.Store.InsertDeployment(ctx, tx, dep); err != nil {
		return adoptedDeployment{}, err
	}
	res, err := s.recordGroup(ctx, tx, dep.ID, dest.compKey, group)
	if err != nil {
		return res, err
	}
	res.Name = dep.Name
	audit := map[string]any{"name": dep.Name, "vms": res.Vms, "addresses": res.Addresses, "records": res.Records,
		"catalog_item": dest.item}
	if dest.compKey != "" {
		audit["component"] = dest.compKey
	}
	maps.Copy(audit, detail)
	return res, s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: actor, Action: "deployment.adopted", TargetType: "deployment",
		TargetID: dep.ID, ProjectID: &dest.project.ID, Detail: audit})
}

// joinDeployment records VMs into the deployment dest has, when it takes
// them: ready, no request running on it, its project not archived, and
// they fit. Why not is a *misfit.
func (s *Service) joinDeployment(ctx context.Context, tx pgx.Tx, dest *destination, group []*adoptee, actor *string,
	detail map[string]any) (adoptedDeployment, error) {
	id := dest.existing.ID
	if err := s.Store.LockDeployment(ctx, tx, id); err != nil {
		return adoptedDeployment{}, err
	}
	dep, err := s.Store.GetDeploymentIn(ctx, tx, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && dep.Status == "deleted" {
		return adoptedDeployment{}, &misfit{code: "busy", msg: "The deployment " + dest.name + " was deleted."}
	}
	if err != nil {
		return adoptedDeployment{}, err
	}
	active, err := s.Store.ActiveDeploymentRequest(ctx, tx, id, "", "", false)
	switch {
	case err != nil:
		return adoptedDeployment{}, err
	case active != "":
		return adoptedDeployment{}, &misfit{code: "busy", msg: active + " is still running on " + dep.Name + ": adopt the VMs once it ends."}
	case dep.Status != "ready":
		return adoptedDeployment{}, &misfit{code: "busy", msg: dep.Name + " is " + dep.Status + ": VMs join a ready deployment."}
	case dest.project.ArchivedAt != nil:
		return adoptedDeployment{}, &misfit{code: "no_fit", msg: dest.project.Name + " is archived: it takes nothing new."}
	case dep.VersionID != dest.version.ID:
		return adoptedDeployment{}, &misfit{code: "busy", msg: dep.Name + " was updated meanwhile: try again."}
	}
	seats, err := s.Store.DeploymentSeats(ctx, tx, id)
	if err != nil {
		return adoptedDeployment{}, err
	}
	have := make([]seat, len(seats))
	for i, h := range seats {
		have[i] = seat{name: h.Name, component: h.Component}
	}
	f := dest.fitting(have, group)
	_ = json.Unmarshal(dep.Inputs, &f.inputs)
	raised, m := fit(f)
	if m != nil {
		return adoptedDeployment{}, m
	}
	res, err := s.recordGroup(ctx, tx, id, dest.compKey, group)
	if err != nil {
		return res, err
	}
	res.Name = dep.Name
	if raised != nil {
		b, _ := json.Marshal(raised)
		if err := s.Store.SetDeploymentVersion(ctx, tx, id, dep.VersionID, b); err != nil {
			return res, err
		}
	}
	audit := map[string]any{"name": dep.Name, "vms": res.Vms, "addresses": res.Addresses, "records": res.Records}
	if dest.compKey != "" {
		audit["component"] = dest.compKey
	}
	if raised != nil {
		audit["inputs"] = raised
	}
	maps.Copy(audit, detail)
	return res, s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: actor, Action: "deployment.vm_adopted", TargetType: "deployment",
		TargetID: id, ProjectID: &dep.ProjectID, Detail: audit})
}

// recordGroup records VMs as resources of a deployment.
func (s *Service) recordGroup(ctx context.Context, tx pgx.Tx, depID, component string, group []*adoptee) (adoptedDeployment, error) {
	res := adoptedDeployment{Id: depID, Vms: []string{}}
	for _, a := range group {
		if err := s.recordAdoptee(ctx, tx, depID, component, a); err != nil {
			return res, err
		}
		res.Vms = append(res.Vms, a.vm.Name)
		res.Addresses += len(a.addresses)
		res.Records += len(a.records)
	}
	return res, nil
}

// adoptionItem is the published VM item adopted VMs' deployments are of,
// offered in the project; or why it can't be.
func (s *Service) adoptionItem(ctx context.Context, userID, id string, project store.Project) (store.CatalogItem, store.ItemVersion, string) {
	const choose = "Choose a published VM item: it gives the deployments their OS family and day-2 actions."
	item, err := s.Store.GetItem(ctx, id, userID)
	if err != nil || item.PublishedVersionID == nil {
		return item, store.ItemVersion{}, choose
	}
	version, err := s.Store.GetVersion(ctx, *item.PublishedVersionID)
	if err != nil {
		return item, version, choose
	}
	if b, err := blueprint.Parse(version.Blueprint); err != nil || b.Kind != blueprint.KindVM {
		return item, version, choose
	}
	if project.ID != "" {
		if ok, err := s.Store.IsEntitled(ctx, item.ID, project.ID); err == nil && !ok {
			return item, version, item.Name + " isn't offered in " + project.Name + "."
		}
	}
	return item, version, ""
}

// lookUp reads a VM in vCenter, then what EfficientIP has for its
// addresses and names.
func (s *Service) lookUp(ctx context.Context, a *adoptee) error {
	if s.VSphere == nil {
		return &UpstreamError{System: "vCenter", Err: errors.New("no vCenter is set up")}
	}
	vc, err := s.VSphere(ctx, a.inv.VCenter)
	if err != nil {
		return &UpstreamError{System: "vCenter " + a.inv.VCenter, Err: err}
	}
	a.vcenter = vc.Name()
	if a.vm, err = vc.GetVM(ctx, a.inv.Moref); err != nil {
		if vsphere.IsNotFound(err) {
			return &goneError{name: a.inv.Name, at: a.at}
		}
		return &UpstreamError{System: "vCenter " + a.inv.VCenter, Err: err}
	}
	var ips, names []string
	for _, n := range a.vm.NICs {
		ips = append(ips, n.IPs...)
	}
	if a.vm.IP != "" && !slices.Contains(ips, a.vm.IP) {
		ips = append(ips, a.vm.IP)
	}
	// IPv4 only: EfficientIP's space for rvav is IPv4.
	ips = slices.DeleteFunc(ips, func(ip string) bool { return strings.Contains(ip, ":") })
	if strings.Contains(a.vm.Hostname, ".") {
		names = append(names, a.vm.Hostname)
	}
	if s.IPAM != nil && len(ips) > 0 {
		if a.addresses, a.records, err = s.IPAM.Find(ctx, ips, names); err != nil {
			return &UpstreamError{System: "EfficientIP", Err: err}
		}
	}
	// Its name: its A record's, else its PTR's, else EfficientIP's or the
	// guest's own when it is a full name.
	for _, r := range a.records {
		if r.Type == "A" {
			a.fqdn = r.FQDN
			break
		}
	}
	for _, r := range a.records {
		if a.fqdn == "" && r.Type == "PTR" {
			a.fqdn = strings.TrimSuffix(r.Value, ".")
		}
	}
	for _, ad := range a.addresses {
		if a.fqdn == "" && strings.Contains(ad.Hostname, ".") {
			a.fqdn = ad.Hostname
		}
	}
	if a.fqdn == "" && len(names) > 0 {
		a.fqdn = names[0]
	}
	return nil
}

// goneError is a VM vCenter no longer has.
type goneError struct{ name, at string }

func (e *goneError) Error() string { return e.name + " is no longer in vCenter." }

// takenError is a VM, or an address or record, another deployment already
// has.
type takenError struct {
	vcenter, moref, what string
	vm                   bool
}

func (e *takenError) Error() string {
	return e.what + " belongs to another deployment."
}

func (e *takenError) at(vms []vmRef) string {
	for i, v := range vms {
		if v.Vcenter == e.vcenter && v.Moref == e.moref {
			return fmt.Sprintf("/vms/%d", i)
		}
	}
	return "/vms"
}

// recordAdoptee records a VM and what EfficientIP has for it as the
// deployment's resources, as provisioning would have.
func (s *Service) recordAdoptee(ctx context.Context, tx pgx.Tx, depID, component string, a *adoptee) error {
	props := vmprops.Build(a.vcenter, *a.vm, vmprops.Ours{FQDN: a.fqdn, Component: component}, nil, 0)
	props, _ = vmprops.Settle(nil, props, vmprops.All...)
	ext := cmp.Or(a.vm.InstanceUUID, a.vm.Moref)
	if _, err := s.Store.UpsertResource(ctx, tx, store.Resource{ID: ids.New(ids.Resource), DeploymentID: depID, Type: "vsphere.vm",
		Name: a.vm.Name, ExternalID: &ext, Properties: props}); err != nil {
		if errors.Is(err, store.ErrTaken) {
			return &takenError{vcenter: a.inv.VCenter, moref: a.inv.Moref, what: a.vm.Name, vm: true}
		}
		return err
	}
	for _, ad := range a.addresses {
		ext := ad.ExternalID()
		props, _ := json.Marshal(map[string]any{"address": ad.IP, "subnet": ad.Subnet, "efficientip_id": ad.ID,
			"hostname": a.vm.Name, "fqdn": a.fqdn})
		if _, err := s.Store.UpsertResource(ctx, tx, store.Resource{ID: ids.New(ids.Resource), DeploymentID: depID, Type: "ipam.address",
			Name: ad.IP, ExternalID: &ext, Properties: props}); err != nil {
			if errors.Is(err, store.ErrTaken) {
				return &takenError{vcenter: a.inv.VCenter, moref: a.inv.Moref, what: "Its address " + ad.IP}
			}
			return err
		}
	}
	for _, r := range a.records {
		ext := cmp.Or(r.ID, r.Type+":"+r.FQDN)
		props, _ := json.Marshal(map[string]any{"fqdn": r.FQDN, "record_type": r.Type, "value": r.Value, "zone": r.Zone,
			"efficientip_id": r.ID})
		if _, err := s.Store.UpsertResource(ctx, tx, store.Resource{ID: ids.New(ids.Resource), DeploymentID: depID, Type: "dns.record",
			Name: r.Type + " " + r.FQDN, ExternalID: &ext, Properties: props}); err != nil {
			if errors.Is(err, store.ErrTaken) {
				return &takenError{vcenter: a.inv.VCenter, moref: a.inv.Moref, what: "Its DNS record " + r.Type + " " + r.FQDN}
			}
			return err
		}
	}
	return nil
}

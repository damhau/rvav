package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

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
	if !p.IsAdmin() {
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
		Cluster: set(v.Cluster), Host: set(v.Host), IpAddress: set(v.IPAddress), Hostname: set(v.Hostname)}
}

// adoptee is a VM being adopted: as vCenter has it now, and what
// EfficientIP has for it.
type adoptee struct {
	inv       store.UnmanagedVM
	vcenter   string
	vm        *vsphere.VM
	fqdn      string
	addresses []efficientip.Address
	records   []efficientip.Record
}

// AdoptVMs adopts VMs into deployments: each its own, named after it, or
// all in one. All are adopted, or none.
func (s *Service) AdoptVMs(ctx context.Context, p *auth.Principal, d api.OnboardingDraft) (api.OnboardingResult, error) {
	out := api.OnboardingResult{Deployments: []struct {
		Addresses int      `json:"addresses"`
		Id        string   `json:"id"`
		Name      string   `json:"name"`
		Records   int      `json:"records"`
		Vms       []string `json:"vms"`
	}{}}
	if !p.IsAdmin() {
		return out, ErrForbidden
	}
	var errs []api.FieldError
	add := func(at, code, msg string) { errs = append(errs, fieldErr(at, code, msg)) }

	project, err := s.Store.GetProject(ctx, d.ProjectId)
	switch {
	case errors.Is(err, store.ErrNotFound):
		add("/project_id", "not_found", "No such project.")
	case err != nil:
		return out, err
	case project.ArchivedAt != nil:
		add("/project_id", "archived", project.Name+" is archived: it takes nothing new.")
	}
	var lease store.LeasePolicy
	if project.ID != "" {
		if !slices.Contains(project.Environments, d.Environment) {
			add("/environment", "invalid", fmt.Sprintf("%s has no environment %s.", project.Name, d.Environment))
		} else if lease, err = s.Store.LeasePolicy(ctx, project.ID, d.Environment); err != nil && !errors.Is(err, store.ErrNotFound) {
			return out, err
		}
		members, err := s.Store.ProjectMemberRoles(ctx, project.ID)
		if err != nil {
			return out, err
		}
		if !slices.ContainsFunc(members, func(m store.MemberRole) bool { return m.ID == d.OwnerId && m.Role != "viewer" }) {
			add("/owner_id", "invalid", "Choose a member of "+project.Name+" (not a viewer): owners run the actions on their deployments.")
		}
	}
	item, version, msg := s.adoptionItem(ctx, p, d.CatalogItemId, project)
	if msg != "" {
		add("/catalog_item_id", "invalid", msg)
	}
	days := lease.DefaultDays
	if d.LeaseDays != nil {
		days = d.LeaseDays
		switch {
		case *days < 1:
			add("/lease_days", "too_short", "A lease is at least one day.")
		case lease.MaxDays != nil && *days > *lease.MaxDays:
			add("/lease_days", "too_long", fmt.Sprintf("Leases in %s can be at most %d days.", d.Environment, *lease.MaxDays))
		}
	}
	one := d.DeploymentName != nil && *d.DeploymentName != ""
	if one && !deploymentNamePattern.MatchString(*d.DeploymentName) {
		add("/deployment_name", "pattern", "Lower-case letters, digits, dots and dashes, up to 63, for example web-prd-erp.")
	}

	if len(d.Vms) == 0 || len(d.Vms) > maxAdopt {
		add("/vms", "out_of_range", fmt.Sprintf("Adopt from 1 to %d VMs at once.", maxAdopt))
		return out, &ValidationError{Errors: errs}
	}
	vms := make([]adoptee, len(d.Vms))
	seen := map[string]bool{}
	for i, ref := range d.Vms {
		at := fmt.Sprintf("/vms/%d", i)
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
			return out, err
		}
		vms[i].inv = v
	}
	if len(errs) > 0 {
		return out, &ValidationError{Errors: errs}
	}

	// Read each VM in vCenter and look it up in EfficientIP, a few at a time.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for i := range vms {
		g.Go(func() error { return s.lookUp(gctx, &vms[i]) })
	}
	if err := g.Wait(); err != nil {
		return out, err
	}
	// An address or record two VMs share (a virtual address): one
	// deployment can hold it once; two can't both.
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
				add(fmt.Sprintf("/vms/%d", i), "shared", fmt.Sprintf("%s is %s's too: adopt them into one deployment.", what, vms[j].inv.Name))
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
	var expires *time.Time
	if days != nil {
		t := time.Now().UTC().Add(time.Duration(*days) * 24 * time.Hour).Truncate(time.Second)
		expires = &t
	}
	var adopted []store.Deployment
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		for _, group := range groups {
			name := group[0].inv.Name
			if one {
				name = *d.DeploymentName
			}
			dep := store.Deployment{ID: ids.New(ids.Deployment), Name: name, Status: "ready", ProjectID: project.ID,
				Environment: d.Environment, ItemID: item.ID, VersionID: version.ID, OwnerID: d.OwnerId,
				LeaseExpiresAt: expires, ExpiryAction: orDefault(lease.ExpiryAction, "notify_only")}
			if expires != nil {
				dep.LeaseExtensionsMax = &lease.MaxExtensions
			}
			if err := s.Store.InsertDeployment(ctx, tx, dep); err != nil {
				return err
			}
			result := struct {
				Addresses int      `json:"addresses"`
				Id        string   `json:"id"`
				Name      string   `json:"name"`
				Records   int      `json:"records"`
				Vms       []string `json:"vms"`
			}{Id: dep.ID, Name: dep.Name, Vms: []string{}}
			for _, a := range group {
				if err := s.recordAdoptee(ctx, tx, dep.ID, a); err != nil {
					return err
				}
				result.Vms = append(result.Vms, a.vm.Name)
				result.Addresses += len(a.addresses)
				result.Records += len(a.records)
			}
			if err := s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "deployment.adopted", TargetType: "deployment",
				TargetID: dep.ID, ProjectID: &project.ID, Detail: map[string]any{"name": dep.Name, "vms": result.Vms,
					"addresses": result.Addresses, "records": result.Records, "catalog_item": item.Slug}}); err != nil {
				return err
			}
			out.Deployments = append(out.Deployments, result)
			adopted = append(adopted, dep)
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
	for _, dep := range adopted {
		s.Events.Deployment(ctx, dep.ID)
	}
	return out, nil
}

// adoptionItem is the published VM item adopted VMs' deployments are of,
// offered in the project; or why it can't be.
func (s *Service) adoptionItem(ctx context.Context, p *auth.Principal, id string, project store.Project) (store.CatalogItem, store.ItemVersion, string) {
	const choose = "Choose a published VM item: it gives the deployments their OS family and day-2 actions."
	item, err := s.Store.GetItem(ctx, id, p.User.ID)
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
			return &ValidationError{Errors: []api.FieldError{fieldErr("/vms", "not_found", a.inv.Name+" is no longer in vCenter.")}}
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

// takenError is an address or record another deployment already has.
type takenError struct {
	vcenter, moref, what string
}

func (e *takenError) Error() string {
	return e.what + " belongs to another deployment."
}

func (e *takenError) at(vms []struct {
	Moref   string `json:"moref"`
	Vcenter string `json:"vcenter"`
}) string {
	for i, v := range vms {
		if v.Vcenter == e.vcenter && v.Moref == e.moref {
			return fmt.Sprintf("/vms/%d", i)
		}
	}
	return "/vms"
}

// recordAdoptee records a VM and what EfficientIP has for it as the
// deployment's resources, as provisioning would have.
func (s *Service) recordAdoptee(ctx context.Context, tx pgx.Tx, depID string, a *adoptee) error {
	props := vmprops.Build(a.vcenter, *a.vm, vmprops.Ours{FQDN: a.fqdn}, nil, 0)
	props, _ = vmprops.Settle(nil, props, vmprops.All...)
	ext := cmp.Or(a.vm.InstanceUUID, a.vm.Moref)
	if _, err := s.Store.UpsertResource(ctx, tx, store.Resource{ID: ids.New(ids.Resource), DeploymentID: depID, Type: "vsphere.vm",
		Name: a.vm.Name, ExternalID: &ext, Properties: props}); err != nil {
		if errors.Is(err, store.ErrTaken) {
			return &takenError{vcenter: a.inv.VCenter, moref: a.inv.Moref, what: a.vm.Name}
		}
		return err
	}
	for _, ad := range a.addresses {
		ext := cmp.Or(ad.ID, "ip:"+ad.IP)
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

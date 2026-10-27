package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/store"
)

// memberChoices are who may own the deployment: its project's members.
func (s *Service) memberChoices(ctx context.Context, d store.DeploymentRow) ([]api.Option, error) {
	users, err := s.Store.ProjectMembers(ctx, d.ProjectID)
	if err != nil {
		return nil, err
	}
	out := []api.Option{}
	for _, u := range users {
		email := u.Email
		out = append(out, api.Option{Value: u.ID, Label: u.Name, Meta: &email})
	}
	return out, nil
}

// projectChoices are the projects the principal may move the deployment
// to: where they can request, with its environment. Those whose placement
// rules or hard quota don't allow its VMs are disabled, with the reason.
func (s *Service) projectChoices(ctx context.Context, p *auth.Principal, d store.DeploymentRow, t day2.Target) ([]api.Option, error) {
	var only []string
	if !p.IsAdmin() {
		only = []string{}
		for id := range p.ProjectRoles {
			if p.CanRequest(id) {
				only = append(only, id)
			}
		}
		slices.Sort(only)
	}
	page, err := s.Store.ListProjects(ctx, only, 500, nil)
	if err != nil {
		return nil, err
	}
	out := []api.Option{}
	for _, proj := range page.Items {
		if proj.ID == d.ProjectID || !slices.Contains(proj.Environments, d.Environment) {
			continue
		}
		code := proj.Code
		opt := api.Option{Value: proj.ID, Label: proj.Name, Meta: &code}
		reason, err := s.cannotMove(ctx, proj, d, t)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			yes := true
			opt.Disabled, opt.DisabledReason = &yes, &reason
		}
		out = append(out, opt)
	}
	return out, nil
}

// cannotMove says why the deployment's VMs can't move to the project ("" when they can).
func (s *Service) cannotMove(ctx context.Context, proj store.Project, d store.DeploymentRow, t day2.Target) (string, error) {
	pl, err := s.placement(ctx, proj.ID, d.Environment)
	if err != nil {
		return "", err
	}
	add := map[string]int{}
	for _, r := range t.Resources {
		if r.Type != "vsphere.vm" {
			continue
		}
		vm := r.VM
		cluster := deref(vm.Cluster)
		rule := pl.rule(cluster)
		if rule == nil {
			return fmt.Sprintf("%s does not place VMs on %s.", proj.Name, cluster), nil
		}
		for _, disk := range vm.Disks {
			if ds := deref(disk.Datastore); ds != "" && !matchAny(rule.DatastorePatterns, ds) {
				return fmt.Sprintf("%s does not use datastore %s.", proj.Name, ds), nil
			}
		}
		for _, n := range vm.Nics {
			if !matchAny(rule.NetworkPatterns, n.Network) {
				return fmt.Sprintf("%s does not use network %s.", proj.Name, n.Network), nil
			}
		}
		add["vcpu"] += vm.CpuCount
		add["memory_gb"] += vm.MemoryMb / 1024
		add["vm_count"]++
		for _, disk := range vm.Disks {
			add["storage_gb"] += disk.CapacityGb
		}
	}
	return s.hardQuotaExceeded(ctx, proj, add)
}

// hardQuotaExceeded says which hard quota of the project adding would
// exceed ("" when none).
func (s *Service) hardQuotaExceeded(ctx context.Context, proj store.Project, add map[string]int) (string, error) {
	limits, err := s.Store.QuotaLimits(ctx, proj.ID, "")
	if err != nil {
		return "", err
	}
	used, err := s.Store.QuotaUsed(ctx, proj.ID, "")
	if err != nil {
		return "", err
	}
	reserved, err := s.Store.QuotaReserved(ctx, proj.ID, "")
	if err != nil {
		return "", err
	}
	for _, l := range limits {
		if l.Limit == nil || l.Enforcement != "hard" || add[l.Resource] == 0 {
			continue
		}
		if have := used[l.Resource] + reserved[l.Resource]; have+add[l.Resource] > *l.Limit {
			return fmt.Sprintf("It needs %d %s; %s has %d of %d left.", add[l.Resource], quotaLabel(l.Resource),
				proj.Name, max(*l.Limit-have, 0), *l.Limit), nil
		}
	}
	return "", nil
}

// checkAction runs the checks of an action that need vCenter's inventory
// or DNS.
func (s *Service) checkAction(ctx context.Context, a *day2.Action, t day2.Target, inputs map[string]any) ([]api.FieldError, error) {
	if a.Key != "rename" {
		return nil, nil
	}
	name, _ := inputs["name"].(string)
	taken, err := s.Store.InventoryVMNames(ctx, name)
	if err != nil {
		return nil, err
	}
	if taken[name] {
		return []api.FieldError{fieldErr("/inputs/name", "taken", "A VM named "+name+" already exists in vCenter.")}, nil
	}
	if fqdn := renamedFQDN(t, name); fqdn != "" && s.IPAM != nil {
		exists, err := s.IPAM.NameExists(ctx, fqdn)
		if err != nil {
			return nil, &UpstreamError{System: "EfficientIP", Err: err}
		}
		if exists {
			return []api.FieldError{fieldErr("/inputs/name", "taken", fqdn+" is already in DNS.")}, nil
		}
	}
	return nil, nil
}

func renamedFQDN(t day2.Target, name string) string {
	old := deref(t.VM.Fqdn)
	for i := range old {
		if old[i] == '.' {
			return name + old[i:]
		}
	}
	return ""
}

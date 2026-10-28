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
		if proj.ID == d.ProjectID || proj.ArchivedAt != nil || !slices.Contains(proj.Environments, d.Environment) {
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
	}
	add := deploymentUsage(t)
	env, err := s.Store.GetEnvironment(ctx, d.Environment)
	if err != nil {
		return "", err
	}
	q, err := s.quotaCheck(ctx, proj, d.Environment, env.Name, "It", add)
	if err != nil || len(q.Hard) == 0 {
		return "", err
	}
	return q.Hard[0], nil
}

// deploymentUsage is what the deployment's VMs count against a quota.
func deploymentUsage(t day2.Target) map[string]int {
	add := map[string]int{}
	for _, r := range t.Resources {
		if r.Type != "vsphere.vm" {
			continue
		}
		add["vcpu"] += r.VM.CpuCount
		add["memory_gb"] += r.VM.MemoryMb / 1024
		add["vm_count"]++
		for _, disk := range r.VM.Disks {
			add["storage_gb"] += disk.CapacityGb
		}
	}
	return add
}

// day2Add is what the action adds to its deployment's project quota.
func day2Add(a *day2.Action, t day2.Target, inputs map[string]any) map[string]int {
	num := func(k string) int { v, _ := inputs[k].(float64); return int(v) }
	switch a.Key {
	case "resize":
		return map[string]int{"vcpu": max(num("cpu_count")-t.VM.CpuCount, 0), "memory_gb": max(num("memory_gb")-t.VM.MemoryMb/1024, 0)}
	case "disk_add":
		return map[string]int{"storage_gb": num("size_gb")}
	case "disk_extend":
		for _, disk := range t.VM.Disks {
			if disk.Label == inputs["disk"] {
				return map[string]int{"storage_gb": max(num("size_gb")-disk.CapacityGb, 0)}
			}
		}
	}
	return nil
}

// day2Quota checks what the action adds to a quota: resizing and disks add
// to the deployment's project, moving adds the deployment to the new one. A
// hard limit passed refuses it; it returns the resources over a soft one.
func (s *Service) day2Quota(ctx context.Context, a *day2.Action, d store.DeploymentRow, t day2.Target, inputs map[string]any) ([]string, error) {
	var proj store.Project
	var add map[string]int
	what := "The change"
	switch a.Key {
	case "disk_add", "disk_extend":
		add = day2Add(a, t, inputs)
		var err error
		if proj, err = s.Store.GetProject(ctx, d.ProjectID); err != nil {
			return nil, err
		}
		what = "The disk"
	case "resize":
		cpu, _ := inputs["cpu_count"].(float64)
		mem, _ := inputs["memory_gb"].(float64)
		add = map[string]int{"vcpu": max(int(cpu)-t.VM.CpuCount, 0), "memory_gb": max(int(mem)-t.VM.MemoryMb/1024, 0)}
		var err error
		if proj, err = s.Store.GetProject(ctx, d.ProjectID); err != nil {
			return nil, err
		}
		what = "The new size"
	case "change_project":
		id, _ := inputs["project"].(string)
		var err error
		if proj, err = s.Store.GetProject(ctx, id); err != nil {
			return nil, err
		}
		add = deploymentUsage(t)
		what = "Moving it"
	default:
		return nil, nil
	}
	env, err := s.Store.GetEnvironment(ctx, d.Environment)
	if err != nil {
		return nil, err
	}
	q, err := s.quotaCheck(ctx, proj, d.Environment, env.Name, what, add)
	if err != nil {
		return nil, err
	}
	if len(q.Hard) > 0 {
		field := map[string]string{"resize": "/inputs/cpu_count", "change_project": "/inputs/project"}[a.Key]
		if field == "" {
			field = "/inputs/size_gb"
		}
		errs := make([]api.FieldError, len(q.Hard))
		for i, m := range q.Hard {
			errs[i] = fieldErr(field, "quota_exceeded", m)
		}
		return nil, &ValidationError{Errors: errs}
	}
	return q.OverSoft, nil
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

package core

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// tagCategories puts on a VM target the tag categories its users may
// change: those its environment allows that its vCenter has (X1.3).
func (s *Service) tagCategories(ctx context.Context, d store.DeploymentRow, t *day2.Target) error {
	if t.Type != "vsphere.vm" {
		return nil
	}
	env, err := s.Store.GetEnvironment(ctx, d.Environment)
	if err != nil || len(env.TagCategories) == 0 {
		return err
	}
	cats, err := s.Store.InventoryTagCategoriesOf(ctx, t.VM.Vcenter)
	if err != nil {
		return err
	}
	for _, c := range cats {
		if slices.Contains(env.TagCategories, c.Name) {
			t.TagCategories = append(t.TagCategories, day2.TagCategory{Name: c.Name, Single: c.Single, Tags: c.Tags})
		}
	}
	return nil
}

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
	page, err := s.Store.ListProjects(ctx, p.Projects(auth.RequestSubmit), 500, nil)
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
		reason, err := s.cannotMove(ctx, p, proj, d, t)
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
func (s *Service) cannotMove(ctx context.Context, p *auth.Principal, proj store.Project, d store.DeploymentRow, t day2.Target) (string, error) {
	taken, err := s.Store.TakenDeploymentNames(ctx, s.Store.Pool, proj.ID, d.ID, []string{d.Name})
	if err != nil {
		return "", err
	}
	if len(taken) > 0 {
		return takenMsg(proj.Name, taken[0]) + " Rename this one first (Edit deployment).", nil
	}
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
			if ds := deref(disk.Datastore); ds != "" && !matchAny(rule.DatastorePatterns, ds) &&
				!slices.ContainsFunc(pl.datastoresFor(cluster), func(d store.InvDatastore) bool { return d.Name == ds }) {
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
	q, err := s.quotaCheck(ctx, s.Store.Pool, ownerScope(p, proj, env, d, movedLevels), "It", add)
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

// movedLevels are the quotas moving a deployment to another project
// checks: what it holds across rvav doesn't change. handedLevels are
// those handing it over checks: the new owner's (ADR-0047).
var (
	movedLevels  = []api.QuotaLevel{api.QuotaLevelProject, api.QuotaLevelEnvironment, api.QuotaLevelProjectUser}
	handedLevels = []api.QuotaLevel{api.QuotaLevelProjectUser, api.QuotaLevelGlobalUser}
)

// ownerScope is where a deployment counts in proj, for its owner.
func ownerScope(p *auth.Principal, proj store.Project, env store.Environment, d store.DeploymentRow, levels []api.QuotaLevel) quotaScope {
	return quotaScope{Project: proj, Env: env.Key, EnvName: env.Name, OwnerID: d.OwnerID, OwnerName: d.OwnerName,
		You: d.OwnerID == p.User.ID, Levels: levels}
}

// day2Change is what a day-2 action counts against the quotas and the
// deployment limits (ADR-0047): resizing and disks add to the deployment's
// project, moving adds the deployment to the new one, and handing it over
// to its new owner. The limits check a resize against the size of each
// deployment, a move against the new project's limits, and a handover
// against the new owner's count. It is nil when the action counts nowhere.
func (s *Service) day2Change(ctx context.Context, p *auth.Principal, a *day2.Action, d store.DeploymentRow, t day2.Target,
	inputs map[string]any) (*quotaChange, error) {
	projectID := d.ProjectID
	now := deploymentUsage(t)
	ch := &quotaChange{what: "The change", quotaField: "/inputs/size_gb"}
	ask := &limitAsk{VCPU: now["vcpu"], MemoryGB: now["memory_gb"]}
	var levels []api.QuotaLevel
	switch a.Key {
	case "disk_add", "disk_extend":
		ch.add, ch.what, ask = day2Add(a, t, inputs), "The disk", nil
	case "resize":
		ch.add, ch.what, ch.quotaField = day2Add(a, t, inputs), "The new size", "/inputs/cpu_count"
		if cpu, ok := inputs["cpu_count"].(float64); ok {
			ask.AddVCPU = int(cpu) - t.VM.CpuCount
		}
		if mem, ok := inputs["memory_gb"].(float64); ok {
			ask.AddMemory = int(mem) - t.VM.MemoryMb/1024
		}
		ask.VCPU, ask.MemoryGB = ask.VCPU+ask.AddVCPU, ask.MemoryGB+ask.AddMemory
		ch.limitField = func(k api.DeploymentLimitKind) string {
			if k == api.MemoryGbPerDeployment {
				return "/inputs/memory_gb"
			}
			return "/inputs/cpu_count"
		}
	case "change_project":
		projectID, _ = inputs["project"].(string)
		ch.add, levels, ch.what, ch.quotaField = now, movedLevels, "Moving it", "/inputs/project"
		ask.ToOwner, ask.ToProject, ask.AddVCPU, ask.AddMemory = 1, 1, ask.VCPU, ask.MemoryGB
		ch.limitField = func(api.DeploymentLimitKind) string { return "/inputs/project" }
	case "change_owner":
		id, _ := inputs["owner"].(string)
		owner, err := s.Store.GetUser(ctx, id)
		if err != nil {
			return nil, err
		}
		d.OwnerID, d.OwnerName = owner.ID, owner.Name
		ch.add, levels, ch.what, ch.quotaField = now, handedLevels, "Handing it over", "/inputs/owner"
		ask.ToOwner, ask.VCPU, ask.MemoryGB = 1, 0, 0
		ch.limitField = func(api.DeploymentLimitKind) string { return "/inputs/owner" }
	default:
		return nil, nil
	}
	proj, err := s.Store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	env, err := s.Store.GetEnvironment(ctx, d.Environment)
	if err != nil {
		return nil, err
	}
	ch.scope = ownerScope(p, proj, env, d, levels)
	if ask != nil {
		ask.quotaScope = ownerScope(p, proj, env, d, nil)
	}
	ch.limits = ask
	return ch, nil
}

// day2Reserve is what a day-2 request holds while it waits and runs, and
// where: a move holds its deployment in the new project and a handover
// with the new owner, until it is done.
func day2Reserve(a *day2.Action, t day2.Target, inputs map[string]any) (map[string]int, *provision.QuotaTo) {
	switch a.Key {
	case "change_project":
		id, _ := inputs["project"].(string)
		return deploymentUsage(t), &provision.QuotaTo{ProjectID: id}
	case "change_owner":
		id, _ := inputs["owner"].(string)
		return deploymentUsage(t), &provision.QuotaTo{OwnerID: id}
	}
	return day2Add(a, t, inputs), nil
}

// checkAction runs the checks of an action that need vCenter's inventory,
// DNS or the project's deployments.
func (s *Service) checkAction(ctx context.Context, a *day2.Action, d store.DeploymentRow, t day2.Target, inputs map[string]any) ([]api.FieldError, error) {
	switch a.Key {
	case "edit":
		return s.checkEdit(ctx, d, t, inputs)
	case "rename":
	default:
		return nil, nil
	}
	name, _ := inputs["name"].(string)
	taken, err := s.Store.InventoryVMNames(ctx, s.Store.Pool, name)
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

// diskChoices are where a disk added to the VM may go: the datastores its
// cluster's placement rule allows, under that rule's storage policy, else
// one of its vCenter's. A VM no rule places is offered none: its disks go
// on its datastore.
func (s *Service) diskChoices(ctx context.Context, pl *placement, t day2.Target, c *day2.Choices) error {
	r := pl.rule(*t.VM.Cluster)
	if r == nil {
		return nil
	}
	datastores := pl.datastoresFor(*t.VM.Cluster)
	slices.SortFunc(datastores, func(a, b store.InvDatastore) int { return cmp.Compare(a.Name, b.Name) })
	for _, d := range datastores {
		meta := fmt.Sprintf("%d GB free", d.FreeGB)
		c.Datastores = append(c.Datastores, api.Option{Value: d.Name, Label: d.Name, Meta: &meta})
	}
	if c.RulePolicy = r.StoragePolicy; c.RulePolicy != "" {
		c.StoragePolicies = []string{r.StoragePolicy}
		return nil
	}
	policies, err := s.Store.InventoryStoragePolicies(ctx)
	if err != nil {
		return err
	}
	for _, p := range policies {
		if p.VCenter == t.VM.Vcenter && !slices.Contains(c.StoragePolicies, p.Name) {
			c.StoragePolicies = append(c.StoragePolicies, p.Name)
		}
	}
	slices.Sort(c.StoragePolicies)
	return nil
}

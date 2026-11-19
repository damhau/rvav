package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// limitKinds are what a deployment limit policy caps, in the order shown.
var limitKinds = []api.DeploymentLimitKind{api.DeploymentsPerUser, api.DeploymentsPerProject, api.VcpuPerDeployment,
	api.MemoryGbPerDeployment}

var limitNames = map[api.DeploymentLimitKind]string{
	api.DeploymentsPerUser: "deployments per user", api.DeploymentsPerProject: "deployments per project",
	api.VcpuPerDeployment: "vCPU per deployment", api.MemoryGbPerDeployment: "memory per deployment",
}

func limitOf(pol store.DeploymentLimitPolicy, k api.DeploymentLimitKind) *int {
	return map[api.DeploymentLimitKind]*int{api.DeploymentsPerUser: pol.DeploymentsPerUser,
		api.DeploymentsPerProject: pol.DeploymentsPerProject, api.VcpuPerDeployment: pol.VCPUPerDeployment,
		api.MemoryGbPerDeployment: pol.MemoryGBPerDeployment}[k]
}

func presentLimitPolicy(pol store.DeploymentLimitPolicy) api.DeploymentLimitPolicy {
	return api.DeploymentLimitPolicy{Id: pol.ID, Name: pol.Name, ProjectId: pol.ProjectID, Environment: pol.Environment,
		Priority: pol.Priority, Enabled: pol.Enabled, Enforcement: api.PolicyEnforcement(pol.Enforcement),
		DeploymentsPerUser: pol.DeploymentsPerUser, DeploymentsPerProject: pol.DeploymentsPerProject,
		VcpuPerDeployment: pol.VCPUPerDeployment, MemoryGbPerDeployment: pol.MemoryGBPerDeployment, SetByAdmin: pol.SetByAdmin}
}

func (s *Service) DeploymentLimitPolicies(ctx context.Context, p *auth.Principal) ([]api.DeploymentLimitPolicy, error) {
	if !readsPolicies(p) {
		return nil, ErrForbidden
	}
	list, err := s.Store.DeploymentLimitPolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := []api.DeploymentLimitPolicy{}
	for _, pol := range list {
		if showsPolicy(p, pol.ProjectID) {
			out = append(out, presentLimitPolicy(pol))
		}
	}
	return out, nil
}

// SaveDeploymentLimitPolicy adds (id "") or changes a policy, under the
// rules of ADR-0045: a project admin writes their project's, never a
// global one nor one an administrator set.
func (s *Service) SaveDeploymentLimitPolicy(ctx context.Context, p *auth.Principal, id string,
	w api.DeploymentLimitPolicyWrite) (api.DeploymentLimitPolicy, error) {
	project, errs, err := s.policyProject(ctx, p, w.ProjectId)
	if err != nil {
		return api.DeploymentLimitPolicy{}, err
	}
	if id != "" {
		if old, err := s.Store.GetDeploymentLimitPolicy(ctx, id); errors.Is(err, store.ErrNotFound) || err == nil && !showsPolicy(p, old.ProjectID) {
			return api.DeploymentLimitPolicy{}, ErrNotFound
		} else if err != nil {
			return api.DeploymentLimitPolicy{}, err
		} else if !changesPolicy(p, old.ProjectID, old.SetByAdmin) {
			return api.DeploymentLimitPolicy{}, ErrForbidden
		}
	}
	if strings.TrimSpace(w.Name) == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the policy a name."))
	}
	pol := store.DeploymentLimitPolicy{ID: id, Name: strings.TrimSpace(w.Name), ProjectID: project, Priority: 100,
		Enabled: w.Enabled == nil || *w.Enabled, SetByAdmin: p.Everywhere(auth.PoliciesManage),
		DeploymentsPerUser: w.DeploymentsPerUser, DeploymentsPerProject: w.DeploymentsPerProject,
		VCPUPerDeployment: w.VcpuPerDeployment, MemoryGBPerDeployment: w.MemoryGbPerDeployment}
	if w.Priority != nil {
		pol.Priority = *w.Priority
	}
	if w.Environment != nil && *w.Environment != "" {
		if _, err := s.Store.GetEnvironment(ctx, *w.Environment); errors.Is(err, store.ErrNotFound) {
			errs = append(errs, fieldErr("/environment", "unknown", fmt.Sprintf("%q is not an environment.", *w.Environment)))
		} else if err != nil {
			return api.DeploymentLimitPolicy{}, err
		}
		pol.Environment = w.Environment
	}
	set := false
	for _, k := range limitKinds {
		v := limitOf(pol, k)
		if v == nil {
			continue
		}
		set = true
		if least := map[bool]int{true: 0, false: 1}[k == api.DeploymentsPerUser || k == api.DeploymentsPerProject]; *v < least {
			errs = append(errs, fieldErr("/"+string(k), "minimum", fmt.Sprintf("%d or more.", least)))
		}
	}
	if !set {
		errs = append(errs, fieldErr("", "required", "Set at least one limit: deployments per user or per project, or vCPU or memory per deployment."))
	}
	enf, enfErrs := enforcement(w.Enforcement)
	pol.Enforcement = enf
	if errs = append(errs, enfErrs...); len(errs) > 0 {
		return api.DeploymentLimitPolicy{}, &ValidationError{Errors: errs}
	}
	action := "policy.limit.update"
	if id == "" {
		pol.ID, action = ids.New("dlp"), "policy.limit.create"
	}
	if err := s.Store.SaveDeploymentLimitPolicy(ctx, pol); err != nil {
		return api.DeploymentLimitPolicy{}, err
	}
	s.audit(ctx, p, action, "deployment_limit_policy", pol.ID, project, w)
	return presentLimitPolicy(pol), nil
}

func (s *Service) DeleteDeploymentLimitPolicy(ctx context.Context, p *auth.Principal, id string) error {
	if !p.Everywhere(auth.PoliciesManage) && !p.Anywhere(auth.ProjectPolicies) {
		return ErrForbidden
	}
	old, err := s.Store.GetDeploymentLimitPolicy(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound) || err == nil && !showsPolicy(p, old.ProjectID):
		return ErrNotFound
	case err != nil:
		return err
	case !changesPolicy(p, old.ProjectID, old.SetByAdmin):
		return ErrForbidden
	}
	if err := s.Store.DeleteDeploymentLimitPolicy(ctx, id); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.audit(ctx, p, "policy.limit.delete", "deployment_limit_policy", id, old.ProjectID, nil)
	return nil
}

// limitChoice is the policy that decides a limit, and the limit.
type limitChoice struct {
	pol   store.DeploymentLimitPolicy
	limit int
}

// limitsHere are the deployment limits for a project and environment, each
// decided on its own (ADR-0047): the policies are tried by priority, the
// global and the project's together, and the first enabled one that sets
// the limit decides. When it is a project's, the first global one that
// sets it after still decides if it is hard and stricter, as day-2
// policies combine (ADR-0045). why says, per policy that applies, what it
// decides and what it gives way on; decides, whether it decides any.
func limitsHere(all []store.DeploymentLimitPolicy, projectID, env string) (chosen map[api.DeploymentLimitKind]limitChoice,
	why map[string]string, decides map[string]bool) {
	chosen, why, decides = map[api.DeploymentLimitKind]limitChoice{}, map[string]string{}, map[string]bool{}
	gave := map[string][]string{}
	for _, k := range limitKinds {
		var first *limitChoice
		for _, pol := range all {
			v := limitOf(pol, k)
			if v == nil || !limitApplies(pol, projectID, env) {
				continue
			}
			if first == nil {
				first = &limitChoice{pol: pol, limit: *v}
				if pol.ProjectID == nil {
					break
				}
				continue
			}
			if pol.ProjectID != nil {
				continue
			}
			stricter := *v < first.limit || *v == first.limit && first.pol.Enforcement == store.EnforcementSoft
			if pol.Enforcement != store.EnforcementSoft && stricter {
				gave[first.pol.ID] = append(gave[first.pol.ID], fmt.Sprintf("Its %s gives way to the global %s, hard and stricter.",
					limitNames[k], pol.Name))
				first = &limitChoice{pol: pol, limit: *v}
			}
			break
		}
		if first != nil {
			chosen[k] = *first
		}
	}
	names := map[string][]string{}
	for _, k := range limitKinds {
		if c, ok := chosen[k]; ok {
			names[c.pol.ID] = append(names[c.pol.ID], limitNames[k])
		}
	}
	for _, pol := range all {
		var parts []string
		if len(names[pol.ID]) > 0 {
			decides[pol.ID] = true
			parts = append(parts, "Decides "+joinNames(names[pol.ID])+".")
		}
		if parts = append(parts, gave[pol.ID]...); len(parts) > 0 {
			why[pol.ID] = strings.Join(parts, " ")
		}
	}
	return chosen, why, decides
}

func limitApplies(pol store.DeploymentLimitPolicy, projectID, env string) bool {
	return pol.Enabled && (pol.ProjectID == nil || *pol.ProjectID == projectID) && (pol.Environment == nil || *pol.Environment == env)
}

// limitAsk is a change the deployment limits check: the deployments it
// adds to its owner's and the project's count, and the size of its largest
// deployment after it, with how much the change grows it.
type limitAsk struct {
	quotaScope
	ToOwner, ToProject int
	VCPU, MemoryGB     int
	AddVCPU, AddMemory int
	// Many: the change makes several deployments; the size is the largest's.
	Many bool
}

// limitCheck is what a change does to the deployment limits that apply.
type limitCheck struct {
	Lines []api.DeploymentLimitLine
	// Hard are the hard limits it goes over, by kind, in words.
	Hard map[api.DeploymentLimitKind]string
	// Soft are the soft limits' warnings (ADR-0045).
	Soft []api.PolicyWarning
}

func (s *Service) checkLimits(ctx context.Context, ask limitAsk) (limitCheck, error) {
	out := limitCheck{Hard: map[api.DeploymentLimitKind]string{}}
	all, err := s.Store.DeploymentLimitPolicies(ctx)
	if err != nil || len(all) == 0 {
		return out, err
	}
	chosen, _, _ := limitsHere(all, ask.Project.ID, ask.Env)
	if len(chosen) == 0 {
		return out, nil
	}
	counts, err := s.Store.DeploymentCounts(ctx, ask.Project.ID, ask.OwnerID)
	if err != nil {
		return out, err
	}
	for _, k := range limitKinds {
		c, ok := chosen[k]
		if !ok {
			continue
		}
		owned, held := countIn(counts, c.pol.Environment)
		var value, add int
		switch k {
		case api.DeploymentsPerUser:
			value, add = owned+ask.ToOwner, ask.ToOwner
		case api.DeploymentsPerProject:
			value, add = held+ask.ToProject, ask.ToProject
		case api.VcpuPerDeployment:
			value, add = ask.VCPU, ask.AddVCPU
		case api.MemoryGbPerDeployment:
			value, add = ask.MemoryGB, ask.AddMemory
		}
		count := k == api.DeploymentsPerUser || k == api.DeploymentsPerProject
		if count && add == 0 || !count && value == 0 {
			continue
		}
		line := api.DeploymentLimitLine{Kind: k, Limit: c.limit, Value: value, Exceeds: add > 0 && value > c.limit,
			Enforcement: api.PolicyEnforcement(c.pol.Enforcement), Policy: c.pol.Name, Environment: c.pol.Environment}
		out.Lines = append(out.Lines, line)
		if !line.Exceeds {
			continue
		}
		soft := c.pol.Enforcement == store.EnforcementSoft
		msg := ask.limitMessage(k, c, value, soft)
		if soft {
			out.Soft = append(out.Soft, present.Warning(c.pol.Name, msg))
		} else {
			out.Hard[k] = msg
		}
	}
	return out, nil
}

// countIn sums the counts in env, or in every environment when env is nil.
func countIn(counts []store.DeploymentCount, env *string) (owned, all int) {
	for _, c := range counts {
		if env == nil || *env == c.Environment {
			owned, all = owned+c.Owned, all+c.All
		}
	}
	return owned, all
}

// limitMessage says which limit a change goes over, and, for a hard one,
// what to do.
func (ask limitAsk) limitMessage(k api.DeploymentLimitKind, c limitChoice, value int, soft bool) string {
	where := ask.Project.Name
	if c.pol.Environment != nil {
		where += " (" + ask.EnvName + ")"
	}
	may := map[bool]string{false: "may", true: "should"}[soft]
	whose := "you"
	if !ask.You {
		whose = ask.OwnerName
	}
	which := "this one"
	if ask.Many {
		which = "the largest"
	}
	var msg, then string
	switch k {
	case api.DeploymentsPerUser:
		msg = fmt.Sprintf("Each user %s have at most %s in %s under %s, and %s would have %d.", may, deployments(c.limit), where, c.pol.Name, whose, value)
		then = "Delete one"
	case api.DeploymentsPerProject:
		msg = fmt.Sprintf("%s %s have at most %s under %s, and it would have %d.", where, may, deployments(c.limit), c.pol.Name, value)
		then = "Delete one"
	case api.VcpuPerDeployment:
		msg = fmt.Sprintf("Deployments in %s %s have at most %d vCPU under %s, and %s would have %d.", where, may, c.limit, c.pol.Name, which, value)
		then = "Choose a smaller size"
	case api.MemoryGbPerDeployment:
		msg = fmt.Sprintf("Deployments in %s %s have at most %d GB of memory under %s, and %s would have %d.", where, may, c.limit, c.pol.Name, which, value)
		then = "Choose a smaller size"
	}
	if soft {
		return msg
	}
	whom := "a project admin"
	if c.pol.ProjectID == nil || c.pol.SetByAdmin {
		whom = "an administrator"
	}
	return msg + " " + then + ", or ask " + whom + " to raise the limit."
}

func deployments(n int) string {
	if n == 1 {
		return "1 deployment"
	}
	return fmt.Sprintf("%d deployments", n)
}

// largestDeployment is the vCPU and memory of the largest deployment the
// evaluation makes: a multi-VM item's VMs make one deployment, a single-VM
// item's each VM its own, and a composite's VM steps theirs.
func (ev *Evaluation) largestDeployment() (vcpu, memoryGB int) {
	switch ev.Blueprint.Kind {
	case blueprint.KindVM:
		if ev.Blueprint.Multi() {
			for _, p := range ev.Parts {
				vcpu, memoryGB = vcpu+p.vm.VCPU*p.count, memoryGB+p.vm.MemoryGB*p.count
			}
			return vcpu, memoryGB
		}
		for _, p := range ev.Parts {
			vcpu, memoryGB = max(vcpu, p.vm.VCPU), max(memoryGB, p.vm.MemoryGB)
		}
	case blueprint.KindComposite:
		for _, part := range ev.Composite {
			if part.sub != nil {
				c, m := part.sub.largestDeployment()
				vcpu, memoryGB = max(vcpu, c), max(memoryGB, m)
			}
		}
	}
	return vcpu, memoryGB
}

// evaluateLimits checks the deployments a new request makes against the
// deployment limits (ADR-0047): a hard limit passed is an error, a soft
// one a warning the requester accepts.
func (s *Service) evaluateLimits(ctx context.Context, ev *Evaluation, p *auth.Principal) error {
	n := ev.deploymentCount()
	if n == 0 {
		return nil
	}
	vcpu, mem := ev.largestDeployment()
	c, err := s.checkLimits(ctx, limitAsk{quotaScope: ev.ownerScope(p), ToOwner: n, ToProject: n, VCPU: vcpu, MemoryGB: mem,
		AddVCPU: vcpu, AddMemory: mem, Many: n > 1})
	if err != nil {
		return err
	}
	ev.Limits = c.Lines
	ev.Errors = append(ev.Errors, c.errors(anywhere)...)
	ev.Warnings = append(ev.Warnings, c.Soft...)
	return nil
}

// anywhere places a limit's error on the request as a whole.
func anywhere(api.DeploymentLimitKind) string { return "" }

func (c limitCheck) errors(field func(api.DeploymentLimitKind) string) []api.FieldError {
	var errs []api.FieldError
	for _, k := range limitKinds {
		if msg, ok := c.Hard[k]; ok {
			errs = append(errs, fieldErr(field(k), "limit_exceeded", msg))
		}
	}
	return errs
}

// updateLimits checks what an update leaves its deployment with against
// the vCPU and memory limits of each deployment: only an update that grows
// it can go over one (ADR-0047). It returns the soft limits' warnings.
func (s *Service) updateLimits(ctx context.Context, p *auth.Principal, ev *Evaluation, d store.DeploymentRow,
	u *updatePlan) ([]api.PolicyWarning, error) {
	now := deploymentUsage(u.target)
	c, err := s.checkLimits(ctx, limitAsk{quotaScope: ownerScope(p, ev.Project, ev.Env, d, nil),
		VCPU: now["vcpu"] + u.delta["vcpu"], MemoryGB: now["memory_gb"] + u.delta["memory_gb"],
		AddVCPU: u.delta["vcpu"], AddMemory: u.delta["memory_gb"]})
	if err != nil {
		return nil, err
	}
	ev.Limits = c.Lines
	ev.Errors = append(ev.Errors, c.errors(anywhere)...)
	return c.Soft, nil
}

// editLimits checks an approver's edits against the vCPU and memory
// limits of each deployment as they are now: the request's deployments
// are counted already, and its requester accepted any soft limit.
func (s *Service) editLimits(ctx context.Context, requester *auth.Principal, ev *Evaluation) error {
	vcpu, mem := ev.largestDeployment()
	if vcpu == 0 && mem == 0 {
		return nil
	}
	c, err := s.checkLimits(ctx, limitAsk{quotaScope: ev.ownerScope(requester), VCPU: vcpu, MemoryGB: mem, AddVCPU: vcpu,
		AddMemory: mem, Many: ev.deploymentCount() > 1})
	if err != nil {
		return err
	}
	ev.Errors = append(ev.Errors, c.errors(anywhere)...)
	return nil
}

// day2Limits checks a day-2 action against the deployment limits: a
// resize against the size of each deployment, a move against the new
// project's limits, and handing it over against its new owner's count. A
// hard limit passed refuses it; it returns the soft limits' warnings.
func (s *Service) day2Limits(ctx context.Context, p *auth.Principal, a *day2.Action, d store.DeploymentRow, t day2.Target,
	inputs map[string]any) ([]api.PolicyWarning, error) {
	now := deploymentUsage(t)
	ask := limitAsk{VCPU: now["vcpu"], MemoryGB: now["memory_gb"]}
	projectID := d.ProjectID
	var field func(api.DeploymentLimitKind) string
	switch a.Key {
	case "resize":
		if cpu, ok := inputs["cpu_count"].(float64); ok {
			ask.AddVCPU = int(cpu) - t.VM.CpuCount
		}
		if mem, ok := inputs["memory_gb"].(float64); ok {
			ask.AddMemory = int(mem) - t.VM.MemoryMb/1024
		}
		ask.VCPU, ask.MemoryGB = ask.VCPU+ask.AddVCPU, ask.MemoryGB+ask.AddMemory
		field = func(k api.DeploymentLimitKind) string {
			if k == api.MemoryGbPerDeployment {
				return "/inputs/memory_gb"
			}
			return "/inputs/cpu_count"
		}
	case "change_project":
		projectID, _ = inputs["project"].(string)
		ask.ToOwner, ask.ToProject, ask.AddVCPU, ask.AddMemory = 1, 1, ask.VCPU, ask.MemoryGB
		field = func(api.DeploymentLimitKind) string { return "/inputs/project" }
	case "change_owner":
		id, _ := inputs["owner"].(string)
		owner, err := s.Store.GetUser(ctx, id)
		if err != nil {
			return nil, err
		}
		d.OwnerID, d.OwnerName = owner.ID, owner.Name
		ask.ToOwner, ask.VCPU, ask.MemoryGB = 1, 0, 0
		field = func(api.DeploymentLimitKind) string { return "/inputs/owner" }
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
	ask.quotaScope = ownerScope(p, proj, env, d, nil)
	c, err := s.checkLimits(ctx, ask)
	if err != nil {
		return nil, err
	}
	if errs := c.errors(field); len(errs) > 0 {
		return nil, &ValidationError{Errors: errs}
	}
	return c.Soft, nil
}

// explainLimits says which policy decides each deployment limit in a
// project and environment, counting what as owns there.
func (s *Service) explainLimits(ctx context.Context, as *auth.Principal, proj store.Project, env store.Environment,
	full bool) (api.PolicyLimits, error) {
	out := api.PolicyLimits{Lines: []api.PolicyLimitLine{}, Considered: []api.PolicyConsidered{}}
	all, err := s.Store.DeploymentLimitPolicies(ctx)
	if err != nil || len(all) == 0 {
		return out, err
	}
	chosen, why, decides := limitsHere(all, proj.ID, env.Key)
	counts, err := s.Store.DeploymentCounts(ctx, proj.ID, as.User.ID)
	if err != nil {
		return out, err
	}
	for _, k := range limitKinds {
		c, ok := chosen[k]
		if !ok {
			continue
		}
		line := api.PolicyLimitLine{Kind: k, Limit: c.limit, Enforcement: api.PolicyEnforcement(c.pol.Enforcement), Policy: c.pol.Name,
			Environment: c.pol.Environment}
		owned, held := countIn(counts, c.pol.Environment)
		switch k {
		case api.DeploymentsPerUser:
			line.Used = &owned
		case api.DeploymentsPerProject:
			line.Used = &held
		}
		out.Lines = append(out.Lines, line)
	}
	for _, pol := range all {
		if !full && pol.ProjectID != nil && *pol.ProjectID != proj.ID {
			continue
		}
		c := api.PolicyConsidered{Id: pol.ID, Name: pol.Name}
		switch {
		case !pol.Enabled:
			c.Why = "Disabled."
		case pol.ProjectID != nil && *pol.ProjectID != proj.ID:
			c.Why = "Only for another project."
		case pol.Environment != nil && *pol.Environment != env.Key:
			c.Why = "Only for " + *pol.Environment + "."
		case decides[pol.ID]:
			c.Applies, c.Why = true, why[pol.ID]
		case why[pol.ID] != "":
			c.Why = why[pol.ID]
		default:
			c.Why = "A policy tried before it decides each of its limits."
		}
		out.Considered = append(out.Considered, c)
	}
	return out, nil
}

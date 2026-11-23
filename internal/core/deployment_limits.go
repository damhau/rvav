package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
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
	list, err := s.Store.DeploymentLimitPolicies(ctx, s.Store.Pool)
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
	} else if e := policyNameLength(w.Name); e != nil {
		errs = append(errs, *e)
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
	s.auditGone(ctx, p, "policy.limit.delete", "deployment_limit_policy", id, old.Name, old.ProjectID, nil)
	return nil
}

// limitChoice is a policy that holds a limit, and the limit.
type limitChoice struct {
	pol   store.DeploymentLimitPolicy
	limit int
}

// limitsHere are the deployment limits for a project and environment, each
// held on its own (ADR-0047): the policies are tried by priority, the
// global and the project's together, and the first enabled one that sets
// the limit decides. Every hard global policy that sets it holds too, each
// counted in its own environment or all, so a project's policy tightens a
// hard global limit and never loosens it; a soft global one gives way to a
// project's tried first. The deciding policy comes first.
func limitsHere(all []store.DeploymentLimitPolicy, projectID, env string) map[api.DeploymentLimitKind][]limitChoice {
	held := map[api.DeploymentLimitKind][]limitChoice{}
	for _, k := range limitKinds {
		for _, pol := range all {
			v := limitOf(pol, k)
			if v == nil || !limitApplies(pol, projectID, env) {
				continue
			}
			if len(held[k]) == 0 || pol.ProjectID == nil && pol.Enforcement != store.EnforcementSoft {
				held[k] = append(held[k], limitChoice{pol: pol, limit: *v})
			}
		}
	}
	return held
}

// tightestLimit is the one of the limits held with the least room left,
// the earliest on a tie.
func tightestLimit(held []limitChoice, room func(limitChoice) int) limitChoice {
	best := held[0]
	for _, c := range held[1:] {
		if room(c) < room(best) {
			best = c
		}
	}
	return best
}

// limitReasons says, per policy that holds a limit, what it decides: the
// limits shown are its own, a deciding policy's other limits give way to a
// hard global one with less room, and a hard global one's not shown hold
// too.
func limitReasons(held map[api.DeploymentLimitKind][]limitChoice, shown map[api.DeploymentLimitKind]limitChoice) (
	why map[string]string, holds map[string]bool) {
	why, holds = map[string]string{}, map[string]bool{}
	decided, gave, also := map[string][]string{}, map[string][]string{}, map[string][]string{}
	var order []string
	for _, k := range limitKinds {
		for i, c := range held[k] {
			id := c.pol.ID
			if !holds[id] {
				order = append(order, id)
			}
			holds[id] = true
			switch {
			case shown[k].pol.ID == id:
				decided[id] = append(decided[id], limitNames[k])
			case i == 0:
				gave[id] = append(gave[id], fmt.Sprintf("Its %s gives way to the global %s, hard and stricter.", limitNames[k], shown[k].pol.Name))
			default:
				also[id] = append(also[id], limitNames[k])
			}
		}
	}
	for _, id := range order {
		var parts []string
		if len(decided[id]) > 0 {
			parts = append(parts, "Decides "+joinNames(decided[id])+".")
		}
		parts = append(parts, gave[id]...)
		if n := len(also[id]); n > 0 {
			verb := map[bool]string{true: "holds", false: "hold"}[n == 1]
			parts = append(parts, fmt.Sprintf("Its %s %s too: a hard global limit applies on top of the policy that decides.",
				joinNames(also[id]), verb))
		}
		why[id] = strings.Join(parts, " ")
	}
	return why, holds
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

// checkLimits checks a change against the deployment limits, reading
// through db.
func (s *Service) checkLimits(ctx context.Context, db store.DB, ask limitAsk) (limitCheck, error) {
	out := limitCheck{Hard: map[api.DeploymentLimitKind]string{}}
	all, err := s.Store.DeploymentLimitPolicies(ctx, db)
	if err != nil || len(all) == 0 {
		return out, err
	}
	held := limitsHere(all, ask.Project.ID, ask.Env)
	if len(held) == 0 {
		return out, nil
	}
	counts, err := s.Store.DeploymentCounts(ctx, db, ask.Project.ID, ask.OwnerID)
	if err != nil {
		return out, err
	}
	for _, k := range limitKinds {
		var lines []api.DeploymentLimitLine
		for _, c := range held[k] {
			owned, inProject := countIn(counts, c.pol.Environment)
			var value, add int
			switch k {
			case api.DeploymentsPerUser:
				value, add = owned+ask.ToOwner, ask.ToOwner
			case api.DeploymentsPerProject:
				value, add = inProject+ask.ToProject, ask.ToProject
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
			lines = append(lines, line)
			if !line.Exceeds {
				continue
			}
			soft := c.pol.Enforcement == store.EnforcementSoft
			msg := ask.limitMessage(k, c, value, soft)
			if soft {
				out.Soft = append(out.Soft, present.Warning(c.pol.Name, msg))
			} else if _, ok := out.Hard[k]; !ok {
				out.Hard[k] = msg
			}
		}
		if len(lines) > 0 {
			out.Lines = append(out.Lines, bindingLine(lines))
		}
	}
	return out, nil
}

// bindingLine is the one line shown of a limit's: the first hard one gone
// over, else the first soft one, else the one with the least room left.
func bindingLine(lines []api.DeploymentLimitLine) api.DeploymentLimitLine {
	rank := func(l api.DeploymentLimitLine) int {
		switch {
		case l.Exceeds && l.Enforcement != api.PolicyEnforcementSoft:
			return 0
		case l.Exceeds:
			return 1
		}
		return 2
	}
	best := lines[0]
	for _, l := range lines[1:] {
		if r, b := rank(l), rank(best); r < b || r == b && r == 2 && l.Limit-l.Value < best.Limit-best.Value {
			best = l
		}
	}
	return best
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

// requestAsk is what the deployments a new request makes ask of the
// deployment limits (ADR-0047); nil when it makes none.
func (ev *Evaluation) requestAsk(p *auth.Principal) *limitAsk {
	n := ev.deploymentCount()
	if n == 0 {
		return nil
	}
	vcpu, mem := ev.largestDeployment()
	return &limitAsk{quotaScope: ev.ownerScope(p), ToOwner: n, ToProject: n, VCPU: vcpu, MemoryGB: mem,
		AddVCPU: vcpu, AddMemory: mem, Many: n > 1}
}

// evaluateLimits checks the deployments a new request makes against the
// deployment limits: a hard limit passed is an error, a soft one a warning
// the requester accepts.
func (s *Service) evaluateLimits(ctx context.Context, ev *Evaluation, p *auth.Principal) error {
	ask := ev.requestAsk(p)
	if ask == nil {
		return nil
	}
	c, err := s.checkLimits(ctx, s.Store.Pool, *ask)
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
func (s *Service) updateLimits(ctx context.Context, ev *Evaluation, u *updatePlan) ([]api.PolicyWarning, error) {
	c, err := s.checkLimits(ctx, s.Store.Pool, *u.change.limits)
	if err != nil {
		return nil, err
	}
	ev.Limits = c.Lines
	ev.Errors = append(ev.Errors, c.errors(anywhere)...)
	return c.Soft, nil
}

// updateAsk is what an update asks of the vCPU and memory limits of each
// deployment.
func updateAsk(p *auth.Principal, ev *Evaluation, d store.DeploymentRow, u *updatePlan) *limitAsk {
	now := deploymentUsage(u.target)
	return &limitAsk{quotaScope: ownerScope(p, ev.Project, ev.Env, d, nil),
		VCPU: now["vcpu"] + u.delta["vcpu"], MemoryGB: now["memory_gb"] + u.delta["memory_gb"],
		AddVCPU: u.delta["vcpu"], AddMemory: u.delta["memory_gb"]}
}

// editLimits checks an approver's edits against the vCPU and memory
// limits of each deployment as they are now: the request's deployments
// are counted already, and its requester accepted any soft limit.
func (s *Service) editLimits(ctx context.Context, requester *auth.Principal, ev *Evaluation) error {
	vcpu, mem := ev.largestDeployment()
	if vcpu == 0 && mem == 0 {
		return nil
	}
	c, err := s.checkLimits(ctx, s.Store.Pool, limitAsk{quotaScope: ev.ownerScope(requester), VCPU: vcpu, MemoryGB: mem,
		AddVCPU: max(vcpu-ev.edit.vcpu, 0), AddMemory: max(mem-ev.edit.memoryGB, 0), Many: ev.deploymentCount() > 1})
	if err != nil {
		return err
	}
	ev.Errors = append(ev.Errors, c.errors(anywhere)...)
	return nil
}

// explainLimits says which policy decides each deployment limit in a
// project and environment, counting what as owns there: of the limits
// held, the one with the least room left.
func (s *Service) explainLimits(ctx context.Context, as *auth.Principal, proj store.Project, env store.Environment,
	full bool) (api.PolicyLimits, error) {
	out := api.PolicyLimits{Lines: []api.PolicyLimitLine{}, Considered: []api.PolicyConsidered{}}
	all, err := s.Store.DeploymentLimitPolicies(ctx, s.Store.Pool)
	if err != nil || len(all) == 0 {
		return out, err
	}
	held := limitsHere(all, proj.ID, env.Key)
	counts, err := s.Store.DeploymentCounts(ctx, s.Store.Pool, proj.ID, as.User.ID)
	if err != nil {
		return out, err
	}
	used := func(k api.DeploymentLimitKind, c limitChoice) *int {
		owned, inProject := countIn(counts, c.pol.Environment)
		switch k {
		case api.DeploymentsPerUser:
			return &owned
		case api.DeploymentsPerProject:
			return &inProject
		}
		return nil
	}
	shown := map[api.DeploymentLimitKind]limitChoice{}
	for _, k := range limitKinds {
		if len(held[k]) == 0 {
			continue
		}
		c := tightestLimit(held[k], func(c limitChoice) int {
			if n := used(k, c); n != nil {
				return c.limit - *n
			}
			return c.limit
		})
		shown[k] = c
		out.Lines = append(out.Lines, api.PolicyLimitLine{Kind: k, Limit: c.limit, Enforcement: api.PolicyEnforcement(c.pol.Enforcement),
			Policy: c.pol.Name, Environment: c.pol.Environment, Used: used(k, c)})
	}
	why, holds := limitReasons(held, shown)
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
		case holds[pol.ID]:
			c.Applies, c.Why = true, why[pol.ID]
		default:
			c.Why = "A policy tried before it decides each of its limits."
		}
		out.Considered = append(out.Considered, c)
	}
	return out, nil
}

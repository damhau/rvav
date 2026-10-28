package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// Quotas and the lease, approval and day-2 policies, for administrators
// (ADR-0020): their CEL is checked on sample variables when saved, and an
// explain query says which of them decide where.

var (
	quotaResources = []string{"vcpu", "memory_gb", "storage_gb", "vm_count"}
	day2RoleNames  = []string{"owner", "viewer", "member", "project-admin", "admin"}
)

// ── Quotas ──────────────────────────────────────────────────────────────

func (s *Service) ProjectQuotas(ctx context.Context, p *auth.Principal, projectID string) ([]api.QuotaLimit, error) {
	proj, err := s.visibleProject(ctx, p, projectID)
	if err != nil {
		return nil, err
	}
	if !canSeeSettings(p, proj.ID) {
		return nil, ErrForbidden
	}
	list, err := s.Store.ProjectQuotas(ctx, proj.ID)
	if err != nil {
		return nil, err
	}
	out := make([]api.QuotaLimit, len(list))
	for i, q := range list {
		out[i] = api.QuotaLimit{Environment: q.Environment, Resource: api.QuotaResource(q.Resource), Limit: q.Limit,
			Enforcement: api.QuotaLimitEnforcement(q.Enforcement)}
	}
	return out, nil
}

func (s *Service) SetProjectQuotas(ctx context.Context, p *auth.Principal, projectID string, items []api.QuotaLimit) ([]api.QuotaLimit, error) {
	if !p.IsAdmin() {
		return nil, ErrForbidden
	}
	proj, err := s.Store.GetProject(ctx, projectID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var errs []api.FieldError
	seen := map[string]bool{}
	var limits []store.QuotaLimit
	for i, q := range items {
		ptr := fmt.Sprintf("/items/%d", i)
		if !slices.Contains(quotaResources, string(q.Resource)) {
			errs = append(errs, fieldErr(ptr+"/resource", "enum", "vcpu, memory_gb, storage_gb or vm_count."))
		}
		if q.Environment != "" && !slices.Contains(proj.Environments, q.Environment) {
			errs = append(errs, fieldErr(ptr+"/environment", "not_allowed", fmt.Sprintf("%s doesn't deploy to %s.", proj.Name, q.Environment)))
		}
		if q.Limit != nil && *q.Limit < 0 {
			errs = append(errs, fieldErr(ptr+"/limit", "minimum", "Zero or more, or no limit."))
		}
		if q.Enforcement != api.QuotaLimitEnforcementHard && q.Enforcement != api.QuotaLimitEnforcementSoft {
			errs = append(errs, fieldErr(ptr+"/enforcement", "enum", "hard or soft."))
		}
		key := q.Environment + "/" + string(q.Resource)
		if seen[key] {
			errs = append(errs, fieldErr(ptr, "duplicate", "This resource already has a limit here."))
		}
		seen[key] = true
		limits = append(limits, store.QuotaLimit{Environment: q.Environment, Resource: string(q.Resource), Limit: q.Limit, Enforcement: string(q.Enforcement)})
	}
	if len(errs) > 0 {
		return nil, &ValidationError{Errors: errs}
	}
	if err := s.Store.ReplaceQuotas(ctx, proj.ID, limits); err != nil {
		return nil, err
	}
	s.audit(ctx, p, "policy.quota.set", "project", proj.ID, &proj.ID, items)
	return s.ProjectQuotas(ctx, p, proj.ID)
}

// ── Lease policies ──────────────────────────────────────────────────────

func presentLeasePolicy(l store.LeasePolicy) api.LeasePolicy {
	return api.LeasePolicy{Environment: l.Environment, ProjectId: l.ProjectID, DefaultDays: l.DefaultDays, MaxDays: l.MaxDays,
		MaxExtensions: l.MaxExtensions, GraceDays: l.GraceDays, ExpiryAction: api.LeaseExpiryAction(l.ExpiryAction)}
}

func (s *Service) LeasePolicies(ctx context.Context, p *auth.Principal) ([]api.LeasePolicy, error) {
	if !p.SeesEverything() {
		return nil, ErrForbidden
	}
	list, err := s.Store.LeasePolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.LeasePolicy, len(list))
	for i, l := range list {
		out[i] = presentLeasePolicy(l)
	}
	return out, nil
}

func (s *Service) SetLeasePolicy(ctx context.Context, p *auth.Principal, l api.LeasePolicy) (api.LeasePolicy, error) {
	if !p.IsAdmin() {
		return api.LeasePolicy{}, ErrForbidden
	}
	var errs []api.FieldError
	if _, err := s.Store.GetEnvironment(ctx, l.Environment); errors.Is(err, store.ErrNotFound) {
		errs = append(errs, fieldErr("/environment", "unknown", fmt.Sprintf("%q is not an environment.", l.Environment)))
	} else if err != nil {
		return api.LeasePolicy{}, err
	}
	var project *string
	if l.ProjectId != nil && *l.ProjectId != "" {
		proj, err := s.Store.GetProject(ctx, *l.ProjectId)
		if errors.Is(err, store.ErrNotFound) {
			errs = append(errs, fieldErr("/project_id", "unknown", "There is no such project."))
		} else if err != nil {
			return api.LeasePolicy{}, err
		} else {
			project = &proj.ID
		}
	}
	for ptr, v := range map[string]*int{"/default_days": l.DefaultDays, "/max_days": l.MaxDays} {
		if v != nil && *v < 1 {
			errs = append(errs, fieldErr(ptr, "minimum", "At least a day, or none."))
		}
	}
	if l.DefaultDays != nil && l.MaxDays != nil && *l.DefaultDays > *l.MaxDays {
		errs = append(errs, fieldErr("/default_days", "maximum", "The default can't be longer than the maximum."))
	}
	if l.MaxExtensions < 0 || l.GraceDays < 0 {
		errs = append(errs, fieldErr("/max_extensions", "minimum", "Extensions and grace days are zero or more."))
	}
	switch l.ExpiryAction {
	case api.PowerOffThenDelete, api.Delete, api.NotifyOnly:
	default:
		errs = append(errs, fieldErr("/expiry_action", "enum", "power_off_then_delete, delete or notify_only."))
	}
	if len(errs) > 0 {
		return api.LeasePolicy{}, &ValidationError{Errors: errs}
	}
	row := store.LeasePolicy{Environment: l.Environment, ProjectID: project, DefaultDays: l.DefaultDays, MaxDays: l.MaxDays,
		MaxExtensions: l.MaxExtensions, GraceDays: l.GraceDays, ExpiryAction: string(l.ExpiryAction)}
	if err := s.Store.SetLeasePolicy(ctx, row); err != nil {
		return api.LeasePolicy{}, err
	}
	s.audit(ctx, p, "policy.lease.set", "lease_policy", l.Environment, project, l)
	return presentLeasePolicy(row), nil
}

func (s *Service) DeleteLeaseOverride(ctx context.Context, p *auth.Principal, environment, projectID string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	if err := s.Store.DeleteLeaseOverride(ctx, environment, projectID); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.audit(ctx, p, "policy.lease.delete", "lease_policy", environment, &projectID, nil)
	return nil
}

// ── Approval policies ──────────────────────────────────────────────────

// sampleVars are what approval conditions see, with sample values.
func sampleVars(inputs map[string]any) policy.Vars {
	if inputs == nil {
		inputs = map[string]any{}
	}
	return policy.Vars{"inputs": inputs, "environment": "dev",
		"project": map[string]any{"id": "prj_sample", "code": "sample", "name": "Sample"},
		"item":    map[string]any{"slug": "sample", "category": "compute", "type": "vm_blueprint"},
		"vm":      map[string]any{"vcpu": 2, "memory_gb": 4, "disk_gb": 40, "count": 1},
		"user":    map[string]any{"id": "usr_sample", "roles": []string{"user"}, "groups": []string{}},
		"quota":   quotaVars(nil)}
}

// checkApprovalExpr compiles an approval condition and evaluates it on
// sample variables: it must give true or false. An input the sample lacks
// is fine (forms differ); anything else wrong is caught now.
func checkApprovalExpr(expr string) error {
	if err := policy.Check(expr); err != nil {
		return err
	}
	v, err := policy.Eval(expr, sampleVars(nil))
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			return nil
		}
		return err
	}
	if _, ok := v.(bool); !ok {
		return fmt.Errorf("it gives %v, not true or false", v)
	}
	return nil
}

func (s *Service) checkLevels(ctx context.Context, levels []api.ApprovalLevel, ptr string, required bool) ([]api.FieldError, error) {
	var errs []api.FieldError
	if required && len(levels) == 0 {
		errs = append(errs, fieldErr(ptr, "required", "Give at least one level of approvers."))
	}
	var ids []string
	for i, l := range levels {
		lp := fmt.Sprintf("%s/%d", ptr, i)
		if strings.TrimSpace(l.Name) == "" {
			errs = append(errs, fieldErr(lp+"/name", "required", "Name the level."))
		}
		if deref(l.Group) == "" && (l.Approvers == nil || len(*l.Approvers) == 0) {
			errs = append(errs, fieldErr(lp, "required", "Give an OIDC group or approvers."))
		}
		if l.Approvers != nil {
			ids = append(ids, *l.Approvers...)
		}
		if l.Timeout != nil && *l.Timeout != "" {
			if d, err := time.ParseDuration(*l.Timeout); err != nil || d <= 0 {
				errs = append(errs, fieldErr(lp+"/timeout", "invalid", "A duration such as 48h."))
			}
		}
		if l.OnTimeout != nil && (l.Timeout == nil || *l.Timeout == "") {
			errs = append(errs, fieldErr(lp+"/on_timeout", "required", "What happens on time-out needs a timeout."))
		}
		if l.OnTimeout != nil && *l.OnTimeout == api.ApprovalLevelOnTimeoutEscalate && l.EscalateTo == nil {
			errs = append(errs, fieldErr(lp+"/escalate_to", "required", "Say whom to escalate to."))
		}
		if l.EscalateTo != nil && l.EscalateTo.Approvers != nil {
			ids = append(ids, *l.EscalateTo.Approvers...)
		}
	}
	if len(ids) > 0 {
		found, err := s.Store.FindUsers(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, ok := found[id]; !ok {
				errs = append(errs, fieldErr(ptr, "unknown", "There is no user "+id+"."))
			}
		}
	}
	return errs, nil
}

func presentApprovalPolicy(pol store.AdminApprovalPolicy) api.ApprovalPolicy {
	out := api.ApprovalPolicy{Id: pol.ID, Name: pol.Name, Reason: pol.Reason, Condition: pol.Condition, ProjectId: pol.ProjectID,
		Priority: pol.Priority, Enabled: pol.Enabled, AutoApprove: pol.AutoApprove, Levels: []api.ApprovalLevel{}}
	if pol.ItemType != nil {
		t := api.CatalogItemType(*pol.ItemType)
		out.ItemType = &t
	}
	_ = json.Unmarshal(pol.Levels, &out.Levels)
	return out
}

func (s *Service) ApprovalPolicies(ctx context.Context, p *auth.Principal) ([]api.ApprovalPolicy, error) {
	if !p.SeesEverything() {
		return nil, ErrForbidden
	}
	list, err := s.Store.AllApprovalPolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.ApprovalPolicy, len(list))
	for i, pol := range list {
		out[i] = presentApprovalPolicy(pol)
	}
	return out, nil
}

func (s *Service) checkProjectRef(ctx context.Context, id *string) (*string, []api.FieldError, error) {
	if id == nil || *id == "" {
		return nil, nil, nil
	}
	proj, err := s.Store.GetProject(ctx, *id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, []api.FieldError{fieldErr("/project_id", "unknown", "There is no such project.")}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &proj.ID, nil, nil
}

func (s *Service) SaveApprovalPolicy(ctx context.Context, p *auth.Principal, id string, w api.ApprovalPolicyWrite) (api.ApprovalPolicy, error) {
	if !p.IsAdmin() {
		return api.ApprovalPolicy{}, ErrForbidden
	}
	if id != "" {
		if _, err := s.Store.GetApprovalPolicy(ctx, id); errors.Is(err, store.ErrNotFound) {
			return api.ApprovalPolicy{}, ErrNotFound
		} else if err != nil {
			return api.ApprovalPolicy{}, err
		}
	}
	var errs []api.FieldError
	if strings.TrimSpace(w.Name) == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the policy a name."))
	}
	if strings.TrimSpace(w.Condition) == "" {
		errs = append(errs, fieldErr("/condition", "required", "When does it apply? For example environment == \"prd\"."))
	} else if err := checkApprovalExpr(w.Condition); err != nil {
		errs = append(errs, fieldErr("/condition", "invalid", "This condition is not valid CEL: "+err.Error()))
	}
	if w.AutoApprove != nil && *w.AutoApprove != "" {
		if err := checkApprovalExpr(*w.AutoApprove); err != nil {
			errs = append(errs, fieldErr("/auto_approve", "invalid", "This expression is not valid CEL: "+err.Error()))
		}
	}
	levelErrs, err := s.checkLevels(ctx, w.Levels, "/levels", true)
	if err != nil {
		return api.ApprovalPolicy{}, err
	}
	project, projErrs, err := s.checkProjectRef(ctx, w.ProjectId)
	if err != nil {
		return api.ApprovalPolicy{}, err
	}
	if errs = append(append(errs, levelErrs...), projErrs...); len(errs) > 0 {
		return api.ApprovalPolicy{}, &ValidationError{Errors: errs}
	}
	action := "policy.approval.update"
	if id == "" {
		id, action = ids.New("pol"), "policy.approval.create"
	}
	levels, _ := json.Marshal(w.Levels)
	pol := store.AdminApprovalPolicy{Enabled: w.Enabled == nil || *w.Enabled}
	pol.ID, pol.Name, pol.Reason, pol.Condition, pol.ProjectID = id, strings.TrimSpace(w.Name), deref(w.Reason), w.Condition, project
	pol.Levels, pol.Priority, pol.AutoApprove = levels, 100, w.AutoApprove
	if w.Priority != nil {
		pol.Priority = *w.Priority
	}
	if w.ItemType != nil {
		t := string(*w.ItemType)
		pol.ItemType = &t
	}
	if err := s.Store.SaveApprovalPolicy(ctx, pol); err != nil {
		return api.ApprovalPolicy{}, err
	}
	s.audit(ctx, p, action, "approval_policy", id, project, w)
	return presentApprovalPolicy(pol), nil
}

func (s *Service) DeleteApprovalPolicy(ctx context.Context, p *auth.Principal, id string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	if err := s.Store.DeleteApprovalPolicy(ctx, id); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.audit(ctx, p, "policy.approval.delete", "approval_policy", id, nil, nil)
	return nil
}

// ── Day-2 policies ──────────────────────────────────────────────────────

func presentDay2Policy(pol store.AdminDay2Policy) api.Day2Policy {
	out := api.Day2Policy{Id: pol.ID, Name: pol.Name, Reason: pol.Reason, Priority: pol.Priority, Actions: nonNilStrings(pol.Actions),
		Roles: nonNilStrings(pol.Roles), Condition: pol.Condition, Effect: api.Day2PolicyEffect(pol.Effect), ProjectId: pol.ProjectID,
		Enabled: pol.Enabled}
	var levels []api.ApprovalLevel
	if json.Unmarshal(pol.Levels, &levels) == nil && len(levels) > 0 {
		out.Levels = &levels
	}
	return out
}

func (s *Service) Day2Policies(ctx context.Context, p *auth.Principal) ([]api.Day2Policy, error) {
	if !p.SeesEverything() {
		return nil, ErrForbidden
	}
	list, err := s.Store.AllDay2Policies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Day2Policy, len(list))
	for i, pol := range list {
		out[i] = presentDay2Policy(pol)
	}
	return out, nil
}

func (s *Service) SaveDay2Policy(ctx context.Context, p *auth.Principal, id string, w api.Day2PolicyWrite) (api.Day2Policy, error) {
	if !p.IsAdmin() {
		return api.Day2Policy{}, ErrForbidden
	}
	if id != "" {
		if _, err := s.Store.GetDay2Policy(ctx, id); errors.Is(err, store.ErrNotFound) {
			return api.Day2Policy{}, ErrNotFound
		} else if err != nil {
			return api.Day2Policy{}, err
		}
	}
	actions, roles, condition := []string{"*"}, derefSlice(w.Roles), "true"
	if w.Actions != nil {
		actions = *w.Actions
	}
	if w.Condition != nil && strings.TrimSpace(*w.Condition) != "" {
		condition = *w.Condition
	}
	var errs []api.FieldError
	if strings.TrimSpace(w.Name) == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the policy a name."))
	}
	if len(actions) == 0 {
		errs = append(errs, fieldErr("/actions", "required", "Name the actions: keys or patterns such as snapshot_* or *."))
	}
	errs = append(errs, checkPatterns(actions, "/actions")...)
	for i, r := range roles {
		if !slices.Contains(day2RoleNames, r) {
			errs = append(errs, fieldErr(fmt.Sprintf("/roles/%d", i), "enum", "owner, viewer, member, project-admin or admin."))
		}
	}
	if err := checkCondition(condition, []string{"vsphere.vm", day2.DeploymentType}); err != nil {
		errs = append(errs, fieldErr("/condition", "invalid", "This condition is not valid CEL: "+err.Error()))
	}
	var levels []api.ApprovalLevel
	if w.Levels != nil {
		levels = *w.Levels
	}
	switch w.Effect {
	case api.Day2PolicyWriteEffectAllow, api.Day2PolicyWriteEffectDeny:
	case api.Day2PolicyWriteEffectApproval:
		levelErrs, err := s.checkLevels(ctx, levels, "/levels", true)
		if err != nil {
			return api.Day2Policy{}, err
		}
		errs = append(errs, levelErrs...)
	default:
		errs = append(errs, fieldErr("/effect", "enum", "allow, deny or approval."))
	}
	project, projErrs, err := s.checkProjectRef(ctx, w.ProjectId)
	if err != nil {
		return api.Day2Policy{}, err
	}
	if errs = append(errs, projErrs...); len(errs) > 0 {
		return api.Day2Policy{}, &ValidationError{Errors: errs}
	}
	action := "policy.day2.update"
	if id == "" {
		id, action = ids.New("d2p"), "policy.day2.create"
	}
	raw, _ := json.Marshal(levels)
	if levels == nil {
		raw = []byte("[]")
	}
	pol := store.AdminDay2Policy{Enabled: w.Enabled == nil || *w.Enabled}
	pol.ID, pol.Name, pol.Reason, pol.Priority = id, strings.TrimSpace(w.Name), deref(w.Reason), 100
	pol.Actions, pol.Roles, pol.Condition, pol.Effect, pol.Levels, pol.ProjectID = actions, roles, condition, string(w.Effect), raw, project
	if w.Priority != nil {
		pol.Priority = *w.Priority
	}
	if err := s.Store.SaveDay2Policy(ctx, pol); err != nil {
		return api.Day2Policy{}, err
	}
	s.audit(ctx, p, action, "day2_policy", id, project, w)
	return presentDay2Policy(pol), nil
}

func (s *Service) DeleteDay2Policy(ctx context.Context, p *auth.Principal, id string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	if err := s.Store.DeleteDay2Policy(ctx, id); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.audit(ctx, p, "policy.day2.delete", "day2_policy", id, nil, nil)
	return nil
}

// ── What applies here ──────────────────────────────────────────────────

// ExplainPolicies says, for a project and environment (and an item or a
// day-2 action), which policies decide and why the others don't.
func (s *Service) ExplainPolicies(ctx context.Context, p *auth.Principal, q api.PolicyExplainQuery) (api.PolicyExplanation, error) {
	out := api.PolicyExplanation{Quota: []api.PolicyQuotaLine{}}
	proj, err := s.visibleProject(ctx, p, q.ProjectId)
	if err != nil {
		return out, err
	}
	if !canSeeSettings(p, proj.ID) {
		return out, ErrForbidden
	}
	env, err := s.Store.GetEnvironment(ctx, q.Environment)
	if errors.Is(err, store.ErrNotFound) {
		return out, &ValidationError{Errors: []api.FieldError{fieldErr("/environment", "unknown", "There is no such environment.")}}
	} else if err != nil {
		return out, err
	}
	as := p
	if q.AsUserId != nil && *q.AsUserId != "" && *q.AsUserId != p.User.ID {
		if as, err = auth.PrincipalFor(ctx, s.Store, *q.AsUserId); errors.Is(err, store.ErrNotFound) {
			return out, &ValidationError{Errors: []api.FieldError{fieldErr("/as_user_id", "unknown", "There is no such user.")}}
		} else if err != nil {
			return out, err
		}
	}
	inputs := map[string]any{}
	if q.Inputs != nil {
		inputs = *q.Inputs
	}
	if out.Lease, err = s.explainLease(ctx, proj, env); err != nil {
		return out, err
	}
	if out.Naming, err = s.explainNaming(ctx, proj, env); err != nil {
		return out, err
	}
	if out.Quota, err = s.explainQuota(ctx, proj.ID, env.Key); err != nil {
		return out, err
	}
	if q.ItemId != nil && *q.ItemId != "" {
		dec, err := s.explainApproval(ctx, as, proj, env, *q.ItemId, inputs)
		if err != nil {
			return out, err
		}
		out.Approval = &dec
	}
	if q.Action != nil && *q.Action != "" {
		dec, err := s.explainDay2(ctx, as, proj, env, *q.Action, inputs)
		if err != nil {
			return out, err
		}
		out.Day2 = &dec
	}
	return out, nil
}

func (s *Service) explainLease(ctx context.Context, proj store.Project, env store.Environment) (api.PolicyDecision, error) {
	dec := api.PolicyDecision{Considered: []api.PolicyConsidered{}}
	all, err := s.Store.LeasePolicies(ctx)
	if err != nil {
		return dec, err
	}
	var own, override *store.LeasePolicy
	for i, l := range all {
		switch {
		case l.Environment == env.Key && l.ProjectID == nil:
			own = &all[i]
		case l.Environment == env.Key && *l.ProjectID == proj.ID:
			override = &all[i]
		}
	}
	consider := func(l *store.LeasePolicy, id, name string, applies bool, why string) {
		if l != nil {
			dec.Considered = append(dec.Considered, api.PolicyConsidered{Id: id, Name: name, Applies: applies, Why: why})
		}
	}
	consider(override, "lease:"+env.Key+":"+proj.ID, proj.Name+" in "+env.Name, true, "The project's own lease in this environment.")
	consider(own, "lease:"+env.Key, env.Name, override == nil, map[bool]string{true: "The environment's lease.",
		false: "The project's own lease overrides it."}[override == nil])
	l := override
	if l == nil {
		l = own
	}
	if l == nil {
		msg := "No lease: deployments here don't expire."
		dec.Outcome = &msg
		return dec, nil
	}
	name := env.Name
	if l == override {
		name = proj.Name + " in " + env.Name
	}
	dec.DecidedBy = &name
	msg := "No lease: deployments here don't expire."
	if l.DefaultDays != nil {
		msg = fmt.Sprintf("Deployments get %d days", *l.DefaultDays)
		if l.MaxDays != nil {
			msg += fmt.Sprintf(", up to %d with up to %d extensions", *l.MaxDays, l.MaxExtensions)
		}
		msg += "; at the end: " + strings.ReplaceAll(l.ExpiryAction, "_", " ") + "."
	}
	dec.Outcome = &msg
	return dec, nil
}

func (s *Service) explainNaming(ctx context.Context, proj store.Project, env store.Environment) (api.PolicyDecision, error) {
	dec := api.PolicyDecision{Considered: []api.PolicyConsidered{}}
	all, err := s.Store.NamingPolicies(ctx)
	if err != nil {
		return dec, err
	}
	offers, err := s.Store.Offers(ctx, "naming")
	if err != nil {
		return dec, err
	}
	chosen, err := s.Store.NamingPolicyFor(ctx, proj.ID, env.Key)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return dec, err
	}
	for _, n := range all {
		offered := slices.ContainsFunc(offers[n.ID], func(o store.Offer) bool {
			return (o.ProjectID == nil || *o.ProjectID == proj.ID) && (o.Environment == nil || *o.Environment == env.Key)
		})
		why := "Not offered to " + proj.Name + " in " + env.Name + "."
		switch {
		case n.ID == chosen.ID:
			why = "Offered here, and the most specific offer."
		case offered:
			why = "Offered here, but a more specific policy wins."
		}
		dec.Considered = append(dec.Considered, api.PolicyConsidered{Id: n.ID, Name: n.Name, Applies: n.ID == chosen.ID, Why: why})
	}
	msg := "No naming policy here: each blueprint's own template names the VMs."
	if chosen.ID != "" {
		dec.DecidedBy = &chosen.Name
		msg = "VMs are named " + chosen.Template + "."
	}
	dec.Outcome = &msg
	return dec, nil
}

func (s *Service) explainQuota(ctx context.Context, projectID, env string) ([]api.PolicyQuotaLine, error) {
	out := []api.PolicyQuotaLine{}
	for _, scope := range []string{"", env} {
		limits, err := s.Store.QuotaLimits(ctx, projectID, scope)
		if err != nil {
			return nil, err
		}
		if len(limits) == 0 {
			continue
		}
		used, err := s.Store.QuotaUsed(ctx, projectID, scope)
		if err != nil {
			return nil, err
		}
		reserved, err := s.Store.QuotaReserved(ctx, projectID, scope)
		if err != nil {
			return nil, err
		}
		for _, l := range limits {
			out = append(out, api.PolicyQuotaLine{Resource: api.QuotaResource(l.Resource), Scope: scope, Limit: l.Limit,
				Enforcement: api.PolicyQuotaLineEnforcement(l.Enforcement), Used: used[l.Resource], Reserved: reserved[l.Resource]})
		}
	}
	return out, nil
}

func (s *Service) explainApproval(ctx context.Context, as *auth.Principal, proj store.Project, env store.Environment, itemID string,
	inputs map[string]any) (api.PolicyDecision, error) {
	dec := api.PolicyDecision{Considered: []api.PolicyConsidered{}}
	item, err := s.Store.GetItem(ctx, itemID, as.User.ID)
	if errors.Is(err, store.ErrNotFound) {
		return dec, &ValidationError{Errors: []api.FieldError{fieldErr("/item_id", "unknown", "There is no such item.")}}
	} else if err != nil {
		return dec, err
	}
	all, err := s.Store.AllApprovalPolicies(ctx)
	if err != nil {
		return dec, err
	}
	ev := &Evaluation{Inputs: inputs, Env: env, Project: proj, Count: 1}
	ev.Item = item
	vars := PolicyVars(ev, as)
	var deciding []string
	for _, pol := range all {
		c := api.PolicyConsidered{Id: pol.ID, Name: pol.Name}
		switch {
		case !pol.Enabled:
			c.Why = "Disabled."
		case pol.ItemType != nil && *pol.ItemType != item.Type:
			c.Why = "Only for " + *pol.ItemType + " items."
		case pol.ProjectID != nil && *pol.ProjectID != proj.ID:
			c.Why = "Only for another project."
		default:
			match, err := policy.Bool(pol.Condition, vars)
			switch {
			case err != nil:
				c.Why = "Its condition needs inputs not given here (" + err.Error() + "); on a request it is checked again."
			case !match:
				c.Why = "Its condition " + pol.Condition + " is false here."
			default:
				auto := false
				if pol.AutoApprove != nil && *pol.AutoApprove != "" {
					auto, _ = policy.Bool(*pol.AutoApprove, vars)
				}
				c.Applies = !auto
				c.Why = map[bool]string{true: "Applies, but approves by itself: " + deref(pol.AutoApprove) + ".",
					false: "Applies: " + pol.Condition + "."}[auto]
				if !auto {
					deciding = append(deciding, pol.Name)
				}
			}
		}
		dec.Considered = append(dec.Considered, c)
	}
	msg := "No approval needed."
	if len(deciding) > 0 {
		names := strings.Join(deciding, ", ")
		dec.DecidedBy = &names
		msg = "Needs approval: " + names + "."
	}
	dec.Outcome = &msg
	return dec, nil
}

func (s *Service) explainDay2(ctx context.Context, as *auth.Principal, proj store.Project, env store.Environment, key string,
	inputs map[string]any) (api.PolicyDecision, error) {
	dec := api.PolicyDecision{Considered: []api.PolicyConsidered{}}
	a := day2.Get(key)
	if a == nil {
		if !strings.HasPrefix(key, "awx.") {
			return dec, &ValidationError{Errors: []api.FieldError{fieldErr("/action", "unknown", "There is no day-2 action "+key+".")}}
		}
		a = &day2.Action{Key: key, Name: key, Group: day2.GroupAutomation}
	}
	all, err := s.Store.AllDay2Policies(ctx)
	if err != nil {
		return dec, err
	}
	// On a deployment of theirs in the project, as its owner.
	d := store.DeploymentRow{}
	d.ID, d.Name, d.OwnerID, d.ProjectID, d.ProjectCode, d.ProjectName = "dep_sample", "sample", as.User.ID, proj.ID, proj.Code, proj.Name
	roles := day2Roles(as, d)
	t := day2.Target{ResourceID: "res_sample", ResourceName: "sample", Type: "vsphere.vm",
		VM: api.VmProperties{Moref: "vm-1", PowerState: api.On}}
	vars := day2Vars(as, d, env, a, t, inputs, roles)
	vars["quota"] = quotaVars(nil)
	decided := false
	for _, pol := range all {
		c := api.PolicyConsidered{Id: pol.ID, Name: pol.Name}
		switch {
		case decided:
			c.Why = "An earlier policy decided."
		case !pol.Enabled:
			c.Why = "Disabled."
		case pol.ProjectID != nil && *pol.ProjectID != proj.ID:
			c.Why = "Only for another project."
		case !matchesAction(pol.Actions, a.Key):
			c.Why = "Not for " + a.Key + "."
		case len(pol.Roles) > 0 && !slices.ContainsFunc(pol.Roles, func(r string) bool { return slices.Contains(roles, r) }):
			c.Why = "Only for " + strings.Join(pol.Roles, ", ") + "; here: " + strings.Join(roles, ", ") + "."
		default:
			match, err := policy.Day2Bool(pol.Condition, vars)
			switch {
			case err != nil:
				c.Why = "Its condition can't be evaluated here (" + err.Error() + ")."
			case !match:
				c.Why = "Its condition " + pol.Condition + " is false here."
			default:
				c.Applies, decided = true, true
				c.Why = "Decides: " + pol.Effect + "."
				dec.DecidedBy = &pol.Name
				msg := map[string]string{"allow": a.Name + " is allowed.", "deny": a.Name + " is denied: " + orDefault(pol.Reason, "no reason given") + ".",
					"approval": a.Name + " needs approval."}[pol.Effect]
				dec.Outcome = &msg
			}
		}
		dec.Considered = append(dec.Considered, c)
	}
	if !decided {
		msg := "No policy matches: " + a.Name + " is denied."
		dec.Outcome = &msg
	}
	return dec, nil
}

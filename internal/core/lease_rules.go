package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// Lease rules (ADR-0048): a lease for the deployments a CEL condition
// matches, tried by priority before the environment's lease and the
// project's override; and how long a deployment lives in all.

func presentLeaseRule(r store.LeaseRule) api.LeaseRule {
	return api.LeaseRule{Id: r.ID, Name: r.Name, Condition: r.Condition, Environment: r.Environment, ProjectId: r.ProjectID,
		Priority: r.Priority, DefaultDays: r.DefaultDays, MaxDays: r.MaxDays, MaxExtensions: r.MaxExtensions, GraceDays: r.GraceDays,
		ExpiryAction: api.LeaseExpiryAction(r.ExpiryAction), Enforcement: api.PolicyEnforcement(cmp.Or(r.Enforcement, store.EnforcementHard)),
		MaxLifetimeDays: r.MaxLifetimeDays, SetByAdmin: r.SetByAdmin}
}

// leaseTerms checks what a lease policy or rule gives.
func leaseTerms(defaultDays, maxDays *int, extensions, grace int, action api.LeaseExpiryAction, enf *api.PolicyEnforcement,
	lifetime *int) (store.LeaseTerms, []api.FieldError) {
	var errs []api.FieldError
	for _, f := range []struct {
		ptr string
		v   *int
	}{{"/default_days", defaultDays}, {"/max_days", maxDays}, {"/max_lifetime_days", lifetime}} {
		if f.v != nil && *f.v < 1 {
			errs = append(errs, fieldErr(f.ptr, "minimum", "At least a day, or none."))
		}
	}
	if defaultDays != nil && maxDays != nil && *defaultDays > *maxDays {
		errs = append(errs, fieldErr("/default_days", "maximum", "The default can't be longer than the maximum."))
	}
	if lifetime != nil && maxDays != nil && *maxDays > *lifetime {
		errs = append(errs, fieldErr("/max_days", "maximum", "The maximum can't be longer than the lifetime."))
	} else if lifetime != nil && defaultDays != nil && *defaultDays > *lifetime {
		errs = append(errs, fieldErr("/default_days", "maximum", "The default can't be longer than the lifetime."))
	}
	if extensions < 0 || grace < 0 {
		errs = append(errs, fieldErr("/max_extensions", "minimum", "Extensions and grace days are zero or more."))
	}
	switch action {
	case api.PowerOffThenDelete, api.Delete, api.NotifyOnly:
	default:
		errs = append(errs, fieldErr("/expiry_action", "enum", "power_off_then_delete, delete or notify_only."))
	}
	e, enfErrs := enforcement(enf)
	return store.LeaseTerms{DefaultDays: defaultDays, MaxDays: maxDays, MaxExtensions: extensions, GraceDays: grace,
		ExpiryAction: string(action), Enforcement: e, MaxLifetimeDays: lifetime}, append(errs, enfErrs...)
}

// looser says what l gives beyond ref, which a project admin's lease can't
// (ADR-0045, ADR-0048): name is what ref is, such as "Development".
func looser(l, ref store.LeaseTerms, name string) []api.FieldError {
	if ref.DefaultDays == nil && ref.MaxDays == nil && ref.MaxLifetimeDays == nil {
		return nil // deployments there don't expire: any lease is stricter
	}
	var errs []api.FieldError
	if ref.DefaultDays != nil && l.DefaultDays == nil {
		errs = append(errs, fieldErr("/default_days", "looser", name+" gives deployments a lease: give them one."))
	}
	if ref.MaxDays != nil && (l.MaxDays == nil || *l.MaxDays > *ref.MaxDays) {
		errs = append(errs, fieldErr("/max_days", "looser", fmt.Sprintf("%s allows %d days at most.", name, *ref.MaxDays)))
	}
	if l.MaxExtensions > ref.MaxExtensions {
		errs = append(errs, fieldErr("/max_extensions", "looser", fmt.Sprintf("%s allows %d extensions at most.", name, ref.MaxExtensions)))
	}
	if l.GraceDays > ref.GraceDays {
		errs = append(errs, fieldErr("/grace_days", "looser", fmt.Sprintf("%s gives %d grace days at most.", name, ref.GraceDays)))
	}
	rank := map[string]int{"notify_only": 0, "power_off_then_delete": 1, "delete": 2}
	if rank[l.ExpiryAction] < rank[ref.ExpiryAction] {
		errs = append(errs, fieldErr("/expiry_action", "looser", name+"'s leases end with "+strings.ReplaceAll(ref.ExpiryAction, "_", " ")+"."))
	}
	if ref.MaxLifetimeDays != nil && (l.MaxLifetimeDays == nil || *l.MaxLifetimeDays > *ref.MaxLifetimeDays) {
		errs = append(errs, fieldErr("/max_lifetime_days", "looser", fmt.Sprintf("%s lets deployments live %d days at most.", name, *ref.MaxLifetimeDays)))
	}
	if l.Enforcement == store.EnforcementSoft && ref.Enforcement != store.EnforcementSoft {
		errs = append(errs, fieldErr("/enforcement", "looser", name+"'s lease is hard: yours must be too."))
	}
	return errs
}

func (s *Service) LeaseRules(ctx context.Context, p *auth.Principal) ([]api.LeaseRule, error) {
	if !readsPolicies(p) {
		return nil, ErrForbidden
	}
	list, err := s.Store.LeaseRules(ctx)
	if err != nil {
		return nil, err
	}
	out := []api.LeaseRule{}
	for _, r := range list {
		if showsPolicy(p, r.ProjectID) {
			out = append(out, presentLeaseRule(r))
		}
	}
	return out, nil
}

func (s *Service) SaveLeaseRule(ctx context.Context, p *auth.Principal, id string, w api.LeaseRuleWrite) (api.LeaseRule, error) {
	project, errs, err := s.policyProject(ctx, p, w.ProjectId)
	if err != nil {
		return api.LeaseRule{}, err
	}
	if id != "" {
		if old, err := s.Store.GetLeaseRule(ctx, id); errors.Is(err, store.ErrNotFound) || err == nil && !showsPolicy(p, old.ProjectID) {
			return api.LeaseRule{}, ErrNotFound
		} else if err != nil {
			return api.LeaseRule{}, err
		} else if !changesPolicy(p, old.ProjectID, old.SetByAdmin) {
			return api.LeaseRule{}, ErrForbidden
		}
	}
	if strings.TrimSpace(w.Name) == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the rule a name."))
	}
	if strings.TrimSpace(w.Condition) == "" {
		errs = append(errs, fieldErr("/condition", "required", `Which deployments does it give this lease? For example item.slug == "gpu-server".`))
	} else if err := checkApprovalExpr(w.Condition); err != nil {
		errs = append(errs, fieldErr("/condition", "invalid", "This condition is not valid CEL: "+err.Error()))
	}
	var env *store.Environment
	if w.Environment != nil && *w.Environment != "" {
		e, err := s.Store.GetEnvironment(ctx, *w.Environment)
		if errors.Is(err, store.ErrNotFound) {
			errs = append(errs, fieldErr("/environment", "unknown", fmt.Sprintf("%q is not an environment.", *w.Environment)))
		} else if err != nil {
			return api.LeaseRule{}, err
		} else {
			env = &e
		}
	}
	terms, termErrs := leaseTerms(w.DefaultDays, w.MaxDays, w.MaxExtensions, w.GraceDays, w.ExpiryAction, w.Enforcement, w.MaxLifetimeDays)
	errs = append(errs, termErrs...)
	if len(errs) == 0 && !p.Everywhere(auth.PoliciesManage) {
		looserErrs, err := s.looserRule(ctx, *project, env, terms)
		if err != nil {
			return api.LeaseRule{}, err
		}
		errs = append(errs, looserErrs...)
	}
	if len(errs) > 0 {
		return api.LeaseRule{}, &ValidationError{Errors: errs}
	}
	action := "policy.lease_rule.update"
	if id == "" {
		id, action = ids.New("lsr"), "policy.lease_rule.create"
	}
	r := store.LeaseRule{ID: id, Name: strings.TrimSpace(w.Name), Condition: w.Condition, ProjectID: project, Priority: 100,
		LeaseTerms: terms, SetByAdmin: p.Everywhere(auth.PoliciesManage)}
	if env != nil {
		r.Environment = &env.Key
	}
	if w.Priority != nil {
		r.Priority = *w.Priority
	}
	if err := s.Store.SaveLeaseRule(ctx, r); err != nil {
		return api.LeaseRule{}, err
	}
	s.audit(ctx, p, action, "lease_rule", id, project, w)
	return presentLeaseRule(r), nil
}

// looserRule says what a project admin's rule gives beyond the lease
// administrators set for the project, in each environment it may apply
// in: the project's override an administrator set, or the environment's.
func (s *Service) looserRule(ctx context.Context, projectID string, env *store.Environment, terms store.LeaseTerms) ([]api.FieldError, error) {
	proj, err := s.Store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	keys := proj.Environments
	if env != nil {
		keys = []string{env.Key}
	}
	all, err := s.Store.LeasePolicies(ctx)
	if err != nil {
		return nil, err
	}
	var errs []api.FieldError
	seen := map[string]bool{}
	for _, key := range keys {
		e, err := s.Store.GetEnvironment(ctx, key)
		if err != nil {
			return nil, err
		}
		if ref, name := adminLease(all, proj, e); ref != nil {
			for _, fe := range looser(terms, ref.LeaseTerms, name) {
				if !seen[fe.Field] {
					seen[fe.Field] = true
					errs = append(errs, fe)
				}
			}
		}
	}
	return errs, nil
}

// adminLease is the lease administrators set for a project in an
// environment, nil for none: the project's override an administrator
// wrote, or the environment's own.
func adminLease(all []store.LeasePolicy, proj store.Project, env store.Environment) (*store.LeasePolicy, string) {
	var own *store.LeasePolicy
	for i, l := range all {
		switch {
		case l.Environment != env.Key:
		case l.ProjectID == nil:
			own = &all[i]
		case *l.ProjectID == proj.ID && l.SetByAdmin:
			return &all[i], proj.Name + " in " + env.Name
		}
	}
	return own, env.Name
}

func (s *Service) DeleteLeaseRule(ctx context.Context, p *auth.Principal, id string) error {
	if !p.Everywhere(auth.PoliciesManage) && !p.Anywhere(auth.ProjectPolicies) {
		return ErrForbidden
	}
	old, err := s.Store.GetLeaseRule(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound) || err == nil && !showsPolicy(p, old.ProjectID):
		return ErrNotFound
	case err != nil:
		return err
	case !changesPolicy(p, old.ProjectID, old.SetByAdmin):
		return ErrForbidden
	}
	if err := s.Store.DeleteLeaseRule(ctx, id); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	s.audit(ctx, p, "policy.lease_rule.delete", "lease_rule", id, old.ProjectID, nil)
	return nil
}

// leaseChoice is the lease a request gets, and what decides it.
type leaseChoice struct {
	store.LeaseTerms
	// Policy names what decides: a rule, "<project> in <environment>"
	// for the project's override, or the environment; "" for nothing.
	Policy string
	// Rule is the rule that decides, nil when a lease policy does.
	Rule *store.LeaseRule
	// Cap is the administrators' rule that holds Rule when it is a
	// project admin's: the deployment keeps the stricter of the two.
	Cap *store.LeaseRule
	// Considered are the rules and policies tried, and why each applies or not.
	Considered []api.PolicyConsidered
}

// chooseLease finds the lease for a request (ADR-0048). The rules are
// tried by priority, global and the project's together, and the first that
// matches decides; with none, the project's override, else the
// environment's lease policy. A project admin's rule that gives more than
// the lease administrators set (the first of their rules that matches,
// else the project's override an administrator set, else the
// environment's lease) is skipped. A condition that can't be evaluated is
// skipped in a preview; on submit (strict) the rule applies when it gives
// no more than the lease without rules. full lists other projects' rules.
func (s *Service) chooseLease(ctx context.Context, proj store.Project, env store.Environment, vars policy.Vars,
	strict, full bool) (leaseChoice, error) {
	out := leaseChoice{Considered: []api.PolicyConsidered{}}
	policies, err := s.Store.LeasePolicies(ctx)
	if err != nil {
		return out, err
	}
	rules, err := s.Store.LeaseRules(ctx)
	if err != nil {
		return out, err
	}
	var own, override *store.LeasePolicy
	for i, l := range policies {
		switch {
		case l.Environment != env.Key:
		case l.ProjectID == nil:
			own = &policies[i]
		case *l.ProjectID == proj.ID:
			override = &policies[i]
		}
	}
	base, baseName := own, env.Name
	if override != nil {
		base, baseName = override, proj.Name+" in "+env.Name
	}
	envNames := map[string]string{env.Key: env.Name}
	envName := func(key string) string {
		if _, ok := envNames[key]; !ok {
			envNames[key] = key
			if e, err := s.Store.GetEnvironment(ctx, key); err == nil {
				envNames[key] = e.Name
			}
		}
		return envNames[key]
	}
	type tried struct {
		rule *store.LeaseRule
		// at is its line in Considered; failed: its condition can't be evaluated.
		at     int
		failed bool
	}
	var matched []tried
	for i := range rules {
		r := &rules[i]
		if !full && r.ProjectID != nil && *r.ProjectID != proj.ID {
			continue
		}
		c := api.PolicyConsidered{Id: r.ID, Name: r.Name}
		switch {
		case r.ProjectID != nil && *r.ProjectID != proj.ID:
			c.Why = "Only for another project."
		case r.Environment != nil && *r.Environment != env.Key:
			c.Why = "Only for " + envName(*r.Environment) + "."
		default:
			match, err := policy.Bool(r.Condition, vars)
			switch {
			case err != nil && !strict:
				c.Why = "Its condition needs inputs not given here (" + err.Error() + "); on a request it applies unless it gives more than the lease without rules."
			case err != nil:
				s.Log.Warn("lease rule could not be evaluated", "rule", r.ID, "err", err)
				if base != nil && len(looser(r.LeaseTerms, base.LeaseTerms, baseName)) > 0 {
					c.Why = "Its condition can't be evaluated (" + err.Error() + "), and it gives more than " + baseName + "."
				} else {
					c.Why = "Its condition can't be evaluated (" + err.Error() + "): it applies, as it gives no more than the lease without rules."
					matched = append(matched, tried{rule: r, at: len(out.Considered), failed: true})
				}
			case !match:
				c.Why = "Its condition " + r.Condition + " is false here."
			default:
				matched = append(matched, tried{rule: r, at: len(out.Considered)})
			}
		}
		out.Considered = append(out.Considered, c)
	}
	// What administrators set, which a project admin's rule can't loosen.
	var authority *store.LeaseTerms
	var authorityRule *store.LeaseRule
	authorityName := ""
	for _, t := range matched {
		if t.rule.ProjectID == nil || t.rule.SetByAdmin {
			authority, authorityRule, authorityName = &t.rule.LeaseTerms, t.rule, "the rule "+t.rule.Name
			break
		}
	}
	if authority == nil {
		if l, name := adminLease(policies, proj, env); l != nil {
			authority, authorityName = &l.LeaseTerms, name
		}
	}
	for _, t := range matched {
		c := &out.Considered[t.at]
		if out.Rule != nil {
			c.Why = "An earlier rule decides."
			continue
		}
		if t.rule.ProjectID != nil && !t.rule.SetByAdmin && authority != nil {
			if errs := looser(t.rule.LeaseTerms, *authority, authorityName); len(errs) > 0 {
				c.Why = "It would decide, but a project admin's rule can't give more than " + authorityName + ": " + errs[0].Message
				continue
			}
			out.Cap = authorityRule
		}
		c.Applies = true
		if !t.failed {
			c.Why = "Decides: " + t.rule.Condition + "."
		}
		out.Rule, out.LeaseTerms, out.Policy = t.rule, t.rule.LeaseTerms, t.rule.Name
	}
	why := map[bool]string{true: "A lease rule decides first.", false: ""}
	if override != nil {
		w := cmp.Or(why[out.Rule != nil], "The project's own lease in this environment.")
		out.Considered = append(out.Considered, api.PolicyConsidered{Id: "lease:" + env.Key + ":" + proj.ID, Name: proj.Name + " in " + env.Name,
			Applies: out.Rule == nil, Why: w})
	}
	if own != nil {
		w := cmp.Or(why[out.Rule != nil], map[bool]string{true: "The environment's lease.", false: "The project's own lease overrides it."}[override == nil])
		out.Considered = append(out.Considered, api.PolicyConsidered{Id: "lease:" + env.Key, Name: env.Name,
			Applies: out.Rule == nil && override == nil, Why: w})
	}
	if out.Rule == nil && base != nil {
		out.LeaseTerms, out.Policy = base.LeaseTerms, baseName
	}
	if out.Rule == nil && base == nil {
		out.ExpiryAction, out.Enforcement = "notify_only", store.EnforcementHard
	}
	return out, nil
}

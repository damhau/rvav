package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
)

// Each member's quota in a project, and rvav's own (ADR-0047).

// checkQuotaLine checks a limit's resource, amount and enforcement.
func checkQuotaLine(ptr, resource string, limit *int, enf string) []api.FieldError {
	var errs []api.FieldError
	if !slices.Contains(quotaResources, resource) {
		errs = append(errs, fieldErr(ptr+"/resource", "enum", "vcpu, memory_gb, storage_gb or vm_count."))
	}
	if limit != nil && *limit < 0 {
		errs = append(errs, fieldErr(ptr+"/limit", "minimum", "Zero or more, or no limit."))
	}
	if enf != store.EnforcementHard && enf != store.EnforcementSoft {
		errs = append(errs, fieldErr(ptr+"/enforcement", "enum", "hard or soft."))
	}
	return errs
}

func presentUserQuotas(list []store.UserQuota) []api.UserQuota {
	out := make([]api.UserQuota, len(list))
	for i, q := range list {
		out[i] = api.UserQuota{Resource: api.QuotaResource(q.Resource), Limit: q.Limit,
			Enforcement: api.UserQuotaEnforcement(q.Enforcement), SetByAdmin: q.SetByAdmin}
	}
	return out
}

// UserQuotas are what each member may own in the project.
func (s *Service) UserQuotas(ctx context.Context, p *auth.Principal, projectID string) ([]api.UserQuota, error) {
	proj, err := s.visibleProject(ctx, p, projectID)
	if err != nil {
		return nil, err
	}
	if !seesQuotas(p, proj.ID) {
		return nil, ErrForbidden
	}
	list, err := s.Store.UserQuotas(ctx, s.Store.Pool, &proj.ID)
	return presentUserQuotas(list), err
}

// SetUserQuotas replaces what each member may own in the project. Whoever
// manages its quotas writes any limit, and the lines they add or change
// become theirs (set_by_admin). Its project admins write limits within the
// project's own, and leave those lines alone (ADR-0047).
func (s *Service) SetUserQuotas(ctx context.Context, p *auth.Principal, projectID string, items []api.UserQuotaWrite) ([]api.UserQuota, error) {
	proj, err := s.visibleProject(ctx, p, projectID)
	if err != nil {
		return nil, err
	}
	admin := p.In(auth.ProjectQuotasManage, proj.ID)
	if !admin && !p.In(auth.ProjectPolicies, proj.ID) {
		return nil, ErrForbidden
	}
	old, err := s.Store.UserQuotas(ctx, s.Store.Pool, &proj.ID)
	if err != nil {
		return nil, err
	}
	project, err := s.Store.QuotaLimits(ctx, s.Store.Pool, proj.ID, "")
	if err != nil {
		return nil, err
	}
	var errs []api.FieldError
	var rows []store.UserQuota
	for i, q := range items {
		ptr := fmt.Sprintf("/items/%d", i)
		errs = append(errs, checkQuotaLine(ptr, string(q.Resource), q.Limit, string(q.Enforcement))...)
		if slices.ContainsFunc(rows, func(r store.UserQuota) bool { return r.Resource == string(q.Resource) }) {
			errs = append(errs, fieldErr(ptr, "duplicate", "This resource already has a limit for each member."))
		}
		row := store.UserQuota{ProjectID: &proj.ID, Resource: string(q.Resource), Limit: q.Limit, Enforcement: string(q.Enforcement),
			SetByAdmin: admin}
		if j := slices.IndexFunc(old, func(o store.UserQuota) bool { return sameUserQuota(o, row) }); j >= 0 {
			row.SetByAdmin = old[j].SetByAdmin
		} else if !admin {
			errs = append(errs, looserUserQuota(ptr, proj, project, row)...)
		}
		rows = append(rows, row)
	}
	if !admin {
		for _, o := range old {
			if o.SetByAdmin && !slices.ContainsFunc(rows, func(r store.UserQuota) bool { return sameUserQuota(o, r) }) {
				return nil, fmt.Errorf("%w: an administrator set each member's %s limit: only they can change it", ErrForbidden,
					quotaLabel(o.Resource))
			}
		}
	}
	if len(errs) > 0 {
		return nil, &ValidationError{Errors: errs}
	}
	if err := s.Store.ReplaceUserQuotas(ctx, proj.ID, rows); err != nil {
		return nil, err
	}
	s.audit(ctx, p, "policy.quota.user.set", "project", proj.ID, &proj.ID, items)
	list, err := s.Store.UserQuotas(ctx, s.Store.Pool, &proj.ID)
	return presentUserQuotas(list), err
}

func sameUserQuota(a, b store.UserQuota) bool {
	return a.Resource == b.Resource && a.Enforcement == b.Enforcement &&
		(a.Limit == nil) == (b.Limit == nil) && (a.Limit == nil || *a.Limit == *b.Limit)
}

// looserUserQuota says when a project admin's limit for each member gives
// more than the project's own quota.
func looserUserQuota(ptr string, proj store.Project, project []store.QuotaLimit, q store.UserQuota) []api.FieldError {
	i := slices.IndexFunc(project, func(l store.QuotaLimit) bool { return l.Resource == q.Resource && l.Limit != nil })
	if i < 0 || q.Limit != nil && *q.Limit <= *project[i].Limit {
		return nil
	}
	return []api.FieldError{fieldErr(ptr+"/limit", "looser",
		fmt.Sprintf("%s has %s in all: each member's limit can't be more.", proj.Name, quotaAmount(q.Resource, *project[i].Limit)))}
}

// GlobalQuotas are what all of rvav, and each user across it, may hold.
func (s *Service) GlobalQuotas(ctx context.Context, p *auth.Principal) ([]api.GlobalQuota, error) {
	if !seesQuotas(p, "") {
		return nil, ErrForbidden
	}
	return s.globalQuotas(ctx)
}

func (s *Service) globalQuotas(ctx context.Context) ([]api.GlobalQuota, error) {
	whole, err := s.Store.GlobalQuotas(ctx, s.Store.Pool)
	if err != nil {
		return nil, err
	}
	each, err := s.Store.UserQuotas(ctx, s.Store.Pool, nil)
	if err != nil {
		return nil, err
	}
	out := []api.GlobalQuota{}
	for _, q := range whole {
		out = append(out, api.GlobalQuota{Resource: api.QuotaResource(q.Resource), Limit: q.Limit,
			Enforcement: api.GlobalQuotaEnforcement(q.Enforcement)})
	}
	for _, q := range each {
		out = append(out, api.GlobalQuota{Resource: api.QuotaResource(q.Resource), PerUser: true, Limit: q.Limit,
			Enforcement: api.GlobalQuotaEnforcement(q.Enforcement)})
	}
	return out, nil
}

// SetGlobalQuotas replaces rvav's quota; administrators' only.
func (s *Service) SetGlobalQuotas(ctx context.Context, p *auth.Principal, items []api.GlobalQuota) ([]api.GlobalQuota, error) {
	if !p.Everywhere(auth.ProjectQuotasManage) {
		return nil, ErrForbidden
	}
	var errs []api.FieldError
	var whole []store.QuotaLimit
	var each []store.UserQuota
	seen := map[string]bool{}
	for i, q := range items {
		ptr := fmt.Sprintf("/items/%d", i)
		errs = append(errs, checkQuotaLine(ptr, string(q.Resource), q.Limit, string(q.Enforcement))...)
		key := fmt.Sprintf("%s/%v", q.Resource, q.PerUser)
		if seen[key] {
			errs = append(errs, fieldErr(ptr, "duplicate", "This resource already has a limit here."))
		}
		seen[key] = true
		if q.PerUser {
			each = append(each, store.UserQuota{Resource: string(q.Resource), Limit: q.Limit, Enforcement: string(q.Enforcement), SetByAdmin: true})
		} else {
			whole = append(whole, store.QuotaLimit{Resource: string(q.Resource), Limit: q.Limit, Enforcement: string(q.Enforcement)})
		}
	}
	if len(errs) > 0 {
		return nil, &ValidationError{Errors: errs}
	}
	if err := s.Store.ReplaceGlobalQuotas(ctx, whole, each); err != nil {
		return nil, err
	}
	s.audit(ctx, p, "policy.quota.global.set", "quota", "rvav", nil, items)
	return s.globalQuotas(ctx)
}

// explainQuota lists the limits at every level, with what they count now;
// the user's levels count what as owns.
func (s *Service) explainQuota(ctx context.Context, as *auth.Principal, proj store.Project, env store.Environment) ([]api.PolicyQuotaLine, error) {
	out := []api.PolicyQuotaLine{}
	sc := quotaScope{Project: proj, Env: env.Key, EnvName: env.Name, OwnerID: as.User.ID, OwnerName: as.User.Name}
	for _, level := range quotaLevels {
		lv, err := s.quotaLevel(ctx, s.Store.Pool, sc, level)
		if err != nil {
			return nil, err
		}
		if len(lv.limits) == 0 {
			continue
		}
		used, err := s.Store.QuotaUsedBy(ctx, s.Store.Pool, lv.filter)
		if err != nil {
			return nil, err
		}
		reserved, err := s.Store.QuotaReservedBy(ctx, s.Store.Pool, lv.filter)
		if err != nil {
			return nil, err
		}
		for _, l := range lv.limits {
			out = append(out, api.PolicyQuotaLine{Resource: api.QuotaResource(l.Resource), Level: level, Scope: lv.filter.Environment,
				Limit: l.Limit, Enforcement: api.PolicyQuotaLineEnforcement(l.Enforcement), Used: used[l.Resource],
				Reserved: reserved[l.Resource]})
		}
	}
	return out, nil
}

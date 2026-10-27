package core

import (
	"context"
	"fmt"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// QuotaCheck is what adding resources does to a project's quotas, project-
// wide and in one environment (ADR-0014).
type QuotaCheck struct {
	// Impacts are the project-wide lines, then the environment's limits.
	Impacts []api.QuotaImpact
	// Hard says, per hard limit passed, what is left; the change is refused.
	Hard []string
	// OverSoft are the resources over a soft limit: the change needs approval.
	OverSoft []string
}

// quotaCheck checks adding to the project in the environment; what names
// the change in messages ("This request").
func (s *Service) quotaCheck(ctx context.Context, proj store.Project, env, envName, what string, add map[string]int) (QuotaCheck, error) {
	var out QuotaCheck
	out.Impacts = []api.QuotaImpact{}
	scopes := []string{""}
	if env != "" {
		scopes = append(scopes, env)
	}
	for _, scope := range scopes {
		limits, err := s.Store.QuotaLimits(ctx, proj.ID, scope)
		if err != nil {
			return out, err
		}
		if scope != "" && len(limits) == 0 {
			continue
		}
		used, err := s.Store.QuotaUsed(ctx, proj.ID, scope)
		if err != nil {
			return out, err
		}
		reserved, err := s.Store.QuotaReserved(ctx, proj.ID, scope)
		if err != nil {
			return out, err
		}
		byRes := map[string]store.QuotaLimit{}
		for _, l := range limits {
			byRes[l.Resource] = l
		}
		where := proj.Name
		if scope != "" {
			where = proj.Name + " in " + envName
		}
		for _, res := range quotaOrder {
			l, ok := byRes[res]
			if scope != "" && !ok {
				continue // environment lines only where it has a limit
			}
			impact := api.QuotaImpact{Resource: api.QuotaResource(res), Used: used[res] + reserved[res], Adding: add[res]}
			if scope != "" {
				impact.Environment = present.Ptr(scope)
			}
			if ok {
				impact.Limit = l.Limit
				impact.Enforcement = present.Ptr(api.QuotaImpactEnforcement(l.Enforcement))
				if l.Limit != nil && impact.Adding > 0 && impact.Used+impact.Adding > *l.Limit {
					impact.Exceeds = true
					if l.Enforcement == "hard" {
						out.Hard = append(out.Hard, fmt.Sprintf(
							"%s needs %d %s but %s has %d left. Choose a smaller size or ask a project admin to raise the quota.",
							what, impact.Adding, quotaLabel(res), where, max(*l.Limit-impact.Used, 0)))
					} else if !contains(out.OverSoft, res) {
						out.OverSoft = append(out.OverSoft, res)
					}
				}
			}
			out.Impacts = append(out.Impacts, impact)
		}
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// QuotaCheck is what adding resources does to the quotas that apply: the
// project's, in its environment, its owner's in the project, all of rvav's
// and its owner's across rvav (ADR-0014, ADR-0047). Every one must hold.
type QuotaCheck struct {
	// Impacts are the project-wide lines, then each other level's limits.
	Impacts []api.QuotaImpact
	// Hard says, per hard limit passed, what is left; the change is refused.
	Hard []string
	// OverSoft are the resources over a soft limit: the change needs approval.
	OverSoft []string
}

// quotaLevels are the levels a quota is checked at, in the order shown.
var quotaLevels = []api.QuotaLevel{api.QuotaLevelProject, api.QuotaLevelEnvironment, api.QuotaLevelProjectUser,
	api.QuotaLevelGlobal, api.QuotaLevelGlobalUser}

// quotaScope is where a change counts: its project and environment, and
// whoever owns what it builds or changes.
type quotaScope struct {
	Project      store.Project
	Env, EnvName string
	OwnerID      string
	OwnerName    string
	// You: the caller is the owner, so messages say "your".
	You bool
	// Levels are those checked; none checks them all.
	Levels []api.QuotaLevel
}

// possessive is whose quota a user level is, in a message.
func (sc quotaScope) possessive() string {
	if sc.You {
		return "your"
	}
	return sc.OwnerName + "'s"
}

// quotaLevel is one level's limits, whose use they count, and how a
// message names it.
type quotaLevel struct {
	limits      []store.QuotaLimit
	filter      store.QuotaFilter
	where, whom string
}

func (s *Service) quotaLevel(ctx context.Context, sc quotaScope, level api.QuotaLevel) (quotaLevel, error) {
	var out quotaLevel
	var err error
	proj := sc.Project
	switch level {
	case api.QuotaLevelProject:
		out.limits, err = s.Store.QuotaLimits(ctx, proj.ID, "")
		out.filter, out.where, out.whom = store.QuotaFilter{ProjectID: proj.ID}, proj.Name, "a project admin"
	case api.QuotaLevelEnvironment:
		if sc.Env != "" {
			out.limits, err = s.Store.QuotaLimits(ctx, proj.ID, sc.Env)
		}
		out.filter = store.QuotaFilter{ProjectID: proj.ID, Environment: sc.Env}
		out.where, out.whom = proj.Name+" in "+sc.EnvName, "a project admin"
	case api.QuotaLevelProjectUser:
		out.limits, err = userLimits(s.Store.UserQuotas(ctx, &proj.ID))
		out.filter = store.QuotaFilter{ProjectID: proj.ID, OwnerID: sc.OwnerID}
		out.where, out.whom = sc.possessive()+" quota in "+proj.Name, "a project admin"
	case api.QuotaLevelGlobal:
		out.limits, err = s.Store.GlobalQuotas(ctx)
		out.where, out.whom = "rvav", "an administrator"
	case api.QuotaLevelGlobalUser:
		out.limits, err = userLimits(s.Store.UserQuotas(ctx, nil))
		out.filter = store.QuotaFilter{OwnerID: sc.OwnerID}
		out.where, out.whom = sc.possessive()+" quota across rvav", "an administrator"
	}
	return out, err
}

func userLimits(list []store.UserQuota, err error) ([]store.QuotaLimit, error) {
	out := make([]store.QuotaLimit, len(list))
	for i, q := range list {
		out[i] = store.QuotaLimit{Resource: q.Resource, Limit: q.Limit, Enforcement: q.Enforcement}
	}
	return out, err
}

// quotaCheck checks adding to the levels of sc; what names the change in
// messages ("This request").
func (s *Service) quotaCheck(ctx context.Context, sc quotaScope, what string, add map[string]int) (QuotaCheck, error) {
	var out QuotaCheck
	out.Impacts = []api.QuotaImpact{}
	levels := sc.Levels
	if len(levels) == 0 {
		levels = quotaLevels
	}
	for _, level := range levels {
		lv, err := s.quotaLevel(ctx, sc, level)
		if err != nil {
			return out, err
		}
		// Beyond the project's own lines, only where there is a limit.
		if level != api.QuotaLevelProject && len(lv.limits) == 0 {
			continue
		}
		used, err := s.Store.QuotaUsedBy(ctx, lv.filter)
		if err != nil {
			return out, err
		}
		reserved, err := s.Store.QuotaReservedBy(ctx, lv.filter)
		if err != nil {
			return out, err
		}
		byRes := map[string]store.QuotaLimit{}
		for _, l := range lv.limits {
			byRes[l.Resource] = l
		}
		for _, res := range quotaOrder {
			l, ok := byRes[res]
			if level != api.QuotaLevelProject && !ok {
				continue
			}
			impact := api.QuotaImpact{Resource: api.QuotaResource(res), Level: level, Used: used[res] + reserved[res], Adding: add[res]}
			if level == api.QuotaLevelEnvironment {
				impact.Environment = present.Ptr(sc.Env)
			}
			if ok {
				impact.Limit = l.Limit
				impact.Enforcement = present.Ptr(api.QuotaImpactEnforcement(l.Enforcement))
				if l.Limit != nil && impact.Adding > 0 && impact.Used+impact.Adding > *l.Limit {
					impact.Exceeds = true
					if l.Enforcement == "hard" {
						out.Hard = append(out.Hard, fmt.Sprintf(
							"%s needs %s but %s has %d left. Choose a smaller size or ask %s to raise the quota.",
							what, quotaAmount(res, impact.Adding), lv.where, max(*l.Limit-impact.Used, 0), lv.whom))
					} else if !slices.Contains(out.OverSoft, res) {
						out.OverSoft = append(out.OverSoft, res)
					}
				}
			}
			out.Impacts = append(out.Impacts, impact)
		}
	}
	return out, nil
}

// quotaAmount is an amount of a quota resource in words: "1 VM", "4 vCPU".
func quotaAmount(res string, n int) string {
	if res == "vm_count" && n == 1 {
		return "1 VM"
	}
	return fmt.Sprintf("%d %s", n, quotaLabel(res))
}

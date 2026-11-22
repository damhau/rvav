package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

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

func (s *Service) quotaLevel(ctx context.Context, db store.DB, sc quotaScope, level api.QuotaLevel) (quotaLevel, error) {
	out := quotaLevel{where: sc.where(level), whom: "a project admin"}
	var err error
	proj := sc.Project
	switch level {
	case api.QuotaLevelProject:
		out.limits, err = s.Store.QuotaLimits(ctx, db, proj.ID, "")
		out.filter = store.QuotaFilter{ProjectID: proj.ID}
	case api.QuotaLevelEnvironment:
		if sc.Env != "" {
			out.limits, err = s.Store.QuotaLimits(ctx, db, proj.ID, sc.Env)
		}
		out.filter = store.QuotaFilter{ProjectID: proj.ID, Environment: sc.Env}
	case api.QuotaLevelProjectUser:
		out.limits, err = userLimits(s.Store.UserQuotas(ctx, db, &proj.ID))
		out.filter = store.QuotaFilter{ProjectID: proj.ID, OwnerID: sc.OwnerID}
	case api.QuotaLevelGlobal:
		out.limits, err = s.Store.GlobalQuotas(ctx, db)
		out.whom = "an administrator"
	case api.QuotaLevelGlobalUser:
		out.limits, err = userLimits(s.Store.UserQuotas(ctx, db, nil))
		out.filter = store.QuotaFilter{OwnerID: sc.OwnerID}
		out.whom = "an administrator"
	}
	return out, err
}

// where names a level of sc in a message.
func (sc quotaScope) where(level api.QuotaLevel) string {
	switch level {
	case api.QuotaLevelEnvironment:
		return sc.Project.Name + " in " + sc.EnvName
	case api.QuotaLevelProjectUser:
		return sc.possessive() + " quota in " + sc.Project.Name
	case api.QuotaLevelGlobal:
		return "rvav"
	case api.QuotaLevelGlobalUser:
		return sc.possessive() + " quota across rvav"
	}
	return sc.Project.Name
}

func userLimits(list []store.UserQuota, err error) ([]store.QuotaLimit, error) {
	out := make([]store.QuotaLimit, len(list))
	for i, q := range list {
		out[i] = store.QuotaLimit{Resource: q.Resource, Limit: q.Limit, Enforcement: q.Enforcement}
	}
	return out, err
}

// quotaCheck checks adding to the levels of sc, reading through db; what
// names the change in messages ("This request").
func (s *Service) quotaCheck(ctx context.Context, db store.DB, sc quotaScope, what string, add map[string]int) (QuotaCheck, error) {
	var out QuotaCheck
	out.Impacts = []api.QuotaImpact{}
	levels := sc.Levels
	if len(levels) == 0 {
		levels = quotaLevels
	}
	for _, level := range levels {
		lv, err := s.quotaLevel(ctx, db, sc, level)
		if err != nil {
			return out, err
		}
		// Beyond the project's own lines, only where there is a limit.
		if level != api.QuotaLevelProject && len(lv.limits) == 0 {
			continue
		}
		used, err := s.Store.QuotaUsedBy(ctx, db, lv.filter)
		if err != nil {
			return out, err
		}
		reserved, err := s.Store.QuotaReservedBy(ctx, db, lv.filter)
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

// quotaChange is what a request counts against the quotas and the
// deployment limits, checked when it is asked for and again under its
// locks.
type quotaChange struct {
	scope      quotaScope
	add        map[string]int
	what       string
	quotaField string
	// limits is nil when the deployment limits don't check the change.
	limits     *limitAsk
	limitField func(api.DeploymentLimitKind) string
}

// checkChange checks a change against the quotas and the deployment
// limits, reading through db. A hard limit passed refuses it; it returns
// the resources over a soft quota and the soft limits' warnings.
func (s *Service) checkChange(ctx context.Context, db store.DB, ch *quotaChange) ([]string, []api.PolicyWarning, error) {
	if ch == nil {
		return nil, nil, nil
	}
	q, err := s.quotaCheck(ctx, db, ch.scope, ch.what, ch.add)
	if err != nil {
		return nil, nil, err
	}
	errs := make([]api.FieldError, len(q.Hard))
	for i, m := range q.Hard {
		errs[i] = fieldErr(ch.quotaField, "quota_exceeded", m)
	}
	var c limitCheck
	if ch.limits != nil {
		if c, err = s.checkLimits(ctx, db, *ch.limits); err != nil {
			return nil, nil, err
		}
		field := ch.limitField
		if field == nil {
			field = anywhere
		}
		errs = append(errs, c.errors(field)...)
	}
	if len(errs) > 0 {
		return nil, nil, &ValidationError{Errors: errs}
	}
	return q.OverSoft, c.Soft, nil
}

// holdChange holds the change's project and owner until the transaction
// ends, then checks it again through tx: a request asked for at the same
// time may have taken what this one counted on. It must come before the
// transaction's other locks.
func (s *Service) holdChange(ctx context.Context, tx pgx.Tx, ch *quotaChange, overSoft []string, acceptAll *bool,
	listed *[]string) error {
	if ch == nil {
		return nil
	}
	if err := s.Store.LockQuotas(ctx, tx, []string{ch.scope.Project.ID}, []string{ch.scope.OwnerID}); err != nil {
		return err
	}
	again, warnings, err := s.checkChange(ctx, tx, ch)
	if err != nil {
		return err
	}
	for _, res := range again {
		if !slices.Contains(overSoft, res) {
			// Who approves it was decided without this soft quota.
			return &ConflictError{Msg: "A request asked for at the same time took what this one counted on: " +
				"it now goes over a soft quota, which needs approval. Ask for it again.", Retry: true}
		}
	}
	return accepted(warnings, acceptAll, listed)
}

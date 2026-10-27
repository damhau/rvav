package core

import (
	"context"
	"encoding/json"
	"path"
	"slices"

	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// decision is what the day-2 policies say about one action (ADR-0013).
type decision struct {
	// Denied says why the action is refused ("" when it may run).
	Denied string
	// Chain is the approval it needs, if any.
	Chain  []ChainLevel
	Policy string
}

// judge applies the day-2 policies of one deployment for one principal.
type judge struct {
	s        *Service
	p        *auth.Principal
	d        store.DeploymentRow
	env      store.Environment
	policies []store.Day2Policy
	roles    []string
}

func (s *Service) judge(ctx context.Context, p *auth.Principal, d store.DeploymentRow) (*judge, error) {
	policies, err := s.Store.Day2Policies(ctx, d.ProjectID)
	if err != nil {
		return nil, err
	}
	env, err := s.Store.GetEnvironment(ctx, d.Environment)
	if err != nil {
		return nil, err
	}
	return &judge{s: s, p: p, d: d, env: env, policies: policies, roles: day2Roles(p, d)}, nil
}

// decide finds the first day-2 policy that matches the action, the
// principal's roles on the deployment and its condition. Nothing matching
// denies. rvav itself (retention, leases) is not subject to the policies.
// inputs is nil when listing actions, before the form is filled in.
func (j *judge) decide(a *day2.Action, t day2.Target, inputs map[string]any) decision {
	p, d, roles := j.p, j.d, j.roles
	if p.User.ID == SystemUserID {
		return decision{}
	}
	vars := day2Vars(p, d, j.env, a, t, inputs, roles)
	for _, pol := range j.policies {
		if !matchesAction(pol.Actions, a.Key) || (len(pol.Roles) > 0 && !slices.ContainsFunc(pol.Roles, func(r string) bool { return slices.Contains(roles, r) })) {
			continue
		}
		match, err := policy.Day2Bool(pol.Condition, vars)
		if err != nil && inputs == nil {
			// Listing actions: the condition may need inputs the form hasn't
			// given yet. It is decided when the action runs.
			continue
		}
		if err != nil {
			// A condition that can't be evaluated must not open anything:
			// deny, and say which policy is broken.
			j.s.Log.Warn("day-2 policy could not be evaluated", "policy", pol.ID, "err", err)
			return decision{Denied: "The day-2 policy “" + pol.Name + "” could not be evaluated. Ask an administrator.", Policy: pol.ID}
		}
		if !match {
			continue
		}
		switch pol.Effect {
		case "allow":
			return decision{Policy: pol.ID}
		case "deny":
			reason := pol.Reason
			if reason == "" {
				reason = a.Name + " is not allowed here."
			}
			return decision{Denied: reason, Policy: pol.ID}
		case "approval":
			var levels []level
			_ = json.Unmarshal(pol.Levels, &levels)
			var chain []ChainLevel
			ap := store.ApprovalPolicy{ID: pol.ID, Name: pol.Name, Reason: orDefault(pol.Reason, pol.Name), Condition: pol.Condition}
			for _, l := range levels {
				chain = addLevel(chain, ap, l)
			}
			return decision{Chain: chain, Policy: pol.ID}
		}
	}
	return decision{Denied: "Your role in " + d.ProjectName + " does not allow " + a.Name + "."}
}

// reasons are why the decision needs approval.
func (dec decision) reasons() []string {
	out := []string{}
	for _, l := range dec.Chain {
		for _, r := range l.Reasons {
			if !slices.Contains(out, r) {
				out = append(out, r)
			}
		}
	}
	return out
}

// day2Roles are the principal's roles on the deployment, as day-2 policies name them.
func day2Roles(p *auth.Principal, d store.DeploymentRow) []string {
	var roles []string
	if d.OwnerID == p.User.ID {
		roles = append(roles, "owner")
	}
	if r := p.ProjectRole(d.ProjectID); r != "" {
		roles = append(roles, r)
	}
	if p.IsAdmin() {
		roles = append(roles, "admin")
	}
	return roles
}

func matchesAction(patterns []string, key string) bool {
	for _, pat := range patterns {
		if ok, _ := path.Match(pat, key); ok {
			return true
		}
	}
	return false
}

func day2Vars(p *auth.Principal, d store.DeploymentRow, env store.Environment, a *day2.Action, t day2.Target,
	inputs map[string]any, roles []string) policy.Vars {
	resource := map[string]any{"type": t.Type, "name": t.ResourceName}
	if t.Type == "vsphere.vm" {
		resource["power_state"] = string(t.VM.PowerState)
		resource["cpu_count"] = t.VM.CpuCount
		resource["memory_gb"] = t.VM.MemoryMb / 1024
	}
	return policy.Vars{
		"action":      map[string]any{"key": a.Key, "group": a.Group, "destructive": a.Destructive},
		"resource":    resource,
		"deployment":  map[string]any{"id": d.ID, "name": d.Name, "owner_id": d.OwnerID},
		"project":     map[string]any{"id": d.ProjectID, "code": d.ProjectCode, "name": d.ProjectName},
		"environment": map[string]any{"key": env.Key, "name": env.Name, "production": env.Production},
		"user":        map[string]any{"id": p.User.ID, "roles": nonNilStrings(roles), "groups": nonNilStrings(p.Groups)},
		"inputs":      inputs,
	}
}

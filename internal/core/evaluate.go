package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/naming"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Evaluation is a request draft checked against the catalog, placement,
// naming, approval, quota and lease rules. Preview returns it; Submit
// requires it to have no errors.
type Evaluation struct {
	Project    store.Project
	Env        store.Environment
	Item       store.CatalogItem
	Version    store.ItemVersion
	Blueprint  blueprint.Blueprint
	Inputs     map[string]any
	Count      int
	VM         blueprint.VMPlan
	Rule       *store.PlacementRule
	Subnet     *store.NetworkSubnet
	Naming     *naming.Template
	NamePrefix string
	Names      []string
	// names is what was learned about names, kept for Submit.
	names     *nameCheck
	Addresses []string
	Approval  api.ApprovalPreview
	// Chain is the approval levels to pass, merged across policies.
	Chain []ChainLevel
	// AutoApproved names the matching policies whose auto_approve held.
	AutoApproved []string
	QuotaAdd     map[string]int
	Quota        []api.QuotaImpact
	// OverSoft are the resources over a soft quota (approval policies see them).
	OverSoft   []string
	Lease      LeaseInfo
	OnBehalfOf *store.User
	// Fields are the form's rule results for these inputs.
	Fields api.FieldStates
	Errors []api.FieldError
}

type LeaseInfo struct {
	DefaultDays   *int
	MaxDays       *int
	Days          *int
	ExpiresAt     *time.Time
	MaxExtensions int
	ExpiryAction  string
}

var quotaOrder = []string{"vcpu", "memory_gb", "storage_gb", "vm_count"}

// Evaluate checks a draft. Not-found and permission problems are returned as
// errors; everything the user can fix is collected in Evaluation.Errors.
// strict is set on submit: unresolved policy expressions then require approval.
func (s *Service) Evaluate(ctx context.Context, p *auth.Principal, d api.RequestDraft, strict bool) (*Evaluation, error) {
	ev := &Evaluation{Inputs: map[string]any{}}
	for k, v := range d.Inputs {
		ev.Inputs[k] = v
	}
	add := func(field, code, msg string) { ev.Errors = append(ev.Errors, fieldErr(field, code, msg)) }

	var err error
	if ev.Project, err = s.visibleProject(ctx, p, d.ProjectId); err != nil {
		return nil, err
	}
	if !p.CanRequest(ev.Project.ID) {
		return nil, fmt.Errorf("%w: you can view %s but not request in it", ErrForbidden, ev.Project.Name)
	}
	env, err := s.Store.GetEnvironment(ctx, d.Environment)
	switch {
	case errors.Is(err, store.ErrNotFound):
		add("/environment", "unknown_environment", fmt.Sprintf("There is no environment %q.", d.Environment))
	case err != nil:
		return nil, err
	case !slices.Contains(ev.Project.Environments, env.Key):
		add("/environment", "not_allowed", fmt.Sprintf("%s cannot deploy to %s.", ev.Project.Name, env.Name))
	}
	ev.Env = env

	ev.Item, err = s.Store.GetItem(ctx, d.CatalogItemId, p.User.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ok, err := s.Store.IsEntitled(ctx, ev.Item.ID, ev.Project.ID); err != nil {
		return nil, err
	} else if !ok || ev.Item.PublishedVersionID == nil {
		return nil, ErrNotFound
	}
	if d.VersionId != *ev.Item.PublishedVersionID {
		add("/version_id", "stale_version", "This form is out of date. Reload it to get the current version.")
	}
	if ev.Version, err = s.Store.GetVersion(ctx, *ev.Item.PublishedVersionID); err != nil {
		return nil, err
	}
	if ev.Blueprint, err = blueprint.Parse(ev.Version.Blueprint); err != nil {
		return nil, err
	}
	if ev.Blueprint.Kind != blueprint.KindVM && ev.Blueprint.Kind != blueprint.KindAWX {
		add("", "not_available", ev.Item.Name+" cannot be requested yet.")
		return ev, nil
	}

	// Rules first: computed values are inputs like any other.
	ev.Fields = fieldStates(ev.Version.FormSchema, ev.Version.UISchema, formVars(ev.Inputs, ev.Project, ev.Env.Key, p))
	ruleErrs := applyStates(ev.Fields, ev.Inputs)
	fieldErrs, err := validateInputs(ev.Version.ID, ev.Version.FormSchema, ev.Inputs)
	if err != nil {
		return nil, err
	}
	for _, e := range ruleErrs {
		if !slices.ContainsFunc(fieldErrs, func(f api.FieldError) bool { return f.Field == e.Field }) {
			fieldErrs = append(fieldErrs, e)
		}
	}
	ev.Errors = append(ev.Errors, fieldErrs...)
	if err := s.evaluatePickers(ctx, ev, strict); err != nil {
		return nil, err
	}

	ev.Count = 1
	if d.Count != nil {
		ev.Count = *d.Count
	}
	if ev.Count < 1 || ev.Count > 20 {
		add("/count", "out_of_range", "Request between 1 and 20 at a time.")
		ev.Count = 1
	}
	if ev.Blueprint.Kind == blueprint.KindAWX && ev.Count != 1 {
		add("/count", "out_of_range", "This item runs once per request.")
		ev.Count = 1
	}

	if d.OnBehalfOf != nil && *d.OnBehalfOf != "" {
		if p.ProjectRole(ev.Project.ID) != "project-admin" {
			add("/on_behalf_of", "not_allowed", "Only project admins can request for someone else.")
		} else if u, err := s.Store.GetUser(ctx, *d.OnBehalfOf); err != nil {
			add("/on_behalf_of", "unknown_user", "This user does not exist.")
		} else {
			ev.OnBehalfOf = &u
		}
	}
	if d.ScheduledAt != nil && d.ScheduledAt.Before(time.Now().Add(-time.Minute)) {
		add("/scheduled_at", "in_past", "Pick a start time in the future.")
	}

	if ev.Blueprint.Kind == blueprint.KindVM {
		if err := s.evaluateVM(ctx, ev, strict); err != nil {
			return nil, err
		}
	}
	// Quota first: approval policies may ask approval for a soft quota.
	if err := s.evaluateQuota(ctx, ev, strict); err != nil {
		return nil, err
	}
	if err := s.evaluateApproval(ctx, p, ev, strict); err != nil {
		return nil, err
	}
	if err := s.evaluateLease(ctx, ev, d.LeaseDays); err != nil {
		return nil, err
	}
	return ev, nil
}

// evaluatePickers checks inputs picked from dns.zones, awx.inventories and
// awx.credentials against what those sources offer now: the picker alone
// doesn't stop another value. On submit a value that can't be checked is
// an error; in the preview it is skipped.
func (s *Service) evaluatePickers(ctx context.Context, ev *Evaluation, strict bool) error {
	var ui api.UiSchema
	if err := json.Unmarshal(ev.Version.UISchema, &ui); err != nil {
		return err
	}
	for name, f := range ui.Fields {
		value, _ := ev.Inputs[name].(string)
		src := f.ValueSource
		if value == "" || src == nil || src.Kind != api.Builtin || src.Name == nil {
			continue
		}
		var allowed []string
		var err error
		switch *src.Name {
		case "dns.zones":
			allowed, err = s.dnsZones(ctx)
		case "awx.inventories", "awx.credentials":
			allowed, err = s.awxChoices(ctx, *src.Name, resolveParams(src.Params, ev.Inputs))
		default:
			continue
		}
		if err != nil {
			if strict {
				return err
			}
			continue
		}
		if !slices.Contains(allowed, value) {
			ev.Errors = append(ev.Errors, fieldErr("/inputs/"+name, "not_allowed", fmt.Sprintf("%s is not one of the choices for %s.", value, f.Label)))
		}
	}
	return nil
}

func (s *Service) evaluateVM(ctx context.Context, ev *Evaluation, strict bool) error {
	add := func(field, code, msg string) { ev.Errors = append(ev.Errors, fieldErr(field, code, msg)) }
	plan, _ := ev.Blueprint.EvaluateVM(ev.Inputs)
	ev.VM = plan

	pl, err := s.placement(ctx, ev.Project.ID, ev.Env.Key)
	if err != nil {
		return err
	}
	if len(pl.rules) == 0 && ev.Env.Key != "" {
		add("", "no_placement", fmt.Sprintf("%s has no placement for %s. Ask an administrator to add one.", ev.Project.Name, ev.Env.Name))
	}
	if plan.Cluster != "" {
		if pl.cluster(plan.Cluster) == nil {
			add("/inputs/cluster", "not_allowed", fmt.Sprintf("Cluster %s is not available to %s in %s.", plan.Cluster, ev.Project.Name, ev.Env.Name))
		} else {
			ev.Rule = pl.rule(plan.Cluster)
			if plan.Datastore != "" && !slices.ContainsFunc(pl.datastoresFor(plan.Cluster), func(d store.InvDatastore) bool { return d.Name == plan.Datastore }) {
				add("/inputs/datastore", "not_allowed", fmt.Sprintf("Datastore %s cannot be used on %s.", plan.Datastore, plan.Cluster))
			}
			if plan.Network != "" && !slices.ContainsFunc(pl.networksFor(plan.Cluster), func(n store.InvNetwork) bool { return n.Name == plan.Network }) {
				add("/inputs/network", "not_allowed", fmt.Sprintf("Network %s cannot be used on %s.", plan.Network, plan.Cluster))
			}
		}
	}
	if plan.Network != "" {
		if sn, ok := pl.subnets[plan.Network]; ok {
			ev.Subnet = &sn
		} else {
			add("/inputs/network", "no_subnet", "No EfficientIP subnet is linked to this network. Ask an administrator to link one.")
		}
	}
	if templates, err := s.Store.InventoryTemplates(ctx); err != nil {
		return err
	} else if len(templates) > 0 && !slices.ContainsFunc(templates, func(t store.InvTemplate) bool { return t.Name == plan.Template }) {
		add("", "template_missing", fmt.Sprintf("Template %s was not found in vCenter. Ask a catalog admin to check this item.", plan.Template))
	}

	tpl, err := naming.Parse(ev.Blueprint.Naming)
	if err != nil {
		return err
	}
	ev.Naming = &tpl
	prefix, err := tpl.Prefix(naming.Values(ev.Project.Code, ev.Env.Key, ev.Inputs))
	if err != nil {
		return nil // inputs used by the name are still missing; the schema reports them
	}
	ev.NamePrefix = prefix
	taken, err := s.Store.InventoryVMNames(ctx, prefix)
	if err != nil {
		return err
	}
	next, err := s.Store.PeekName(ctx, prefix)
	if err != nil {
		return err
	}
	ev.names = &nameCheck{ctx: ctx, ipam: s.IPAM, vcenter: taken}
	if ev.Subnet != nil {
		ev.names.zone = ev.Subnet.DNSZone
	}
	for len(ev.Names) < ev.Count {
		name := tpl.Name(prefix, next)
		next++
		if !ev.names.taken(name) {
			ev.Names = append(ev.Names, name)
		}
	}
	if err := ev.names.err; err != nil {
		if strict {
			return err
		}
		s.Log.Warn("checking VM names in DNS", "err", err)
	}
	if len(ev.Names) > 0 && !naming.Valid(ev.Names[0]) {
		add("/inputs/role", "invalid_name", fmt.Sprintf("%s is not a valid host name.", ev.Names[0]))
	}

	if !strict && ev.Subnet != nil && s.IPAM != nil {
		if ip, err := s.IPAM.NextFree(ctx, subnetRef(*ev.Subnet)); err == nil {
			ev.Addresses = []string{ip}
		} else if errors.Is(err, efficientip.ErrSubnetFull) {
			add("/inputs/network", "subnet_full", fmt.Sprintf("Subnet %s has no free address. Pick another network.", ev.Subnet.Subnet))
		} else {
			s.Log.Warn("ipam next free address", "subnet", ev.Subnet.Subnet, "err", err)
		}
	}
	return nil
}

func subnetRef(n store.NetworkSubnet) efficientip.Subnet {
	return efficientip.Subnet{ID: n.EfficientIPSubnetID, CIDR: n.Subnet, Gateway: n.Gateway}
}

func prefixLength(cidr string) int {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return 24
	}
	return p.Bits()
}

// PolicyVars are the CEL variables approval conditions can use.
func PolicyVars(ev *Evaluation, p *auth.Principal) policy.Vars {
	return policy.Vars{
		"inputs":      ev.Inputs,
		"environment": ev.Env.Key,
		"project":     map[string]any{"id": ev.Project.ID, "code": ev.Project.Code, "name": ev.Project.Name},
		"item":        map[string]any{"slug": ev.Item.Slug, "category": ev.Item.Category, "type": ev.Item.Type},
		"vm":          map[string]any{"vcpu": ev.VM.VCPU, "memory_gb": ev.VM.MemoryGB, "disk_gb": ev.VM.DiskGB, "count": ev.Count},
		"user":        map[string]any{"id": p.User.ID, "roles": p.GlobalRoles, "groups": nonNilStrings(p.Groups)},
		"quota":       quotaVars(ev.OverSoft),
	}
}

// quotaVars is what policies see of the quota: whether a soft limit is
// passed, and on which resources.
func quotaVars(overSoft []string) map[string]any {
	return map[string]any{"over_soft": len(overSoft) > 0, "over": nonNilStrings(overSoft)}
}

// level is one level of an approval policy, as stored in its levels JSON.
type level struct {
	Name      string   `json:"name"`
	Group     string   `json:"group"`
	Approvers []string `json:"approvers"`
	Mode      string   `json:"mode"`
	// Timeout is a Go duration such as "48h"; empty for none.
	Timeout    string `json:"timeout"`
	OnTimeout  string `json:"on_timeout"`
	EscalateTo *struct {
		Group     string   `json:"group"`
		Approvers []string `json:"approvers"`
	} `json:"escalate_to"`
}

// ChainLevel is one level a request must pass, possibly from several
// policies (ADR-0011).
type ChainLevel struct {
	Name          string
	Group         string
	Approvers     []string
	Mode          string
	PolicyIDs     []string
	Reasons       []string
	Rules         []string
	Timeout       time.Duration
	OnTimeout     string
	EscalateGroup string
	EscalateIDs   []string
}

// addLevel merges a policy level into the chain: levels with the same
// approvers become one, keeping every reason, the stricter mode and the
// shorter timeout.
func addLevel(chain []ChainLevel, pol store.ApprovalPolicy, l level) []ChainLevel {
	timeout, _ := time.ParseDuration(l.Timeout)
	in := ChainLevel{Name: l.Name, Group: l.Group, Approvers: l.Approvers, Mode: orDefault(l.Mode, "any"),
		PolicyIDs: []string{pol.ID}, Reasons: []string{pol.Reason}, Rules: []string{pol.Condition}}
	if timeout > 0 {
		in.Timeout, in.OnTimeout = timeout, orDefault(l.OnTimeout, "reject")
		if l.EscalateTo != nil {
			in.EscalateGroup, in.EscalateIDs = l.EscalateTo.Group, l.EscalateTo.Approvers
		}
	}
	key := func(c ChainLevel) string {
		ids := slices.Clone(c.Approvers)
		slices.Sort(ids)
		return c.Group + "|" + strings.Join(ids, ",")
	}
	for i := range chain {
		c := &chain[i]
		if key(*c) != key(in) {
			continue
		}
		c.PolicyIDs = append(c.PolicyIDs, pol.ID)
		if !slices.Contains(c.Reasons, pol.Reason) {
			c.Reasons = append(c.Reasons, pol.Reason)
		}
		c.Rules = append(c.Rules, pol.Condition)
		if in.Mode == "all" {
			c.Mode = "all"
		}
		mergeTimeout(c, in)
		return chain
	}
	return append(chain, in)
}

// mergeTimeout keeps the shorter timeout with the strictest action (reject,
// then escalate, then approve), so merging never weakens a policy: a level
// with no timeout never approves by itself, so it drops the other's approve.
func mergeTimeout(c *ChainLevel, in ChainLevel) {
	rank := map[string]int{"approve": 0, "escalate": 1, "reject": 2}
	switch {
	case in.Timeout == 0 && c.Timeout == 0:
		return
	case in.Timeout == 0 || c.Timeout == 0:
		timed := *c
		if c.Timeout == 0 {
			timed = in
		}
		if timed.OnTimeout == "approve" {
			c.Timeout, c.OnTimeout, c.EscalateGroup, c.EscalateIDs = 0, "", "", nil
			return
		}
		c.Timeout, c.OnTimeout, c.EscalateGroup, c.EscalateIDs = timed.Timeout, timed.OnTimeout, timed.EscalateGroup, timed.EscalateIDs
	default:
		strict := *c
		if rank[in.OnTimeout] > rank[c.OnTimeout] {
			strict = in
		}
		c.Timeout = min(c.Timeout, in.Timeout)
		c.OnTimeout, c.EscalateGroup, c.EscalateIDs = strict.OnTimeout, strict.EscalateGroup, strict.EscalateIDs
	}
}

func (s *Service) evaluateApproval(ctx context.Context, p *auth.Principal, ev *Evaluation, strict bool) error {
	policies, err := s.Store.ApprovalPolicies(ctx, ev.Project.ID, ev.Item.Type)
	if err != nil {
		return err
	}
	vars := PolicyVars(ev, p)
	ev.Approval = api.ApprovalPreview{Policies: []struct {
		Levels []struct {
			Approvers []api.UserRef                          `json:"approvers"`
			Mode      *api.ApprovalPreviewPoliciesLevelsMode `json:"mode,omitempty"`
			Name      *string                                `json:"name,omitempty"`
		} `json:"levels"`
		Name     string `json:"name"`
		PolicyId string `json:"policy_id"`
		Reason   string `json:"reason"`
	}{}}
	ev.Chain, ev.AutoApproved = nil, nil
	for _, pol := range policies {
		match, err := policy.Bool(pol.Condition, vars)
		if err != nil {
			if !strict {
				continue // inputs the condition needs are not filled in yet
			}
			s.Log.Warn("approval policy could not be evaluated, requiring approval", "policy", pol.ID, "err", err)
			match = true
		}
		if !match {
			continue
		}
		if pol.AutoApprove != nil && *pol.AutoApprove != "" {
			// An expression that fails to evaluate approves nothing.
			if auto, err := policy.Bool(*pol.AutoApprove, vars); err == nil && auto {
				ev.AutoApproved = append(ev.AutoApproved, pol.Name)
				continue
			}
		}
		var levels []level
		_ = json.Unmarshal(pol.Levels, &levels)
		var ids []string
		for _, l := range levels {
			ids = append(ids, l.Approvers...)
			ev.Chain = addLevel(ev.Chain, pol, l)
		}
		users, err := s.Store.FindUsers(ctx, ids)
		if err != nil {
			return err
		}
		entry := struct {
			Levels []struct {
				Approvers []api.UserRef                          `json:"approvers"`
				Mode      *api.ApprovalPreviewPoliciesLevelsMode `json:"mode,omitempty"`
				Name      *string                                `json:"name,omitempty"`
			} `json:"levels"`
			Name     string `json:"name"`
			PolicyId string `json:"policy_id"`
			Reason   string `json:"reason"`
		}{Name: pol.Name, PolicyId: pol.ID, Reason: pol.Reason}
		for _, l := range levels {
			lv := struct {
				Approvers []api.UserRef                          `json:"approvers"`
				Mode      *api.ApprovalPreviewPoliciesLevelsMode `json:"mode,omitempty"`
				Name      *string                                `json:"name,omitempty"`
			}{Approvers: []api.UserRef{}, Name: present.Ptr(l.Name)}
			if l.Mode != "" {
				lv.Mode = present.Ptr(api.ApprovalPreviewPoliciesLevelsMode(l.Mode))
			}
			for _, id := range l.Approvers {
				if u, ok := users[id]; ok {
					lv.Approvers = append(lv.Approvers, present.UserRef(u.ID, u.Name, u.Email))
				}
			}
			entry.Levels = append(entry.Levels, lv)
		}
		ev.Approval.Policies = append(ev.Approval.Policies, entry)
	}
	ev.Approval.Required = len(ev.Chain) > 0
	return nil
}

func (s *Service) evaluateQuota(ctx context.Context, ev *Evaluation, _ bool) error {
	ev.QuotaAdd = map[string]int{"vcpu": 0, "memory_gb": 0, "storage_gb": 0, "vm_count": 0}
	if ev.Blueprint.Kind == blueprint.KindVM {
		ev.QuotaAdd = map[string]int{
			"vcpu": ev.VM.VCPU * ev.Count, "memory_gb": ev.VM.MemoryGB * ev.Count,
			"storage_gb": ev.VM.DiskGB * ev.Count, "vm_count": ev.Count,
		}
	}
	q, err := s.quotaCheck(ctx, ev.Project, ev.Env.Key, ev.Env.Name, "This request", ev.QuotaAdd)
	if err != nil {
		return err
	}
	ev.Quota, ev.OverSoft = q.Impacts, q.OverSoft
	for _, msg := range q.Hard {
		ev.Errors = append(ev.Errors, fieldErr("", "quota_exceeded", msg))
	}
	return nil
}

func quotaLabel(res string) string {
	switch res {
	case "vcpu":
		return "vCPU"
	case "memory_gb":
		return "GB of memory"
	case "storage_gb":
		return "GB of storage"
	}
	return "VMs"
}

func (s *Service) evaluateLease(ctx context.Context, ev *Evaluation, requested *int) error {
	if ev.Blueprint.Kind != blueprint.KindVM || ev.Env.Key == "" {
		return nil
	}
	pol, err := s.Store.LeasePolicy(ctx, ev.Project.ID, ev.Env.Key)
	if err != nil {
		return err
	}
	ev.Lease = LeaseInfo{DefaultDays: pol.DefaultDays, MaxDays: pol.MaxDays, MaxExtensions: pol.MaxExtensions, ExpiryAction: pol.ExpiryAction}
	days := pol.DefaultDays
	if requested != nil {
		days = requested
	}
	if days != nil && pol.MaxDays != nil && *days > *pol.MaxDays {
		ev.Errors = append(ev.Errors, fieldErr("/lease_days", "too_long",
			fmt.Sprintf("Leases in %s can be at most %d days.", ev.Env.Name, *pol.MaxDays)))
	}
	if days != nil && *days < 1 {
		ev.Errors = append(ev.Errors, fieldErr("/lease_days", "too_short", "A lease is at least one day."))
	}
	ev.Lease.Days = days
	if days != nil {
		t := time.Now().UTC().Add(time.Duration(*days) * 24 * time.Hour).Truncate(time.Second)
		ev.Lease.ExpiresAt = &t
	}
	return nil
}

// Preview turns an evaluation into the API response.
func (ev *Evaluation) Preview() api.RequestPreview {
	sort.SliceStable(ev.Errors, func(i, j int) bool { return ev.Errors[i].Field < ev.Errors[j].Field })
	out := api.RequestPreview{
		Valid:    len(ev.Errors) == 0,
		Errors:   append([]api.FieldError{}, ev.Errors...),
		Quota:    ev.Quota,
		Approval: ev.Approval,
		Fields:   &ev.Fields,
	}
	if out.Quota == nil {
		out.Quota = []api.QuotaImpact{}
	}
	if ev.Names != nil {
		out.Names = present.Ptr(ev.Names)
	}
	if ev.Addresses != nil {
		out.Addresses = present.Ptr(ev.Addresses)
	}
	if ev.Blueprint.Kind == blueprint.KindVM {
		pl := &struct {
			Cluster   *string `json:"cluster,omitempty"`
			Datastore *string `json:"datastore,omitempty"`
			Folder    *string `json:"folder,omitempty"`
			Network   *string `json:"network,omitempty"`
			Subnet    *string `json:"subnet,omitempty"`
		}{}
		set := func(s string) *string {
			if s == "" {
				return nil
			}
			return &s
		}
		pl.Cluster, pl.Datastore, pl.Network = set(ev.VM.Cluster), set(ev.VM.Datastore), set(ev.VM.Network)
		if ev.Rule != nil {
			pl.Folder = set(ev.Rule.Folder)
		}
		if ev.Subnet != nil {
			pl.Subnet = set(ev.Subnet.Subnet)
		}
		out.Placement = pl
		out.Lease = &struct {
			DefaultDays *int       `json:"default_days,omitempty"`
			ExpiresAt   *time.Time `json:"expires_at,omitempty"`
			MaxDays     *int       `json:"max_days,omitempty"`
		}{DefaultDays: ev.Lease.DefaultDays, MaxDays: ev.Lease.MaxDays, ExpiresAt: ev.Lease.ExpiresAt}
	}
	return out
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

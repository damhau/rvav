package core

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vraimport"
)

// adoptNSX takes over the NSX segments and security groups vRA made for a
// deployment it adopted (ADR-0028): found by name, tagged as rvav's and the
// deployment's, and recorded on it, so that rvav deletes them with it. A
// group's security policies come with it when their rules name only the
// deployment's groups; a group other rules use is left as it is, as NSX
// wouldn't delete it.
func (s *Service) adoptNSX(ctx context.Context, depID string, pl vraimport.DeploymentPlan) ([]vraimport.Note, error) {
	var notes []vraimport.Note
	note := func(level, where, text string) {
		notes = append(notes, vraimport.Note{Level: level, Where: where, Text: text})
	}
	if len(pl.NSXSegments)+len(pl.NSXGroups) == 0 {
		return nil, nil
	}
	if !s.Config.NSX.Configured() {
		note(vraimport.Warning, "nsx", "rvav has no NSX Manager: the deployment's NSX segments and groups stay as vRA left them, and aren't deleted with it.")
		return notes, nil
	}
	client, err := s.nsx()
	if err != nil {
		return nil, err
	}
	instance := cmp.Or(s.Config.IPAM.Instance, "rvav")
	owner := []nsx.Tag{{Scope: nsx.OwnerScope, Tag: instance}, {Scope: nsx.DeployScope, Tag: depID}}
	withOwner := func(tags []nsx.Tag) []nsx.Tag {
		out := slices.DeleteFunc(slices.Clone(tags), func(t nsx.Tag) bool { return t.Scope == nsx.OwnerScope || t.Scope == nsx.DeployScope })
		return append(out, owner...)
	}
	record := func(typ, name, external string, props map[string]any) error {
		raw, _ := json.Marshal(props)
		_, err := s.Store.UpsertResource(ctx, s.Store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: depID, Type: typ,
			Name: name, ExternalID: &external, Properties: raw})
		return err
	}
	upstream := func(err error) error { return &UpstreamError{System: "NSX", Err: err} }
	rvavs := func(tags []nsx.Tag) bool {
		return slices.ContainsFunc(tags, func(t nsx.Tag) bool { return t.Scope == nsx.OwnerScope || t.Scope == nsx.DeployScope })
	}
	// one is the object of a name, when exactly one has it: display names
	// aren't unique in NSX.
	one := func(kind, name string, n int) bool {
		switch n {
		case 0:
			note(vraimport.Warning, "nsx", "NSX has no "+kind+" "+name+": not adopted.")
		case 1:
			return true
		default:
			note(vraimport.Warning, "nsx", fmt.Sprintf("NSX has %d %ss named %s: rvav can't tell which is the deployment's, none is adopted.", n, kind, name))
		}
		return false
	}

	if len(pl.NSXSegments) > 0 {
		segs, err := client.Segments(ctx)
		if err != nil {
			return nil, upstream(err)
		}
		for _, name := range pl.NSXSegments {
			named := slices.DeleteFunc(slices.Clone(segs), func(sg nsx.Segment) bool { return sg.DisplayName != name && sg.ID != name })
			if !one("segment", name, len(named)) {
				continue
			}
			sg := named[0]
			if rvavs(sg.Tags) {
				note(vraimport.Warning, "nsx", "NSX segment "+name+" is already rvav's: left as it is.")
				continue
			}
			// Only its tags: PATCH keeps the rest as vRA made it.
			if err := client.Tag(ctx, nsx.SegmentPath, sg.ID, withOwner(sg.Tags)); err != nil {
				return nil, upstream(err)
			}
			props := map[string]any{"segment_id": sg.ID, "tier1": strings.TrimPrefix(sg.ConnectivityPath, nsx.Tier1Path)}
			if len(sg.Subnets) > 0 {
				props["gateway"], props["subnet"] = sg.Subnets[0].GatewayAddress, sg.Subnets[0].Network
			}
			if err := record("nsx.segment", sg.DisplayName, sg.ID, props); err != nil {
				return nil, err
			}
			note(vraimport.Info, "nsx", "NSX segment "+name+" adopted: rvav deletes it with the deployment. Its subnet stays in EfficientIP.")
		}
	}
	if len(pl.NSXGroups) == 0 {
		return notes, nil
	}
	groups, err := client.Groups(ctx)
	if err != nil {
		return nil, upstream(err)
	}
	policies, err := client.SecurityPolicies(ctx)
	if err != nil {
		return nil, upstream(err)
	}
	var found []nsx.Group
	paths := map[string]nsx.Group{}
	for _, name := range pl.NSXGroups {
		named := slices.DeleteFunc(slices.Clone(groups), func(g nsx.Group) bool { return g.DisplayName != name && g.ID != name })
		if !one("group", name, len(named)) {
			continue
		}
		if rvavs(named[0].Tags) {
			note(vraimport.Warning, "nsx", "NSX group "+name+" is already rvav's: left as it is.")
			continue
		}
		found = append(found, named[0])
		paths[nsx.GroupPath+named[0].ID] = named[0]
	}
	// The policies whose rules name the groups: the deployment's when they
	// name no other group, and the account that made its groups (vRA's)
	// made them too; not one the security team wrote for the application.
	refs := func(r nsx.Rule) []string {
		return slices.DeleteFunc(slices.Concat(r.SourceGroups, r.DestinationGroups, r.Scope), func(p string) bool { return p == "ANY" })
	}
	shared := map[string]string{} // group path → a policy that isn't the deployment's
	var ours []nsx.SecurityPolicy
	for _, pol := range policies {
		names, others := false, false
		mine := pol.CreateUser != ""
		for _, r := range pol.Rules {
			for _, p := range refs(r) {
				g, ok := paths[p]
				names = names || ok
				others = others || !ok
				mine = mine && (!ok || g.CreateUser == pol.CreateUser)
			}
		}
		switch {
		case names && (others || !mine):
			for _, r := range pol.Rules {
				for _, p := range refs(r) {
					if _, ok := paths[p]; ok {
						shared[p] = pol.ID
					}
				}
			}
		case names:
			ours = append(ours, pol)
		}
	}
	for _, g := range found {
		if pol, ok := shared[nsx.GroupPath+g.ID]; ok {
			note(vraimport.Warning, "nsx", "NSX group "+g.DisplayName+" is also used by security policy "+pol+
				", which isn't the deployment's: left as it is, and not deleted with the deployment.")
			continue
		}
		if err := client.Tag(ctx, nsx.GroupPath, g.ID, withOwner(g.Tags)); err != nil {
			return nil, upstream(err)
		}
		if err := record("nsx.group", g.ID, g.ID, map[string]any{"group_id": g.ID, "title": g.DisplayName}); err != nil {
			return nil, err
		}
		note(vraimport.Info, "nsx", "NSX group "+g.DisplayName+" adopted: rvav deletes it with the deployment. Its VMs stay in it as vRA put them.")
	}
	for _, pol := range ours {
		if slices.ContainsFunc(pol.Rules, func(r nsx.Rule) bool {
			return slices.ContainsFunc(refs(r), func(p string) bool { _, ok := shared[p]; return ok })
		}) || rvavs(pol.Tags) {
			continue
		}
		if err := client.Tag(ctx, nsx.PolicyPath, pol.ID, withOwner(pol.Tags)); err != nil {
			return nil, upstream(err)
		}
		if err := record("nsx.policy", pol.ID, pol.ID, map[string]any{"policy_id": pol.ID, "rules": pol.Rules}); err != nil {
			return nil, err
		}
		note(vraimport.Info, "nsx", "The firewall rules of "+pol.DisplayName+" adopted: rvav deletes them with the deployment.")
	}
	return notes, nil
}

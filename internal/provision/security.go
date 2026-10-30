package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/store"
)

// NSX security groups (ADR-0028): a deployment's groups and the security
// policy of the firewall rules into them, made before its VMs; each VM
// tagged once NSX sees it, so that it joins its groups; deleted after the
// VMs.

// SecurityInput is a deployment's groups and rules to make.
type SecurityInput struct {
	RequestID, ProjectID, Key string
	Security                  SecurityUnit
}

// Membership is what a VM was given to join its NSX groups: the groups, and
// the tags rvav set on it (drift is measured from them); Offered are the
// groups its item lets it join, by a day-2 action.
type Membership struct {
	VM           string    `json:"vm"`
	InstanceUUID string    `json:"instance_uuid"`
	Groups       []VMGroup `json:"groups"`
	Tags         []nsx.Tag `json:"tags"`
	Offered      []VMGroup `json:"offered,omitempty"`
}

// ownerTags are the tags of everything rvav makes for a deployment.
func (a *Activities) ownerTags(deploymentID string) []nsx.Tag {
	return []nsx.Tag{{Scope: nsx.OwnerScope, Tag: a.instance()}, {Scope: nsx.DeployScope, Tag: deploymentID}}
}

func (a *Activities) nsxTimeout() time.Duration {
	if a.NSXTimeout > 0 {
		return a.NSXTimeout
	}
	return 5 * time.Minute
}

// MakeSecurity makes a deployment's groups, then the security policy of
// its rules, or updates what an earlier run made. It records each on the
// deployment as soon as it exists, so a request that fails later still
// deletes them.
func (a *Activities) MakeSecurity(ctx context.Context, in SecurityInput) error {
	return a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		u := in.Security
		if a.NSX == nil {
			return "", "", permanent("rvav has no NSX Manager: set one in Integrations")
		}
		refused := func(what string, err error) error {
			var apiErr *nsx.APIError
			if errors.As(err, &apiErr) && apiErr.Status == 400 {
				return permanent("NSX refused %s: %s", what, apiErr.Message)
			}
			return fmt.Errorf("NSX: %w", err)
		}
		var paths, names []string
		for _, g := range u.Groups {
			// A group of that ID rvav didn't make is in the way.
			existing, err := a.NSX.Group(ctx, g.ID)
			switch {
			case err == nil && !nsx.Owned(existing.Tags, a.instance()):
				return "", "", permanent("NSX has a group %s that rvav didn't make", g.ID)
			case err != nil && !errors.Is(err, nsx.ErrNotFound):
				return "", "", fmt.Errorf("NSX: %w", err)
			}
			if err := a.NSX.PutGroup(ctx, nsx.Group{ID: g.ID, DisplayName: g.ID, Expression: []nsx.Condition{nsx.TagCondition(g.Tag)},
				Tags: a.ownerTags(u.DeploymentID)}); err != nil {
				return "", "", refused("group "+g.ID, err)
			}
			if err := a.recordNSXResource(ctx, in.RequestID, in.ProjectID, u.DeploymentID, "nsx.group", g.ID, map[string]any{
				"group_id": g.ID, "key": g.Key, "title": g.Title, "tag": g.Tag}); err != nil {
				return "", "", err
			}
			paths, names = append(paths, nsx.GroupPath+g.ID), append(names, g.ID)
		}
		detail := strings.Join(names, ", ")
		if len(u.Rules) == 0 {
			return "Made NSX security groups", detail, nil
		}
		existing, err := a.NSX.SecurityPolicy(ctx, u.Policy)
		switch {
		case err == nil && !nsx.Owned(existing.Tags, a.instance()):
			return "", "", permanent("NSX has a security policy %s that rvav didn't make", u.Policy)
		case err != nil && !errors.Is(err, nsx.ErrNotFound):
			return "", "", fmt.Errorf("NSX: %w", err)
		}
		if err := a.NSX.PutSecurityPolicy(ctx, nsx.SecurityPolicy{ID: u.Policy, DisplayName: u.Policy, Category: "Application",
			Scope: paths, Rules: u.Rules, Tags: a.ownerTags(u.DeploymentID)}); err != nil {
			return "", "", refused("the firewall rules of "+u.Policy, err)
		}
		if err := a.recordNSXResource(ctx, in.RequestID, in.ProjectID, u.DeploymentID, "nsx.policy", u.Policy, map[string]any{
			"policy_id": u.Policy, "rules": u.Rules}); err != nil {
			return "", "", err
		}
		return "Made NSX security groups", fmt.Sprintf("%s · %d firewall rule%s in %s", detail, len(u.Rules), plural(len(u.Rules)), u.Policy), nil
	})
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// JoinGroups tags a VM so that it is in its NSX groups, once NSX has seen
// it in vCenter: the tags of its groups, and rvav's own; tags others set
// are kept.
func (a *Activities) JoinGroups(ctx context.Context, in VMInput) error {
	return a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		if a.NSX == nil {
			return "", "", permanent("rvav has no NSX Manager: set one in Integrations")
		}
		res := a.findResource(ctx, in.RequestID, in.VM.DeploymentID, "vsphere.vm", in.VM.Name)
		if res == nil || res.ExternalID == nil {
			return "", "", fmt.Errorf("VM %s isn't recorded yet", in.VM.Name)
		}
		uuid := *res.ExternalID
		vm, err := a.waitForVM(ctx, uuid, in.VM.Name)
		if err != nil {
			return "", "", err
		}
		want := a.ownerTags(in.VM.DeploymentID)
		for _, g := range in.VM.NSXGroups {
			want = append(want, g.Tags...)
		}
		want = uniqueTags(want)
		if err := a.NSX.SetVMTags(ctx, uuid, withTags(vm.Tags, want)); err != nil {
			return "", "", fmt.Errorf("NSX: %w", err)
		}
		m := Membership{VM: in.VM.Name, InstanceUUID: uuid, Groups: in.VM.NSXGroups, Tags: want, Offered: in.VM.NSXOffered}
		if err := a.recordMembership(ctx, in.RequestID, in.ProjectID, in.VM.DeploymentID, m); err != nil {
			return "", "", err
		}
		return "Joined NSX security groups", in.VM.groupNames(), nil
	})
}

// changeGroups puts a VM in one more of the groups its item offers, or
// takes it out of one (a day-2 action): the group's tags added, or those no
// other group of the VM needs removed.
func (a *Activities) changeGroups(ctx context.Context, in OpInput) (string, string, error) {
	if a.NSX == nil {
		return "", "", permanent("rvav has no NSX Manager: set one in Integrations")
	}
	res, err := a.Store.GetResource(ctx, argStr(in.Op.Args, "membership"))
	if err != nil || res.Type != "nsx.membership" {
		return "", "", permanent("The VM isn't in NSX security groups.")
	}
	var m Membership
	_ = json.Unmarshal(res.Properties, &m)
	id := argStr(in.Op.Args, "group")
	has := func(list []VMGroup) int { return slices.IndexFunc(list, func(g VMGroup) bool { return g.ID == id }) }
	join := in.Op.Op == "nsx_join"
	switch {
	case join && has(m.Offered) < 0:
		return "", "", permanent("The VM's item doesn't offer NSX group %s.", id)
	case join && has(m.Groups) >= 0, !join && has(m.Groups) < 0:
		return "", "", nil // already so
	}
	vm, err := a.NSX.VM(ctx, m.InstanceUUID)
	if errors.Is(err, nsx.ErrNotFound) {
		return "", "", permanent("NSX no longer has VM %s.", m.VM)
	} else if err != nil {
		return "", "", fmt.Errorf("NSX: %w", err)
	}
	var g VMGroup
	if join {
		g = m.Offered[has(m.Offered)]
		m.Groups = append(m.Groups, g)
	} else {
		g = m.Groups[has(m.Groups)]
		m.Groups = slices.Delete(m.Groups, has(m.Groups), has(m.Groups)+1)
	}
	want := a.ownerTags(in.DeploymentID)
	for _, x := range m.Groups {
		want = append(want, x.Tags...)
	}
	want = uniqueTags(want)
	// What rvav set and no longer needs goes; what it needs is added.
	tags := withTags(withoutTags(vm.Tags, withoutTags(m.Tags, want)), want)
	if err := a.NSX.SetVMTags(ctx, m.InstanceUUID, tags); err != nil {
		return "", "", fmt.Errorf("NSX: %w", err)
	}
	m.Tags = want
	if err := a.recordMembership(ctx, in.RequestID, in.ProjectID, in.DeploymentID, m); err != nil {
		return "", "", err
	}
	if join {
		return "", "In NSX group " + g.Name, nil
	}
	return "", "No longer in NSX group " + g.Name, nil
}

// waitForVM waits until NSX has seen a VM in vCenter.
func (a *Activities) waitForVM(ctx context.Context, uuid, name string) (nsx.VM, error) {
	deadline := time.Now().Add(a.nsxTimeout())
	for {
		vm, err := a.NSX.VM(ctx, uuid)
		if err == nil {
			return vm, nil
		}
		if !errors.Is(err, nsx.ErrNotFound) {
			return nsx.VM{}, fmt.Errorf("NSX: %w", err)
		}
		if time.Now().After(deadline) {
			return nsx.VM{}, fmt.Errorf("NSX hasn't seen VM %s in vCenter after %s", name, a.nsxTimeout())
		}
		select {
		case <-ctx.Done():
			return nsx.VM{}, ctx.Err()
		case <-time.After(a.pollInterval()):
		}
	}
}

// withTags is a VM's tags with want added, each once.
func withTags(have, want []nsx.Tag) []nsx.Tag {
	out := slices.Clone(have)
	for _, t := range want {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// withoutTags is a VM's tags without drop.
func withoutTags(have, drop []nsx.Tag) []nsx.Tag {
	return slices.DeleteFunc(slices.Clone(have), func(t nsx.Tag) bool { return slices.Contains(drop, t) })
}

func uniqueTags(tags []nsx.Tag) []nsx.Tag {
	var out []nsx.Tag
	for _, t := range tags {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// recordMembership records, or updates, what a VM was given to join its
// groups.
func (a *Activities) recordMembership(ctx context.Context, requestID, projectID, deploymentID string, m Membership) error {
	p, _ := json.Marshal(m)
	r := store.Resource{ID: ids.New(ids.Resource), DeploymentID: deploymentID, RequestID: &requestID, Type: "nsx.membership",
		Name: m.VM, ExternalID: &m.InstanceUUID, Properties: p}
	if old := a.findDeploymentResource(ctx, deploymentID, "nsx.membership", m.VM); old != nil {
		r.ID, r.RequestID = old.ID, old.RequestID
	}
	res, err := a.Store.UpsertResource(ctx, a.Store.Pool, r)
	if err != nil {
		return err
	}
	a.Events.Resource(ctx, projectID, res)
	return nil
}

// findDeploymentResource is a deployment's resource of a type and name,
// whichever request made it.
func (a *Activities) findDeploymentResource(ctx context.Context, deploymentID, typ, name string) *store.Resource {
	list, err := a.Store.ListResources(ctx, deploymentID)
	if err != nil {
		return nil
	}
	for i := range list {
		if list[i].Type == typ && list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// recordNSXResource records a group or policy on the deployment, once.
func (a *Activities) recordNSXResource(ctx context.Context, requestID, projectID, deploymentID, typ, id string, props map[string]any) error {
	if a.findResource(ctx, requestID, deploymentID, typ, id) != nil {
		return nil
	}
	p, _ := json.Marshal(props)
	res, err := a.Store.UpsertResource(ctx, a.Store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: deploymentID,
		RequestID: &requestID, Type: typ, Name: id, ExternalID: &id, Properties: p})
	if err != nil {
		return err
	}
	a.Events.Resource(ctx, projectID, res)
	return nil
}

// leaveGroups takes the tags rvav set off a VM, if NSX still has it.
func (a *Activities) leaveGroups(ctx context.Context, r store.Resource) (string, error) {
	if a.NSX == nil {
		return "", permanent("rvav has no NSX Manager: VM %s can't leave its groups", r.Name)
	}
	var m Membership
	_ = json.Unmarshal(r.Properties, &m)
	vm, err := a.NSX.VM(ctx, m.InstanceUUID)
	switch {
	case errors.Is(err, nsx.ErrNotFound):
		return "", nil // gone with the VM
	case err != nil:
		return "", fmt.Errorf("NSX: %w", err)
	}
	if err := a.NSX.SetVMTags(ctx, m.InstanceUUID, withoutTags(vm.Tags, m.Tags)); err != nil {
		return "", fmt.Errorf("NSX: %w", err)
	}
	return "took " + r.Name + " out of its NSX groups", nil
}

// deleteGroup deletes a group rvav made; one it didn't is left alone.
func (a *Activities) deleteGroup(ctx context.Context, r store.Resource) (string, error) {
	if a.NSX == nil {
		return "", permanent("rvav has no NSX Manager: group %s can't be deleted", r.Name)
	}
	g, err := a.NSX.Group(ctx, r.Name)
	switch {
	case errors.Is(err, nsx.ErrNotFound):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("NSX: %w", err)
	case !nsx.Owned(g.Tags, a.instance()):
		return "", permanent("group %s isn't rvav's in NSX any more: left alone", r.Name)
	}
	if err := a.NSX.DeleteGroup(ctx, r.Name); err != nil {
		return "", fmt.Errorf("NSX: %w", err)
	}
	return "deleted NSX group " + r.Name, nil
}

// deletePolicy deletes the security policy of a deployment's rules.
func (a *Activities) deletePolicy(ctx context.Context, r store.Resource) (string, error) {
	if a.NSX == nil {
		return "", permanent("rvav has no NSX Manager: the firewall rules of %s can't be deleted", r.Name)
	}
	p, err := a.NSX.SecurityPolicy(ctx, r.Name)
	switch {
	case errors.Is(err, nsx.ErrNotFound):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("NSX: %w", err)
	case !nsx.Owned(p.Tags, a.instance()):
		return "", permanent("security policy %s isn't rvav's in NSX any more: left alone", r.Name)
	}
	if err := a.NSX.DeleteSecurityPolicy(ctx, r.Name); err != nil {
		return "", fmt.Errorf("NSX: %w", err)
	}
	return "deleted the firewall rules of " + r.Name, nil
}

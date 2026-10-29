package vsphere

import (
	"context"
	"errors"
	"fmt"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

// DRS rules keeping a blueprint component's VMs on separate hosts
// (ADR-0024).

func (cl *Client) cluster(ctx context.Context, name string) (*object.ClusterComputeResource, error) {
	c, err := cl.client(ctx)
	if err != nil {
		return nil, err
	}
	ref, err := findRef(ctx, c, "ClusterComputeResource", name)
	if err != nil {
		return nil, err
	}
	return object.NewClusterComputeResource(c, ref), nil
}

// ruleNamed returns the cluster's rule with that name, or nil.
func ruleNamed(ctx context.Context, cc *object.ClusterComputeResource, name string) (types.BaseClusterRuleInfo, error) {
	cfg, err := cc.Configuration(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range cfg.Rule {
		if r.GetClusterRuleInfo().Name == name {
			return r, nil
		}
	}
	return nil, nil
}

// SetAntiAffinityRule creates the cluster's VM anti-affinity rule name
// with these VMs, or makes the existing one keep exactly them.
func (cl *Client) SetAntiAffinityRule(ctx context.Context, cluster, name string, morefs []string) error {
	cc, err := cl.cluster(ctx, cluster)
	if err != nil {
		return err
	}
	existing, err := ruleNamed(ctx, cc, name)
	if err != nil {
		return err
	}
	info := &types.ClusterAntiAffinityRuleSpec{ClusterRuleInfo: types.ClusterRuleInfo{Name: name, Enabled: types.NewBool(true)}}
	for _, m := range morefs {
		info.Vm = append(info.Vm, vmRef(m))
	}
	op := types.ArrayUpdateOperationAdd
	if existing != nil {
		info.Key, info.RuleUuid = existing.GetClusterRuleInfo().Key, existing.GetClusterRuleInfo().RuleUuid
		op = types.ArrayUpdateOperationEdit
	}
	spec := &types.ClusterConfigSpecEx{RulesSpec: []types.ClusterRuleSpec{{
		ArrayUpdateSpec: types.ArrayUpdateSpec{Operation: op}, Info: info}}}
	task, err := cc.Reconfigure(ctx, spec, true)
	if err != nil {
		return err
	}
	if err := task.Wait(ctx); err != nil {
		return fmt.Errorf("DRS rule %s in %s: %w", name, cluster, err)
	}
	return nil
}

// DeleteRule removes the cluster's rule name. A rule or cluster already
// gone is not an error.
func (cl *Client) DeleteRule(ctx context.Context, cluster, name string) error {
	cc, err := cl.cluster(ctx, cluster)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	existing, err := ruleNamed(ctx, cc, name)
	if err != nil || existing == nil {
		return err
	}
	spec := &types.ClusterConfigSpecEx{RulesSpec: []types.ClusterRuleSpec{{
		ArrayUpdateSpec: types.ArrayUpdateSpec{Operation: types.ArrayUpdateOperationRemove, RemoveKey: existing.GetClusterRuleInfo().Key}}}}
	task, err := cc.Reconfigure(ctx, spec, true)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// AntiAffinityRule returns the morefs of the VMs the cluster's rule name
// keeps apart (nil when there is no such rule).
func (cl *Client) AntiAffinityRule(ctx context.Context, cluster, name string) ([]string, error) {
	cc, err := cl.cluster(ctx, cluster)
	if err != nil {
		return nil, err
	}
	r, err := ruleNamed(ctx, cc, name)
	if err != nil || r == nil {
		return nil, err
	}
	aa, ok := r.(*types.ClusterAntiAffinityRuleSpec)
	if !ok {
		return nil, fmt.Errorf("DRS rule %s in %s is not an anti-affinity rule", name, cluster)
	}
	out := []string{}
	for _, v := range aa.Vm {
		out = append(out, v.Value)
	}
	return out, nil
}

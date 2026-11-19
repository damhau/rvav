package provision

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/store"
)

// forget releases from rvav the deployment's resources, or one VM's own
// (day2.OwnResources), as they are when it runs: their rows go, nothing
// outside rvav changes (X1.3). Forgetting what is gone already is nothing.
// After a deletion that went on past failures, it says what they left.
func (a *Activities) forget(ctx context.Context, in OpInput) (string, error) {
	list, err := a.Store.ListResources(ctx, in.DeploymentID)
	if err != nil {
		return "", err
	}
	var lowered json.RawMessage
	var note string
	if id := argStr(in.Op.Args, "vm"); id != "" {
		all := make([]day2.Resource, 0, len(list))
		for _, r := range list {
			res := day2.Resource{ID: r.ID, Type: r.Type, Name: r.Name, Properties: r.Properties}
			if r.Type == "vsphere.vm" {
				res.VM = day2.VM(r.Properties)
			}
			all = append(all, res)
		}
		i := slices.IndexFunc(all, func(r day2.Resource) bool { return r.ID == id })
		if i < 0 {
			return "Released already", nil
		}
		own := day2.OwnResources(all[i], all)
		list = slices.DeleteFunc(list, func(r store.Resource) bool {
			return !slices.ContainsFunc(own, func(o day2.Resource) bool { return o.ID == r.ID })
		})
		comp := componentOf(all[i].VM)
		remaining := 0
		for _, r := range all {
			if r.Type == "vsphere.vm" && r.ID != id && componentOf(r.VM) == comp {
				remaining++
			}
		}
		if lowered, note, err = a.lowerCount(ctx, in.DeploymentID, comp, remaining); err != nil {
			return "", err
		}
	}
	var vms, left []string
	err = a.Store.Tx(ctx, func(tx pgx.Tx) error {
		if lowered != nil {
			if err := a.Store.SetDeploymentInputs(ctx, tx, in.DeploymentID, lowered); err != nil {
				return err
			}
		}
		for _, r := range list {
			// A generated private key not downloaded goes with the
			// deployment, as on deletion (ADR-0039).
			if r.Type == AccessResource {
				if err := a.Store.ForgetDeploymentKey(ctx, tx, r.DeploymentID, nil); err != nil {
					return err
				}
			}
			if err := a.Store.DeleteResource(ctx, tx, r.ID); err != nil {
				return err
			}
			if r.Type == "vsphere.vm" {
				vms = append(vms, r.Name)
			}
			left = append(left, leftName(r))
		}
		return a.Store.RefreshDeploymentSyncState(ctx, tx, in.DeploymentID)
	})
	if err != nil {
		return "", err
	}
	a.Events.Deployment(ctx, in.DeploymentID)
	switch {
	case in.Op.Args["left"] == true && len(left) == 0:
		return "Nothing was left", nil
	case in.Op.Args["left"] == true:
		return "Left: " + strings.Join(left, ", "), nil
	case len(vms) == 0:
		return "", nil
	}
	return "Left in vCenter: " + strings.Join(vms, ", ") + note, nil
}

func componentOf(vm api.VmProperties) string { return cmp.Or(deref(vm.Component), blueprint.SingleKey) }

// lowerCount gives the deployment's inputs with its component's count
// input lowered for a VM unregistered, as a join raises it, so the next
// update doesn't build it again (X1.6): never below the VMs it keeps or
// the form's minimum. Nil when the count isn't an input it has, or is
// shared with another component.
func (a *Activities) lowerCount(ctx context.Context, deploymentID, component string, remaining int) (json.RawMessage, string, error) {
	d, err := a.Store.GetDeployment(ctx, deploymentID)
	if err != nil {
		return nil, "", err
	}
	v, err := a.Store.GetVersion(ctx, d.VersionID)
	if err != nil {
		return nil, "", err
	}
	bp, err := blueprint.Parse(v.Blueprint)
	if err != nil {
		return nil, "", nil
	}
	comps := bp.ComponentList()
	i := slices.IndexFunc(comps, func(c blueprint.Component) bool { return c.Key == component })
	if i < 0 {
		return nil, "", nil
	}
	field := comps[i].CountInput()
	var inputs map[string]any
	_ = json.Unmarshal(d.Inputs, &inputs)
	was, ok := inputs[field].(float64)
	if field == "" || !ok {
		return nil, "", nil
	}
	var schema struct {
		Properties map[string]struct {
			Minimum *float64 `json:"minimum"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(v.FormSchema, &schema)
	n := max(was-1, float64(remaining))
	if m := schema.Properties[field].Minimum; m != nil {
		n = max(n, *m)
	}
	if n == was {
		return nil, "", nil
	}
	out := maps.Clone(inputs)
	out[field] = n
	for _, c := range comps {
		_, before, _ := bp.EvaluateComponent(c, inputs)
		_, after, _ := bp.EvaluateComponent(c, out)
		if c.Key != component && after != before {
			return nil, "", nil
		}
	}
	b, err := json.Marshal(out)
	return b, fmt.Sprintf("; %s is now %v", field, n), err
}

var leftKinds = map[string]string{
	"vsphere.vm": "VM", "vsphere.drs_rule": "DRS rule", "dns.record": "DNS record", "ipam.address": "IP address",
	"nsx.membership": "NSX group membership of", "nsx.policy": "firewall rule", "nsx.group": "NSX group",
	"nsx.segment": "NSX segment", "ipam.subnet": "subnet", AccessResource: "remote access",
}

func leftName(r store.Resource) string {
	if kind, ok := leftKinds[r.Type]; ok {
		return kind + " " + r.Name
	}
	return r.Type + " " + r.Name
}

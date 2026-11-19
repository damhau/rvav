package provision

import (
	"context"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

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
	}
	var vms, left []string
	err = a.Store.Tx(ctx, func(tx pgx.Tx) error {
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
	return "Left in vCenter: " + strings.Join(vms, ", "), nil
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

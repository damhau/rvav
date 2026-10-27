package day2

import (
	"fmt"

	"github.com/damhau/rvav/internal/api"
)

// Leases (ADR-0014): the owner extends within the policy, beyond it with
// approval; rvav powers the VMs off when the lease ends.

func init() {
	register(&Action{
		Key: "extend_lease", Name: "Extend the lease", Group: GroupLifecycle, Types: []string{DeploymentType},
		Unavailable: func(t Target, _ Choices) string {
			if t.Lease.ExpiresAt == nil {
				return "The deployment has no lease."
			}
			return ""
		},
		Form: func(t Target, c Choices) *api.ActionForm {
			days := 30
			if c.LeaseDefaultDays != nil {
				days = *c.LeaseDefaultDays
			}
			help := fmt.Sprintf("%d of %d extensions used.", t.Lease.ExtensionsUsed, c.LeaseMaxExtensions)
			if c.LeaseMaxDays != nil {
				days = min(days, *c.LeaseMaxDays)
				help = fmt.Sprintf("Up to %d days without approval. %s", *c.LeaseMaxDays, help)
			}
			return form("New lease", number("days", "Days from now", help, 1, 3650, days))
		},
		Approval: func(t Target, c Choices, in map[string]any) string {
			if t.Lease.ExtensionsUsed >= c.LeaseMaxExtensions {
				return fmt.Sprintf("All %d extensions without approval are used", c.LeaseMaxExtensions)
			}
			if days := intInput(in, "days"); in != nil && c.LeaseMaxDays != nil && days > *c.LeaseMaxDays {
				return fmt.Sprintf("Longer than the %d days allowed without approval", *c.LeaseMaxDays)
			}
			return ""
		},
		Ops: func(t Target, in map[string]any) []Op {
			days := intInput(in, "days")
			return []Op{{Key: "rvav.lease", Title: fmt.Sprintf("Extend the lease of %s by %d days", t.ResourceName, days),
				System: "rvav", Op: "set_lease", Args: map[string]any{"days": days}}}
		},
	})
	register(&Action{
		Key: "lease_expire", Name: "End of the lease", Group: GroupLifecycle, Types: []string{DeploymentType}, Hidden: true,
		Ops: func(t Target, _ map[string]any) []Op {
			var ops []Op
			for _, r := range t.Resources {
				if r.Type == vmType && r.VM.PowerState != api.Off {
					ops = append(ops, vcenter("vsphere.power_off@"+r.Name, "Power off "+r.Name, "power_off",
						map[string]any{"resource": r.ID, "moref": r.VM.Moref}))
				}
			}
			return append(ops, Op{Key: "rvav.lease_expired", Title: "Record the end of the lease", System: "rvav", Op: "lease_expired"})
		},
	})
}

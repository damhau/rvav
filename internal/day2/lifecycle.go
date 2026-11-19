package day2

import (
	"strings"

	"github.com/damhau/rvav/internal/api"
)

// Access and lifecycle: rename, ISO images, the deployment's name, owner
// and project, delete.

// NamePattern is what a VM name may be: a DNS label.
const NamePattern = `^[a-z][a-z0-9-]{0,61}[a-z0-9]$`

// The most characters of a deployment's name and of what it is for
// (ADR-0046).
const (
	MaxDeploymentName        = 80
	MaxDeploymentDescription = 2000
)

func init() {
	register(&Action{
		Key: "rename", Name: "Rename", Group: GroupConfiguration, Types: []string{vmType},
		Form: func(t Target, _ Choices) *api.ActionForm {
			name := text("name", "New name", 63, t.ResourceName)
			name.schema["pattern"] = NamePattern
			help := "Renames the VM in vCenter, its address in EfficientIP and its DNS records. The guest's own hostname does not change."
			name.ui.Help = &help
			return form("Name", name)
		},
		Validate: func(t Target, in map[string]any) []api.FieldError {
			if strInput(in, "name") == t.ResourceName {
				return invalid("name", "no_change", "This is the VM's current name.")
			}
			return nil
		},
		Ops: func(t Target, in map[string]any) []Op {
			name := strInput(in, "name")
			old := deref(t.VM.Fqdn)
			fqdn := name
			if _, domain, ok := strings.Cut(old, "."); ok {
				fqdn = name + "." + domain
			}
			args := map[string]any{"name": name, "old_name": t.ResourceName, "fqdn": fqdn, "old_fqdn": old}
			var ops []Op
			if old != "" {
				ops = append(ops, Op{Key: "dns.check", Title: "Check " + fqdn + " is free", System: "efficientip", Op: "check_name", Args: args})
			}
			ops = append(ops, vcenter("vsphere.rename", "Rename "+t.ResourceName+" to "+name+" in vCenter", "rename_vm", args))
			if old != "" {
				ops = append(ops, Op{Key: "ipam.rename", Title: "Rename the address in EfficientIP", Detail: old + " → " + fqdn,
					System: "efficientip", Op: "rename_address", Args: args},
					Op{Key: "dns.rename", Title: "Replace the A and PTR records", Detail: old + " → " + fqdn,
						System: "efficientip", Op: "rename_dns", Args: args})
			}
			return append(ops, Op{Key: "rvav.rename", Title: "Record the new name", System: "rvav", Op: "rename_records", Args: args})
		},
	})
	register(&Action{
		Key: "accept_changes", Name: "Accept the changes made in vCenter", Group: GroupConfiguration, Types: []string{vmType},
		Unavailable: func(t Target, _ Choices) string {
			if len(t.Drift) == 0 {
				return "The VM is as rvav left it."
			}
			return ""
		},
		Ops: func(t Target, _ map[string]any) []Op {
			var what []string
			for _, d := range t.Drift {
				what = append(what, d.Label)
			}
			return []Op{{Key: "rvav.accept", Title: "Take " + t.ResourceName + " as it is in vCenter", Detail: strings.Join(what, ", "),
				System: "rvav", Op: "accept_changes"}}
		},
	})
	register(&Action{
		Key: "iso_mount", Name: "Mount an ISO image", Group: GroupConfiguration, Types: []string{vmType},
		Unavailable: func(t Target, _ Choices) string { return suspended(t) },
		Form: func(t Target, _ Choices) *api.ActionForm {
			iso := text("iso", "ISO image", 500, "")
			iso.schema["pattern"] = `^\[[^\]]+\] .+\.[iI][sS][oO]$`
			placeholder, help := "[nfs-iso] linux/rhel-9.4-x86_64-dvd.iso", "A path on a datastore, as vCenter shows it."
			iso.ui.Placeholder, iso.ui.Help = &placeholder, &help
			return form("ISO image", iso)
		},
		Ops: func(t Target, in map[string]any) []Op {
			iso := strInput(in, "iso")
			op := vcenter("vsphere.iso_mount", "Mount "+iso, "mount_iso", map[string]any{"iso": iso})
			if t.VM.Iso != nil {
				op.Detail = "Replaces " + *t.VM.Iso
			}
			return []Op{op}
		},
	})
	register(&Action{
		Key: "iso_unmount", Name: "Unmount the ISO image", Group: GroupConfiguration, Types: []string{vmType},
		Unavailable: func(t Target, _ Choices) string {
			if t.VM.Iso == nil {
				return "No ISO image is mounted."
			}
			return suspended(t)
		},
		Ops: func(t Target, _ map[string]any) []Op {
			return []Op{vcenter("vsphere.iso_unmount", "Unmount "+deref(t.VM.Iso), "unmount_iso", nil)}
		},
	})
	register(&Action{
		Key: "console", Name: "Open the console", Group: GroupAccess, Types: []string{vmType}, Direct: true,
		Unavailable: poweredOn,
	})
	register(&Action{
		Key: "change_owner", Name: "Change owner", Group: GroupAccess, Types: []string{DeploymentType},
		Unavailable: func(t Target, c Choices) string {
			for _, m := range c.Members {
				if m.Value != t.OwnerID {
					return ""
				}
			}
			return "No other member of the project can own it."
		},
		Form: func(t Target, c Choices) *api.ActionForm {
			var opts []api.Option
			for _, m := range c.Members {
				if m.Value != t.OwnerID {
					opts = append(opts, m)
				}
			}
			return form("New owner", choice("owner", "Owner", opts, ""))
		},
		Ops: func(t Target, in map[string]any) []Op {
			return []Op{{Key: "rvav.owner", Title: "Hand " + t.ResourceName + " over to its new owner", System: "rvav",
				Op: "set_owner", Args: map[string]any{"owner": strInput(in, "owner")}}}
		},
	})
	register(&Action{
		Key: "change_project", Name: "Move to another project", Group: GroupAccess, Types: []string{DeploymentType},
		Unavailable: func(_ Target, c Choices) string {
			for _, p := range c.Projects {
				if p.Disabled == nil || !*p.Disabled {
					return ""
				}
			}
			if len(c.Projects) == 0 {
				return "You can request in no other project with this environment."
			}
			return "No other project can take it: " + deref(c.Projects[0].DisabledReason)
		},
		Form: func(_ Target, c Choices) *api.ActionForm {
			f := choice("project", "Project", c.Projects, "")
			var ok []any
			for _, p := range c.Projects {
				if p.Disabled == nil || !*p.Disabled {
					ok = append(ok, p.Value)
				}
			}
			f.schema["enum"] = ok
			help := "The project's quota and placement rules must allow it."
			f.ui.Help = &help
			return form("Project", f)
		},
		Ops: func(t Target, in map[string]any) []Op {
			project := strInput(in, "project")
			return []Op{
				{Key: "vsphere.move", Title: "Move the VMs to the project's folder", System: "vcenter", Op: "move_folder",
					Args: map[string]any{"project": project}},
				{Key: "rvav.project", Title: "Move " + t.ResourceName + " to its new project", System: "rvav", Op: "set_project",
					Args: map[string]any{"project": project}},
			}
		},
	})
	register(&Action{
		Key: "edit", Name: "Edit deployment", Group: GroupConfiguration, Types: []string{DeploymentType},
		Form: func(t Target, _ Choices) *api.ActionForm {
			name := text("name", "Name", MaxDeploymentName, t.ResourceName)
			help := "Unique among the project's deployments. Its VMs keep their names."
			name.ui.Help = &help
			desc := textarea("description", "Description", MaxDeploymentDescription)
			if t.Description != "" {
				desc.value = t.Description
			}
			return form("Deployment", name, desc)
		},
		Validate: func(t Target, in map[string]any) []api.FieldError {
			name, desc := DeploymentDetails(in)
			if name == t.ResourceName && desc == t.Description {
				return invalid("name", "no_change", "These are the deployment's name and description now.")
			}
			return nil
		},
		Ops: func(t Target, in map[string]any) []Op {
			name, desc := DeploymentDetails(in)
			title := "Rename " + t.ResourceName + " to " + name
			if name == t.ResourceName {
				title = "Record " + name + "'s new description"
			}
			return []Op{{Key: "rvav.details", Title: title, System: "rvav", Op: "set_details",
				Args: map[string]any{"name": name, "description": desc}}}
		},
	})
	register(&Action{
		Key: "allow_delete", Name: "Allow deletion", Group: GroupLifecycle, Types: []string{DeploymentType}, Admin: true, OnFailed: true,
		Unavailable: func(t Target, _ Choices) string {
			if len(protected(t)) == 0 {
				return "Nothing keeps its VMs from being deleted."
			}
			return ""
		},
		Confirm: "Its blueprint keeps these VMs from being deleted. Once allowed, those who may delete the deployment can.",
		Ops: func(t Target, _ map[string]any) []Op {
			return []Op{{Key: "rvav.allow_delete", Title: "Allow deleting " + strings.Join(protected(t), ", "), System: "rvav",
				Op: "allow_delete"}}
		},
	})
	register(&Action{
		Key: "delete", Name: "Delete", Group: GroupLifecycle, Types: []string{DeploymentType}, Destructive: true, TypeName: true,
		During: "deleting", After: "deleted", OnFailed: true,
		Confirm: "Deletes the VMs and releases their IP addresses and DNS records. This can't be undone.",
		Unavailable: func(t Target, _ Choices) string {
			if names := protected(t); len(names) > 0 {
				return "Its blueprint keeps " + strings.Join(names, ", ") + " from being deleted: an administrator allows it first (Allow deletion)."
			}
			return ""
		},
		Form: func(Target, Choices) *api.ActionForm {
			f := form("If a step fails", toggle(InputIgnoreFailures, "Go on past failures",
				"Go on past steps in vCenter, EfficientIP or NSX that fail. What they don't delete stays there: "+
					"the request lists it, and rvav forgets it."))
			// Set by rvav's own deletion when a lease ends, not shown.
			f.Schema["properties"].(map[string]any)["lease"] = map[string]any{"type": "boolean"}
			return f
		},
		Ops: func(t Target, in map[string]any) []Op {
			ops := deleteOps(t, in)
			if in[InputIgnoreFailures] != true {
				return ops
			}
			// rvav's own steps, such as the lease check, still stop it.
			for i := range ops {
				ops[i].KeepGoing = ops[i].System != "rvav"
			}
			return append(ops, Op{Key: KeyLeft, Title: "Forget what was left", Detail: "What failed steps didn't delete",
				System: "rvav", Op: OpForget, Args: map[string]any{"left": true}})
		},
	})
}

// InputIgnoreFailures is Delete's option to go on past failed steps (B18),
// and KeyLeft its last step, which lists what they left and forgets it.
const (
	InputIgnoreFailures = "ignore_failures"
	KeyLeft             = "rvav.left"
)

func deleteOps(t Target, in map[string]any) []Op {
	var ops []Op
	if in["lease"] == true {
		ops = append(ops, leaseCheck()) // deleting because the lease ended
	}
	// The DRS rules keeping a component's VMs apart, first (ADR-0024).
	var rules []string
	for _, r := range t.Resources {
		if r.Type == "vsphere.drs_rule" {
			rules = append(rules, r.Name)
		}
	}
	if len(rules) > 0 {
		ops = append(ops, Op{Key: "vsphere.drs_rule.delete", Title: "Remove the DRS rules", Detail: strings.Join(rules, ", "),
			System: "vcenter", Op: "delete_resources", Args: map[string]any{"type": "vsphere.drs_rule"}})
	}
	deleteAll := func(typ, key, title, system string) {
		var names []string
		for _, r := range t.Resources {
			if r.Type == typ {
				names = append(names, r.Name)
			}
		}
		if len(names) > 0 {
			ops = append(ops, Op{Key: key, Title: title, Detail: strings.Join(names, ", "), System: system,
				Op: "delete_resources", Args: map[string]any{"type": typ}})
		}
	}
	// The VMs out of their NSX groups first (ADR-0028).
	deleteAll("nsx.membership", "nsx.membership.delete", "Take the VMs out of their NSX groups", "nsx")
	for _, r := range t.Resources {
		if r.Type == vmType {
			ops = append(ops, vcenter("vsphere.destroy@"+r.Name, "Delete VM "+r.Name, "destroy_vm", map[string]any{"resource": r.ID}))
		}
	}
	// The firewall rules, then the groups they name; NSX segments
	// made for the deployment after the addresses on them, their
	// subnets last (ADR-0028).
	for _, typ := range []struct{ typ, key, title, system string }{
		{"dns.record", "dns.delete", "Delete the DNS records", "efficientip"},
		{"ipam.address", "ipam.release", "Release the IP addresses", "efficientip"},
		{"nsx.policy", "nsx.policy.delete", "Delete the firewall rules", "nsx"},
		{"nsx.group", "nsx.group.delete", "Delete the NSX security groups", "nsx"},
		{"nsx.segment", "nsx.segment.delete", "Delete the NSX segments", "nsx"},
		{"ipam.subnet", "ipam.subnet.delete", "Delete the segments' subnets", "efficientip"},
		// A generated private key not downloaded is forgotten (ADR-0039).
		{"guest.access", "rvav.access.forget", "Forget the remote access", "rvav"},
	} {
		deleteAll(typ.typ, typ.key, typ.title, typ.system)
	}
	return ops
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// protected are the deployment's VMs kept from deletion (ADR-0038).
func protected(t Target) []string {
	var names []string
	for _, r := range t.Resources {
		if r.PreventDelete {
			names = append(names, r.Name)
		}
	}
	return names
}

// DeploymentDetails are the name and description an edit gives, trimmed.
func DeploymentDetails(in map[string]any) (string, string) {
	return strings.TrimSpace(strInput(in, "name")), strings.TrimSpace(strInput(in, "description"))
}

package day2

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/efficientip"
)

// OpForget releases resources from rvav and leaves them where they are.
const OpForget = "forget"

// Unregistering (X1.3) releases a VM or a whole deployment from rvav and
// changes nothing outside it: the VMs stay in vCenter, their addresses and
// DNS records in EfficientIP, their NSX groups, memberships, rules and
// segments in NSX. Onboarding finds the VMs again, with what EfficientIP
// has for them. Only those who may change the project may (Admin): by
// default administrators, who onboard VMs too.
func init() {
	register(&Action{
		Key: "vm_unregister", Name: "Unregister", Group: GroupLifecycle, Types: []string{vmType}, Admin: true, OnFailed: true,
		Confirm: "rvav forgets the VM: it stays in vCenter as it is, and its IP addresses, DNS records and NSX group memberships " +
			"stay in EfficientIP and NSX. It no longer counts in the project's quota. An administrator can onboard it again.",
		// A deployment's only VM is unregistered with the deployment.
		Applies: func(t Target) bool {
			return slices.ContainsFunc(t.Resources, func(r Resource) bool { return r.Type == vmType && r.ID != t.ResourceID })
		},
		Ops: func(t Target, _ map[string]any) []Op {
			op := Op{Key: "rvav.unregister", Title: "Release " + t.ResourceName + " from rvav", System: "rvav", Op: OpForget,
				Args: map[string]any{"vm": t.ResourceID}, Detail: "Stays in vCenter"}
			i := slices.IndexFunc(t.Resources, func(r Resource) bool { return r.ID == t.ResourceID })
			if i < 0 {
				return []Op{op}
			}
			var with []string
			for _, r := range OwnResources(t.Resources[i], t.Resources)[1:] {
				if r.Type == "nsx.membership" {
					with = append(with, "its NSX group memberships")
				} else {
					with = append(with, r.Name)
				}
			}
			if len(with) > 0 {
				op.Detail += ", with " + strings.Join(with, ", ")
			}
			return []Op{op}
		},
	})
	register(&Action{
		Key: "unregister", Name: "Unregister", Group: GroupLifecycle, Types: []string{DeploymentType}, Admin: true, OnFailed: true,
		After: "deleted",
		Confirm: "rvav forgets the deployment: its VMs stay in vCenter as they are, and their IP addresses, DNS records, " +
			"NSX groups, memberships, rules and segments stay in EfficientIP and NSX. Its lease ends and it no longer counts " +
			"in the project's quota. An administrator can onboard its VMs again.",
		Ops: func(t Target, _ map[string]any) []Op {
			var vms []string
			for _, r := range t.Resources {
				if r.Type == vmType {
					vms = append(vms, r.Name)
				}
			}
			op := Op{Key: "rvav.unregister", Title: "Release " + t.ResourceName + " from rvav", System: "rvav", Op: OpForget}
			if len(vms) > 0 {
				op.Detail = "Stay in vCenter: " + strings.Join(vms, ", ")
			}
			return []Op{op}
		},
	})
}

// OwnResources are a VM's resource, then those that are its own among the
// deployment's: its addresses and their DNS records, and its NSX group
// memberships. An address another of the deployment's VMs has too stays
// with them.
func OwnResources(vm Resource, all []Resource) []Resource {
	shared := map[string]bool{}
	for _, r := range all {
		if r.Type == vmType && r.ID != vm.ID {
			for _, ip := range ipsOf(r.VM) {
				shared[ip] = true
			}
		}
	}
	mine := ipsOf(vm.VM)
	names := map[string]bool{}
	if vm.VM.Fqdn != nil && *vm.VM.Fqdn != "" {
		names[*vm.VM.Fqdn] = true
	}
	out := []Resource{vm}
	for _, r := range all {
		if r.Type != "ipam.address" {
			continue
		}
		var p struct {
			Address  string `json:"address"`
			Hostname string `json:"hostname"`
			FQDN     string `json:"fqdn"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		if p.Hostname != vm.Name && !slices.Contains(mine, p.Address) || shared[p.Address] {
			continue
		}
		names[p.Address], names[efficientip.PTRName(p.Address)] = true, true
		if p.FQDN != "" {
			names[p.FQDN] = true
		}
		out = append(out, r)
	}
	delete(names, "")
	for _, r := range all {
		switch r.Type {
		case "dns.record":
			var p struct {
				FQDN  string `json:"fqdn"`
				Value string `json:"value"`
			}
			_ = json.Unmarshal(r.Properties, &p)
			if names[p.FQDN] || names[strings.TrimSuffix(p.Value, ".")] {
				out = append(out, r)
			}
		case "nsx.membership":
			if r.Name == vm.Name {
				out = append(out, r)
			}
		}
	}
	return out
}

func ipsOf(vm api.VmProperties) []string {
	var out []string
	for _, n := range vm.Nics {
		if n.IpAddresses != nil {
			out = append(out, *n.IpAddresses...)
		}
	}
	return out
}

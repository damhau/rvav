package core

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/auth"
)

var groupUnsafe = regexp.MustCompile(`[^a-z0-9_]+`)

func groupName(prefix, value string) string {
	return prefix + "_" + strings.Trim(groupUnsafe.ReplaceAllString(strings.ToLower(value), "_"), "_")
}

// AnsibleInventory is the inventory AWX reads through the rvav.platform
// collection (ADR-0016), in the format of `ansible-inventory --list`: every
// VM of a deployment that is not deleted, with ansible_host its reserved
// address, groups by project, environment, catalog item and OS family, and
// rvav's context as host variables. Administrators and auditors.
func (s *Service) AnsibleInventory(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	if !p.IsAdmin() && !p.HasGlobal("auditor") {
		return nil, ErrForbidden
	}
	hosts, err := s.Store.InventoryHosts(ctx)
	if err != nil {
		return nil, err
	}
	hostvars := map[string]any{}
	groups := map[string][]string{"rvav": {}}
	for _, h := range hosts {
		var vm struct {
			Moref      string `json:"moref"`
			Fqdn       string `json:"fqdn"`
			PowerState string `json:"power_state"`
			GuestOS    string `json:"guest_os"`
		}
		_ = json.Unmarshal(h.VM, &vm)
		vars := map[string]any{"rvav_deployment_id": h.DeploymentID, "rvav_deployment": h.DeploymentName,
			"rvav_deployment_status": h.Status, "rvav_project": h.Project, "rvav_environment": h.Environment,
			"rvav_item": h.Item, "rvav_owner": h.Owner, "rvav_moref": vm.Moref, "rvav_power_state": vm.PowerState}
		switch {
		case h.Address != nil && *h.Address != "":
			vars["ansible_host"] = *h.Address
		case vm.Fqdn != "":
			vars["ansible_host"] = vm.Fqdn
		}
		if vm.Fqdn != "" {
			vars["rvav_fqdn"] = vm.Fqdn
		}
		family := h.OSFamily
		if family == "" {
			family = "linux"
			if strings.Contains(strings.ToLower(vm.GuestOS), "windows") {
				family = "windows"
			}
		}
		vars["rvav_os_family"] = family
		hostvars[h.Name] = vars
		for _, g := range []string{"rvav", groupName("project", h.Project), groupName("env", h.Environment),
			groupName("item", h.Item), groupName("os", family)} {
			groups[g] = append(groups[g], h.Name)
		}
	}
	out := map[string]any{"_meta": map[string]any{"hostvars": hostvars}}
	children := []string{}
	for g, members := range groups {
		out[g] = map[string]any{"hosts": members}
		children = append(children, g)
	}
	slices.Sort(children)
	out["all"] = map[string]any{"children": children}
	return out, nil
}

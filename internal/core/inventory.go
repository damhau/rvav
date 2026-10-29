package core

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
)

var groupUnsafe = regexp.MustCompile(`[^a-z0-9_]+`)

func groupName(prefix, value string) string {
	return prefix + "_" + strings.Trim(groupUnsafe.ReplaceAllString(strings.ToLower(value), "_"), "_")
}

// AnsibleInventory is the inventory AWX reads through the rvav.platform
// collection (ADR-0017), in the format of `ansible-inventory --list`: every
// VM of a deployment that is not being deleted, with ansible_host its
// reserved address, groups by project, environment, catalog item, its tags
// and OS family, and rvav's context as host variables, the inputs the form
// exposes to the inventory among them (rvav_inputs). Administrators and
// auditors.
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
			Component  string `json:"component"`
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
		if inputs := exposedInputs(h.UISchema, h.Inputs); len(inputs) > 0 {
			vars["rvav_inputs"] = inputs
		}
		hostvars[h.Name] = vars
		names := []string{"rvav", groupName("project", h.Project), groupName("env", h.Environment),
			groupName("item", h.Item), groupName("os", family)}
		for _, t := range h.Tags {
			names = append(names, groupName("tag", t))
		}
		// A multi-VM deployment's component (ADR-0024).
		if vm.Component != "" && vm.Component != blueprint.SingleKey {
			vars["rvav_component"] = vm.Component
			names = append(names, groupName("component", vm.Component))
		}
		for _, g := range slices.Compact(names) {
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

// exposedInputs are the deployment's inputs whose field is marked
// expose_to_inventory; secret ones never are, and neither is a value that
// looks like a Jinja template: an AWX on an older ansible-core could
// evaluate it.
func exposedInputs(ui, inputs json.RawMessage) map[string]any {
	var schema api.UiSchema
	var values map[string]any
	if json.Unmarshal(ui, &schema) != nil || json.Unmarshal(inputs, &values) != nil {
		return nil
	}
	out := map[string]any{}
	for name, f := range schema.Fields {
		v, ok := values[name]
		if ok && f.ExposeToInventory != nil && *f.ExposeToInventory && (f.Secret == nil || !*f.Secret) && !looksLikeTemplate(v) {
			out[name] = v
		}
	}
	return out
}

func looksLikeTemplate(v any) bool {
	b, _ := json.Marshal(v)
	return strings.Contains(string(b), "{{") || strings.Contains(string(b), "{%") || strings.Contains(string(b), "{#")
}

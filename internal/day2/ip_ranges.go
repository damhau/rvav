package day2

import (
	"encoding/json"
	"slices"
)

// AddressSystem is the system the addresses among resources come from,
// those with ids or all of them when ids is nil: rvav when its own IP
// ranges gave each one (ADR-0063), else EfficientIP.
func AddressSystem(all []Resource, ids []string) string {
	found := false
	for _, r := range all {
		if r.Type != "ipam.address" || ids != nil && !slices.Contains(ids, r.ID) {
			continue
		}
		var p struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		if p.Source != "rvav" {
			return "efficientip"
		}
		found = true
	}
	if !found {
		return "efficientip"
	}
	return "rvav"
}

// rangedVM says whether every address of the VM named came from rvav's IP
// ranges, which make no DNS records (ADR-0063).
func rangedVM(t Target, name string) bool {
	var ids []string
	for _, r := range t.Resources {
		var p struct {
			Hostname string `json:"hostname"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		if r.Type == "ipam.address" && p.Hostname == name {
			ids = append(ids, r.ID)
		}
	}
	return len(ids) > 0 && AddressSystem(t.Resources, ids) == "rvav"
}

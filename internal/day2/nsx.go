package day2

import (
	"encoding/json"
	"slices"

	"github.com/damhau/rvav/internal/api"
)

// NSX security groups (ADR-0028): a VM joins or leaves one of those its
// item offers (its deployment's groups, and the existing groups its VMs
// join), by the tags rvav sets on it. Which users may is the day-2
// policies' to decide.

// membershipType is a VM's resource of what it was given to join its
// groups.
const membershipType = "nsx.membership"

// nsxGroup is a group as a membership records it.
type nsxGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// membership is a VM's groups, and those it may join.
func membership(t Target) (id string, joined, offered []nsxGroup, ok bool) {
	for _, r := range t.Resources {
		if r.Type == membershipType && r.Name == t.ResourceName {
			var m struct {
				Groups  []nsxGroup `json:"groups"`
				Offered []nsxGroup `json:"offered"`
			}
			_ = json.Unmarshal(r.Properties, &m)
			return r.ID, m.Groups, m.Offered, true
		}
	}
	return "", nil, nil, false
}

// joinable are the offered groups a VM isn't in.
func joinable(joined, offered []nsxGroup) []nsxGroup {
	return slices.DeleteFunc(slices.Clone(offered), func(g nsxGroup) bool {
		return slices.ContainsFunc(joined, func(j nsxGroup) bool { return j.ID == g.ID })
	})
}

func groupOptions(groups []nsxGroup) []api.Option {
	opts := make([]api.Option, len(groups))
	for i, g := range groups {
		opts[i] = api.Option{Value: g.ID, Label: g.Name}
	}
	return opts
}

func groupName(groups []nsxGroup, id string) string {
	for _, g := range groups {
		if g.ID == id {
			return g.Name
		}
	}
	return id
}

func init() {
	register(&Action{
		Key: "nsx_group_add", Name: "Add to a security group", Group: GroupConfiguration, Types: []string{vmType},
		Unavailable: func(t Target, _ Choices) string {
			_, joined, offered, ok := membership(t)
			switch {
			case !ok:
				return "The VM isn't in NSX security groups."
			case len(joinable(joined, offered)) == 0:
				return "The VM is in every security group its item offers."
			}
			return ""
		},
		Form: func(t Target, _ Choices) *api.ActionForm {
			_, joined, offered, _ := membership(t)
			return form("Security group", choice("group", "Group", groupOptions(joinable(joined, offered)), ""))
		},
		Ops: func(t Target, in map[string]any) []Op {
			id, _, offered, _ := membership(t)
			group := strInput(in, "group")
			return []Op{{Key: "nsx.group_add", Title: "Add " + t.ResourceName + " to NSX group " + groupName(offered, group),
				System: "nsx", Op: "nsx_join", Args: map[string]any{"membership": id, "group": group}}}
		},
	})
	register(&Action{
		Key: "nsx_group_remove", Name: "Remove from a security group", Group: GroupConfiguration, Types: []string{vmType},
		Confirm: "The firewall rules of the group no longer apply to the VM.",
		Unavailable: func(t Target, _ Choices) string {
			if _, joined, _, ok := membership(t); !ok || len(joined) == 0 {
				return "The VM isn't in NSX security groups."
			}
			return ""
		},
		Form: func(t Target, _ Choices) *api.ActionForm {
			_, joined, _, _ := membership(t)
			return form("Security group", choice("group", "Group", groupOptions(joined), ""))
		},
		Ops: func(t Target, in map[string]any) []Op {
			id, joined, _, _ := membership(t)
			group := strInput(in, "group")
			return []Op{{Key: "nsx.group_remove", Title: "Remove " + t.ResourceName + " from NSX group " + groupName(joined, group),
				System: "nsx", Op: "nsx_leave", Args: map[string]any{"membership": id, "group": group}}}
		},
	})
}

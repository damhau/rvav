package day2

import (
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
)

// TagCategory is a vSphere tag category whose tags a VM's users may change
// (X1.3): its environment allows it, and the VM's vCenter has it.
type TagCategory struct {
	Name string
	// Single takes one tag per VM.
	Single bool
	Tags   []string
}

func init() {
	register(&Action{
		Key: "tags_edit", Name: "Edit tags", Group: GroupConfiguration, Types: []string{vmType},
		Unavailable: func(t Target, c Choices) string {
			if len(t.TagCategories) == 0 {
				return "No tag category may be changed in " + c.Environment + ": an administrator chooses them on the environment."
			}
			if t.VM.Tags == nil {
				return "rvav hasn't read the VM's tags yet. Try again after the next inventory sync."
			}
			if !slices.ContainsFunc(t.TagCategories, func(c TagCategory) bool { return len(c.Tags) > 0 }) {
				return "The tag categories VMs may change have no tags in vCenter yet."
			}
			return ""
		},
		Form: func(t Target, _ Choices) *api.ActionForm {
			var opts []api.Option
			var single []string
			for _, c := range t.TagCategories {
				for _, tag := range c.Tags {
					opts = append(opts, api.Option{Value: c.Name + "/" + tag, Label: c.Name + "/" + tag})
				}
				if c.Single {
					single = append(single, c.Name)
				}
			}
			help := "Tags of other categories stay as they are."
			if len(single) > 0 {
				help += " A VM takes one " + strings.Join(single, " or ") + " tag at most."
			}
			return form("Tags", checklist("tags", "Tags", help, opts, editable(t)))
		},
		Validate: func(t Target, in map[string]any) []api.FieldError {
			picked := strsInput(in, "tags")
			for _, c := range t.TagCategories {
				if n := len(inCategory(picked, c.Name)); c.Single && n > 1 {
					return invalid("tags", "single", "A VM takes one %s tag at most: choose one.", c.Name)
				}
			}
			if sameTags(picked, editable(t)) {
				return invalid("tags", "unchanged", "The VM already has these tags.")
			}
			return nil
		},
		Ops: func(t Target, in map[string]any) []Op {
			picked, now := strsInput(in, "tags"), editable(t)
			categories := make([]string, len(t.TagCategories))
			for i, c := range t.TagCategories {
				categories[i] = c.Name
			}
			var detail []string
			if add := missing(picked, now); len(add) > 0 {
				detail = append(detail, "Adds "+strings.Join(add, ", "))
			}
			if remove := missing(now, picked); len(remove) > 0 {
				detail = append(detail, "Removes "+strings.Join(remove, ", "))
			}
			op := vcenter("vsphere.tags", "Change the tags of "+t.ResourceName, "set_tags",
				map[string]any{"categories": categories, "tags": picked})
			op.Detail = strings.Join(detail, " · ")
			return []Op{op}
		},
	})
}

// editable are the VM's tags the action may change.
func editable(t Target) []string {
	out := []string{}
	if t.VM.Tags == nil {
		return out
	}
	for _, tag := range *t.VM.Tags {
		cat, _, _ := strings.Cut(tag, "/")
		if slices.ContainsFunc(t.TagCategories, func(c TagCategory) bool { return c.Name == cat }) {
			out = append(out, tag)
		}
	}
	return out
}

func inCategory(tags []string, category string) []string {
	var out []string
	for _, t := range tags {
		if strings.HasPrefix(t, category+"/") {
			out = append(out, t)
		}
	}
	return out
}

func sameTags(a, b []string) bool {
	return len(missing(a, b)) == 0 && len(missing(b, a)) == 0
}

// missing are the tags of a that b lacks.
func missing(a, b []string) []string {
	var out []string
	for _, t := range a {
		if !slices.Contains(b, t) {
			out = append(out, t)
		}
	}
	return out
}

func checklist(name, label, help string, options []api.Option, value []string) field {
	values := make([]string, len(options))
	for i, o := range options {
		values[i] = o.Value
	}
	return field{name: name, schema: map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"type": "string", "enum": values}},
		ui: api.UiField{Label: label, Help: &help, Widget: api.UiWidgetChecklist, Options: &options}, value: value}
}

func strsInput(inputs map[string]any, name string) []string {
	out := []string{}
	switch v := inputs[name].(type) {
	case []string:
		return v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

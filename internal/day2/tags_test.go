package day2

import (
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/api"
)

// TestEditTags: Edit tags offers the tags of the categories the VM's users
// may change, starts from the VM's, keeps a single category to one tag,
// refuses a change to nothing, and sets those categories only (X1.3).
func TestEditTags(t *testing.T) {
	a := Get("tags_edit")
	tags := []string{"app/shop", "backup/daily"}
	vm := Target{Type: vmType, ResourceName: "web-dev-app001", VM: api.VmProperties{Tags: &tags}}
	c := Choices{Environment: "Development"}
	if why := a.Unavailable(vm, c); why != "No tag category may be changed in Development: an administrator chooses them on the environment." {
		t.Errorf("without categories: %q", why)
	}
	vm.TagCategories = []TagCategory{{Name: "backup", Tags: []string{"daily", "weekly"}}, {Name: "tier", Single: true, Tags: []string{"gold", "silver"}}}
	if why := a.Unavailable(vm, c); why != "" {
		t.Errorf("with categories: %q", why)
	}
	if why := a.Unavailable(Target{Type: vmType, TagCategories: vm.TagCategories}, c); why == "" {
		t.Error("tags not read yet: available")
	}

	f := a.Form(vm, c)
	field := f.UiSchema.Fields["tags"]
	var values []string
	for _, o := range *field.Options {
		values = append(values, o.Value)
	}
	if !slices.Equal(values, []string{"backup/daily", "backup/weekly", "tier/gold", "tier/silver"}) ||
		!slices.Equal(f.InitialValues["tags"].([]string), []string{"backup/daily"}) ||
		deref(field.Help) != "Tags of other categories stay as they are. A VM takes one tier tag at most." {
		t.Fatalf("form = %v, %v, %q", values, f.InitialValues, deref(field.Help))
	}

	in := func(tags ...string) map[string]any {
		l := make([]any, len(tags))
		for i, t := range tags {
			l[i] = t
		}
		return map[string]any{"tags": l}
	}
	if errs := a.Validate(vm, in("tier/gold", "tier/silver")); len(errs) != 1 || *errs[0].Code != "single" {
		t.Errorf("two tier tags = %+v", errs)
	}
	if errs := a.Validate(vm, in("backup/daily")); len(errs) != 1 || *errs[0].Code != "unchanged" {
		t.Errorf("no change = %+v", errs)
	}
	if errs := a.Validate(vm, in("backup/weekly", "tier/gold")); len(errs) != 0 {
		t.Errorf("a change = %+v", errs)
	}
	ops := a.Ops(vm, in("backup/weekly", "tier/gold"))
	if len(ops) != 1 || ops[0].Op != "set_tags" || !slices.Equal(ops[0].Args["categories"].([]string), []string{"backup", "tier"}) ||
		!slices.Equal(ops[0].Args["tags"].([]string), []string{"backup/weekly", "tier/gold"}) ||
		ops[0].Title != "Change the tags of web-dev-app001" || ops[0].Detail != "Adds backup/weekly, tier/gold · Removes backup/daily" {
		t.Fatalf("ops = %+v", ops)
	}
}

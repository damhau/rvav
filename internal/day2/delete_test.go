package day2

import (
	"slices"
	"strings"
	"testing"
)

// TestDeleteGoesOnPastFailures: asked to, a deletion goes on past its
// failed steps in vCenter, EfficientIP, NSX and Active Directory, and ends
// by forgetting what they left; rvav's own steps still stop it (B18). The
// lease input stays accepted, and hidden.
func TestDeleteGoesOnPastFailures(t *testing.T) {
	del := Get("delete")
	target := Target{Type: DeploymentType, ResourceName: "erp-dev", Resources: []Resource{
		{ID: "res_vm1", Type: vmType, Name: "app01"},
		{ID: "res_a1", Type: "ipam.address", Name: "10.0.0.1"},
		{ID: "res_r1", Type: "dns.record", Name: "A app01.corp.example"},
		{ID: "res_m1", Type: "nsx.membership", Name: "app01"},
		{ID: "res_k1", Type: "guest.access", Name: "ssh key"},
		{ID: "res_c1", Type: ComputerType, Name: "APP01$"},
	}}
	keys := func(ops []Op, keepGoing bool) []string {
		var out []string
		for _, op := range ops {
			if op.KeepGoing == keepGoing {
				out = append(out, op.Key)
			}
		}
		return out
	}

	plain := del.Ops(target, map[string]any{})
	if got := keys(plain, true); len(got) != 0 || slices.ContainsFunc(plain, func(op Op) bool { return op.Key == KeyLeft }) {
		t.Fatalf("without the option: %+v", plain)
	}
	ops := del.Ops(target, map[string]any{InputIgnoreFailures: true, "lease": true})
	if got, want := keys(ops, true), []string{"nsx.membership.delete", "vsphere.destroy@app01", "ad.computer.delete", "dns.delete", "ipam.release"}; !slices.Equal(got, want) {
		t.Errorf("going on past = %v, want %v", got, want)
	}
	if got, want := keys(ops, false), []string{"rvav.lease_check", "rvav.access.forget", KeyLeft}; !slices.Equal(got, want) {
		t.Errorf("stopping at = %v, want %v", got, want)
	}
	if last := ops[len(ops)-1]; last.Op != OpForget || last.Args["left"] != true {
		t.Errorf("last = %+v", last)
	}

	f := del.Form(target, Choices{})
	props := f.Schema["properties"].(map[string]any)
	if _, ok := props["lease"]; !ok {
		t.Error("the lease input is refused")
	}
	if shown := f.UiSchema.Layout.Sections[0].Fields; !slices.Equal(shown, []string{InputIgnoreFailures}) {
		t.Errorf("shown = %v", shown)
	}
	if help := f.UiSchema.Fields[InputIgnoreFailures].Help; help == nil || !strings.HasPrefix(*help, "Go on past steps in vCenter, EfficientIP, NSX or Active Directory that fail.") {
		t.Errorf("help = %v", help)
	}
	if f.Schema["required"].([]string) == nil || len(f.Schema["required"].([]string)) != 0 {
		t.Errorf("required = %v", f.Schema["required"])
	}
}

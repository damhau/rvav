package core

import (
	"testing"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/store"
)

func TestSourceIn(t *testing.T) {
	added := vcenterSet{"vc02": true, "vc03": true}
	rhel := store.VMTemplate{Key: "rhel9", Sources: []store.TemplateSource{
		{VCenter: "", Template: "tpl-rhel9-gva"},
		{VCenter: "vc02", Template: "rhel9", Library: "os"},
	}}
	for _, tc := range []struct {
		vc, want string
		ok       bool
	}{
		{"vc01", "tpl-rhel9-gva", true}, // the vCenter integration's, whatever its name
		{"", "tpl-rhel9-gva", true},
		{"vc02", "rhel9", true},
		{"vc03", "", false}, // added, and no source names it
	} {
		src, ok := sourceIn(rhel, tc.vc, added)
		if ok != tc.ok || src.Template != tc.want {
			t.Errorf("sourceIn(%q) = %+v, %v; want %q, %v", tc.vc, src, ok, tc.want, tc.ok)
		}
	}
	zurichOnly := store.VMTemplate{Sources: []store.TemplateSource{{VCenter: "vc02", Template: "tpl-zrh"}}}
	if _, ok := sourceIn(zurichOnly, "vc01", added); ok {
		t.Error("a template only in vc02 has a source in the vCenter integration's")
	}
}

func TestInInventory(t *testing.T) {
	added := vcenterSet{"vc02": true}
	inv := []store.InvTemplate{
		{VCenter: "vc01", Name: "tpl-rhel9"},
		{VCenter: "vc02", Name: "rhel9", Library: "os"},
	}
	for _, tc := range []struct {
		src  store.TemplateSource
		want bool
	}{
		{store.TemplateSource{VCenter: "", Template: "tpl-rhel9"}, true},
		{store.TemplateSource{VCenter: "vc02", Template: "tpl-rhel9"}, false},
		{store.TemplateSource{VCenter: "vc02", Template: "rhel9", Library: "os"}, true},
		{store.TemplateSource{VCenter: "vc02", Template: "rhel9"}, false},
		{store.TemplateSource{VCenter: "", Template: "rhel9", Library: "os"}, false}, // vc02 isn't the integration's
	} {
		if got := inInventory(tc.src, inv, added); got != tc.want {
			t.Errorf("inInventory(%+v) = %v", tc.src, got)
		}
	}
}

// A vCenter whose clusters all lack the template rules them all out, and
// says why; one with it is chosen even with less room.
func TestChooseWithoutTemplate(t *testing.T) {
	pl := pooled("")
	pl.clusters[0].VCenter, pl.datastores[0].VCenter = "vc01", "vc01" // a, the least room
	plan := blueprint.VMPlan{DiskGB: 50}
	inVC01 := func(vc string) bool { return vc == "vc01" }
	ch, _, why := pl.choose(placementQuery{plan: plan, count: 1, anyDatastore: true, hasTemplate: inVC01, noTemplate: "none", vmGB: 50})
	if why != "" || ch.cluster == nil || ch.cluster.Name != "a" {
		t.Fatalf("chose %+v (%s), want a", ch.cluster, why)
	}
	nowhere := func(string) bool { return false }
	if ch, _, why := pl.choose(placementQuery{plan: plan, count: 1, anyDatastore: true, hasTemplate: nowhere, noTemplate: "none",
		vmGB: 50}); ch.cluster != nil || why != "none" {
		t.Fatalf("chose %+v (%q), want none", ch.cluster, why)
	}
}

func TestVCenterNames(t *testing.T) {
	if got := vcenterNames([]string{"vc02"}); got != "vCenter vc02" {
		t.Error(got)
	}
	if got := vcenterNames([]string{"", "vc02"}); got != "vCenters the vCenter integration's, vc02" {
		t.Error(got)
	}
}

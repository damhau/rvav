package core

import (
	"slices"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/present"
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

func TestCheckImage(t *testing.T) {
	linux := func(ud string, cons *api.TemplateConstraints) api.VmTemplateWrite {
		return api.VmTemplateWrite{OsFamily: api.VmTemplateWriteOsFamilyLinux, UserData: &ud, Constraints: cons}
	}
	errsOf := func(w api.VmTemplateWrite) map[string]string {
		_, _, errs := checkImage(w)
		out := map[string]string{}
		for _, e := range errs {
			out[e.Field] = e.Message
		}
		return out
	}
	ud, cons, errs := checkImage(linux("  #cloud-config\npackages: [chrony]\nfqdn: {{ .vm.fqdn }}\n", &api.TemplateConstraints{
		Compute: []string{" rvav-os/rhel9 "}, Storage: []string{"!rvav-tier/bronze:soft"}, Network: []string{}}))
	if len(errs) > 0 || ud != "#cloud-config\npackages: [chrony]\nfqdn: {{ .vm.fqdn }}" ||
		!slices.Equal(cons.Compute, []string{"rvav-os/rhel9"}) || !slices.Equal(cons.Storage, []string{"!rvav-tier/bronze:soft"}) {
		t.Fatalf("%q %v %v", ud, cons, errs)
	}
	win := linux("#cloud-config\n", nil)
	win.OsFamily = api.VmTemplateWriteOsFamilyWindows
	spec := linux("#cloud-config\n", nil)
	spec.Customization = present.Ptr("linux-base")
	windows := func(ud string, cloudbase bool, customization string) api.VmTemplateWrite {
		w := linux(ud, nil)
		w.OsFamily, w.CloudbaseInit, w.Customization = api.VmTemplateWriteOsFamilyWindows, &cloudbase, &customization
		return w
	}
	for _, ud := range []string{"#ps1_sysnative\nWrite-Host {{ .vm.name | json }}", "rem cmd\necho on", "#cloud-config\nset_timezone: UTC", ""} {
		if _, _, errs := checkImage(windows(ud, true, "")); len(errs) > 0 {
			t.Errorf("cloudbase-init user data %q: %v", ud, errs)
		}
	}
	cbiLinux := linux("", nil)
	cbiLinux.CloudbaseInit = present.Ptr(true)
	for _, tc := range []struct {
		w     api.VmTemplateWrite
		field string
		msg   string
	}{
		{win, "/user_data", "read user data with cloudbase-init: mark the template as using it"},
		{windows("echo hi", true, ""), "/user_data", "Start the user data with #cloud-config, #ps1_sysnative"},
		{windows("", true, "win-base"), "/cloudbase_init", "doesn't use a customization specification"},
		{cbiLinux, "/cloudbase_init", "cloudbase-init is for Windows templates"},
		{spec, "/user_data", "doesn't use a customization specification"},
		{linux("packages: [chrony]", nil), "/user_data", "Start the user data with #cloud-config"},
		{linux("#cloud-config\nmotd: {{ .inputs.motd }}", nil), "/user_data", "print inputs through json"},
		{linux("#!/bin/sh\necho {{ .vm.name ", nil), "/user_data", "not a valid template"},
		{linux("", &api.TemplateConstraints{Compute: []string{"${inputs.zone}"}}), "/constraints/compute/0", "${...} is for blueprints"},
		{linux("", &api.TemplateConstraints{Network: []string{"ok/tag", "env:prod"}}), "/constraints/network/1", "vRA's way"},
	} {
		if got := errsOf(tc.w)[tc.field]; !strings.Contains(got, tc.msg) {
			t.Errorf("%s: %q, want %q", tc.field, got, tc.msg)
		}
	}
	if got := imageConstraints(&store.VMTemplate{Constraints: cons}); len(got) != 2 || got[0].Kind != blueprint.Compute ||
		got[0].Tag != "rvav-os/rhel9" || got[1].Kind != blueprint.Storage || !got[1].Not || !got[1].Soft {
		t.Fatalf("image constraints %+v", got)
	}
}

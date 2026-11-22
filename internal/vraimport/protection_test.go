package vraimport

import (
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// TestProtected: where vRA keeps a machine from deletion. Its resource's
// flag, else its template version's (a counted machine by its resource's
// name), read from the deployment's inputs; an expression rvav can't work
// out keeps it, with a doubt.
func TestProtected(t *testing.T) {
	e := vra.SampleEstate(time.Now())
	web, pair := e.Deployments[0], e.Deployments[1]
	if p := Protected(&e, pair, pair.Resources[0]); !p.On || p.Doubt != "" {
		t.Fatalf("vra-api-001's resource flag = %+v", p)
	}
	if p := Protected(&e, pair, pair.Resources[1]); p.On {
		t.Fatalf("vra-api-002 = %+v", p)
	}

	flag := func(v string) {
		bp := &e.Blueprints[0]
		bp.Versions[1].Content = strings.Replace(rhel(), "    type: Cloud.vSphere.Machine\n",
			"    type: Cloud.vSphere.Machine\n    preventDelete: "+v+"\n", 1)
	}
	machine := web.Resources[0]
	for v, want := range map[string]Protection{
		"true":                    {On: true},
		"false":                   {},
		"${input.keep}":           {On: true},
		"'${ input.lax }'":        {},
		"${input.nobody}":         {On: true, Doubt: "preventDelete is ${input.nobody}, which rvav can't work out"},
		"${count.index == 0}":     {On: true, Doubt: "preventDelete is ${count.index == 0}, which rvav can't work out"},
		"'${input.environment}'":  {On: true, Doubt: "preventDelete is ${input.environment}, which rvav can't work out"},
		"\"${input.keepString}\"": {On: true},
	} {
		flag(v)
		web.Inputs = map[string]any{"keep": true, "lax": "false", "environment": "dev", "keepString": "true"}
		if got := Protected(&e, web, machine); got != want {
			t.Errorf("preventDelete: %s = %+v, want %+v", v, got, want)
		}
	}

	flag("true")
	machine.Name = "Cloud_vSphere_Machine_1[1]"
	if p := Protected(&e, web, machine); !p.On {
		t.Fatalf("a counted machine = %+v", p)
	}
	if p := Protected(&e, pair, vra.Resource{Name: "Cloud_vSphere_Machine_1", Type: vra.MachineType}); p.On {
		t.Fatalf("version 1 has no flag, version 2's doesn't count: %+v", p)
	}
	web.BlueprintVersion = ""
	if p := Protected(&e, web, machine); p.On {
		t.Fatalf("no version: the template's own content, without the flag = %+v", p)
	}
	machine.Properties = map[string]any{"preventDelete": "true"}
	if p := Protected(&e, web, machine); !p.On {
		t.Fatalf("a flag in its properties = %+v", p)
	}
	machine.PreventDelete = new(false)
	if p := Protected(&e, web, machine); p.On {
		t.Fatalf("its resource's own flag first = %+v", p)
	}
	web.BlueprintID, machine = "bp-nope", vra.Resource{Name: "Cloud_vSphere_Machine_1"}
	if p := Protected(&e, web, machine); p.On {
		t.Fatalf("a template the estate hasn't = %+v", p)
	}
}

// TestPlanProtection: the plan says which VMs vRA keeps from deletion and
// which rvav does once adopted, and warns when rvav no longer does.
func TestPlanProtection(t *testing.T) {
	now := time.Now()
	e := vra.SampleEstate(now)
	r := Rvav{Now: now, Users: map[string]string{"lea.martin@corp.example": "usr_l"},
		Projects: []RvavProject{{ID: "prj_web", Name: "web platform", Environments: []string{"dev"}, Members: []string{"usr_l"}}},
		VMs: map[string]VMRef{"vra-api-001": {VCenter: "vc1", Moref: "vm-1", Name: "vra-api-001"},
			"vra-api-002": {VCenter: "vc1", Moref: "vm-2", Name: "vra-api-002"}},
		Items: map[string]string{"rhel-9-server": "itm_rhel"}}
	pair := func() DeploymentPlan { t.Helper(); return PlanDeployments(&e, r)[1] }

	p := pair()
	if p.Status != Adopt || len(p.VMs) != 2 || !p.VMs[0].PreventDelete || p.VMs[1].PreventDelete ||
		!hasNote(p.Notes, "vms", "vRA keeps vra-api-001 from deletion (preventDelete)") {
		t.Fatalf("to adopt: %+v", p)
	}

	r.Adopted = map[string][]string{"dep-vra-2": {"dep_1"}}
	r.VMs["vra-api-001"] = VMRef{VCenter: "vc1", Moref: "vm-1", Name: "vra-api-001", Managed: true, PreventDelete: true}
	r.VMs["vra-api-002"] = VMRef{VCenter: "vc1", Moref: "vm-2", Name: "vra-api-002", Managed: true}
	if p := pair(); p.Status != Adopted || !p.VMs[0].PreventDelete || p.VMs[1].PreventDelete ||
		!hasNote(p.Notes, "vms", "Kept from deletion, as in vRA: vra-api-001.") || hasNote(p.Notes, "vms", "rvav doesn't") {
		t.Fatalf("adopted: %+v", p)
	}
	r.VMs["vra-api-001"] = VMRef{VCenter: "vc1", Moref: "vm-1", Name: "vra-api-001", Managed: true}
	if p := pair(); p.VMs[0].PreventDelete || !hasNote(p.Notes, "vms", "vRA keeps vra-api-001 from deletion, rvav doesn't") {
		t.Fatalf("allowed in rvav: %+v", p)
	}
	r.VMs["vra-api-001"] = VMRef{VCenter: "vc1", Moref: "vm-1", Name: "vra-api-001"}
	if p := pair(); !hasNote(p.Notes, "vms", "Not adopted with it: vra-api-001. Adopt them from Onboarding into its deployment. "+
		"vRA keeps vra-api-001 from deletion: choose Keep from deletion there.") || hasNote(p.Notes, "vms", "rvav doesn't") {
		t.Fatalf("one left to adopt by hand: %+v", p)
	}

	r.Adopted = nil
	r.VMs["vra-api-001"] = VMRef{Name: "vra-api-001", Ambiguous: true}
	r.VMs["vra-api-002"] = VMRef{VCenter: "vc1", Moref: "vm-2", Name: "vra-api-002"}
	if p := pair(); p.Status != Skipped || !hasNote(p.Notes, "vms", "vRA keeps vra-api-001 from deletion: choose Keep from deletion there.") {
		t.Fatalf("a name two vCenters have: %+v", p)
	}

	r.VMs["vra-api-001"] = VMRef{VCenter: "vc1", Moref: "vm-1", Name: "vra-api-001"}
	e.Deployments[1].Resources[0].PreventDelete = nil
	e.Deployments[1].Resources[0].Properties["preventDelete"] = "${input.size == 'medium'}"
	if p := pair(); !p.VMs[0].PreventDelete || !hasNote(p.Notes, "vms.vra-api-001", "kept from deletion, the safer side") {
		t.Fatalf("an expression: %+v", p)
	}
}

func rhel() string {
	e := vra.SampleEstate(time.Now())
	return e.Blueprints[0].Versions[1].Content
}

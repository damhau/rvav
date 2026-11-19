package vraimport

import (
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// A disk's provisioningType becomes its provisioning, a project's
// placementPolicy the strategy its placement rules should take (ADR-0036).
func TestMapPlacementPolicyAndProvisioning(t *testing.T) {
	const content = `formatVersion: 1
resources:
  vm:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: small
      attachedDisks:
        - source: ${resource.thin.id}
        - source: ${resource.eager.id}
        - source: ${resource.thick.id}
        - source: ${resource.odd.id}
  thin:
    type: Cloud.vSphere.Disk
    properties: {capacityGb: 10, provisioningType: thin}
  eager:
    type: Cloud.vSphere.Disk
    properties: {capacityGb: 20, provisioningType: eagerZeroedThick}
  thick:
    type: Cloud.vSphere.Disk
    properties: {capacityGb: 30, provisioningType: thick}
  odd:
    type: Cloud.vSphere.Disk
    properties: {capacityGb: 40, provisioningType: '${input.type}'}
`
	e := vra.SampleEstate(time.Now())
	p := MapTemplate(&e, vra.Blueprint{ID: "bp-disks", Name: "Disks", Versions: []vra.BlueprintVersion{
		{Version: "1", Status: "RELEASED", Content: content}}})
	if p.Doc == nil {
		t.Fatalf("plan = %s %+v", p.Status, p.Notes)
	}
	disks := p.Doc.Versions[0].Blueprint["disks"].([]map[string]any)
	for i, want := range []any{"thin", "thick_eager_zeroed", "thick_lazy_zeroed", nil} {
		if disks[i]["provisioning"] != want {
			t.Fatalf("disk %d = %+v", i, disks[i])
		}
	}
	// Each keyed by its resource, as updates match disks by key (#191).
	for i, want := range []string{"thin", "eager", "thick", "odd"} {
		if disks[i]["key"] != want {
			t.Fatalf("disk %d = %+v", i, disks[i])
		}
	}
	if !has(p, Unmapped, "resources.odd.provisioningType", "isn't thin, thick or eagerZeroedThick") || p.Status != Partial {
		t.Fatalf("notes = %s %+v", p.Status, p.Notes)
	}

	plan := PlanImport(&e, Rvav{Now: time.Now(), ProjectMap: map[string]string{}},
		ImportState{Links: map[string]Link{}, Items: map[string]ItemState{}, Slugs: map[string]string{}})
	for _, pp := range plan.Projects {
		want := map[string]string{"vra-prj-web": "spread"}[pp.VRAID]
		if pp.Strategy != want {
			t.Fatalf("%s: placement policy %q, strategy %q", pp.Name, pp.PlacementPolicy, pp.Strategy)
		}
	}
	for policy, want := range map[string]string{"DEFAULT": "most_free_memory", "SPREAD_MEMORY": "most_free_memory", "BINPACK": "binpack",
		"spread": "spread", "": "", "ROUND_ROBIN": ""} {
		if got := strategyFor(policy); got != want {
			t.Fatalf("strategyFor(%q) = %q", policy, got)
		}
	}
}

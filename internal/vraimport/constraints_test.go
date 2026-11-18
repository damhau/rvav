package vraimport

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

func TestConstraintTag(t *testing.T) {
	s := scope{machines: map[string]machineRef{"db": {key: "db"}}, environment: true}
	for _, c := range []struct{ in, want, err string }{
		{in: "env:prod", want: "env/prod"},
		{in: "env:prod:hard", want: "env/prod"},
		{in: "tier:gold:soft", want: "tier/gold:soft"},
		{in: "!zone:pci", want: "!zone/pci"},
		{in: " !zone:pci:SOFT ", want: "!zone/pci:soft"},
		{in: "env:${input.environment}", want: "env/${environment}"},
		{in: "backup:${input.backup}", want: "backup/${inputs.backup}"},
		{in: "app:${to_lower(input.app)}", want: "app/${inputs.app.lowerAscii()}"},
		{in: "url:http://x", want: "url/http://x"},
		{in: "linux", err: "without a value"},
		{in: "linux:hard", err: "without a value"},
		{in: "${input.placement}", err: "whole tag comes from an expression"},
		{in: "near:${resource.db.address}", err: "reads a machine"},
		{in: "", err: "empty"},
	} {
		got, err := s.constraint(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: %q, %v; want an error with %q", c.in, got, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// TestMapConstraints: a template's constraint tags on machines, their
// storage, disks and networks are the blueprint's constraints, and those
// rvav can't write are reported (T1.2).
func TestMapConstraints(t *testing.T) {
	const content = `formatVersion: 1
inputs:
  backup:
    type: string
resources:
  db:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: small
      constraints:
        - tag: env:prod
        - tag: '!zone:pci'
        - tag: gpu
      storage:
        constraints:
          - tag: backup:${input.backup}
          - tag: tier:platinum:soft
      attachedDisks:
        - source: ${resource.logs.id}
      networks:
        - network: ${resource.app.id}
  web:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: small
      constraints:
        - tag: env:prod
      networks:
        - network: ${resource.other.id}
  logs:
    type: Cloud.vSphere.Disk
    properties:
      capacityGb: 50
      constraints:
        - tag: tier:bronze
  app:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
      constraints:
        - tag: '!zone:dmz'
  other:
    type: Cloud.vSphere.Network
    properties:
      networkType: existing
`
	e := vra.SampleEstate(time.Now())
	p := MapTemplate(&e, vra.Blueprint{ID: "bp-tags", Name: "Tagged", Versions: []vra.BlueprintVersion{
		{Version: "1", Status: "RELEASED", Content: content}}})
	if p.Doc == nil {
		t.Fatalf("plan = %s %+v", p.Status, p.Notes)
	}
	v := p.Doc.Versions[0]
	comps := v.Blueprint["components"].([]map[string]any)
	db, web := comps[0], comps[1]
	cons := db["constraints"].(map[string]any)
	if !slices.Equal(cons["compute"].([]any), []any{"env/prod", "!zone/pci"}) ||
		!slices.Equal(cons["storage"].([]any), []any{"backup/${inputs.backup}", "tier/platinum:soft"}) ||
		!slices.Equal(cons["network"].([]any), []any{"!zone/dmz"}) {
		t.Fatalf("db's constraints = %+v", cons)
	}
	if d := db["disks"].([]map[string]any)[0]; !slices.Equal(d["constraints"].([]any), []any{"tier/bronze"}) {
		t.Fatalf("db's disk = %+v", d)
	}
	if b := db["bindings"].(map[string]any); b["cluster"] != nil || b["datastore"] != nil || b["network"] != nil {
		t.Fatalf("db's bindings = %+v", b)
	}
	// web's network has no constraints: the form asks where it goes, and
	// rvav checks the requester's choice against its constraints.
	if b := web["bindings"].(map[string]any); b["cluster"] != "inputs.cluster" || b["network"] != "inputs.network" ||
		!slices.Equal(web["constraints"].(map[string]any)["compute"].([]any), []any{"env/prod"}) {
		t.Fatalf("web = %+v", web)
	}
	if _, ok := v.FormSchema["properties"].(map[string]any)["cluster"]; !ok {
		t.Fatal("the form asks for no cluster for web")
	}
	for _, want := range []struct{ level, where, text string }{
		{Unmapped, "resources.db.constraints[2]", "without a value"},
		{Info, "resources.db.constraints", "Placed by its constraint tags"},
		{Warning, "resources.web.constraints", "no constraint tags"},
	} {
		if !has(p, want.level, want.where, want.text) {
			t.Errorf("no %s note at %s with %q: %+v", want.level, want.where, want.text, p.Notes)
		}
	}
}

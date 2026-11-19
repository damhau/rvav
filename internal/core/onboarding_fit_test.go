package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/blueprint"
)

// TestFit: VMs join a component of a deployment when its count takes
// them, raised through the input that sets it within the form's bounds;
// their OS family and names must fit too (X1.5).
func TestFit(t *testing.T) {
	threeTier, err := blueprint.Parse(json.RawMessage(`{"kind": "vm", "guest": {"os_family": "linux"}, "components": [
		{"key": "web", "title": "Web", "count": "inputs.web_nodes"},
		{"key": "app", "title": "Application", "count": 1},
		{"key": "db", "title": "Database", "count": "inputs.web_nodes > 3 ? 2 : 1"},
		{"key": "win", "title": "Windows", "count": 3, "guest": {"os_family": "windows"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	single, err := blueprint.Parse(json.RawMessage(`{"kind": "vm", "guest": {"os_family": "linux"}}`))
	if err != nil {
		t.Fatal(err)
	}
	schema := json.RawMessage(`{"properties": {"web_nodes": {"type": "integer", "minimum": 1, "maximum": 4}}}`)
	comp := func(bp blueprint.Blueprint, key string) blueprint.Component {
		for _, c := range bp.ComponentList() {
			if c.Key == key {
				return c
			}
		}
		t.Fatalf("no component %s", key)
		return blueprint.Component{}
	}
	linux := func(names ...string) []seat {
		var out []seat
		for _, n := range names {
			out = append(out, seat{name: n, guestOS: "rhel9_64Guest"})
		}
		return out
	}
	have := []seat{{name: "web01", component: "web"}, {name: "web02", component: "web"}, {name: "app01", component: "app"}}
	cases := []struct {
		name   string
		f      fitting
		inputs string // the inputs recorded then, "" when they stay
		vm     int
		refuse string // part of why not
	}{
		{name: "single takes any number", f: fitting{bp: single, comp: comp(single, "vm"), label: "RHEL 9", have: linux("a", "b"),
			vms: linux("c", "d")}},
		{name: "single, same name", f: fitting{bp: single, comp: comp(single, "vm"), label: "RHEL 9", have: linux("a"),
			vms: linux("b", "a")}, vm: 1, refuse: "has a VM named a already"},
		{name: "count input raised", f: fitting{bp: threeTier, comp: comp(threeTier, "web"), schema: schema,
			inputs: map[string]any{"web_nodes": float64(2)}, have: have, vms: linux("web03")}, inputs: `{"web_nodes":3}`},
		{name: "raise changes another", f: fitting{bp: threeTier, comp: comp(threeTier, "web"), schema: schema,
			inputs: map[string]any{"web_nodes": float64(3)}, have: have, vms: linux("web03", "web04")},
			refuse: "Setting web_nodes to 4 for Web would change how many VMs Database has too"},
		{name: "count input within", f: fitting{bp: threeTier, comp: comp(threeTier, "web"), schema: schema,
			inputs: map[string]any{"web_nodes": float64(3)}, have: have, vms: linux("web03")}},
		{name: "count input unset", f: fitting{bp: threeTier, comp: comp(threeTier, "web"), schema: schema, have: nil,
			vms: linux("web01")}, inputs: `{"web_nodes":1}`},
		{name: "count input at its maximum", f: fitting{bp: threeTier, comp: comp(threeTier, "web"), schema: schema,
			inputs: map[string]any{"web_nodes": float64(3)}, have: have, vms: linux("web03", "web04", "web05")}, vm: 2,
			refuse: "Web has as many VMs as web_nodes says, 4 at most, and has 2 already"},
		{name: "constant count", f: fitting{bp: threeTier, comp: comp(threeTier, "app"), have: have, vms: linux("app02")},
			refuse: "Application has 1 VM (its count), and has 1 already"},
		{name: "constant count, room", f: fitting{bp: threeTier, comp: comp(threeTier, "app"), vms: linux("app01")}},
		{name: "OS family", f: fitting{bp: threeTier, comp: comp(threeTier, "win"), vms: linux("win01")},
			refuse: "win01 runs Linux (rhel9_64Guest): Windows VMs run Windows"},
		{name: "unknown guest", f: fitting{bp: threeTier, comp: comp(threeTier, "win"),
			vms: []seat{{name: "win01", guestOS: "otherGuest64"}}}},
		{name: "count of no input", f: fitting{bp: threeTier, comp: comp(threeTier, "db"), vms: linux("db01")},
			refuse: "can't be worked out from the deployment's inputs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, m := fit(c.f)
			switch {
			case c.refuse == "" && m != nil:
				t.Fatalf("refused: %s", m.msg)
			case c.refuse != "" && (m == nil || !strings.Contains(m.msg, c.refuse) || m.vm != c.vm || m.code != "no_fit"):
				t.Fatalf("misfit = %+v, want VM %d refused with %q", m, c.vm, c.refuse)
			}
			b, _ := json.Marshal(got)
			if want := c.inputs; want != "" && string(b) != want || want == "" && got != nil {
				t.Fatalf("inputs = %s, want %q", b, want)
			}
		})
	}
}

package core

import (
	"slices"
	"testing"

	"github.com/damhau/rvav/internal/api"
)

func TestCheckGroup(t *testing.T) {
	yes := true
	opts := []string{"CC-1", "CC-2"}
	w := api.PropertyGroupWrite{Name: "Ops", Inputs: []api.PropertyGroupInput{
		{Name: "costCenter", Label: "Cost centre", Type: api.PropertyGroupInputTypeString, Options: &opts, Default: "CC-1"},
		{Name: "disks", Label: "Disks", Type: api.PropertyGroupInputTypeInteger, Default: 2.0},
		{Name: "token", Label: "Token", Type: api.PropertyGroupInputTypeString, Secret: &yes},
	}, Constants: map[string]any{"domain": "corp.example", "tier": 2.0, "on": true}}
	if errs := checkGroup(w); len(errs) != 0 {
		t.Fatalf("a good group = %+v", errs)
	}

	w = api.PropertyGroupWrite{Name: " ", Inputs: []api.PropertyGroupInput{
		{Name: "1x", Label: "x", Type: api.PropertyGroupInputTypeString},
		{Name: "a", Label: "", Type: "date"},
		{Name: "a", Label: "A", Type: api.PropertyGroupInputTypeInteger, Default: 1.5, Options: &opts},
		{Name: "b", Label: "B", Type: api.PropertyGroupInputTypeString, Options: &opts, Default: "CC-9"},
		{Name: "c", Label: "C", Type: api.PropertyGroupInputTypeString, Secret: &yes, Default: "plain"},
		{Name: "d", Label: "D", Type: api.PropertyGroupInputTypeString, Secret: &yes, ExposeToInventory: &yes},
	}, Constants: map[string]any{"bad-key": "x", "nested": map[string]any{"a": 1.0}}}
	var got []string
	for _, e := range checkGroup(w) {
		got = append(got, e.Field)
	}
	want := []string{"/name", "/inputs/0/name", "/inputs/1/label", "/inputs/1/type", "/inputs/2/name", "/inputs/2/options",
		"/inputs/2/default", "/inputs/3/default", "/inputs/4/default", "/inputs/5/expose_to_inventory", "/constants/bad-key", "/constants/nested"}
	if !slices.Equal(got, want) {
		t.Fatalf("errors at\n %v, want\n %v", got, want)
	}
}

func TestWholeNumber(t *testing.T) {
	for _, c := range []struct {
		in   any
		want int
		ok   bool
	}{{2.0, 2, true}, {2.5, 2, false}, {3, 3, true}, {int64(4), 4, true}, {"2", 0, false}, {nil, 0, false}} {
		if got, ok := wholeNumber(c.in); got != c.want || ok != c.ok {
			t.Errorf("wholeNumber(%v) = %d, %v", c.in, got, ok)
		}
	}
}

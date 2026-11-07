package provision

import (
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/secretinput"
)

func TestFillMessage(t *testing.T) {
	vars := policy.Vars{"inputs": map[string]any{"name": "web", "count": float64(3), "big": float64(12000000), "on": true, "none": nil,
		"tags": []any{"a"}, "api": map[string]any{"x": 1}, "password": secretinput.Marker("password")}, "steps": map[string]any{}}
	for tpl, want := range map[string]string{
		"plain":                        "plain",
		"${inputs.name} is up":         "web is up",
		"${inputs.count} of them":      "3 of them",
		"${inputs.big}":                "12000000",
		"on: ${inputs.on}":             "on: true",
		"[${inputs.none}]":             "[]",
		"a ${'}'} brace":               "a } brace",
		"${inputs.name}${inputs.name}": "webweb",
	} {
		got, err := fillMessage(tpl, vars)
		if err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", tpl, got, err, want)
		}
	}
	// What can't be written as text fails, its literals masked.
	for tpl, why := range map[string]string{
		"${inputs.tags}":                    "single value",
		"${inputs.api}":                     "single value",
		"${inputs.password}":                "secret input",
		"${inputs.absent + 'lit-secret'}":   "wasn't sent",
		"${inputs.name + 1 + 'lit-secret'}": "wasn't sent",
		"unclosed ${inputs.name":            "can't be read",
	} {
		_, err := fillMessage(tpl, vars)
		if err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%q: %v, want %q", tpl, err, why)
		} else if strings.Contains(err.Error(), "lit-secret") {
			t.Errorf("%q reads its literal: %v", tpl, err)
		}
	}
}

// A plan holds a branch's sides flat, after their branch: rows as any other,
// which earlier code reads as such.
func TestBranchSteps(t *testing.T) {
	side := func(key string) CompositeStep {
		return CompositeStep{Key: key, Title: key, Wait: &blueprint.Wait{For: "1h"}}
	}
	p := Plan{Composite: []CompositeStep{
		side("first"),
		{Key: "choose", Title: "Choose", Group: 1, Branch: &BranchPlan{
			Cases: []BranchSide{{When: "true", Steps: []CompositeStep{side("a"), side("b")}}, {When: "false"}},
			Else:  []CompositeStep{side("c")}}},
		{Key: "tell", Title: "Tell", Group: 2, Notify: &blueprint.Notify{Requester: true, Message: "Secret text"}},
	}}
	var keys []string
	for _, d := range p.Steps("dev") {
		if strings.HasPrefix(d.Key, KeyStep) {
			keys = append(keys, strings.TrimPrefix(d.Key, KeyStep))
			if strings.Contains(d.Detail, "Secret") || d.Parent != "" {
				t.Errorf("%s = %+v", d.Key, d)
			}
		}
	}
	if got := strings.Join(keys, " "); got != "first choose a b c tell" {
		t.Fatalf("the rows = %s", got)
	}
	b := p.Composite[1].Branch
	for taken, want := range map[int]int{1: 2, 2: 0, 0: 1, 9: 1} {
		if got := len(b.side(taken)); got != want {
			t.Errorf("case %d: %d steps, want %d", taken, got, want)
		}
	}
}

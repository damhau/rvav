package blueprint

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/policy"
)

func TestParseConstraint(t *testing.T) {
	for in, want := range map[string]string{
		"rvav-tier/gold":       "rvav-tier/gold",
		"rvav-tier/gold:hard":  "rvav-tier/gold",
		" !rvav-zone/dmz ":     "!rvav-zone/dmz",
		"rvav-tier/gold:soft":  "rvav-tier/gold:soft",
		"!pci/yes:soft":        "!pci/yes:soft",
		"site/gva/building-2":  "site/gva/building-2",
		"env:prod":             `"env:prod" is vRA's way: rvav writes category/tag, as env/prod`,
		"gold":                 `"gold" is not category/tag, as rvav-tier/gold`,
		"/gold":                `"/gold" is not category/tag, as rvav-tier/gold`,
		"rvav-tier/":           `"rvav-tier/" is not category/tag, as rvav-tier/gold`,
		"rvav-tier/gold:other": "rvav-tier/gold:other",
	} {
		c, err := ParseConstraint(in)
		got := c.String()
		if err != nil {
			got = err.Error()
		}
		if got != want {
			t.Errorf("%q: got %s, want %s", in, got, want)
		}
	}
}

func TestConstraintMeets(t *testing.T) {
	gold := []string{"rvav-tier/gold", "rvav-zone/app"}
	for _, tc := range []struct {
		c    Constraint
		tags []string
		want bool
	}{
		{Constraint{Tag: "rvav-tier/gold"}, gold, true},
		{Constraint{Tag: "rvav-tier/gold"}, nil, false},
		{Constraint{Tag: "rvav-zone/dmz", Not: true}, gold, true},
		{Constraint{Tag: "rvav-zone/app", Not: true}, gold, false},
		{Constraint{Tag: "rvav-zone/dmz", Not: true}, nil, true},
	} {
		if got := tc.c.Meets(tc.tags); got != tc.want {
			t.Errorf("%s on %v = %v", tc.c, tc.tags, got)
		}
	}
}

func TestConstraintsInheritAndEvaluate(t *testing.T) {
	b, err := Parse(json.RawMessage(`{
	  "kind": "vm",
	  "constraints": {"compute": ["rvav-tier/${inputs.tier}", "!rvav-zone/dmz"], "storage": ["rvav-tier/gold:soft"]},
	  "components": [
	    {"key": "web", "constraints": {"compute": ["${inputs.extra}"]},
	     "disks": [{"size_gb": 10}, {"size_gb": 20, "constraints": ["rvav-tier/${inputs.log_tier}:soft"]}]},
	    {"key": "db", "constraints": {"storage": [], "network": ["${environment == 'prd' ? 'pci/yes' : ''}"]}}
	  ]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	cs := b.ComponentList()
	web, db := cs[0], cs[1]
	if fmt.Sprint(web.Constraints) != fmt.Sprint(&Constraints{Compute: []string{"${inputs.extra}"}, Storage: []string{"rvav-tier/gold:soft"}}) {
		t.Errorf("web inherits = %+v", web.Constraints)
	}
	if fmt.Sprint(db.Constraints) != fmt.Sprint(&Constraints{Compute: []string{"rvav-tier/${inputs.tier}", "!rvav-zone/dmz"},
		Storage: []string{}, Network: []string{"${environment == 'prd' ? 'pci/yes' : ''}"}}) {
		t.Errorf("db inherits = %+v", db.Constraints)
	}

	vars := policy.Vars{"inputs": map[string]any{"tier": "gold", "extra": []any{"rvav-zone/app", "!rvav-site/old:soft"}, "log_tier": "silver"},
		"environment": "prd"}
	show := func(list []Constraint, problems []ConstraintProblem) string {
		var out []string
		for _, c := range list {
			out = append(out, fmt.Sprintf("%s %s %d", c.Kind, c, c.Disk))
		}
		for _, p := range problems {
			out = append(out, fmt.Sprintf("%s missing=%v", p.At, p.Missing))
		}
		return strings.Join(out, ", ")
	}
	if got := show(web.EvaluateConstraints(vars)); got != "compute rvav-zone/app 0, compute !rvav-site/old:soft 0, storage rvav-tier/gold:soft 0, storage rvav-tier/silver:soft 2" {
		t.Errorf("web = %s", got)
	}
	if got := show(db.EvaluateConstraints(vars)); got != "compute rvav-tier/gold 0, compute !rvav-zone/dmz 0, network pci/yes 0" {
		t.Errorf("db = %s", got)
	}
	// Text giving none adds no constraint; CEL failing is missing; what
	// isn't a constraint says so.
	vars = policy.Vars{"inputs": map[string]any{"tier": "", "extra": "gold"}, "environment": "dev"}
	if got := show(web.EvaluateConstraints(vars)); got != "storage rvav-tier/gold:soft 0, constraints/compute/0 missing=false, disks/1/constraints/0 missing=true" {
		t.Errorf("web, other inputs = %s", got)
	}
	if got := show(db.EvaluateConstraints(vars)); got != "compute !rvav-zone/dmz 0" {
		t.Errorf("db, other inputs = %s", got)
	}
}

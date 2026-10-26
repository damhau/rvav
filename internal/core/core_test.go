package core

import (
	"encoding/json"
	"testing"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/naming"
	"github.com/damhau/rvav/internal/policy"
)

const formSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object", "additionalProperties": false,
  "required": ["role", "size", "post_config"],
  "properties": {
    "role": {"type": "string", "pattern": "^[a-z]{2,12}$"},
    "size": {"type": "string", "enum": ["S", "M"]},
    "post_config": {"type": "array", "items": {"type": "string"}, "contains": {"const": "linux-baseline"}}
  }
}`

func TestValidateInputs(t *testing.T) {
	errs, err := validateInputs("v-test", json.RawMessage(formSchema), map[string]any{
		"role": "App!", "size": "XL", "post_config": []any{"idm-join"}, "extra": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range errs {
		got[e.Field] = *e.Code
	}
	want := map[string]string{"/inputs/role": "pattern", "/inputs/size": "enum", "/inputs/post_config": "contains", "/inputs/extra": "unknown_field"}
	for f, c := range want {
		if got[f] != c {
			t.Errorf("%s: code %q, want %q (all: %v)", f, got[f], c, got)
		}
	}
	errs, _ = validateInputs("v-test", json.RawMessage(formSchema), map[string]any{})
	if len(errs) != 3 || *errs[0].Code != "required" {
		t.Fatalf("missing fields = %+v", errs)
	}
	if errs, _ := validateInputs("v-test", json.RawMessage(formSchema), map[string]any{"role": "app", "size": "M", "post_config": []any{"linux-baseline"}}); len(errs) != 0 {
		t.Fatalf("valid inputs reported %+v", errs)
	}
}

func TestApprovalConditions(t *testing.T) {
	vars := policy.Vars{"environment": "prd", "vm": map[string]any{"memory_gb": 48}, "item": map[string]any{"category": "compute"}}
	for expr, want := range map[string]bool{
		`environment == "prd"`:                      true,
		`environment == "prd" && vm.memory_gb > 32`: true,
		`item.category == "databases"`:              false,
		`true`:                                      true,
	} {
		got, err := policy.Bool(expr, vars)
		if err != nil || got != want {
			t.Errorf("%s = %v, %v; want %v", expr, got, err, want)
		}
	}
	// Form numbers arrive as JSON numbers (float64); comparing them with
	// integer literals must work.
	if ok, err := policy.Bool(`inputs.memoryGB > 32`, policy.Vars{"inputs": map[string]any{"memoryGB": float64(48)}}); err != nil || !ok {
		t.Fatalf("float input comparison = %v, %v", ok, err)
	}
	if _, err := policy.Bool(`inputs.missing > 1`, policy.Vars{}); err == nil {
		t.Fatal("missing input should be an error")
	}
}

func TestBlueprintBindings(t *testing.T) {
	bp := blueprint.Blueprint{
		Kind: blueprint.KindVM, Template: "tpl-rhel9.4-2026.10",
		Sizes: map[string]blueprint.Size{"M": {VCPU: 4, MemoryGB: 16, DiskGB: 80}},
		Bindings: map[string]string{"vcpu": "sizes[inputs.size].vcpu", "memory_gb": "sizes[inputs.size].memory_gb",
			"disk_gb": "sizes[inputs.size].disk_gb", "cluster": "inputs.cluster", "post_config": "inputs.post_config"},
	}
	plan, missing := bp.EvaluateVM(map[string]any{"size": "M", "cluster": "GVA-DEV-CL01", "post_config": []any{"idm-join", "linux-baseline"}})
	if plan.VCPU != 4 || plan.MemoryGB != 16 || plan.DiskGB != 80 || plan.Cluster != "GVA-DEV-CL01" || len(missing) != 0 {
		t.Fatalf("plan = %+v missing %v", plan, missing)
	}
	if got := bp.OrderedPostConfig(plan.PostConfig, []string{"linux-baseline", "idm-join"}); got[0] != "linux-baseline" {
		t.Fatalf("post-config order = %v", got)
	}
	_, missing = bp.EvaluateVM(map[string]any{})
	if len(missing) == 0 {
		t.Fatal("empty inputs should report missing bindings")
	}
}

func TestNaming(t *testing.T) {
	tpl, err := naming.Parse("{project}-{environment}-{inputs.role}{seq:03}")
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := tpl.Prefix(naming.Values("web", "dev", map[string]any{"role": "App"}))
	if err != nil || prefix != "web-dev-app" {
		t.Fatalf("prefix = %q, %v", prefix, err)
	}
	if name := tpl.Name(prefix, 7); name != "web-dev-app007" || !naming.Valid(name) {
		t.Fatalf("name = %q", name)
	}
	if _, err := tpl.Prefix(naming.Values("web", "dev", nil)); err == nil {
		t.Fatal("missing role should fail")
	}
	if _, err := naming.Parse("{seq}-{project}"); err == nil {
		t.Fatal("seq must be last")
	}
}

func TestPlacementPatterns(t *testing.T) {
	if !matchAny([]string{"GVA-DEV-*"}, "GVA-DEV-CL01") || matchAny([]string{"GVA-DEV-*"}, "GVA-PRD-CL01") {
		t.Fatal("cluster pattern")
	}
	if !matchAny([]string{"dvpg-*-dev-*"}, "dvpg-app-dev-vl310") {
		t.Fatal("network pattern")
	}
}

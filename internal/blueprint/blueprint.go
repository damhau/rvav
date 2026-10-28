// Package blueprint describes what a catalog item version builds and turns
// request inputs into a concrete plan. The blueprint is stored as JSON on the
// item version; bindings are CEL expressions over the inputs.
package blueprint

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/damhau/rvav/internal/policy"
)

const (
	KindVM  = "vm"
	KindAWX = "awx"
	// KindWorkflow runs a registered custom workflow (ADR-0022).
	KindWorkflow = "workflow"
)

type Blueprint struct {
	Kind string `json:"kind"`

	// VM blueprints
	Naming       string                 `json:"naming,omitempty"`
	Template     string                 `json:"template,omitempty"`
	Guest        Guest                  `json:"guest,omitempty"`
	Sizes        map[string]Size        `json:"sizes,omitempty"`
	Bindings     map[string]string      `json:"bindings,omitempty"`
	AWXTemplates map[string]AWXTemplate `json:"awx_templates,omitempty"`

	// AWX items: the job template to launch with the inputs as extra_vars.
	AWX *AWXTemplate `json:"awx,omitempty"`

	// Workflow items: the key of the custom workflow they start, with the
	// inputs.
	Workflow string `json:"workflow,omitempty"`

	// RollbackOnFailure deletes what was created when a step fails. When
	// false, resources are kept so someone can look at the failure.
	RollbackOnFailure bool `json:"rollback_on_failure,omitempty"`
}

type Guest struct {
	OSFamily string `json:"os_family"` // linux or windows
	Domain   string `json:"domain"`
	TimeZone string `json:"time_zone,omitempty"`
}

type Size struct {
	Label    string `json:"label"`
	VCPU     int    `json:"vcpu"`
	MemoryGB int    `json:"memory_gb"`
	DiskGB   int    `json:"disk_gb"`
}

type AWXTemplate struct {
	Title       string `json:"title"`
	JobTemplate string `json:"job_template"`
	// TemplateID is the template's AWX ID, set on items made from a
	// template: it is launched by ID, whatever it is called since.
	TemplateID int  `json:"template_id,omitempty"`
	Workflow   bool `json:"workflow,omitempty"`
	// Inventory and Credentials name inputs (picked from awx.inventories and
	// awx.credentials) that are the launch's inventory and credentials
	// rather than extra variables. The template must prompt for them.
	Inventory   string   `json:"inventory,omitempty"`
	Credentials []string `json:"credentials,omitempty"`
}

// VMPlan is one VM to build, after evaluating the bindings.
type VMPlan struct {
	VCPU       int      `json:"vcpu"`
	MemoryGB   int      `json:"memory_gb"`
	DiskGB     int      `json:"disk_gb"`
	Cluster    string   `json:"cluster"`
	Datastore  string   `json:"datastore"`
	Network    string   `json:"network"`
	Template   string   `json:"template"`
	PostConfig []string `json:"post_config"`
}

func Parse(raw json.RawMessage) (Blueprint, error) {
	var b Blueprint
	if len(raw) == 0 {
		return b, fmt.Errorf("empty blueprint")
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, fmt.Errorf("parse blueprint: %w", err)
	}
	return b, nil
}

// Vars returns the CEL variables for bindings: inputs and the size presets.
func (b Blueprint) Vars(inputs map[string]any) policy.Vars {
	sizes := map[string]any{}
	for k, s := range b.Sizes {
		sizes[k] = map[string]any{"label": s.Label, "vcpu": s.VCPU, "memory_gb": s.MemoryGB, "disk_gb": s.DiskGB}
	}
	return policy.Vars{"inputs": inputs, "sizes": sizes}
}

// EvaluateVM evaluates the bindings. Bindings that cannot be evaluated yet
// (for example a cluster not chosen) leave their field empty and are
// reported in missing, so a preview can still show partial results.
func (b Blueprint) EvaluateVM(inputs map[string]any) (plan VMPlan, missing []string) {
	vars := b.Vars(inputs)
	plan.Template = b.Template
	intField := func(key string, dst *int) {
		expr, ok := b.Bindings[key]
		if !ok {
			return
		}
		v, err := policy.Eval(expr, vars)
		if err != nil {
			missing = append(missing, key)
			return
		}
		switch n := v.(type) {
		case int64:
			*dst = int(n)
		case float64:
			*dst = int(math.Round(n))
		default:
			missing = append(missing, key)
		}
	}
	strField := func(key string, dst *string) {
		expr, ok := b.Bindings[key]
		if !ok {
			return
		}
		v, err := policy.Eval(expr, vars)
		if s, ok2 := v.(string); err == nil && ok2 && s != "" {
			*dst = s
			return
		}
		missing = append(missing, key)
	}
	intField("vcpu", &plan.VCPU)
	intField("memory_gb", &plan.MemoryGB)
	intField("disk_gb", &plan.DiskGB)
	strField("cluster", &plan.Cluster)
	strField("datastore", &plan.Datastore)
	strField("network", &plan.Network)
	if expr, ok := b.Bindings["template"]; ok {
		if v, err := policy.Eval(expr, vars); err == nil {
			if s, ok := v.(string); ok && s != "" {
				plan.Template = s
			}
		}
	}
	if expr, ok := b.Bindings["post_config"]; ok {
		if v, err := policy.Eval(expr, vars); err == nil {
			if list, ok := v.([]any); ok {
				for _, x := range list {
					if s, ok := x.(string); ok {
						plan.PostConfig = append(plan.PostConfig, s)
					}
				}
			}
		}
	}
	return plan, missing
}

// OrderedPostConfig returns the chosen post-config templates in the order
// the blueprint lists them, so jobs always run in a stable order.
func (b Blueprint) OrderedPostConfig(chosen []string, order []string) []string {
	set := map[string]bool{}
	for _, c := range chosen {
		set[c] = true
	}
	var out []string
	for _, k := range order {
		if set[k] {
			out = append(out, k)
			delete(set, k)
		}
	}
	for _, c := range chosen {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}

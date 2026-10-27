package day2

import (
	"encoding/json"
	"strings"

	"github.com/damhau/rvav/internal/api"
)

// AWXDef is a day-2 action backed by an AWX template, as the sync service
// and the administrators set it (ADR-0018).
type AWXDef struct {
	Key, Name, Template string
	TemplateID          int
	Workflow            bool
	Types               []string
	// Condition is a CEL expression over the day-2 policy variables; the
	// action is offered only where it is true ("": everywhere).
	Condition                 string
	Schema, UISchema, Initial json.RawMessage
}

// AWX ops: the inventory refresh and the job, run by the workflow.
const (
	OpRefreshInventory = "refresh_inventory"
	OpJob              = "job"
	SystemAWX          = "awx"
)

// AWXAction runs the template on the VM (or the deployment's VMs) through
// rvav's inventory, after refreshing it, with the form's inputs as extra
// variables.
func AWXAction(d AWXDef) *Action {
	a := &Action{Key: d.Key, Name: d.Name, Group: GroupAutomation, Types: d.Types, Condition: d.Condition,
		Unavailable: func(t Target, _ Choices) string {
			vms := targetVMs(t)
			if len(vms) == 0 {
				return "There is no VM to run it on."
			}
			for _, vm := range vms {
				if vm.VM.PowerState != api.On {
					return vm.Name + " must be powered on."
				}
			}
			return ""
		},
		Ops: func(t Target, inputs map[string]any) []Op {
			var names []string
			for _, vm := range targetVMs(t) {
				names = append(names, vm.Name)
			}
			limit := strings.Join(names, ",")
			kind := "job"
			if d.Workflow {
				kind = "workflow"
			}
			return []Op{
				{Key: "awx.inventory", Title: "Refresh the AWX inventory", System: SystemAWX, Op: OpRefreshInventory},
				{Key: "awx.job:" + d.Template, Title: d.Name, Detail: "AWX " + kind + " " + d.Template + " · limit=" + limit,
					System: SystemAWX, Op: OpJob, Args: map[string]any{"template": d.Template, "template_id": d.TemplateID,
						"workflow": d.Workflow, "limit": limit, "extra_vars": inputs, "action": d.Key}},
			}
		},
	}
	var schema map[string]any
	_ = json.Unmarshal(d.Schema, &schema)
	if props, _ := schema["properties"].(map[string]any); len(props) > 0 {
		form := &api.ActionForm{Schema: schema, InitialValues: map[string]any{}}
		_ = json.Unmarshal(d.UISchema, &form.UiSchema)
		_ = json.Unmarshal(d.Initial, &form.InitialValues)
		a.Form = func(Target, Choices) *api.ActionForm { return form }
	}
	return a
}

// targetVMs are the VMs an action runs on: the target VM, or the
// deployment's VMs.
func targetVMs(t Target) []Resource {
	if t.Type != DeploymentType {
		return []Resource{{ID: t.ResourceID, Type: t.Type, Name: t.ResourceName, VM: t.VM}}
	}
	var out []Resource
	for _, r := range t.Resources {
		if r.Type == vmType {
			out = append(out, r)
		}
	}
	return out
}

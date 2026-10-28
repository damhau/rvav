package day2

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
)

// Day-2 actions from custom workflows (ADR-0022): a registered workflow
// offered on deployments, as AWX templates are (ADR-0018).

// OpWorkflow runs a custom workflow as a child of the request's workflow.
const OpWorkflow = "workflow"

// WorkflowDef is a registered workflow offered as the action wf.<Key>.
type WorkflowDef struct {
	Key, Name, Type, TaskQueue string
	Timeout                    int
	Types                      []string
	Condition                  string
	// Projects, when set, are the only ones it is offered to.
	Projects []string
	Schema   json.RawMessage
}

// WorkflowKey is the action key of a registered workflow.
func WorkflowKey(key string) string { return "wf." + key }

// WorkflowAction builds the action; its form is the workflow's input schema.
func WorkflowAction(d WorkflowDef) *Action {
	a := &Action{Key: WorkflowKey(d.Key), Name: d.Name, Group: GroupAutomation, Types: d.Types, Condition: d.Condition,
		Projects: d.Projects,
		Ops: func(t Target, inputs map[string]any) []Op {
			return []Op{{Key: "workflow:" + d.Key, Title: d.Name, Detail: d.Type + " on " + t.ResourceName, System: "rvav",
				Op: OpWorkflow, Args: map[string]any{"key": d.Key, "title": d.Name, "type": d.Type, "task_queue": d.TaskQueue,
					"timeout_seconds": d.Timeout, "inputs": inputs}}}
		},
	}
	var schema map[string]any
	_ = json.Unmarshal(d.Schema, &schema)
	if props, _ := schema["properties"].(map[string]any); len(props) > 0 {
		form := FormFromSchema(schema)
		a.Form = func(Target, Choices) *api.ActionForm { return form }
	}
	return a
}

// FormFromSchema lays a JSON Schema out as a form: a field per property,
// in one section, its defaults as initial values.
func FormFromSchema(schema map[string]any) *api.ActionForm {
	props, _ := schema["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	slices.Sort(names)
	form := &api.ActionForm{Schema: schema, InitialValues: map[string]any{}}
	form.UiSchema.Fields = map[string]api.UiField{}
	for _, n := range names {
		p, _ := props[n].(map[string]any)
		label, _ := p["title"].(string)
		if label == "" {
			label = strings.ToUpper(n[:1]) + strings.ReplaceAll(n[1:], "_", " ")
		}
		f := api.UiField{Label: label, Widget: api.UiWidgetText}
		if desc, ok := p["description"].(string); ok {
			f.Help = &desc
		}
		switch p["type"] {
		case "integer", "number":
			f.Widget = api.UiWidgetNumber
		case "boolean":
			f.Widget = api.UiWidgetToggle
		}
		if enum, ok := p["enum"].([]any); ok {
			opts := make([]api.Option, 0, len(enum))
			for _, v := range enum {
				s, _ := v.(string)
				opts = append(opts, api.Option{Value: s, Label: s})
			}
			f.Widget, f.Options = api.UiWidgetSelect, &opts
		}
		if def, ok := p["default"]; ok {
			form.InitialValues[n] = def
		}
		form.UiSchema.Fields[n] = f
	}
	form.UiSchema.Layout.Kind = api.Page
	form.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "", Fields: names}}
	return form
}

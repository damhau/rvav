// Package awxcatalog keeps catalog items in step with AWX templates
// (ADR-0016): templates chosen by organisation and label become items with
// forms made from their surveys, and a survey changed in AWX is flagged with
// what changed.
package awxcatalog

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/integrations/awx"
)

var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Form makes a request form from a survey: the JSON Schema, the UI schema,
// initial values, and warnings about questions left out.
func Form(title string, survey []awx.SurveyQuestion) (schema, ui, initial map[string]any, warnings []string) {
	props, fields, initial := map[string]any{}, map[string]any{}, map[string]any{}
	required, order := []string{}, []string{}
	for _, q := range survey {
		if !variableName.MatchString(q.Variable) || q.Variable == "rvav" || strings.HasPrefix(strings.ToLower(q.Variable), "ansible_") {
			// rvav is rvav's context; ansible_* would let a user set how
			// Ansible connects (ansible_ssh_common_args and the like).
			warnings = append(warnings, fmt.Sprintf("Question %q is not in the form: its variable %q can't be used.", q.Question, q.Variable))
			continue
		}
		prop := map[string]any{}
		field := map[string]any{"label": cmpOr(q.Question, q.Variable)}
		if q.Description != "" {
			field["help"] = q.Description
		}
		options := func() []map[string]any {
			out := make([]map[string]any, len(q.Choices))
			for i, c := range q.Choices {
				out[i] = map[string]any{"value": c, "label": c}
			}
			return out
		}
		switch q.Type {
		case "text", "textarea":
			prop["type"] = "string"
			if q.Min != nil && *q.Min > 0 {
				prop["minLength"] = *q.Min
			}
			if q.Max != nil && *q.Max > 0 {
				prop["maxLength"] = *q.Max
			}
			field["widget"] = q.Type
		case "integer", "float":
			prop["type"] = map[string]string{"integer": "integer", "float": "number"}[q.Type]
			if q.Min != nil {
				prop["minimum"] = *q.Min
			}
			if q.Max != nil {
				prop["maximum"] = *q.Max
			}
			field["widget"] = "number"
		case "multiplechoice":
			prop["type"], prop["enum"] = "string", []string(q.Choices)
			field["widget"], field["options"] = "select", options()
			if len(q.Choices) <= 4 {
				field["widget"] = "segmented"
			}
		case "multiselect":
			prop["type"], prop["uniqueItems"] = "array", true
			prop["items"] = map[string]any{"type": "string", "enum": []string(q.Choices)}
			field["widget"], field["options"] = "checklist", options()
		case "password":
			warnings = append(warnings, fmt.Sprintf("Question %q is not in the form: rvav does not take passwords; AWX uses the survey's default.", q.Question))
			continue
		default:
			warnings = append(warnings, fmt.Sprintf("Question %q is not in the form: type %q is not supported.", q.Question, q.Type))
			continue
		}
		if v, ok := defaultValue(q); ok {
			initial[q.Variable] = v
		}
		if q.Required {
			required = append(required, q.Variable)
		}
		props[q.Variable], fields[q.Variable] = prop, field
		order = append(order, q.Variable)
	}
	schema = map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object",
		"additionalProperties": false, "required": required, "properties": props}
	ui = map[string]any{
		"layout": map[string]any{"kind": "page", "sections": []map[string]any{{"key": "inputs", "title": title, "columns": 2, "fields": order}}},
		"fields": fields,
	}
	return schema, ui, initial, warnings
}

// defaultValue is the question's default as the form's value: AWX keeps
// "" for none, numbers as numbers, and multiselect defaults as lines.
func defaultValue(q awx.SurveyQuestion) (any, bool) {
	switch d := q.Default.(type) {
	case nil:
		return nil, false
	case string:
		if d == "" {
			return nil, false
		}
		if q.Type == "multiselect" {
			var out []string
			for _, l := range strings.Split(d, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					out = append(out, l)
				}
			}
			return out, len(out) > 0
		}
		return d, true
	default:
		return d, true
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Diff says what changed between the survey a form was made from and the
// survey AWX has now, a line per change.
func Diff(old, cur []awx.SurveyQuestion) []string {
	byVar := func(s []awx.SurveyQuestion) map[string]awx.SurveyQuestion {
		m := map[string]awx.SurveyQuestion{}
		for _, q := range s {
			m[q.Variable] = q
		}
		return m
	}
	before, after := byVar(old), byVar(cur)
	var out []string
	for _, q := range old {
		if _, ok := after[q.Variable]; !ok {
			out = append(out, fmt.Sprintf("%s removed", q.Variable))
		}
	}
	for _, q := range cur {
		was, ok := before[q.Variable]
		if !ok {
			out = append(out, fmt.Sprintf("%s added (%s)", q.Variable, q.Type))
			continue
		}
		change := func(what, a, b string) {
			if a != b {
				out = append(out, fmt.Sprintf("%s: %s %s → %s", q.Variable, what, a, b))
			}
		}
		change("question", quoted(was.Question), quoted(q.Question))
		change("type", was.Type, q.Type)
		change("required", yesNo(was.Required), yesNo(q.Required))
		change("default", show(was.Default), show(q.Default))
		change("choices", strings.Join(was.Choices, ", "), strings.Join(q.Choices, ", "))
		change("minimum", showInt(was.Min), showInt(q.Min))
		change("maximum", showInt(was.Max), showInt(q.Max))
		change("description", quoted(was.Description), quoted(q.Description))
	}
	if len(out) == 0 && !slices.EqualFunc(old, cur, func(a, b awx.SurveyQuestion) bool { return a.Variable == b.Variable }) {
		out = append(out, "questions reordered")
	}
	return out
}

func quoted(s string) string { return "“" + s + "”" }

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func show(v any) string {
	if v == nil || v == "" {
		return "none"
	}
	return fmt.Sprint(v)
}

func showInt(p *int) string {
	if p == nil {
		return "none"
	}
	return fmt.Sprint(*p)
}

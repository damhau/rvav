package day2

import (
	"cmp"
	"encoding/json"
	"strings"
)

// Custom resources (ADR-0056): their type's day-2 actions run on them, and
// deleting their deployment deletes them first.

// CustomType is a custom resource's type in the resources table.
const CustomType = "custom"

// OpResource runs a step of a custom resource's life: its type's delete,
// update or read (args phase and resource).
const OpResource = "custom_resource"

// CustomTarget is the target type of a custom resource of a resource type:
// the actions the type offers apply to it.
func CustomTarget(typeKey string) string { return CustomType + "." + typeKey }

// IsCustom says whether a target type is a custom resource's.
func IsCustom(typ string) bool { return strings.HasPrefix(typ, CustomType+".") }

// ResourceOp is the operation running a step of a custom resource's life.
func ResourceOp(phase string, r Resource) Op {
	var props struct {
		ResourceType struct {
			Title string `json:"title"`
		} `json:"resource_type"`
		System string `json:"system"`
	}
	_ = json.Unmarshal(r.Properties, &props)
	title := map[string]string{"delete": "Delete ", "read": "Read ", "update": "Update "}[phase] + r.Name
	return Op{Key: "custom." + phase + "@" + r.ID, Title: title, Detail: props.ResourceType.Title, System: cmp.Or(props.System, "rvav"),
		Op: OpResource, Args: map[string]any{"phase": phase, "resource": r.ID}}
}

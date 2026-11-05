package core

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// What a composite's steps can run (ADR-0022), for the composite designer
// (ADR-0032): each with the inputs its step binds and the outputs later
// steps read as steps.<key>.<name>.

// stepOutputs are what a step gives later steps, by what it runs: a VM
// step's are its VMs' (provision.vmOutputs), an AWX step's its job's ID
// (FinishStep), a custom workflow's whatever it returns.
var stepOutputs = map[api.CompositeStepTargetRuns][]api.CompositeStepOutput{
	api.StepRunsVm: {
		{Name: "name", Type: "string", Description: "The first VM's name"},
		{Name: "fqdn", Type: "string", Description: "The first VM's FQDN"},
		{Name: "ip", Type: "string", Description: "The first VM's IP address"},
		{Name: "moref", Type: "string", Description: "The first VM's ID in vCenter"},
		{Name: "deployment_id", Type: "string", Description: "The deployment its VMs are in"},
		{Name: "vms", Type: "array", Description: "Every VM it built: a list of name, fqdn, ip, moref and deployment_id"},
	},
	api.StepRunsAwx: {
		{Name: "job_id", Type: "integer", Description: "The AWX job's ID"},
	},
	api.StepRunsWorkflow: {},
}

func stepTarget(kind api.CompositeStepTargetKind, ref, title string, runs api.CompositeStepTargetRuns,
	inputs []api.CompositeStepInput) api.CompositeStepTarget {
	return api.CompositeStepTarget{Kind: kind, Ref: ref, Title: title, Runs: runs, Planned: runs == api.StepRunsVm,
		Inputs: inputs, Outputs: append([]api.CompositeStepOutput{}, stepOutputs[runs]...),
		OpenOutputs: runs == api.StepRunsWorkflow}
}

// CompositeStepTargets lists the published items a step may request (not
// composites: they don't nest), the AWX templates imported from AWX and the
// custom workflows registered for composite steps: what evaluateComposite
// runs. A workflow item is left out while its workflow can't start (not
// registered for catalog items, or disabled), and a workflow offered to some
// projects only says which: a request from another is refused.
func (s *Service) CompositeStepTargets(ctx context.Context, p *auth.Principal) (api.CompositeStepTargetList, error) {
	out := api.CompositeStepTargetList{Items: []api.CompositeStepTarget{}}
	if !canAdminCatalog(p) {
		return out, ErrForbidden
	}
	var items, templates, workflows []api.CompositeStepTarget
	var after *store.Cursor
	for {
		page, err := s.Store.AdminItems(ctx, store.AdminItemFilter{State: "published", Limit: 200, After: after})
		if err != nil {
			return out, err
		}
		for _, it := range page.Items {
			if it.Type == "composite" || it.PublishedVersionID == nil {
				continue
			}
			v, err := s.Store.GetVersion(ctx, *it.PublishedVersionID)
			if err != nil {
				return out, err
			}
			bp, err := blueprint.Parse(v.Blueprint)
			if err != nil {
				continue // nothing a step could run
			}
			var runs api.CompositeStepTargetRuns
			var projects []string
			switch bp.Kind {
			case blueprint.KindVM:
				runs = api.StepRunsVm
			case blueprint.KindAWX:
				runs = api.StepRunsAwx
			case blueprint.KindWorkflow:
				runs = api.StepRunsWorkflow
				// As evaluateComposite checks it, for every project.
				reg, why, err := s.usableWorkflow(ctx, bp.Workflow, "catalog_item", "")
				if err != nil {
					return out, err
				}
				if why != "" {
					continue
				}
				projects = reg.ProjectIDs
			default:
				continue
			}
			t := stepTarget(api.StepTargetItem, it.Slug, it.Name, runs, formInputs(v.FormSchema, v.UISchema))
			t.ProjectIds = projectLimits(projects)
			t.ItemType = present.Ptr(api.CatalogItemType(it.Type))
			if it.Summary != "" {
				t.Summary = present.Ptr(it.Summary)
			}
			items = append(items, t)
		}
		if page.Next == nil {
			break
		}
		after = page.Next
	}

	rows, err := s.Store.AWXTemplates(ctx)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		if r.Gone {
			continue
		}
		var survey []awx.SurveyQuestion
		_ = json.Unmarshal(r.Survey, &survey)
		title := awxcatalog.DisplayName(r.Name)
		if r.ItemName != nil {
			title = *r.ItemName
		}
		schema, ui, _, _ := awxcatalog.Form(title, survey)
		sj, _ := json.Marshal(schema)
		uj, _ := json.Marshal(ui)
		t := stepTarget(api.StepTargetAwx, r.Name, title, api.StepRunsAwx, formInputs(sj, uj))
		t.AwxId = present.Ptr(r.AWXID)
		if r.Workflow {
			t.AwxWorkflow = present.Ptr(true)
		}
		templates = append(templates, t)
	}

	regs, err := s.Store.WorkflowRegistrations(ctx)
	if err != nil {
		return out, err
	}
	for _, reg := range regs {
		if !reg.Enabled || !slices.Contains(reg.Usages, "composite_step") {
			continue
		}
		t := stepTarget(api.StepTargetWorkflow, reg.Key, reg.Title, api.StepRunsWorkflow,
			formInputs(reg.InputSchema, nil))
		t.ProjectIds = projectLimits(reg.ProjectIDs)
		if reg.Description != "" {
			t.Summary = present.Ptr(reg.Description)
		}
		workflows = append(workflows, t)
	}

	for _, list := range [][]api.CompositeStepTarget{items, templates, workflows} {
		slices.SortStableFunc(list, func(a, b api.CompositeStepTarget) int {
			return cmp.Or(cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)), cmp.Compare(a.Ref, b.Ref))
		})
		out.Items = append(out.Items, list...)
	}
	return out, nil
}

// projectLimits are the projects a target is offered to, nil for all of them.
func projectLimits(ids []string) *[]string {
	if len(ids) == 0 {
		return nil
	}
	out := slices.Clone(ids)
	slices.Sort(out)
	return &out
}

// formInputs are a form's inputs a step binds: its JSON Schema's
// properties, in the order the UI schema's sections show them, without
// read-only ones (computed fields), titled by their labels.
func formInputs(schema, ui json.RawMessage) []api.CompositeStepInput {
	out := []api.CompositeStepInput{}
	var s struct {
		Properties map[string]struct {
			Type        any    `json:"type"`
			Title       string `json:"title"`
			Description string `json:"description"`
			ReadOnly    bool   `json:"readOnly"`
			Enum        []any  `json:"enum"`
			Items       *struct {
				Enum []any `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return out
	}
	var u struct {
		Fields map[string]struct {
			Label string `json:"label"`
			Help  string `json:"help"`
		} `json:"fields"`
		Layout struct {
			Sections []struct {
				Fields []string `json:"fields"`
			} `json:"sections"`
		} `json:"layout"`
	}
	if len(ui) > 0 {
		_ = json.Unmarshal(ui, &u)
	}
	var order []string
	for _, sec := range u.Layout.Sections {
		order = append(order, sec.Fields...)
	}
	// The rest (a schema without a UI schema, as a workflow's): required
	// first, then by name. PostgreSQL's jsonb keeps no key order.
	rest := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		rest = append(rest, name)
	}
	slices.SortFunc(rest, func(a, b string) int {
		ra, rb := slices.Contains(s.Required, a), slices.Contains(s.Required, b)
		if ra != rb {
			if ra {
				return -1
			}
			return 1
		}
		return cmp.Compare(a, b)
	})
	order = append(order, rest...)
	seen := map[string]bool{}
	for _, name := range order {
		prop, ok := s.Properties[name]
		if !ok || seen[name] || prop.ReadOnly {
			continue
		}
		seen[name] = true
		in := api.CompositeStepInput{Name: name, Title: cmp.Or(u.Fields[name].Label, prop.Title, name),
			Type: jsonType(prop.Type), Required: slices.Contains(s.Required, name)}
		if d := cmp.Or(prop.Description, u.Fields[name].Help); d != "" {
			in.Description = present.Ptr(d)
		}
		enum := prop.Enum
		if enum == nil && prop.Items != nil {
			enum = prop.Items.Enum
		}
		if len(enum) > 0 {
			in.Enum = present.Ptr(enum)
		}
		out = append(out, in)
	}
	return out
}

// jsonType is a property's type: its first other than null, when it lists several.
func jsonType(t any) string {
	switch v := t.(type) {
	case string:
		return v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

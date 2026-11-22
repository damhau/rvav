package tfprovider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/damhau/rvav/pkg/rvavclient"
)

// sameJSONAsState plans the state's JSON when the configuration's is the
// same but for its formatting, as after an import: Terraform takes the
// value before for one functionally equal to the configuration's.
type sameJSONAsState struct{}

func (sameJSONAsState) Description(context.Context) string {
	return "The state's JSON when the configuration's differs only in formatting."
}

func (m sameJSONAsState) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (sameJSONAsState) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() ||
		req.ConfigValue.ValueString() == req.StateValue.ValueString() {
		return
	}
	var config, state any
	if json.Unmarshal([]byte(req.ConfigValue.ValueString()), &config) == nil &&
		json.Unmarshal([]byte(req.StateValue.ValueString()), &state) == nil && sameJSON(config, state) {
		resp.PlanValue = req.StateValue
	}
}

// sameItem replaces a deployment for another catalog item only: naming its
// item by ID instead of slug, or by slug instead of ID, changes nothing.
type sameItem struct{ r *restResource }

func (sameItem) Description(context.Context) string {
	return "Replaces the deployment when the configuration names another catalog item."
}

func (m sameItem) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (m sameItem) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || req.PlanValue.IsUnknown() || req.PlanValue.Equal(req.StateValue) {
		return
	}
	var id, project types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("catalog_item_id"), &id)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("project_id"), &project)...)
	if resp.Diagnostics.HasError() {
		return
	}
	named := req.PlanValue.ValueString()
	same := named == id.ValueString()
	if !same && m.r.conn != nil {
		it, err := gone(object(m.r.conn.api.GetCatalogItem(ctx, named,
			&rvavclient.GetCatalogItemParams{ProjectId: project.ValueString()})))
		if err != nil {
			resp.Diagnostics.AddAttributeError(req.Path, "Can't read the catalog item", err.Error())
			return
		}
		same = it != nil && str(it, "id") == id.ValueString()
	}
	if same {
		resp.PlanValue = req.StateValue
		return
	}
	resp.RequiresReplace = true
}

// itemIcon plans the icon rvav gives an item of its type when the
// configuration gives none, so that leaving icon out sets that one again.
type itemIcon struct{}

func (itemIcon) Description(context.Context) string {
	return "rvav's icon for the item's type when not given."
}

func (m itemIcon) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (itemIcon) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if !req.ConfigValue.IsNull() {
		return
	}
	var typ types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("type"), &typ)...)
	if typ.IsUnknown() || typ.IsNull() {
		return
	}
	resp.PlanValue = types.StringValue(defaultIcon(typ.ValueString()))
}

// defaultIcon is the icon rvav gives a new item of a type without one
// (core.defaultIcon).
func defaultIcon(itemType string) string {
	switch itemType {
	case "awx_job_template":
		return "terminal"
	case "awx_workflow":
		return "workflow"
	}
	return "server"
}

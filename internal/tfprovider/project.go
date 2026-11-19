package tfprovider

import (
	"context"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/damhau/rvav/pkg/rvavclient"
)

func newProjectResource() resource.Resource {
	return &restResource{
		name:  "project",
		patch: true,
		schema: schema.Schema{
			MarkdownDescription: "A project: its environments, settings and custom properties. Destroying it archives it: " +
				"it can't request anything new, and its deployments go on. Creating a project with an archived one's code brings that one back.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("project"),
				"code": schema.StringAttribute{Required: true, PlanModifiers: replaced(),
					MarkdownDescription: "Two to eight lower-case letters and digits, in VM names. It can't change."},
				"name":        schema.StringAttribute{Required: true, MarkdownDescription: "The project's name."},
				"description": schema.StringAttribute{Optional: true, MarkdownDescription: "What the project is for."},
				"cost_center": schema.StringAttribute{Optional: true, MarkdownDescription: "Its cost center."},
				"environments": schema.SetAttribute{Required: true, ElementType: types.StringType,
					MarkdownDescription: "The environments it may deploy to, by key."},
				"properties": schema.MapAttribute{Optional: true, ElementType: types.StringType,
					MarkdownDescription: "Custom properties: its VMs get them as vSphere custom attributes, and bindings read them."},
				"deployment_sharing": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("all_members"),
					MarkdownDescription: "Who sees its deployments: `all_members`, or `owner_only` (each their own)."},
			},
		},
		create: createProject,
		read:   readProject,
		update: updateProject,
		del: func(ctx context.Context, c *conn, id string, _ map[string]any) error {
			return done(c.api.UpdateProjectWithBody(ctx, id, jsonType, send(map[string]any{"archived": true})))
		},
	}
}

func createProject(ctx context.Context, c *conn, body map[string]any) (map[string]any, error) {
	sharing := body["deployment_sharing"]
	delete(body, "deployment_sharing")
	obj, err := object(c.api.CreateProjectWithBody(ctx, jsonType, send(body)))
	if rvavclient.IsStatus(err, http.StatusConflict) {
		if back, err2 := unarchive(ctx, c, body); err2 != nil || back != nil {
			obj, err = back, err2
		}
	}
	if err != nil {
		return nil, err
	}
	if err := setSharing(ctx, c, str(obj, "id"), sharing, obj["deployment_sharing"]); err != nil {
		return nil, err
	}
	return readProject(ctx, c, str(obj, "id"), nil)
}

// unarchive brings back the archived project with the code, set as body
// says; nil when no archived project has it.
func unarchive(ctx context.Context, c *conn, body map[string]any) (map[string]any, error) {
	archived := true
	list, err := object(c.api.ListAdminProjects(ctx, &rvavclient.ListAdminProjectsParams{Archived: &archived}))
	old, err := find(list, err, "code", str(body, "code"))
	if err != nil || old == nil || old["archived"] != true {
		return nil, err
	}
	change := map[string]any{"archived": false, "description": "", "cost_center": "", "properties": map[string]any{}}
	for k, v := range body {
		if k != "code" {
			change[k] = v
		}
	}
	return object(c.api.UpdateProjectWithBody(ctx, str(old, "id"), jsonType, send(change)))
}

func readProject(ctx context.Context, c *conn, id string, _ map[string]any) (map[string]any, error) {
	obj, err := gone(object(c.api.GetAdminProject(ctx, id)))
	if obj == nil || err != nil || obj["archived"] == true {
		return nil, err
	}
	return obj, nil
}

func updateProject(ctx context.Context, c *conn, id string, body, state map[string]any) (map[string]any, error) {
	sharing := body["deployment_sharing"]
	delete(body, "deployment_sharing")
	delete(body, "code")
	if err := done(c.api.UpdateProjectWithBody(ctx, id, jsonType, send(body))); err != nil {
		return nil, err
	}
	if err := setSharing(ctx, c, id, sharing, state["deployment_sharing"]); err != nil {
		return nil, err
	}
	return readProject(ctx, c, id, nil)
}

func setSharing(ctx context.Context, c *conn, id string, want, now any) error {
	if want == nil || want == now {
		return nil
	}
	return done(c.api.SetDeploymentSharingWithBody(ctx, id, jsonType, send(map[string]any{"deployment_sharing": want})))
}

// projectDataSource finds a project the caller sees, by code or ID.
type projectDataSource struct{ conn *conn }

func newProjectDataSource() datasource.DataSource { return &projectDataSource{} }

func (d *projectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "A project you see, found by its code or ID.",
		Attributes: map[string]dschema.Attribute{
			"id":                 dschema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "The project's ID. Give it or `code`."},
			"code":               dschema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "The project's code. Give it or `id`."},
			"name":               dschema.StringAttribute{Computed: true},
			"description":        dschema.StringAttribute{Computed: true},
			"cost_center":        dschema.StringAttribute{Computed: true},
			"environments":       dschema.SetAttribute{Computed: true, ElementType: types.StringType},
			"deployment_sharing": dschema.StringAttribute{Computed: true},
		},
	}
}

func (d *projectDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.conn = configured(req.ProviderData, &resp.Diagnostics)
}

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var key struct {
		ID   types.String `tfsdk:"id"`
		Code types.String `tfsdk:"code"`
	}
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, pathOf("id"), &key.ID)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, pathOf("code"), &key.Code)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if key.ID.ValueString() == "" && key.Code.ValueString() == "" {
		resp.Diagnostics.AddError("No project given", "Give the project's code or id.")
		return
	}
	var obj map[string]any
	var err error
	if id := key.ID.ValueString(); id != "" {
		obj, err = gone(object(d.conn.api.GetProject(ctx, id)))
	} else {
		obj, err = d.byCode(ctx, key.Code.ValueString())
	}
	if err == nil && obj == nil {
		resp.Diagnostics.AddError("No such project", "You see no project "+key.ID.ValueString()+key.Code.ValueString()+".")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Can't read the project", err.Error())
		return
	}
	readData(&resp.State, &resp.Diagnostics, obj)
}

func (d *projectDataSource) byCode(ctx context.Context, code string) (map[string]any, error) {
	var cursor *string
	for {
		page, err := object(d.conn.api.ListProjects(ctx, &rvavclient.ListProjectsParams{Cursor: cursor}))
		if err != nil {
			return nil, err
		}
		if p, _ := find(page, nil, "code", code); p != nil {
			return p, nil
		}
		next, _ := page["next_cursor"].(string)
		if next == "" {
			return nil, nil
		}
		cursor = &next
	}
}

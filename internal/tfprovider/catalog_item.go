package tfprovider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/damhau/rvav/pkg/rvavclient"
)

func newCatalogItemResource() resource.Resource {
	return &restResource{
		name:  "catalog_item",
		patch: true,
		schema: schema.Schema{
			MarkdownDescription: "A catalog item and the version Terraform writes: a change of `definition` makes a new version, " +
				"published unless `publish` is false. Destroying an item that was requested withdraws it instead, as rvav keeps it.",
			Attributes: map[string]schema.Attribute{
				"id":      idAttribute("item"),
				"slug":    schema.StringAttribute{Required: true, MarkdownDescription: "Its ID in addresses and in rvavctl: lower-case letters, digits and dashes."},
				"name":    schema.StringAttribute{Required: true, MarkdownDescription: "Its name in the catalog."},
				"summary": schema.StringAttribute{Required: true, MarkdownDescription: "What it gives, in a line."},
				"description": schema.StringAttribute{Optional: true,
					MarkdownDescription: "A longer description, in Markdown."},
				"category": schema.StringAttribute{Required: true, MarkdownDescription: "Its category, by key."},
				"type": schema.StringAttribute{Required: true, PlanModifiers: replaced(),
					MarkdownDescription: "`vm_blueprint`, `awx_job_template`, `awx_workflow`, `composite` or `workflow`. It can't change."},
				"icon": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{itemIcon{}},
					MarkdownDescription: "One of rvav's icons: when not given, the one rvav gives items of its type."},
				"tags":     schema.SetAttribute{Optional: true, ElementType: types.StringType},
				"featured": schema.BoolAttribute{Optional: true, MarkdownDescription: "Shown first in the catalog."},
				"definition": schema.StringAttribute{Optional: true, Computed: true, CustomType: jsontypes.NormalizedType{},
					PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), sameJSONAsState{}},
					MarkdownDescription: "The version's definition as JSON: `form_schema`, `ui_schema`, `initial_values` and `blueprint`, " +
						"as the item's YAML export has them (for example `jsonencode(yamldecode(file(\"rhel9.yaml\")))`). " +
						"rvav starts a new item with a starter definition when not given."},
				"publish": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
					MarkdownDescription: "Publish each version Terraform writes, deprecating the one before. When false, Terraform keeps the item's draft."},
				"changelog": schema.StringAttribute{Optional: true, MarkdownDescription: "What changed, on the versions Terraform writes."},
				"all_projects": schema.BoolAttribute{Optional: true, Computed: true,
					PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
					MarkdownDescription: "Shared with every project, those made later included. Left as it is when not given."},
				"project_ids": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType,
					PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
					MarkdownDescription: "The projects it is shared with. Left as it is when not given."},
				"version":    schema.StringAttribute{Computed: true, MarkdownDescription: "The number of the version Terraform wrote last."},
				"version_id": schema.StringAttribute{Computed: true, MarkdownDescription: "That version's ID."},
			},
		},
		create: createItem,
		read:   readItem,
		update: updateItem,
		del:    deleteItem,
	}
}

func pick(body map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := body[k]; ok {
			out[k] = v
		}
	}
	return out
}

func itemFields(body map[string]any, keys ...string) map[string]any {
	out := pick(body, keys...)
	if d, ok := body["description"]; ok {
		out["description_md"] = d
	}
	return out
}

func createItem(ctx context.Context, c *conn, body map[string]any) (map[string]any, error) {
	obj, err := object(c.api.CreateCatalogItemWithBody(ctx, jsonType, send(itemFields(body,
		"slug", "name", "summary", "category", "type", "icon", "tags", "featured", "definition"))))
	if rvavclient.IsStatus(err, http.StatusConflict) {
		if had, _ := object(c.api.GetAdminCatalogItem(ctx, str(body, "slug"))); had != nil {
			return nil, fmt.Errorf("%w\nrvav already has the item %s with this slug: one made in the web app, or one that "+
				"Terraform withdrew when it was destroyed after someone had requested it. To manage it here, import it: "+
				"terraform import rvav_catalog_item.NAME %[2]s", err, str(had, "id"))
		}
	}
	if err != nil {
		return nil, err
	}
	id := str(obj, "id")
	draft, _ := obj["draft_version"].(map[string]any)
	state := pick(body, "publish", "changelog", "definition")
	state["version_id"] = str(draft, "id")
	if body["publish"] != false {
		if err := publish(ctx, c, id, str(draft, "id"), body["changelog"]); err != nil {
			return nil, err
		}
	}
	if err := share(ctx, c, id, obj, body, nil); err != nil {
		return nil, err
	}
	return readItem(ctx, c, id, state)
}

func publish(ctx context.Context, c *conn, item, version string, changelog any) error {
	body := map[string]any{}
	if changelog != nil {
		body["changelog"] = changelog
	}
	return done(c.api.PublishCatalogItemVersionWithBody(ctx, item, version, jsonType, send(body)))
}

// share sets who may request the item, when the configuration says,
// keeping the audiences set elsewhere.
func share(ctx context.Context, c *conn, id string, item, body, state map[string]any) error {
	all, hasAll := body["all_projects"]
	ids, hasIDs := body["project_ids"]
	if (!hasAll || sameJSON(all, state["all_projects"])) && (!hasIDs || sameJSON(ids, state["project_ids"])) {
		return nil
	}
	ent, _ := item["entitlements"].(map[string]any)
	w := pick(ent, "all_projects", "project_ids", "everywhere", "audiences")
	if hasAll {
		w["all_projects"] = all
	}
	if hasIDs {
		w["project_ids"] = ids
	}
	if _, ok := w["all_projects"]; !ok {
		w["all_projects"] = false
	}
	if _, ok := w["project_ids"]; !ok {
		w["project_ids"] = []any{}
	}
	return done(c.api.SetCatalogItemEntitlementsWithBody(ctx, id, jsonType, send(w)))
}

func readItem(ctx context.Context, c *conn, id string, state map[string]any) (map[string]any, error) {
	obj, err := gone(object(c.api.GetAdminCatalogItem(ctx, id)))
	if obj == nil || err != nil {
		return nil, err
	}
	published, _ := obj["published_version"].(map[string]any)
	draft, _ := obj["draft_version"].(map[string]any)
	pub, ok := state["publish"].(bool)
	if !ok {
		pub = published != nil || draft == nil
	}
	managed := draft
	if pub {
		managed = published
	}
	obj["description"] = obj["description_md"]
	obj["publish"] = pub
	obj["changelog"] = state["changelog"]
	obj["definition"], obj["version"], obj["version_id"] = nil, nil, nil
	if managed != nil {
		vid := str(managed, "id")
		obj["version"], obj["version_id"] = managed["number"], vid
		def, kept := state["definition"]
		// A published version doesn't change; a draft does, in the web app.
		if !kept || vid != str(state, "version_id") || !pub {
			v, err := object(c.api.GetCatalogItemVersion(ctx, id, vid))
			if err != nil {
				return nil, err
			}
			if !kept || !sameJSON(def, v["definition"]) {
				def = v["definition"]
			}
		}
		obj["definition"] = def
	}
	if ent, ok := obj["entitlements"].(map[string]any); ok {
		obj["all_projects"], obj["project_ids"] = ent["all_projects"], ent["project_ids"]
	}
	return obj, nil
}

func updateItem(ctx context.Context, c *conn, id string, body, state map[string]any) (map[string]any, error) {
	obj, err := object(c.api.UpdateCatalogItemWithBody(ctx, id, jsonType, send(itemFields(body,
		"slug", "name", "summary", "category", "icon", "tags", "featured"))))
	if err != nil {
		return nil, err
	}
	pub := body["publish"] != false
	draft, _ := obj["draft_version"].(map[string]any)
	next := pick(body, "publish", "changelog", "definition")
	next["version_id"] = state["version_id"]
	def, hasDef := body["definition"]
	switch {
	case hasDef && (!sameJSON(def, state["definition"]) || state["version_id"] == nil):
		v := pick(body, "definition", "changelog")
		if draft != nil {
			v, err = object(c.api.UpdateCatalogItemVersionWithBody(ctx, id, str(draft, "id"), jsonType, send(v)))
		} else {
			v, err = object(c.api.CreateCatalogItemVersionWithBody(ctx, id, jsonType, send(v)))
		}
		if err != nil {
			return nil, err
		}
		next["version_id"] = str(v, "id")
		if pub {
			err = publish(ctx, c, id, str(v, "id"), body["changelog"])
		}
	case pub && state["publish"] == false && draft != nil:
		next["version_id"] = str(draft, "id")
		err = publish(ctx, c, id, str(draft, "id"), body["changelog"])
	}
	if err != nil {
		return nil, err
	}
	if err := share(ctx, c, id, obj, body, state); err != nil {
		return nil, err
	}
	return readItem(ctx, c, id, next)
}

func deleteItem(ctx context.Context, c *conn, id string, _ map[string]any) error {
	err := done(c.api.DeleteCatalogItem(ctx, id))
	if !rvavclient.IsStatus(err, http.StatusConflict) {
		return err
	}
	obj, rerr := object(c.api.GetAdminCatalogItem(ctx, id))
	if rerr != nil {
		return err
	}
	published, _ := obj["published_version"].(map[string]any)
	if published == nil {
		return &warning{"Catalog item kept", str(obj, "name") + " was requested, so rvav keeps it; it has no requestable version."}
	}
	withdraw := map[string]any{"withdraw": true}
	if derr := done(c.api.DeprecateCatalogItemVersionWithBody(ctx, id, str(published, "id"), jsonType, send(withdraw))); derr != nil {
		return derr
	}
	return &warning{"Catalog item withdrawn", str(obj, "name") + " was requested, so rvav keeps it: it was withdrawn instead, and nobody can request it."}
}

// catalogItemDataSource finds an item by slug or ID: in a project, as its
// requesters see it, or as a catalog admin does.
type catalogItemDataSource struct{ conn *conn }

func newCatalogItemDataSource() datasource.DataSource { return &catalogItemDataSource{} }

func (d *catalogItemDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_catalog_item"
}

func (d *catalogItemDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "A catalog item, by slug or ID: as requesters see it in `project_id`, or as a catalog admin does without it.",
		Attributes: map[string]dschema.Attribute{
			"slug":       dschema.StringAttribute{Required: true, MarkdownDescription: "The item's slug or ID."},
			"project_id": dschema.StringAttribute{Optional: true, MarkdownDescription: "A project you may request it in."},
			"id":         dschema.StringAttribute{Computed: true},
			"name":       dschema.StringAttribute{Computed: true},
			"summary":    dschema.StringAttribute{Computed: true},
			"type":       dschema.StringAttribute{Computed: true},
			"category":   dschema.StringAttribute{Computed: true},
			"version":    dschema.StringAttribute{Computed: true, MarkdownDescription: "The number of the version requests get by default."},
			"version_id": dschema.StringAttribute{Computed: true},
		},
	}
}

func (d *catalogItemDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.conn = configured(req.ProviderData, &resp.Diagnostics)
}

func (d *catalogItemDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var slug, project types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, pathOf("slug"), &slug)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, pathOf("project_id"), &project)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var obj map[string]any
	var err error
	var version map[string]any
	if p := project.ValueString(); p != "" {
		obj, err = object(d.conn.api.GetCatalogItem(ctx, slug.ValueString(), &rvavclient.GetCatalogItemParams{ProjectId: p}))
		version, _ = obj["version"].(map[string]any)
	} else {
		obj, err = object(d.conn.api.GetAdminCatalogItem(ctx, slug.ValueString()))
		version, _ = obj["published_version"].(map[string]any)
	}
	if err != nil {
		resp.Diagnostics.AddError("Can't read the catalog item", err.Error())
		return
	}
	obj["slug"], obj["project_id"] = slug.ValueString(), nil
	if p := project.ValueString(); p != "" {
		obj["project_id"] = p
	}
	obj["version"], obj["version_id"] = nil, nil
	if version != nil {
		obj["version"], obj["version_id"] = version["number"], version["id"]
	}
	readData(&resp.State, &resp.Diagnostics, obj)
}

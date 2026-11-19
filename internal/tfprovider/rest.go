package tfprovider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// restResource is a resource that is one object of rvav's API, its
// attributes named as the API's fields. Its functions get and give the
// API's JSON: body is what the plan sets, state the state as it was.
type restResource struct {
	name   string
	schema schema.Schema
	// patch: an update sends the attributes the configuration no longer
	// sets as empty, as a PATCH needs to clear them.
	patch  bool
	create func(ctx context.Context, c *conn, body map[string]any) (map[string]any, error)
	// read gives nil, nil when the object is gone.
	read   func(ctx context.Context, c *conn, id string, state map[string]any) (map[string]any, error)
	update func(ctx context.Context, c *conn, id string, body, state map[string]any) (map[string]any, error)
	del    func(ctx context.Context, c *conn, id string, state map[string]any) error

	conn   *conn
	fields *field
}

// warning is a delete that left something in rvav, which Terraform says.
type warning struct{ summary, detail string }

func (w *warning) Error() string { return w.summary + ": " + w.detail }

var (
	_ resource.ResourceWithConfigure   = &restResource{}
	_ resource.ResourceWithImportState = &restResource{}
)

func (r *restResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.name
}

func (r *restResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = r.schema
}

func (r *restResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.conn = configured(req.ProviderData, &resp.Diagnostics)
}

func configured(data any, diags *diag.Diagnostics) *conn {
	if data == nil {
		return nil
	}
	c, ok := data.(*conn)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("got %T", data))
	}
	return c
}

func (r *restResource) fieldsOf() *field {
	if r.fields == nil {
		r.fields = resourceFields(r.schema.Attributes)
	}
	return r.fields
}

// put sets state to rvav's object, the way the plan or the state before
// had it where they are the same.
func (r *restResource) put(state *tfsdk.State, diags *diag.Diagnostics, obj map[string]any, prior tftypes.Value) {
	v, err := fromJSON(r.fieldsOf(), obj, prior)
	if err != nil {
		diags.AddError("Unexpected answer from rvav", err.Error())
		return
	}
	state.Raw = v
}

func (r *restResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	body, err := bodyOf(r.fieldsOf(), req.Plan.Raw, false)
	if err != nil {
		resp.Diagnostics.AddError("Invalid configuration", err.Error())
		return
	}
	// An object with an error is one made that then failed: Terraform keeps
	// it, tainted.
	obj, err := r.create(ctx, r.conn, body)
	if obj != nil {
		r.put(&resp.State, &resp.Diagnostics, obj, req.Plan.Raw)
	}
	if err != nil {
		resp.Diagnostics.AddError("rvav refused to create the "+title(r.name), err.Error())
	}
}

func (r *restResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	st := stateJSON(r.fieldsOf(), req.State.Raw)
	obj, err := r.read(ctx, r.conn, str(st, "id"), st)
	if err != nil {
		resp.Diagnostics.AddError("Can't read the "+title(r.name), err.Error())
		return
	}
	if obj == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	r.put(&resp.State, &resp.Diagnostics, obj, req.State.Raw)
}

func (r *restResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	body, err := bodyOf(r.fieldsOf(), req.Plan.Raw, r.patch)
	if err != nil {
		resp.Diagnostics.AddError("Invalid configuration", err.Error())
		return
	}
	st := stateJSON(r.fieldsOf(), req.State.Raw)
	obj, err := r.update(ctx, r.conn, str(st, "id"), body, st)
	if err != nil {
		resp.Diagnostics.AddError("rvav refused to change the "+title(r.name), err.Error())
		return
	}
	r.put(&resp.State, &resp.Diagnostics, obj, req.Plan.Raw)
}

func (r *restResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	st := stateJSON(r.fieldsOf(), req.State.Raw)
	err := r.del(ctx, r.conn, str(st, "id"), st)
	var w *warning
	switch {
	case errors.As(err, &w):
		resp.Diagnostics.AddWarning(w.summary, w.detail)
	case err != nil && !notFound(err):
		resp.Diagnostics.AddError("rvav refused to delete the "+title(r.name), err.Error())
	}
}

func (r *restResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// title is a resource type's name as words: approval policy.
func title(name string) string {
	b := []byte(name)
	for i, c := range b {
		if c == '_' {
			b[i] = ' '
		}
	}
	return string(b)
}

// idAttribute is the ID rvav gives an object.
func idAttribute(what string) schema.StringAttribute {
	return schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: "The " + what + "'s ID in rvav.",
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// replaced is a plan modifier for an attribute rvav can't change later.
func replaced() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.RequiresReplace()}
}

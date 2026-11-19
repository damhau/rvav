package tfprovider

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/damhau/rvav/pkg/rvavclient"
)

func newRoleResource() resource.Resource {
	keyed := func(obj map[string]any) map[string]any {
		obj["id"] = obj["key"]
		return obj
	}
	r := collection{
		get: func(ctx context.Context, c *conn, id string) (*http.Response, error) { return c.api.GetRole(ctx, id) },
		create: func(ctx context.Context, c *conn, b io.Reader) (*http.Response, error) {
			return c.api.CreateRoleWithBody(ctx, jsonType, b)
		},
		del: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.DeleteRole(ctx, id)
		},
		present: keyed,
	}.resource("role", schema.Schema{
		MarkdownDescription: "A custom role: a set of permissions, given to users in rvav or in a project, or to a group. " +
			"Import it by its key.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("role"),
			"key": schema.StringAttribute{Required: true, PlanModifiers: replaced(),
				MarkdownDescription: "Its key: lower-case letters, digits and dashes. It can't change."},
			"name":        required("Its name."),
			"description": optional("What it is for."),
			"permissions": schema.SetAttribute{Required: true, ElementType: types.StringType,
				MarkdownDescription: "Its permissions, for example `project.see`, `policies.manage` or `day2.power:power_on`."},
		},
	})
	r.update = func(ctx context.Context, c *conn, id string, body, _ map[string]any) (map[string]any, error) {
		delete(body, "key")
		obj, err := object(c.api.UpdateRoleWithBody(ctx, id, jsonType, send(body)))
		if obj != nil {
			keyed(obj)
		}
		return obj, err
	}
	r.patch = true
	return r
}

func newRoleAssignmentResource() resource.Resource {
	return collection{
		list: func(ctx context.Context, c *conn) (*http.Response, error) {
			return c.api.ListRoleAssignments(ctx, &rvavclient.ListRoleAssignmentsParams{})
		},
		create: func(ctx context.Context, c *conn, b io.Reader) (*http.Response, error) {
			return c.api.CreateRoleAssignmentWithBody(ctx, jsonType, b)
		},
		del: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.DeleteRoleAssignment(ctx, id)
		},
		present: func(obj map[string]any) map[string]any {
			user, _ := obj["user"].(map[string]any)
			role, _ := obj["role"].(map[string]any)
			return map[string]any{"id": obj["id"], "user_id": user["id"], "role": role["key"]}
		},
	}.resource("role_assignment", schema.Schema{
		MarkdownDescription: "A role given to a user across rvav. It acts for their API tokens too, which carry no groups.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttribute("assignment"),
			"user_id": schema.StringAttribute{Required: true, PlanModifiers: replaced(), MarkdownDescription: "The user, by ID."},
			"role":    schema.StringAttribute{Required: true, PlanModifiers: replaced(), MarkdownDescription: "The role, by key."},
		},
	})
}

func newGroupMappingResource() resource.Resource {
	return collection{
		list: func(ctx context.Context, c *conn) (*http.Response, error) { return c.api.ListGroupMappings(ctx) },
		create: func(ctx context.Context, c *conn, b io.Reader) (*http.Response, error) {
			return c.api.CreateGroupMappingWithBody(ctx, jsonType, b)
		},
		del: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.DeleteGroupMapping(ctx, id)
		},
		present: func(obj map[string]any) map[string]any {
			project, _ := obj["project"].(map[string]any)
			return map[string]any{"id": obj["id"], "oidc_group": obj["oidc_group"], "role": obj["role"], "project_id": project["id"]}
		},
	}.resource("group_mapping", schema.Schema{
		MarkdownDescription: "A role given to an identity provider group's members, across rvav or in a project.",
		Attributes: map[string]schema.Attribute{
			"id":         idAttribute("mapping"),
			"oidc_group": schema.StringAttribute{Required: true, PlanModifiers: replaced(), MarkdownDescription: "The group, as the identity provider names it."},
			"role":       schema.StringAttribute{Required: true, PlanModifiers: replaced(), MarkdownDescription: "The role, by key."},
			"project_id": schema.StringAttribute{Optional: true, PlanModifiers: replaced(),
				MarkdownDescription: "The project the role is given in, for a project role. Default: across rvav."},
		},
	})
}

// newProjectMemberResource is a user's own roles in a project, which have
// no ID in rvav: theirs is the project's and the user's, with a slash.
func newProjectMemberResource() resource.Resource {
	return &restResource{
		name: "project_member",
		schema: schema.Schema{
			MarkdownDescription: "A user's own roles in a project, besides those their groups give. Import it as `<project ID>/<user ID>`.",
			Attributes: map[string]schema.Attribute{
				"id":         idAttribute("membership"),
				"project_id": schema.StringAttribute{Required: true, PlanModifiers: replaced(), MarkdownDescription: "The project, by ID."},
				"user_id":    schema.StringAttribute{Required: true, PlanModifiers: replaced(), MarkdownDescription: "The user, by ID."},
				"roles": schema.SetAttribute{Required: true, ElementType: types.StringType,
					MarkdownDescription: "Their project roles, by key: `project-admin`, `member`, `viewer` or a custom one."},
			},
		},
		create: func(ctx context.Context, c *conn, body map[string]any) (map[string]any, error) {
			return setMember(ctx, c, str(body, "project_id"), str(body, "user_id"), body)
		},
		read: func(ctx context.Context, c *conn, id string, _ map[string]any) (map[string]any, error) {
			project, user, _ := strings.Cut(id, "/")
			list, err := gone(object(c.api.ListProjectMembers(ctx, project)))
			if list == nil || err != nil {
				return nil, err
			}
			for _, m := range items(list) {
				u, _ := m["user"].(map[string]any)
				if str(u, "id") == user && m["direct"] == true {
					return member(project, user, m), nil
				}
			}
			return nil, nil
		},
		update: func(ctx context.Context, c *conn, _ string, body, _ map[string]any) (map[string]any, error) {
			return setMember(ctx, c, str(body, "project_id"), str(body, "user_id"), body)
		},
		del: func(ctx context.Context, c *conn, _ string, state map[string]any) error {
			return done(c.api.RemoveProjectMember(ctx, str(state, "project_id"), str(state, "user_id")))
		},
	}
}

func setMember(ctx context.Context, c *conn, project, user string, body map[string]any) (map[string]any, error) {
	obj, err := object(c.api.SetProjectMemberWithBody(ctx, project, user, jsonType, send(map[string]any{"roles": body["roles"]})))
	if err != nil {
		return nil, err
	}
	return member(project, user, obj), nil
}

func member(project, user string, m map[string]any) map[string]any {
	return map[string]any{"id": project + "/" + user, "project_id": project, "user_id": user, "roles": m["direct_roles"]}
}

// userDataSource finds a user by email.
type userDataSource struct{ conn *conn }

func newUserDataSource() datasource.DataSource { return &userDataSource{} }

func (d *userDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *userDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "A user rvav knows, found by email: someone who has signed in, or whom the directory has.",
		Attributes: map[string]dschema.Attribute{
			"email": dschema.StringAttribute{Required: true, MarkdownDescription: "Their email address."},
			"id":    dschema.StringAttribute{Computed: true, MarkdownDescription: "Their ID in rvav."},
			"name":  dschema.StringAttribute{Computed: true},
		},
	}
}

func (d *userDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.conn = configured(req.ProviderData, &resp.Diagnostics)
}

func (d *userDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var email types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, pathOf("email"), &email)...)
	if resp.Diagnostics.HasError() {
		return
	}
	q := email.ValueString()
	list, err := object(d.conn.api.SearchUsers(ctx, &rvavclient.SearchUsersParams{Q: &q}))
	if err != nil {
		resp.Diagnostics.AddError("Can't search users", err.Error())
		return
	}
	for _, u := range items(list) {
		if strings.EqualFold(str(u, "email"), q) {
			readData(&resp.State, &resp.Diagnostics, map[string]any{"email": q, "id": u["id"], "name": u["name"]})
			return
		}
	}
	resp.Diagnostics.AddError("No such user", "rvav knows no user with the email "+q+".")
}

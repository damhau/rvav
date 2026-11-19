package tfprovider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"

	"github.com/damhau/rvav/pkg/rvavclient"
)

// newLeasePolicyResource is an environment's lease, or a project's
// override of it, which have no ID in rvav: theirs is the environment's
// key, with the project's after a slash.
func newLeasePolicyResource() resource.Resource {
	attrs := leaseTerms()
	attrs["id"] = idAttribute("lease")
	attrs["environment"] = schema.StringAttribute{Required: true, PlanModifiers: replaced(),
		MarkdownDescription: "The environment, by key."}
	attrs["project_id"] = schema.StringAttribute{Optional: true, PlanModifiers: replaced(),
		MarkdownDescription: "A project whose lease in the environment this overrides. Default: the environment's own."}
	return &restResource{
		name: "lease_policy",
		schema: schema.Schema{
			MarkdownDescription: "An environment's lease terms, or a project's override of them. Destroying an environment's own " +
				"leaves it as it is, as every environment has one; destroying a project's override removes it.",
			Attributes: attrs,
		},
		create: func(ctx context.Context, c *conn, body map[string]any) (map[string]any, error) {
			return leaseOf(object(c.api.SetLeasePolicyWithBody(ctx, jsonType, send(body))))
		},
		read: func(ctx context.Context, c *conn, id string, _ map[string]any) (map[string]any, error) {
			env, project, _ := strings.Cut(id, "/")
			list, err := object(c.api.ListLeasePolicies(ctx))
			if err != nil {
				return nil, err
			}
			for _, l := range items(list) {
				if str(l, "environment") == env && str(l, "project_id") == project {
					return leaseOf(l, nil)
				}
			}
			return nil, nil
		},
		update: func(ctx context.Context, c *conn, _ string, body, _ map[string]any) (map[string]any, error) {
			return leaseOf(object(c.api.SetLeasePolicyWithBody(ctx, jsonType, send(body))))
		},
		del: func(ctx context.Context, c *conn, _ string, state map[string]any) error {
			if str(state, "project_id") == "" {
				return &warning{"Lease kept", "Every environment has a lease, so rvav keeps " + str(state, "environment") + "'s as it is."}
			}
			return done(c.api.DeleteLeasePolicy(ctx, &rvavclient.DeleteLeasePolicyParams{
				Environment: str(state, "environment"), ProjectId: str(state, "project_id")}))
		},
	}
}

func leaseOf(obj map[string]any, err error) (map[string]any, error) {
	if obj == nil || err != nil {
		return obj, err
	}
	obj["id"] = str(obj, "environment")
	if p := str(obj, "project_id"); p != "" {
		obj["id"] = str(obj, "environment") + "/" + p
	}
	return obj, nil
}

func quotaEnforcement() schema.StringAttribute {
	return schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("hard"),
		MarkdownDescription: "`hard` refuses a request over the limit; `soft` warns. Default: `hard`."}
}

func quotaResource() schema.StringAttribute {
	return required("`vcpu`, `memory_gb`, `storage_gb` or `vm_count`.")
}

func quotaLimit() schema.Int64Attribute {
	return schema.Int64Attribute{Required: true, MarkdownDescription: "The most that may be used."}
}

// newProjectQuotasResource is a project's quotas, as a whole: rvav sets
// them as one list.
func newProjectQuotasResource() resource.Resource {
	return &restResource{
		name: "project_quotas",
		schema: schema.Schema{
			MarkdownDescription: "A project's quotas: what the project may use in each environment and in all of them, " +
				"and what each of its members may. Terraform sets them all: those it doesn't list are removed.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("project"),
				"project_id": schema.StringAttribute{Required: true, PlanModifiers: replaced(),
					MarkdownDescription: "The project, by ID."},
				"limits": schema.ListNestedAttribute{Optional: true,
					MarkdownDescription: "What the project may use.",
					NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
						"environment": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""),
							MarkdownDescription: "An environment, by key. Default: the project in all environments."},
						"resource":    quotaResource(),
						"limit":       quotaLimit(),
						"enforcement": quotaEnforcement(),
					}}},
				"member_limits": schema.ListNestedAttribute{Optional: true,
					MarkdownDescription: "What each member may use in the project.",
					NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
						"resource":    quotaResource(),
						"limit":       quotaLimit(),
						"enforcement": quotaEnforcement(),
					}}},
			},
		},
		create: func(ctx context.Context, c *conn, body map[string]any) (map[string]any, error) {
			return setProjectQuotas(ctx, c, str(body, "project_id"), body)
		},
		read: func(ctx context.Context, c *conn, id string, _ map[string]any) (map[string]any, error) {
			limits, err := gone(object(c.api.GetProjectQuotas(ctx, id)))
			if limits == nil || err != nil {
				return nil, err
			}
			members, err := object(c.api.GetProjectUserQuotas(ctx, id))
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": id, "project_id": id, "limits": limits["items"], "member_limits": members["items"]}, nil
		},
		update: func(ctx context.Context, c *conn, id string, body, _ map[string]any) (map[string]any, error) {
			return setProjectQuotas(ctx, c, id, body)
		},
		del: func(ctx context.Context, c *conn, id string, _ map[string]any) error {
			_, err := setProjectQuotas(ctx, c, id, map[string]any{})
			return err
		},
	}
}

func listOf(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return []any{}
}

func setProjectQuotas(ctx context.Context, c *conn, id string, body map[string]any) (map[string]any, error) {
	limits, err := object(c.api.SetProjectQuotasWithBody(ctx, id, jsonType, send(map[string]any{"items": listOf(body["limits"])})))
	if err != nil {
		return nil, err
	}
	members, err := object(c.api.SetProjectUserQuotasWithBody(ctx, id, jsonType, send(map[string]any{"items": listOf(body["member_limits"])})))
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "project_id": id, "limits": limits["items"], "member_limits": members["items"]}, nil
}

// newGlobalQuotasResource is rvav's quotas across every project. There is
// one: a configuration has one of these at most.
func newGlobalQuotasResource() resource.Resource {
	set := func(ctx context.Context, c *conn, body map[string]any) (map[string]any, error) {
		obj, err := object(c.api.SetGlobalQuotasWithBody(ctx, jsonType, send(map[string]any{"items": listOf(body["limits"])})))
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": "global", "limits": obj["items"]}, nil
	}
	return &restResource{
		name: "global_quotas",
		schema: schema.Schema{
			MarkdownDescription: "rvav's quotas across every project: for all of rvav, or for each user. There is one set of them: " +
				"Terraform sets them all, and those it doesn't list are removed. Import it with the ID `global`.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("quotas"),
				"limits": schema.ListNestedAttribute{Optional: true,
					NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
						"resource": quotaResource(),
						"per_user": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
							MarkdownDescription: "What each user may use across rvav; else all of rvav together. Default: `false`."},
						"limit":       quotaLimit(),
						"enforcement": quotaEnforcement(),
					}}},
			},
		},
		create: set,
		read: func(ctx context.Context, c *conn, _ string, _ map[string]any) (map[string]any, error) {
			obj, err := object(c.api.GetGlobalQuotas(ctx))
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": "global", "limits": obj["items"]}, nil
		},
		update: func(ctx context.Context, c *conn, _ string, body, _ map[string]any) (map[string]any, error) {
			return set(ctx, c, body)
		},
		del: func(ctx context.Context, c *conn, _ string, _ map[string]any) error {
			_, err := set(ctx, c, map[string]any{})
			return err
		},
	}
}

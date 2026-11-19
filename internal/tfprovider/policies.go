package tfprovider

import (
	"context"
	"io"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
)

func levels(must bool) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Required: must, Optional: !must,
		MarkdownDescription: "The approval levels, in order: each must approve before the next is asked.",
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"name":       required("The level's name, shown to approvers and requesters."),
			"group":      optional("An identity provider group whose members approve."),
			"approvers":  stringSet("Users who approve, by ID."),
			"roles":      stringSet("Roles whose holders in the request's project approve."),
			"manager":    schema.BoolAttribute{Optional: true, MarkdownDescription: "The requester's manager approves."},
			"mode":       optional("`any` (one approver is enough, the default) or `all`."),
			"timeout":    optional("How long the level waits, for example `48h`."),
			"on_timeout": optional("What happens after `timeout`: `escalate`, `approve` or `reject`."),
			"escalate_to": schema.SingleNestedAttribute{Optional: true,
				MarkdownDescription: "Who is asked when the level times out with `escalate`.",
				Attributes: map[string]schema.Attribute{
					"group":     optional("An identity provider group."),
					"approvers": stringSet("Users, by ID."),
				}},
		}},
	}
}

func newApprovalPolicyResource() resource.Resource {
	return collection{
		list: func(ctx context.Context, c *conn) (*http.Response, error) { return c.api.ListApprovalPolicies(ctx) },
		create: func(ctx context.Context, c *conn, b io.Reader) (*http.Response, error) {
			return c.api.CreateApprovalPolicyWithBody(ctx, jsonType, b)
		},
		update: func(ctx context.Context, c *conn, id string, b io.Reader) (*http.Response, error) {
			return c.api.UpdateApprovalPolicyWithBody(ctx, id, jsonType, b)
		},
		del: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.DeleteApprovalPolicy(ctx, id)
		},
	}.resource("approval_policy", schema.Schema{
		MarkdownDescription: "An approval policy: the requests that match its condition wait for its levels' approvers.",
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute("policy"),
			"name":         required("The policy's name."),
			"reason":       optional("What the requester is told."),
			"condition":    condition("over `inputs`, `environment`, `project`, `item`, `vm`, `user` and `quota`, for example `environment == \"prd\"`"),
			"item_type":    optional("Only items of this type: `vm_blueprint`, `awx_job_template`, `awx_workflow`, `composite` or `workflow`."),
			"project_id":   projectScope(),
			"levels":       levels(true),
			"priority":     priority(),
			"enabled":      enabled(),
			"auto_approve": optional("A CEL expression: when true, the policy is met without anyone."),
		},
	})
}

func newDay2PolicyResource() resource.Resource {
	return collection{
		list: func(ctx context.Context, c *conn) (*http.Response, error) { return c.api.ListDay2Policies(ctx) },
		create: func(ctx context.Context, c *conn, b io.Reader) (*http.Response, error) {
			return c.api.CreateDay2PolicyWithBody(ctx, jsonType, b)
		},
		update: func(ctx context.Context, c *conn, id string, b io.Reader) (*http.Response, error) {
			return c.api.UpdateDay2PolicyWithBody(ctx, id, jsonType, b)
		},
		del: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.DeleteDay2Policy(ctx, id)
		},
	}.resource("day2_policy", schema.Schema{
		MarkdownDescription: "A day-2 policy: who may run which actions on deployments, refused, or after an approval.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttribute("policy"),
			"name":    required("The policy's name."),
			"reason":  optional("What the user is told."),
			"actions": stringSet("The actions it covers: keys or patterns (`delete`, `snapshot_*`, `awx.*`, `*`)."),
			"roles":   stringSet("The roles it covers. Default: everyone."),
			"condition": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("true"),
				MarkdownDescription: "A CEL expression over the deployment and the action's inputs. Default: `true`."},
			"effect":      required("`allow`, `deny`, or `approval` (then `levels`)."),
			"levels":      levels(false),
			"project_id":  projectScope(),
			"priority":    priority(),
			"enabled":     enabled(),
			"enforcement": enforcement(),
		},
	})
}

func newNamingPolicyResource() resource.Resource {
	return collection{
		list: func(ctx context.Context, c *conn) (*http.Response, error) { return c.api.ListNamingPolicies(ctx) },
		create: func(ctx context.Context, c *conn, b io.Reader) (*http.Response, error) {
			return c.api.CreateNamingPolicyWithBody(ctx, jsonType, b)
		},
		update: func(ctx context.Context, c *conn, id string, b io.Reader) (*http.Response, error) {
			return c.api.UpdateNamingPolicyWithBody(ctx, id, jsonType, b)
		},
		del: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.DeleteNamingPolicy(ctx, id)
		},
	}.resource("naming_policy", schema.Schema{
		MarkdownDescription: "A naming policy: the template VM names are made from, and where it is offered.",
		Attributes: map[string]schema.Attribute{
			"id":       idAttribute("policy"),
			"name":     required("The policy's name."),
			"template": required("The name's template, for example `{project}-{environment}-{role}{seq:03}`."),
			"counter": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("prefix"),
				MarkdownDescription: "`prefix`: one counter per name before the digits (the default); `policy`: one for the policy."},
			"start": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(1),
				MarkdownDescription: "The counter's first value. Default: `1`."},
			"increment": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(1),
				MarkdownDescription: "What the counter adds each time. Default: `1`."},
			"offers": schema.ListNestedAttribute{Optional: true,
				MarkdownDescription: "Where it is offered: a project, an environment, or both. Default: nowhere.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"project_id":  projectScope(),
					"environment": environmentScope(),
				}}},
		},
	})
}

func leaseTerms() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"default_days":      number("The lease a request gets when it doesn't choose. Default: none, no lease."),
		"max_days":          number("The longest lease a request or an extension may give."),
		"max_extensions":    schema.Int64Attribute{Required: true, MarkdownDescription: "How many times a lease may be extended."},
		"grace_days":        schema.Int64Attribute{Required: true, MarkdownDescription: "Days between a lease's end and the deployment's deletion."},
		"expiry_action":     required("`power_off_then_delete`, `delete` or `notify_only`."),
		"enforcement":       enforcement(),
		"max_lifetime_days": number("The longest a deployment may live, extensions included."),
	}
}

func newLeaseRuleResource() resource.Resource {
	attrs := leaseTerms()
	attrs["id"] = idAttribute("rule")
	attrs["name"] = required("The rule's name.")
	attrs["condition"] = condition("over the request: `item`, `inputs`, `project`, `user`")
	attrs["environment"] = environmentScope()
	attrs["project_id"] = projectScope()
	attrs["priority"] = priority()
	return collection{
		list: func(ctx context.Context, c *conn) (*http.Response, error) { return c.api.ListLeaseRules(ctx) },
		create: func(ctx context.Context, c *conn, b io.Reader) (*http.Response, error) {
			return c.api.CreateLeaseRuleWithBody(ctx, jsonType, b)
		},
		update: func(ctx context.Context, c *conn, id string, b io.Reader) (*http.Response, error) {
			return c.api.UpdateLeaseRuleWithBody(ctx, id, jsonType, b)
		},
		del: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.DeleteLeaseRule(ctx, id)
		},
	}.resource("lease_rule", schema.Schema{
		MarkdownDescription: "A lease rule: the lease terms of the requests its condition matches, before the environment's.",
		Attributes:          attrs,
	})
}

func newDeploymentLimitPolicyResource() resource.Resource {
	return collection{
		list: func(ctx context.Context, c *conn) (*http.Response, error) {
			return c.api.ListDeploymentLimitPolicies(ctx)
		},
		create: func(ctx context.Context, c *conn, b io.Reader) (*http.Response, error) {
			return c.api.CreateDeploymentLimitPolicyWithBody(ctx, jsonType, b)
		},
		update: func(ctx context.Context, c *conn, id string, b io.Reader) (*http.Response, error) {
			return c.api.UpdateDeploymentLimitPolicyWithBody(ctx, id, jsonType, b)
		},
		del: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.DeleteDeploymentLimitPolicy(ctx, id)
		},
	}.resource("deployment_limit_policy", schema.Schema{
		MarkdownDescription: "A deployment limit policy: how many deployments, and how large, a user or a project may have.",
		Attributes: map[string]schema.Attribute{
			"id":                       idAttribute("policy"),
			"name":                     required("The policy's name."),
			"project_id":               projectScope(),
			"environment":              environmentScope(),
			"priority":                 priority(),
			"enabled":                  enabled(),
			"enforcement":              enforcement(),
			"deployments_per_user":     number("Deployments each user may own."),
			"deployments_per_project":  number("Deployments the project may have."),
			"vcpu_per_deployment":      number("vCPUs a deployment may have."),
			"memory_gb_per_deployment": number("Memory a deployment may have, in GB."),
		},
	})
}

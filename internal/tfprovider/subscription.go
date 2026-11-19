package tfprovider

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"

	"github.com/damhau/rvav/pkg/rvavclient"
)

func newSubscriptionResource() resource.Resource {
	return collection{
		get: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.GetSubscription(ctx, id)
		},
		create: func(ctx context.Context, c *conn, b io.Reader) (*http.Response, error) {
			return c.api.CreateSubscriptionWithBody(ctx, jsonType, b)
		},
		update: func(ctx context.Context, c *conn, id string, b io.Reader) (*http.Response, error) {
			return c.api.UpdateSubscriptionWithBody(ctx, id, jsonType, b)
		},
		del: func(ctx context.Context, c *conn, id string) (*http.Response, error) {
			return c.api.DeleteSubscription(ctx, id)
		},
	}.resource("subscription", schema.Schema{
		MarkdownDescription: "An event subscription: rvav calls a webhook, launches an AWX job template or starts a workflow " +
			"on each event of its topic that its condition matches.",
		Attributes: map[string]schema.Attribute{
			"id":          idAttribute("subscription"),
			"name":        required("The subscription's name."),
			"description": optional("What it is for."),
			"topic":       required("The event topic, for example `compute.provision.post` or `project.created`."),
			"condition":   optional("A CEL expression over the event's data. Default: every event."),
			"blocking": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "The request waits for the target, which may change or stop it. Default: `false`."},
			"on_failure": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("stop_request"),
				MarkdownDescription: "What a blocking subscription does when its target fails: `stop_request` (the default) or `continue_request`."},
			"priority": priority(),
			"timeout_seconds": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(30),
				MarkdownDescription: "How long a blocking subscription waits for its target. Default: `30`."},
			"enabled": enabled(),
			"target": schema.SingleNestedAttribute{Required: true,
				MarkdownDescription: "What it calls: exactly one of `webhook`, `awx` and `workflow`.",
				Attributes: map[string]schema.Attribute{
					"webhook": schema.SingleNestedAttribute{Optional: true, Attributes: map[string]schema.Attribute{
						"url":    required("An https URL on a host webhooks may call."),
						"secret": required("The stored secret the body is signed with, by name (`rvav_secret`)."),
					}},
					"awx": schema.SingleNestedAttribute{Optional: true, Attributes: map[string]schema.Attribute{
						"template_id":  number("The job template's ID in AWX."),
						"job_template": optional("Its name, when no ID is given."),
					}},
					"workflow": schema.SingleNestedAttribute{Optional: true, Attributes: map[string]schema.Attribute{
						"key": required("A registered custom workflow allowed as a subscription target."),
					}},
				}},
		},
	})
}

// newSecretResource is a stored secret, by name and project. rvav never
// gives its value back: Terraform keeps the one it set, and can't see a
// change made in the web app.
func newSecretResource() resource.Resource {
	return &restResource{
		name: "secret",
		schema: schema.Schema{
			MarkdownDescription: "A stored secret, for webhooks and integrations to sign or authenticate with. rvav never gives its value " +
				"back: Terraform can't see it changed in the web app. Import it as `<name>`, or `<project ID>/<name>` for a project's.",
			Attributes: map[string]schema.Attribute{
				"id": idAttribute("secret"),
				"name": schema.StringAttribute{Required: true, PlanModifiers: replaced(),
					MarkdownDescription: "Its name: lower-case letters, digits and dashes."},
				"project_id": schema.StringAttribute{Optional: true, PlanModifiers: replaced(),
					MarkdownDescription: "The project whose own secret it is. Default: rvav's, for every project."},
				"kind":        required("`token`, `basic` (with `username`) or `header` (with `header`)."),
				"description": optional("What it is for."),
				"username":    optional("The user name, for `basic`."),
				"header":      optional("The header's name, for `header`."),
				"value":       schema.StringAttribute{Required: true, Sensitive: true, MarkdownDescription: "The secret itself."},
			},
		},
		create: func(ctx context.Context, c *conn, body map[string]any) (map[string]any, error) {
			obj, err := object(c.api.CreateSecretWithBody(ctx, jsonType, send(body)))
			return secretOf(obj, err, body["value"])
		},
		read: func(ctx context.Context, c *conn, id string, state map[string]any) (map[string]any, error) {
			project, name, scoped := strings.Cut(id, "/")
			if !scoped {
				project, name = "", id
			}
			list, err := object(c.api.ListSecrets(ctx))
			if err != nil {
				return nil, err
			}
			for _, s := range items(list) {
				if str(s, "name") == name && str(s, "project_id") == project {
					return secretOf(s, nil, state["value"])
				}
			}
			return nil, nil
		},
		update: func(ctx context.Context, c *conn, _ string, body, _ map[string]any) (map[string]any, error) {
			obj, err := object(c.api.UpdateSecretWithBody(ctx, str(body, "name"), secretProject(body), jsonType, send(body)))
			return secretOf(obj, err, body["value"])
		},
		del: func(ctx context.Context, c *conn, _ string, state map[string]any) error {
			p := secretProject(state)
			return done(c.api.DeleteSecret(ctx, str(state, "name"), &rvavclient.DeleteSecretParams{ProjectId: p.ProjectId}))
		},
	}
}

func secretProject(m map[string]any) *rvavclient.UpdateSecretParams {
	if p := str(m, "project_id"); p != "" {
		return &rvavclient.UpdateSecretParams{ProjectId: &p}
	}
	return &rvavclient.UpdateSecretParams{}
}

func secretOf(obj map[string]any, err error, value any) (map[string]any, error) {
	if obj == nil || err != nil {
		return obj, err
	}
	obj["value"] = value
	obj["id"] = str(obj, "name")
	if p := str(obj, "project_id"); p != "" {
		obj["id"] = p + "/" + str(obj, "name")
	}
	return obj, nil
}

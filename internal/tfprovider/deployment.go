package tfprovider

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/damhau/rvav/pkg/rvavclient"
)

// masked is a secret input's value in what rvav returns.
const masked = "••••"

func newDeploymentResource() resource.Resource {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	r := &restResource{name: "deployment", schema: schema.Schema{
		MarkdownDescription: "A deployment of a catalog item. Terraform requests it and waits for the request; a change of " +
			"`inputs` or `version` is an update request, of `name` or `description` an Edit deployment, and destroying it " +
			"a delete request. Terraform waits for each, approvals included.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("deployment"),
			"project_id": schema.StringAttribute{Required: true, PlanModifiers: replaced(),
				MarkdownDescription: "The project it is requested in."},
			"environment": schema.StringAttribute{Required: true, PlanModifiers: replaced(),
				MarkdownDescription: "The environment's key, `dev`."},
			"catalog_item": nil,
			"version": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keep,
				MarkdownDescription: "The item's version, `1.2`: when not given, the newest the project may request at " +
					"creation. A change updates the deployment to it."},
			"inputs": schema.StringAttribute{Optional: true, CustomType: jsontypes.NormalizedType{},
				MarkdownDescription: "The form's inputs as JSON, with `jsonencode()`. Terraform sends these only: rvav fills " +
					"in the others from the form, as for a request made in the web app. A change updates the deployment, " +
					"its other inputs as they are. Terraform sees the deployment's own values of these inputs, not those " +
					"of secret fields. The plan shows them and the state keeps them: give secret fields in `secret_inputs`."},
			"secret_inputs": schema.StringAttribute{Optional: true, Sensitive: true, WriteOnly: true, CustomType: jsontypes.NormalizedType{},
				MarkdownDescription: "The form's secret fields as JSON, such as a password, sent with `inputs` in each request " +
					"Terraform makes. Write-only (Terraform 1.11 or later): neither the plan nor the state holds them. A " +
					"change of them alone makes no request: rvav uses secret values while a request runs, and keeps none."},
			"name": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keep,
				MarkdownDescription: "The deployment's name: rvav names it when not given."},
			"description": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keep},
			"justification": schema.StringAttribute{Optional: true,
				MarkdownDescription: "Why, for approvers: sent with each request Terraform makes."},
			"accept_warnings": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Requests and updates go ahead despite the warnings of soft policies."},
			"catalog_item_id": schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"version_id":      computed("The version's ID."),
			"status":          computed("`ready`, or `failed` when its last request failed."),
			"owner_id":        schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"address":         computed("Its primary IP address, when it has one."),
			"fqdn":            computed("Its primary address's DNS name, when it has one."),
			"timeouts":        timeouts.Attributes(context.Background(), timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}, create: createDeployment, read: readDeployment, update: updateDeployment, del: deleteDeployment}
	r.schema.Attributes["catalog_item"] = schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{sameItem{r}},
		MarkdownDescription: "The item's slug or ID. Another item replaces the deployment; the same item by its other name doesn't."}
	return r
}

func createDeployment(ctx context.Context, c *conn, body map[string]any) (map[string]any, error) {
	ctx, cancel := within(ctx, body, "create")
	defer cancel()
	project := str(body, "project_id")
	item, version, err := requestable(ctx, c, str(body, "catalog_item"), project, str(body, "version"))
	if err != nil {
		return nil, err
	}
	inputs, err := given(body)
	if err != nil {
		return nil, err
	}
	draft := map[string]any{"catalog_item_id": item, "version_id": version, "project_id": project,
		"environment": body["environment"], "inputs": inputs}
	for k, field := range map[string]string{"name": "deployment_name", "description": "deployment_description",
		"justification": "justification", "accept_warnings": "accept_warnings"} {
		if v, ok := body[k]; ok {
			draft[field] = v
		}
	}
	req, err := object(c.api.CreateRequestWithBody(ctx, &rvavclient.CreateRequestParams{IdempotencyKey: uuid.New()}, jsonType, send(draft)))
	if err != nil {
		return nil, err
	}
	ids, _ := req["deployment_ids"].([]any)
	if len(ids) != 1 {
		return nil, fmt.Errorf("request %s made %d deployments, not one", str(req, "number"), len(ids))
	}
	id, _ := ids[0].(string)
	waitErr := c.wait(ctx, req)
	// The deployment exists from the request on: with its ID in the state,
	// a failed one is tainted, and the next apply deletes it first.
	obj, err := readDeployment(context.WithoutCancel(ctx), c, id, body)
	if obj != nil {
		obj["inputs"] = body["inputs"]
	}
	var stop *interrupted
	if err == nil && errors.As(waitErr, &stop) && errors.Is(waitErr, context.Canceled) {
		// Stopped by hand, the request may still succeed: tainting the
		// deployment would replace a good one at the next apply.
		return obj, &warning{"Deployment still being made", fmt.Sprintf("Terraform stopped waiting for %s, which rvav "+
			"goes on with. Terraform keeps %s in its state, not tainted: the next plan reads its status. To follow the "+
			"request, run rvavctl requests wait %[1]s.", str(req, "number"), id)}
	}
	return obj, errors.Join(waitErr, err)
}

// given are the inputs the configuration gives, secret ones included.
func given(body map[string]any) (map[string]any, error) {
	inputs := map[string]any{}
	if in, ok := body["inputs"].(map[string]any); ok {
		maps.Copy(inputs, in)
	}
	secret, _ := body["secret_inputs"].(map[string]any)
	for k, v := range secret {
		if _, ok := inputs[k]; ok {
			return nil, fmt.Errorf("%s is in both inputs and secret_inputs: give it in secret_inputs only", k)
		}
		inputs[k] = v
	}
	return inputs, nil
}

// requestable is the item's ID and its version's to request in the project:
// number's, else the newest.
func requestable(ctx context.Context, c *conn, item, project, number string) (string, string, error) {
	it, err := object(c.api.GetCatalogItem(ctx, item, &rvavclient.GetCatalogItemParams{ProjectId: project}))
	if err != nil {
		return "", "", err
	}
	newest, _ := it["version"].(map[string]any)
	if number == "" || str(newest, "number") == number {
		return str(it, "id"), str(newest, "id"), nil
	}
	versions, _ := it["versions"].([]any)
	var offered []string
	for _, x := range versions {
		v, _ := x.(map[string]any)
		if str(v, "number") == number {
			return str(it, "id"), str(v, "id"), nil
		}
		offered = append(offered, str(v, "number"))
	}
	if len(offered) == 0 {
		offered = []string{str(newest, "number")}
	}
	return "", "", fmt.Errorf("%s has no version %s the project may request: it offers %s", item, number,
		strings.Join(offered, ", "))
}

func readDeployment(ctx context.Context, c *conn, id string, state map[string]any) (map[string]any, error) {
	d, err := gone(object(c.api.GetDeployment(ctx, id)))
	if d == nil || err != nil || str(d, "status") == "deleted" {
		return nil, err
	}
	project, _ := d["project"].(map[string]any)
	item, _ := d["catalog_item"].(map[string]any)
	version, _ := d["version"].(map[string]any)
	owner, _ := d["owner"].(map[string]any)
	obj := map[string]any{"id": d["id"], "project_id": project["id"], "environment": d["environment"],
		"catalog_item": cmp.Or(str(state, "catalog_item"), str(item, "slug")), "catalog_item_id": item["id"],
		"version": version["number"], "version_id": version["id"], "name": d["name"], "description": d["description"],
		"status": d["status"], "owner_id": owner["id"], "justification": state["justification"],
		"accept_warnings": cmp.Or(state["accept_warnings"], any(false)),
		"inputs":          configuredInputs(state["inputs"], d["inputs"]),
		"timeouts":        state["timeouts"]}
	if a, ok := d["primary_address"].(map[string]any); ok {
		obj["address"], obj["fqdn"] = a["ip"], a["fqdn"]
	}
	return obj, nil
}

// configuredInputs are the deployment's values of the inputs the configuration
// gives. Secret ones, and those rvav didn't keep (a field a rule hides,
// one emptied), stay as configured.
func configuredInputs(given, has any) any {
	want, ok := given.(map[string]any)
	if !ok {
		return nil
	}
	now, _ := has.(map[string]any)
	out := make(map[string]any, len(want))
	for k, v := range want {
		got, ok := now[k]
		if !ok || got == masked {
			got = v
		}
		out[k] = got
	}
	return out
}

func updateDeployment(ctx context.Context, c *conn, id string, body, state map[string]any) (map[string]any, error) {
	ctx, cancel := within(ctx, body, "update")
	defer cancel()
	d, err := object(c.api.GetDeployment(ctx, id))
	if err != nil {
		return nil, err
	}
	name := cmp.Or(str(body, "name"), str(d, "name"))
	desc, ok := body["description"].(string)
	if !ok {
		desc = str(d, "description")
	}
	if name != str(d, "name") || desc != str(d, "description") {
		edit := map[string]any{"action": "edit", "inputs": map[string]any{"name": name, "description": desc}}
		if j, ok := body["justification"]; ok {
			edit["justification"] = j
		}
		req, err := object(c.api.RunDeploymentActionWithBody(ctx, id, &rvavclient.RunDeploymentActionParams{IdempotencyKey: uuid.New()},
			jsonType, send(edit)))
		if err == nil {
			err = c.wait(ctx, req)
		}
		if err != nil {
			return nil, fmt.Errorf("edit deployment: %w", err)
		}
	}
	if str(body, "version") != str(state, "version") || !sameJSON(body["inputs"], state["inputs"]) {
		if err := c.updateTo(ctx, d, body); err != nil {
			return nil, err
		}
	}
	obj, err := readDeployment(context.WithoutCancel(ctx), c, id, body)
	if obj != nil {
		obj["inputs"] = body["inputs"]
	}
	return obj, err
}

// within is ctx ended by the resource's timeout of op, when it sets one:
// by default, Terraform waits as long as a request takes, approvals
// included.
func within(ctx context.Context, body map[string]any, op string) (context.Context, context.CancelFunc) {
	t, _ := body["timeouts"].(map[string]any)
	if d, err := time.ParseDuration(str(t, op)); err == nil && d > 0 {
		return context.WithTimeout(ctx, d)
	}
	return context.WithCancel(ctx)
}

// updateTo updates the deployment to the plan's version and inputs, the
// others as it has them. rvav saying nothing changes isn't an error: the
// configuration then gives the values the deployment has.
func (c *conn) updateTo(ctx context.Context, d, body map[string]any) error {
	project, _ := d["project"].(map[string]any)
	item, _ := d["catalog_item"].(map[string]any)
	version, _ := d["version"].(map[string]any)
	target := str(version, "id")
	if n := str(body, "version"); n != "" && n != str(version, "number") {
		var err error
		if _, target, err = requestable(ctx, c, str(item, "id"), str(project, "id"), n); err != nil {
			return err
		}
	}
	form, err := object(c.api.GetRequestForm(ctx, str(item, "id"),
		&rvavclient.GetRequestFormParams{ProjectId: str(project, "id"), VersionId: &target}))
	if err != nil {
		return err
	}
	inputs, err := given(body)
	if err != nil {
		return err
	}
	draft := map[string]any{"version_id": target, "inputs": updateInputs(form, d, inputs),
		"accept_warnings": cmp.Or(body["accept_warnings"], any(false))}
	if j, ok := body["justification"]; ok {
		draft["justification"] = j
	}
	req, err := object(c.api.UpdateDeploymentWithBody(ctx, str(d, "id"), &rvavclient.UpdateDeploymentParams{IdempotencyKey: uuid.New()},
		jsonType, send(draft)))
	if hasCode(err, "no_change") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return c.wait(ctx, req)
}

// updateInputs are an update's inputs, as the web app's update form starts
// them: the deployment's, but for secrets, which only given ones have; on
// another version, its new fields' initial values. given go over them.
func updateInputs(form, d, given map[string]any) map[string]any {
	schema, _ := form["schema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	ui, _ := form["ui_schema"].(map[string]any)
	fields, _ := ui["fields"].(map[string]any)
	had, _ := d["inputs"].(map[string]any)
	out := map[string]any{}
	for k, v := range had {
		f, _ := fields[k].(map[string]any)
		if _, ok := props[k]; ok && v != masked && f["secret"] != true {
			out[k] = v
		}
	}
	version, _ := d["version"].(map[string]any)
	if str(form, "version_id") != str(version, "id") || len(had) == 0 {
		initial, _ := form["initial_values"].(map[string]any)
		for k, v := range initial {
			if _, ok := had[k]; !ok {
				out[k] = v
			}
		}
	}
	maps.Copy(out, given)
	return out
}

// deleteDeployment requests the delete, and waits for it. A deployment that
// a request still builds or updates, as after an apply stopped while it
// waited, can't be deleted yet: it waits for that request first.
func deleteDeployment(ctx context.Context, c *conn, id string, state map[string]any) error {
	ctx, cancel := within(ctx, state, "delete")
	defer cancel()
	del := map[string]any{"action": "delete", "inputs": map[string]any{}}
	if j, ok := state["justification"]; ok && j != nil {
		del["justification"] = j
	}
	for {
		req, err := object(c.api.RunDeploymentActionWithBody(ctx, id, &rvavclient.RunDeploymentActionParams{IdempotencyKey: uuid.New()},
			jsonType, send(del)))
		if err == nil {
			return c.wait(ctx, req)
		}
		if !rvavclient.IsStatus(err, http.StatusConflict) {
			return err
		}
		deleted, waited, rerr := c.settled(ctx, id)
		switch {
		case rerr != nil:
			return rerr
		case deleted:
			return nil
		case !waited:
			return err
		}
	}
}

// busy are the statuses of a deployment that one of its requests changes.
var busy = map[string]bool{"provisioning": true, "updating": true, "deleting": true}

// settled reads the deployment until no request changes it, or it is
// deleted. waited says whether a request did.
func (c *conn) settled(ctx context.Context, id string) (deleted, waited bool, err error) {
	retry := c.retry(ctx, "reading "+id)
	said := ""
	for {
		d, err := gone(object(c.api.GetDeployment(ctx, id)))
		if err == nil {
			status := str(d, "status")
			if d == nil || status == "deleted" {
				return true, said != "", nil
			}
			if !busy[status] {
				return false, said != "", nil
			}
			if status != said {
				tflog.Info(ctx, id+" is "+status+": waiting for its request to end")
				said = status
			}
		}
		if err := retry.Wait(ctx, err); err != nil {
			return false, false, c.stopped(ctx, id, err)
		}
	}
}

// retry paces a wait, and tells of each error it waits through.
func (c *conn) retry(ctx context.Context, what string) *rvavclient.Retry {
	return &rvavclient.Retry{Interval: c.poll, Retrying: func(err error, pause time.Duration) {
		tflog.Warn(ctx, fmt.Sprintf("%s: %v; trying again in %s", what, err, pause))
	}}
}

// interrupted is a wait that Terraform stopped, as on Ctrl-C, or that its
// timeout ended: rvav goes on with the request.
type interrupted struct {
	what string
	err  error
}

func (e *interrupted) Error() string {
	if errors.Is(e.err, context.DeadlineExceeded) {
		return fmt.Sprintf("%s hasn't finished before the resource's timeout; rvav goes on with it", e.what)
	}
	return fmt.Sprintf("stopped waiting for %s, which rvav goes on with: %v", e.what, e.err)
}

func (e *interrupted) Unwrap() error { return e.err }

// stopped is err as interrupted when the context ended it.
func (c *conn) stopped(ctx context.Context, what string, err error) error {
	if ctx.Err() != nil {
		return &interrupted{what, ctx.Err()}
	}
	return err
}

// wait reads the request until it ends: an error unless it succeeded. It
// goes on through errors that don't last, such as a proxy's 502 during a
// rolling restart.
func (c *conn) wait(ctx context.Context, req map[string]any) error {
	number, said := str(req, "number"), ""
	retry := c.retry(ctx, "reading "+number)
	var err error
	for {
		if err == nil {
			status := str(req, "status")
			step, _ := req["current_step"].(map[string]any)
			if now := strings.TrimSuffix(status+": "+str(step, "title"), ": "); now != said {
				tflog.Info(ctx, number+" "+now)
				said = now
			}
			switch status {
			case "succeeded":
				return nil
			case "failed", "rejected", "cancelled":
				msg := number + " " + status
				if e, ok := req["error"].(map[string]any); ok && str(e, "message") != "" {
					msg += ": " + str(e, "message")
				}
				return errors.New(msg)
			}
		}
		if err := retry.Wait(ctx, err); err != nil {
			return c.stopped(ctx, number, err)
		}
		var next map[string]any
		if next, err = object(c.api.GetRequest(ctx, str(req, "id"))); err == nil {
			req = next
		}
	}
}

// hasCode says whether rvav refused the call with a field error of code.
func hasCode(err error, code string) bool {
	var e *rvavclient.Error
	if !errors.As(err, &e) || e.Problem == nil || e.Problem.Errors == nil {
		return false
	}
	for _, fe := range *e.Problem.Errors {
		if fe.Code != nil && *fe.Code == code {
			return true
		}
	}
	return false
}

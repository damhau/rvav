package e2e

import (
	"context"
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/present"
)

// payloadRecorder keeps every payload the workflow's data converter makes:
// what Temporal's history would hold.
type payloadRecorder struct {
	converter.DataConverter
	mu   sync.Mutex
	seen []string
}

func (r *payloadRecorder) keep(ps ...*commonpb.Payload) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range ps {
		if p != nil {
			r.seen = append(r.seen, string(p.Data))
		}
	}
}

func (r *payloadRecorder) ToPayload(v any) (*commonpb.Payload, error) {
	p, err := r.DataConverter.ToPayload(v)
	r.keep(p)
	return p, err
}

func (r *payloadRecorder) ToPayloads(vs ...any) (*commonpb.Payloads, error) {
	ps, err := r.DataConverter.ToPayloads(vs...)
	if ps != nil {
		r.keep(ps.Payloads...)
	}
	return ps, err
}

func (r *payloadRecorder) holds(s string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.seen {
		if strings.Contains(p, s) {
			return true
		}
	}
	return false
}

// TestSecretsInTemplates: cloud-init user data reads a stored secret as
// {{ secret "name" }} and an AWX step's binding as ${secret.name}; the
// worker opens them, and their values stay out of the plan, the timeline,
// the logs, the events, the audit and Temporal's history. Saving refuses
// an unknown secret, by its line, and one where it would be kept; the
// secret is used by the items naming it (T1.5, ADR-0038).
func TestSecretsInTemplates(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, ciToken, awxPass = "mkeller", "Tok3n-in-UD!", "Awx-S3cret!"
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "ci-token", Kind: api.SecretWriteKindToken,
			Value: present.Ptr(ciToken)}, 201, nil)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "awx-pass", Kind: api.SecretWriteKindToken,
			Value: present.Ptr(awxPass)}, 201, nil)

		create := func(slug string, typ api.CatalogItemType, def api.CatalogVersionDefinition, want int) []api.FieldError {
			t.Helper()
			var problem struct{ Errors []api.FieldError }
			var out any
			if want != 201 {
				out = &problem
			}
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: slug, Summary: slug,
				Category: "compute", Type: typ, Definition: &def}, want, out)
			return problem.Errors
		}
		userData := func(ud string) api.CatalogVersionDefinition {
			return guestItem("tpl-ubuntu24.04-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example",
				"cloud_init": map[string]any{"user_data": ud}})
		}
		// An unknown secret, by its line; one named by an input; one where
		// it would be worked out with the request.
		errs := create("ci-bad", api.VmBlueprint, userData("#cloud-config\nwrite_files:\n  - content: {{ secret \"nope\" | json }}"), 422)
		if len(errs) != 1 || errs[0].Field != "/definition/blueprint/guest/cloud_init/user_data" || errs[0].Message != `Line 3: there is no secret "nope".` {
			t.Fatalf("an unknown secret = %+v", errs)
		}
		errs = create("ci-bad", api.VmBlueprint, userData("#cloud-config\na: {{ secret .inputs.role | json }}"), 422)
		if len(errs) != 1 || !strings.Contains(errs[0].Message, "name the secret in quotes") {
			t.Fatalf("a secret the requester picks = %+v", errs)
		}
		misplaced := userData("#cloud-config\n")
		misplaced.Blueprint["properties"] = map[string]any{"token": "${secret.ci-token}"}
		errs = create("ci-bad", api.VmBlueprint, misplaced, 422)
		if len(errs) != 1 || errs[0].Field != "/definition/blueprint/properties/token" || !strings.HasPrefix(errs[0].Message, "A secret goes only in cloud-init user data") {
			t.Fatalf("a secret in a property = %+v", errs)
		}

		leaks := func(id, value string) {
			t.Helper()
			var n int
			if err := h.store.Pool.QueryRow(ctx, `select (select count(*) from requests where id = $1 and (inputs::text like $2 or plan::text like $2))
				+ (select count(*) from request_steps where request_id = $1 and (detail like $2 or title like $2 or coalesce(error, '') like $2))
				+ (select count(*) from step_log_lines where text like $2)
				+ (select count(*) from integration_events where payload::text like $2)
				+ (select count(*) from audit_events where detail::text like $2)`, id, "%"+value+"%").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("the secret is in %d rows", n)
			}
		}
		withRecorder := func(rec *payloadRecorder, more func(env *testsuite.TestWorkflowEnvironment)) func(env *testsuite.TestWorkflowEnvironment) {
			return func(env *testsuite.TestWorkflowEnvironment) {
				env.SetDataConverter(rec)
				if more != nil {
					more(env)
				}
			}
		}

		// cloud-init: the value in guestinfo, its name only elsewhere.
		item := publishItem(h, "ci-secret", userData("#cloud-config\nwrite_files:\n  - path: /etc/app/token\n    content: {{ secret \"ci-token\" | json }}"))
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "tok", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}, 202, &req, "Idempotency-Key", uuid.NewString())
		rec := &payloadRecorder{DataConverter: converter.GetDefaultDataConverter()}
		h.runWorkflow(req.Id, withRecorder(rec, cloudInitGuest(h, c)))
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		var vm mo.VirtualMachine
		if err := property.DefaultCollector(c).RetrieveOne(ctx, vmRefByName(t, ctx, c, "web-dev-tok01"), []string{"config.extraConfig"}, &vm); err != nil {
			t.Fatal(err)
		}
		var userDataB64 string
		for _, o := range vm.Config.ExtraConfig {
			if ov := o.GetOptionValue(); ov.Key == "guestinfo.userdata" {
				userDataB64, _ = ov.Value.(string)
			}
		}
		if ud, _ := base64.StdEncoding.DecodeString(userDataB64); !strings.Contains(string(ud), `content: "`+ciToken+`"`) {
			t.Fatalf("user data = %q", ud)
		}
		leaks(req.Id, ciToken)
		if !rec.holds(`secret \"ci-token\"`) || rec.holds(ciToken) {
			t.Fatalf("the history holds the secret's name %v, its value %v", rec.holds(`secret \"ci-token\"`), rec.holds(ciToken))
		}

		// An AWX step's binding: the job gets the value, the step's values
		// and the job's input only its name.
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": []any{"role"}, "properties": map[string]any{"role": map[string]any{"type": "string"}}},
			UiSchema:   api.UiSchema{Fields: map[string]api.UiField{"role": {Widget: api.UiWidgetText, Label: "Role"}}}, InitialValues: map[string]any{},
			Blueprint: map[string]any{"kind": "composite", "steps": []any{
				map[string]any{"key": "grant", "awx": map[string]any{"job_template": "role-nginx", "title": "Grant"},
					"with": map[string]any{"role": "inputs.role", "password": "${secret.awx-pass}"}},
			}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Role", Fields: []string{"role"}}}
		bad := def
		bad.Blueprint = map[string]any{"kind": "composite", "steps": []any{
			map[string]any{"key": "grant", "awx": map[string]any{"job_template": "role-nginx"}, "with": map[string]any{"password": "${secret.gone}"}},
			map[string]any{"key": "tell", "notify": map[string]any{"requester": true, "message": "It is ${secret.awx-pass}."}},
		}}
		errs = create("grant-bad", api.Composite, bad, 422)
		if f := slices.Sorted(slices.Values(fieldsOf(errs))); len(f) != 2 || f[0] != "/definition/blueprint/steps/0/with/password" || f[1] != "/definition/blueprint/steps/1/notify/message" {
			t.Fatalf("a composite's secrets = %+v", errs)
		}
		var grant api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "grant", Name: "Grant", Summary: "Grant",
			Category: "compute", Type: api.Composite, Definition: &def}, 201, &grant)
		h.json("POST", "/api/v1/admin/catalog/items/grant/versions/"+grant.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/grant/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: grant.Id, VersionId: grant.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "app"}}, 202, &req, "Idempotency-Key", uuid.NewString())
		rec = &payloadRecorder{DataConverter: converter.GetDefaultDataConverter()}
		h.runWorkflow(req.Id, withRecorder(rec, nil))
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		step, err := h.store.GetStepByKey(ctx, req.Id, "step:grant")
		if err != nil || step.ExternalID == nil {
			t.Fatalf("grant step = %+v, %v", step, err)
		}
		jobID, _ := strconv.Atoi(*step.ExternalID)
		if vars := h.awx.Launched(jobID).ExtraVars; vars["password"] != awxPass || vars["role"] != "app" {
			t.Fatalf("AWX got %v", vars)
		}
		leaks(req.Id, awxPass)
		if !rec.holds("awx-pass") || rec.holds(awxPass) {
			t.Fatalf("the history holds the secret's name %v, its value %v", rec.holds("awx-pass"), rec.holds(awxPass))
		}

		// A template's user data, checked the same way.
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var problem struct{ Errors []api.FieldError }
		tpl := api.VmTemplateWrite{Key: "ubuntu-ci", Label: "Ubuntu cloud-init", OsFamily: api.VmTemplateWriteOsFamilyLinux,
			Offers: &[]api.Offer{{}}, Sources: sourceOn("", "tpl-ubuntu24.04-2026.10"),
			UserData: present.Ptr("#cloud-config\nca: {{ secret \"no-ca\" | json }}\n")}
		h.json("POST", "/api/v1/admin/templates", admin, tpl, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/user_data" || problem.Errors[0].Message != `Line 2: there is no secret "no-ca".` {
			t.Fatalf("a template's unknown secret = %+v", problem.Errors)
		}
		tpl.UserData = present.Ptr("#cloud-config\nca: {{ secret \"ci-token\" | json }}\n")
		h.json("POST", "/api/v1/admin/templates", admin, tpl, 201, nil)

		// Used by the items and templates naming them, which keep them.
		var list api.SecretList
		h.json("GET", "/api/v1/admin/secrets", admin, nil, 200, &list)
		used := map[string]string{}
		for _, s := range list.Items {
			used[s.Name] = strings.Join(*s.UsedBy, ",")
		}
		if used["ci-token"] != "ci-secret,template:ubuntu-ci" || used["awx-pass"] != "grant" {
			t.Fatalf("used by = %v", used)
		}
		h.json("DELETE", "/api/v1/admin/secrets/ci-token", admin, nil, 409, nil)
	})
}

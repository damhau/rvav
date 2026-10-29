package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/secrets"
)

// TestSecretInputs: a form field marked secret is sealed with the secrets
// key, kept out of the request's inputs and plan (and so out of Temporal's
// history), never returned by the API, and passed to AWX at launch; an
// approver's edit of another input keeps it (S14.6).
func TestSecretInputs(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		// Workflows on a Temporal server: the approval's level opens there.
		const taskQueue = "rvav-secret-inputs-e2e"
		h.svc.Workflows = &provision.Temporal{Client: ts.Client(), TaskQueue: taskQueue}
		w := provision.NewWorker(ts.Client(), taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		const admin, password = "lmartin", "Hunt3r-2-pw"
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": []any{"role", "password"}, "properties": map[string]any{
				"role": map[string]any{"type": "string"}, "password": map[string]any{"type": "string"}}},
			UiSchema: api.UiSchema{Fields: map[string]api.UiField{
				"role": {Widget: api.UiWidgetText, Label: "Role", ApproverEditable: new(true)},
				// Secret by its widget alone.
				"password": {Widget: api.UiWidgetSecret, Label: "Password"},
			}},
			Blueprint: map[string]any{"kind": "awx", "awx": map[string]any{"title": "Create role", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Role", Fields: []string{"role", "password"}}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "pg-role-pw", Name: "PostgreSQL role with a password",
			Summary: "A role.", Category: "compute", Type: api.AwxJobTemplate, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/pg-role-pw/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/pg-role-pw/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		submit := func(env string) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: api.EnvironmentKey(env), Inputs: map[string]any{"role": "app", "password": password},
				Justification: new("A role for the app.")}, 202, &req, "Idempotency-Key", uuid.NewString())
			return req
		}
		// Nowhere in the database but sealed, nor in what the API returns.
		leaks := func(id string) {
			t.Helper()
			var n int
			_ = h.store.Pool.QueryRow(ctx, `select (select count(*) from requests where id = $1 and (inputs::text like $2 or plan::text like $2))
				+ (select count(*) from request_steps where request_id = $1 and (detail like $2 or title like $2 or coalesce(error, '') like $2))
				+ (select count(*) from step_log_lines where text like $2)
				+ (select count(*) from integration_events where payload::text like $2)
				+ (select count(*) from audit_log where detail::text like $2)
				+ (select count(*) from request_secrets where request_id = $1 and position(convert_to($3, 'UTF8') in ciphertext) > 0)`,
				id, "%"+password+"%", password).Scan(&n)
			if n != 0 {
				t.Fatalf("the password is in %d rows", n)
			}
			var got api.Request
			h.json("GET", "/api/v1/requests/"+id, "tnguyen", nil, 200, &got)
			if got.Inputs == nil || (*got.Inputs)["password"] != "••••" {
				t.Fatalf("the API returns the inputs as %v", got.Inputs)
			}
			info, err := h.acts.LoadPlan(ctx, id)
			if b, _ := json.Marshal(info); err != nil || strings.Contains(string(b), password) {
				t.Fatalf("the workflow's plan has the password: %v", err)
			}
		}
		launched := func(id string) map[string]any {
			t.Helper()
			step, err := h.store.GetStepByKey(ctx, id, "awx.job:pg-create-role")
			if err != nil || step.ExternalID == nil {
				t.Fatalf("job step = %+v, %v", step, err)
			}
			var jobID int
			fmt.Sscan(*step.ExternalID, &jobID)
			return h.awx.Launched(jobID).ExtraVars
		}

		req := submit("dev")
		var sealed int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from request_secrets where request_id = $1 and field = 'password'`, req.Id).Scan(&sealed)
		if sealed != 1 {
			t.Fatalf("%d sealed inputs", sealed)
		}
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		if vars := launched(req.Id); vars["password"] != password || vars["role"] != "app" {
			t.Fatalf("AWX got %v", vars)
		}
		leaks(req.Id)
		// Not kept once the request can't run again.
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from request_secrets where request_id = $1`, req.Id).Scan(&sealed)
		if sealed != 0 {
			t.Fatalf("%d sealed inputs kept after the request succeeded", sealed)
		}

		// An approver changes the role: the request is evaluated again with
		// the password, sealed again, and AWX still gets it.
		h.json("POST", "/api/v1/admin/approval-policies", "mkeller", api.ApprovalPolicyWrite{Name: "Roles in production",
			Condition: `environment == "prd" && inputs.role == "app"`,
			Levels:    []api.ApprovalLevel{{Name: "Infrastructure leads", Group: new("rvav-infra-leads")}}, Priority: new(5)}, 201, nil)
		req = submit("prd")
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusPendingApproval)
		var level string
		for deadline := time.Now().Add(30 * time.Second); level == ""; time.Sleep(50 * time.Millisecond) {
			_ = h.store.Pool.QueryRow(ctx, `select id from approvals where request_id = $1 and status = 'pending'`, req.Id).Scan(&level)
			if time.Now().After(deadline) {
				t.Fatal("no approval level opened")
			}
		}
		// The secrets key is rotated while the request waits: its password
		// is sealed again with the new key, and the old key can go.
		newKey := base64.StdEncoding.EncodeToString([]byte("another-e2e-secrets-key-32-bytes"))
		both, err := secrets.New(newKey, base64.StdEncoding.EncodeToString([]byte("e2e-only-secrets-key-32-bytes!!!")))
		if err != nil {
			t.Fatal(err)
		}
		if n, err := core.RotateSecrets(ctx, h.store, both); err != nil || n != 1 {
			t.Fatalf("rotated %d, %v", n, err)
		}
		onlyNew, _ := secrets.New(newKey)
		h.svc.Secrets, h.acts.Secrets = onlyNew, onlyNew
		var decided api.Approval
		h.json("POST", "/api/v1/approvals/"+level+"/decision", "mkeller", api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove,
			Inputs: &map[string]any{"role": "ops", "password": "other"}}, 422, nil)
		h.json("POST", "/api/v1/approvals/"+level+"/decision", "mkeller", api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove,
			Inputs: &map[string]any{"role": "ops"}}, 200, &decided)
		if len(decided.Votes) != 1 || len(decided.Votes[0].Changes) != 1 || decided.Votes[0].Changes[0].To != "ops" {
			t.Fatalf("decided %+v", decided)
		}
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		if vars := launched(req.Id); vars["password"] != password || vars["role"] != "ops" {
			t.Fatalf("AWX got %v after the approver's edit", vars)
		}
		leaks(req.Id)
	})
}

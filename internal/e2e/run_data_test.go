package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// TestStepRunData: each step of a run keeps what it was given and what it
// gave, and catalog managers read both with the secrets masked: a secret
// input as ••••, a stored secret by name, a secret echoed back as [removed].
// A requester doesn't read them; a person's answer is read only by those it
// asks and those who read every approval (ADR-0057).
func TestStepRunData(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		// lmartin manages the catalog, as a blueprint's author does; mkeller
		// administers rvav, and reads every approval.
		const admin, author, requester = "mkeller", "lmartin", "tnguyen"
		cmdb := newRESTCMDB(t)
		h.svc.REST.Hosts = []string{cmdb.URL}
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "cmdb-token", Kind: api.SecretWriteKindToken, Value: new(restToken)}, 201, nil)

		def := restDefinition(cmdb.URL)
		def.Blueprint = map[string]any{"kind": "composite", "steps": []any{
			map[string]any{"key": "register", "title": "Register", "rest": map[string]any{
				"method": "POST", "url": cmdb.URL + "/api/ci/${inputs.name}", "secret": "cmdb-token",
				"body": "{'name': inputs.name, 'password': inputs.api_password}", "accept": []any{201}, "keep": "$.result"}},
			map[string]any{"key": "job", "title": "Monitoring", "awx": map[string]any{"job_template": "monitoring-agent"},
				"with": map[string]any{"target": "inputs.name", "password": "inputs.api_password", "token": "${secret.cmdb-token}",
					"id": "steps.register.json.id"}},
			askStep("who", map[string]any{"group": "rvav-data", "timeout": "1h", "on_timeout": "continue"}),
		}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "run-data", Name: "Run data",
			Summary: "Run data.", Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/run-data/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/run-data/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		var req api.Request
		h.json("POST", "/api/v1/requests", requester, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"name": "web01", "api_password": restInput}},
			202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, requester, nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("the request = %s %s", req.Status, errorOf(req))
		}
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", requester, nil, 200, &steps)
		ids := map[string]string{}
		for _, s := range steps.Items {
			ids[s.Key] = s.Id
			if want := strings.HasPrefix(s.Key, "step:"); (s.HasData != nil && *s.HasData) != want {
				t.Errorf("%s has_data = %v", s.Key, s.HasData)
			}
		}
		data := func(key, user string) (api.StepData, string) {
			t.Helper()
			var raw json.RawMessage
			h.json("GET", "/api/v1/requests/"+req.Id+"/steps/"+ids[key]+"/data", user, nil, 200, &raw)
			var d api.StepData
			if err := json.Unmarshal(raw, &d); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), restInput) || strings.Contains(string(raw), restToken) {
				t.Fatalf("%s's data holds a secret: %s", key, raw)
			}
			return d, string(raw)
		}

		// A REST call: the URL and body as sent, the secret input masked;
		// its answer as kept, what it echoed removed.
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps/"+ids["step:register"]+"/data", requester, nil, 403, nil)
		reg, _ := data("step:register", author)
		in, out := valueOf(reg.Inputs), valueOf(reg.Outputs)
		body, _ := in["body"].(map[string]any)
		if in["method"] != "POST" || in["url"] != cmdb.URL+"/api/ci/web01" || in["secret"] != "cmdb-token" ||
			body["name"] != "web01" || body["password"] != "••••" {
			t.Fatalf("the call's inputs = %v", in)
		}
		kept, _ := out["json"].(map[string]any)
		if out["status"] != float64(201) || kept["id"] != float64(42) || kept["auth"] != "Bearer [removed]" {
			t.Fatalf("the call's outputs = %v", out)
		}

		// An AWX job: its variables as bound, rvav's own context left out.
		job, _ := data("step:job", author)
		in, out = valueOf(job.Inputs), valueOf(job.Outputs)
		if in["target"] != "web01" || in["password"] != "••••" || in["token"] != "${secret.cmdb-token}" || in["id"] != float64(42) {
			t.Fatalf("the job's inputs = %v", in)
		}
		if _, ok := in["rvav"]; ok {
			t.Fatalf("the job's inputs hold rvav's context: %v", in)
		}
		if _, ok := out["job_id"].(float64); !ok {
			t.Fatalf("the job's outputs = %v", out)
		}

		// A person's answer: the author isn't asked, so doesn't read it.
		if who, _ := data("step:who", author); !who.OutputsHidden || who.Outputs != nil {
			t.Fatalf("the author reads the answer: %+v", who)
		}
		if who, _ := data("step:who", admin); who.OutputsHidden || valueOf(who.Outputs)["answered"] != false {
			t.Fatalf("an administrator reads %+v", who)
		}

		h.json("GET", "/api/v1/requests/"+req.Id+"/steps/stp_nope/data", author, nil, 404, nil)

		// A retry from scratch forgets what a step was given.
		if _, err := h.store.Pool.Exec(ctx, `update request_steps set status = 'failed' where request_id = $1 and key = 'step:job'`, req.Id); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.Pool.Exec(ctx, `update requests set status = 'failed' where id = $1`, req.Id); err != nil {
			t.Fatal(err)
		}
		h.json("POST", "/api/v1/requests/"+req.Id+"/retry", requester, nil, 202, nil)
		if again, _ := data("step:job", author); again.Inputs != nil || again.Outputs != nil {
			t.Fatalf("a step to run again keeps %+v", again)
		}
	})
}

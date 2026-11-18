package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/provision"
)

// TestFileFields: a file field takes a file of its types and size whose
// content is what its name says, checked by the server whatever the client
// claims. The request keeps it; AWX gets its record, not its content; the
// requester, the approvers and the project's admins download it, always as
// an attachment; an approver's edit keeps it (F1.2, ADR-0040).
func TestFileFields(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-file-fields-e2e"
		h.svc.Workflows = &provision.Temporal{Client: ts.Client(), TaskQueue: taskQueue}
		w := provision.NewWorker(ts.Client(), taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		const admin, requester, approver = "lmartin", "tnguyen", "cdubois"
		def := api.CatalogVersionDefinition{
			FormSchema: map[string]any{"type": "object", "required": []any{"rules"}, "properties": map[string]any{
				"rules": map[string]any{"type": "object"}, "note": map[string]any{"type": "string"}}},
			UiSchema: api.UiSchema{Fields: map[string]api.UiField{
				"rules": {Widget: api.UiWidgetFile, Label: "Firewall rules", Accept: &[]string{".csv", "application/pdf"}, MaxSizeMb: new(1)},
				"note":  {Widget: api.UiWidgetText, Label: "Note", ApproverEditable: new(true)},
			}},
			Blueprint: map[string]any{"kind": "awx", "awx": map[string]any{"title": "Open ports", "job_template": "pg-create-role"}},
		}
		def.UiSchema.Layout.Kind = api.Page
		def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Rules", Fields: []string{"rules", "note"}}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "fw-rules", Name: "Firewall rules",
			Summary: "Ports opened from a file.", Category: "compute", Type: api.AwxJobTemplate, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/fw-rules/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/fw-rules/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		uploadURL := func(name string) string {
			return "/api/v1/forms/files?" + url.Values{"version_id": {item.DraftVersion.Id}, "project_id": {"prj_web"},
				"field": {"rules"}, "name": {name}}.Encode()
		}
		upload := func(user, name, content string, want int) api.FileRef {
			t.Helper()
			var ref api.FileRef
			var out any = &ref
			if want != 201 {
				out = nil
			}
			h.json("POST", uploadURL(name), user, rawYAML(content), want, out, "Content-Type", "image/png")
			return ref
		}
		const rules = "port,source\n443,10.0.0.0/8\n"
		ref := upload(requester, `C:\Users\tnguyen\rules.csv`, rules, 201)
		sum := sha256.Sum256([]byte(rules))
		if ref.Name != "rules.csv" || ref.Size != int64(len(rules)) || ref.ContentType != "text/csv" || ref.Sha256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("kept as %+v", ref)
		}

		big := strings.Repeat("443,10.0.0.0/8\n", 70_000)
		upload(requester, "big.csv", big, 413)
		// Sent in chunks, its size unannounced: the server stops reading at the limit.
		req, _ := http.NewRequestWithContext(ctx, "POST", h.srv.URL+uploadURL("big.csv"), io.MultiReader(strings.NewReader(big)))
		req.Header.Set(auth.DevUserHeader, requester)
		if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 413 {
			t.Fatalf("a chunked upload over the limit = %v, %v", resp, err)
		} else {
			_ = resp.Body.Close()
		}
		upload(requester, "rules.png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", 415)
		upload(requester, "rules.csv", "%PDF-1.7\n1 0 obj\n", 415)
		upload(requester, "rules.pdf", "<!DOCTYPE html><script>alert(1)</script>", 415)
		upload(requester, "rules.csv", "", 400)
		upload("sbrunner", "rules.csv", rules, 404)
		upload("svc-awx", "rules.csv", rules, 403)

		draft := func(rules any) api.RequestDraft {
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
				Environment: "prd", Inputs: map[string]any{"rules": rules, "note": "for the shop"}, Justification: new("Ports for the shop.")}
		}
		refused := func(rules any) {
			t.Helper()
			var problem struct{ Errors []api.FieldError }
			h.json("POST", "/api/v1/requests", requester, draft(rules), 422, &problem, "Idempotency-Key", uuid.NewString())
			if len(problem.Errors) != 1 || problem.Errors[0].Field != "/inputs/rules" || deref(problem.Errors[0].Code) != "gone" {
				t.Fatalf("a request with %v: %+v", rules, problem.Errors)
			}
		}
		others := upload(admin, "admin.csv", rules, 201)
		refused(map[string]any{"id": others.Id})
		refused(map[string]any{"id": "fil_made_up", "name": "rules.csv", "size": 10})
		if code, _, _ := h.do("GET", "/api/v1/files/"+others.Id, requester, nil); code != 404 {
			t.Fatalf("someone else's waiting file = %d", code)
		}

		h.json("POST", "/api/v1/admin/approval-policies", "mkeller", api.ApprovalPolicyWrite{Name: "Firewall rules in production",
			Condition: `environment == "prd"`, Levels: []api.ApprovalLevel{{Name: "Infrastructure heads", Group: new("rvav-infra-heads")}},
			Priority: new(5)}, 201, nil)
		var made api.Request
		h.json("POST", "/api/v1/requests", requester, draft(map[string]any{"id": ref.Id}), 202, &made, "Idempotency-Key", uuid.NewString())
		var got api.Request
		h.json("GET", "/api/v1/requests/"+made.Id, requester, nil, 200, &got)
		if kept := (*got.Inputs)["rules"].(map[string]any); kept["name"] != "rules.csv" || kept["sha256"] != ref.Sha256 {
			t.Fatalf("the request keeps %v", kept)
		}
		refused(map[string]any{"id": ref.Id})

		for user, want := range map[string]int{requester: 200, approver: 200, admin: 200, "mkeller": 200, "svc-awx": 403, "sbrunner": 404} {
			code, body, hdr := h.do("GET", "/api/v1/files/"+ref.Id, user, nil)
			if code != want {
				t.Errorf("%s downloads it: %d, want %d: %s", user, code, want, body)
				continue
			}
			if code == 200 && (string(body) != rules || hdr.Get("Content-Disposition") != "attachment; filename=rules.csv" ||
				hdr.Get("X-Content-Type-Options") != "nosniff" || hdr.Get("Content-Type") != "application/octet-stream") {
				t.Errorf("%s gets %q with %v", user, body, hdr)
			}
		}

		// No more than 20 files wait for their request, for a day.
		for range 19 {
			upload(admin, "more.csv", rules, 201)
		}
		upload(admin, "more.csv", rules, 409)
		if _, err := h.store.Pool.Exec(ctx, `update request_files set created_at = now() - interval '25 hours' where request_id is null`); err != nil {
			t.Fatal(err)
		}
		upload(admin, "more.csv", rules, 201)
		if code, _, _ := h.do("GET", "/api/v1/files/"+others.Id, admin, nil); code != 404 {
			t.Fatalf("a file that waited a day = %d", code)
		}

		// The approver changes the note: the request, evaluated again, keeps its file.
		var level string
		for deadline := time.Now().Add(30 * time.Second); level == ""; time.Sleep(50 * time.Millisecond) {
			_ = h.store.Pool.QueryRow(ctx, `select id from approvals where request_id = $1 and status = 'pending'`, made.Id).Scan(&level)
			if time.Now().After(deadline) {
				t.Fatal("no approval level opened")
			}
		}
		h.json("POST", "/api/v1/approvals/"+level+"/decision", approver, api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove,
			Inputs: &map[string]any{"note": "checked"}}, 200, nil)
		h.waitStatus(made.Id, requester, api.RequestStatusSucceeded)
		step, err := h.store.GetStepByKey(ctx, made.Id, "awx.job:pg-create-role")
		if err != nil || step.ExternalID == nil {
			t.Fatalf("job step = %+v, %v", step, err)
		}
		var job int
		fmt.Sscan(*step.ExternalID, &job)
		vars := h.awx.Launched(job).ExtraVars
		sent, _ := json.Marshal(vars["rules"])
		if vars["note"] != "checked" || !strings.Contains(string(sent), ref.Sha256) || strings.Contains(string(sent), "10.0.0.0/8") {
			t.Fatalf("AWX got %v", vars)
		}
	})
}

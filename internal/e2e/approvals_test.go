package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/provision"
)

// TestApprovals runs the S3 acceptance scenario and the approval rules on a
// real Temporal dev server and worker: a 48 GB production VM approved by an
// infrastructure lead who lowers its RAM, who may see and decide what, two
// levels with delegation and mode all, a timeout that escalates and then
// rejects, auto-approval and rejection.
func TestApprovals(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)

	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const taskQueue = "rvav-approvals-e2e"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: taskQueue}
		w := provision.NewWorker(tc, taskQueue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		const web = "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, "lmartin", nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, "lmartin", nil, 200, &form)
		draft := func(env, role, size string) api.RequestDraft {
			inputs := map[string]any{"role": role, "size": size, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
				"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}
			if env == "prd" {
				inputs["cluster"], inputs["datastore"], inputs["network"] = "GVA-PRD-CL02", "vsan-gva-prd-cl02", "dvpg-web-prd-vl210"
			}
			return api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web,
				Environment: api.EnvironmentKey(env), Inputs: inputs, Justification: strPtr("Fourth front node for Black Friday.")}
		}
		submit := func(user string, d api.RequestDraft) api.Request {
			var r api.Request
			h.json("POST", "/api/v1/requests", user, d, 202, &r, "Idempotency-Key", uuid.NewString())
			return r
		}
		decide := func(user, id string, d api.ApprovalDecision, want int) api.Approval {
			var a api.Approval
			if want != 200 {
				h.json("POST", "/api/v1/approvals/"+id+"/decision", user, d, want, nil)
				return a
			}
			h.json("POST", "/api/v1/approvals/"+id+"/decision", user, d, want, &a)
			return a
		}
		approve := func(comment string) api.ApprovalDecision {
			return api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove, Comment: strPtr(comment)}
		}
		policyStep := func(user, requestID string) api.Step {
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+requestID+"/steps", user, nil, 200, &steps)
			return steps.Items[1]
		}
		policy := func(id, condition, levels string) {
			if _, err := h.store.Pool.Exec(ctx, `insert into approval_policies (id, name, reason, condition, item_type, levels, priority)
				values ($1, $1, 'Test: ' || $1, $2, 'vm_blueprint', $3, 10)`, id, condition, levels); err != nil {
				t.Fatal(err)
			}
		}

		// ── A 48 GB production VM waits for the Infrastructure leads ──────────
		big := submit("tnguyen", draft("prd", "front", "XL"))
		if big.Status != api.RequestStatusPendingApproval {
			t.Fatalf("48 GB prd request = %s", big.Status)
		}
		inbox := h.waitInbox("mkeller", 1)
		sum := inbox.Items[0]
		// Two policies match, with the same approvers: one level, both reasons.
		if sum.Level.Count != 1 || sum.Level.Name != "Infrastructure leads" || len(sum.Reasons) != 2 || !sum.CanDecide {
			t.Fatalf("inbox item = %+v", sum)
		}
		lvl := sum.Id

		// Who may see and decide.
		var other api.ApprovalPage
		h.json("GET", "/api/v1/approvals", "lmartin", nil, 200, &other)
		if other.Total != 0 {
			t.Fatalf("lmartin is no approver but sees %d", other.Total)
		}
		var seen api.Approval
		h.json("GET", "/api/v1/approvals/"+lvl, "lmartin", nil, 200, &seen) // project admin: sees the request
		if seen.CanDecide || len(seen.EditableFields) != 0 {
			t.Fatalf("lmartin may decide: %+v", seen)
		}
		decide("lmartin", lvl, approve("looks fine"), 403)
		h.json("GET", "/api/v1/approvals/"+lvl, "sbrunner", nil, 404, nil) // another project, no approver
		h.json("GET", "/api/v1/approvals?view=all", "lmartin", nil, 403, nil)

		// What the approver sees.
		var detail api.Approval
		h.json("GET", "/api/v1/approvals/"+lvl, "mkeller", nil, 200, &detail)
		if !detail.CanDecide || detail.Justification == nil || len(detail.Rules) != 2 {
			t.Fatalf("approval detail = %+v", detail)
		}
		if i := slices.IndexFunc(detail.Facts, func(f struct {
			Label string `json:"label"`
			Value string `json:"value"`
		}) bool {
			return f.Label == "Size"
		}); i < 0 || detail.Facts[i].Value != "8 vCPU · 48 GB · 120 GB" {
			t.Fatalf("facts = %+v", detail.Facts)
		}
		if len(detail.EditableFields) != 1 || detail.EditableFields[0].Field != "size" || detail.EditableFields[0].Value != "XL" {
			t.Fatalf("editable fields = %+v", detail.EditableFields)
		}

		// Rules on the decision itself.
		decide("mkeller", lvl, api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionReject}, 422)
		decide("mkeller", lvl, api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove,
			Inputs: &map[string]any{"role": "edge"}}, 422)
		decide("mkeller", lvl, api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove,
			Inputs: &map[string]any{"size": "XXL"}}, 422)

		// Meanwhile the item is withdrawn: the request keeps its version, and
		// Marc's edit is checked on it (S8 review).
		var rhel api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", "lmartin", nil, 200, &rhel)
		withdrawn := "/api/v1/admin/catalog/items/rhel9-server/versions/" + rhel.PublishedVersion.Id
		h.json("POST", withdrawn+"/deprecate", "lmartin", nil, 200, nil)

		// Marc lowers RAM to 32 GB and approves.
		done := decide("mkeller", lvl, api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove,
			Comment: strPtr("32 GB is enough for the JVM; page cache can wait."), Inputs: &map[string]any{"size": "L"}}, 200)
		h.json("POST", withdrawn+"/publish", "lmartin", nil, 200, nil)
		if done.Status != api.ApprovalStatusApproved || done.Outcome == nil || *done.Outcome != "Approved by Marc Keller" ||
			len(done.Votes) != 1 || len(done.Votes[0].Changes) != 1 || done.Votes[0].Changes[0].To != "L" {
			t.Fatalf("decided level = %+v", done)
		}
		decide("mkeller", lvl, approve("again"), 409)
		h.waitStatus(big.Id, "tnguyen", api.RequestStatusSucceeded)
		row, err := h.store.GetRequest(ctx, big.Id)
		if err != nil {
			t.Fatal(err)
		}
		var plan provision.Plan
		_ = json.Unmarshal(row.Plan, &plan)
		if plan.VMs[0].MemoryGB != 32 {
			t.Fatalf("provisioned with %d GB", plan.VMs[0].MemoryGB)
		}
		// The requester sees who approved, what changed and why.
		step := policyStep("tnguyen", big.Id)
		if step.Title != "Approved by Marc Keller" || !strings.Contains(deref(step.Detail), "32 GB is enough") ||
			!strings.Contains(deref(step.Detail), "Size changed from XL to L by Marc Keller") {
			t.Fatalf("policy step = %q / %q", step.Title, deref(step.Detail))
		}
		var history struct{ Items []api.Approval }
		h.json("GET", "/api/v1/requests/"+big.Number+"/approvals", "tnguyen", nil, 200, &history)
		if len(history.Items) != 1 || history.Items[0].Status != api.ApprovalStatusApproved {
			t.Fatalf("request approvals = %+v", history.Items)
		}
		// Marc's decided list has it.
		var decided api.ApprovalPage
		h.json("GET", "/api/v1/approvals?status=approved,rejected", "mkeller", nil, 200, &decided)
		if decided.Total != 1 || decided.Items[0].Id != lvl {
			t.Fatalf("mkeller decided = %+v", decided)
		}

		// ── No one decides, or hands on, the approval of their own request ────
		self := submit("mkeller", draft("prd", "selfbig", "XL")) // 48 GB: not auto-approved
		h.json("GET", "/api/v1/requests/"+self.Id+"/approvals", "mkeller", nil, 200, &history)
		selfLevel := history.Items[0].Id
		decide("mkeller", selfLevel, approve("mine"), 403)
		h.json("POST", "/api/v1/approvals/"+selfLevel+"/delegate", "mkeller", api.ApprovalDelegation{UserId: "usr_tnguyen"}, 403, nil)
		h.json("POST", "/api/v1/requests/"+self.Id+"/cancel", "mkeller", nil, 202, nil)

		// ── Two levels: the first delegated, the second needs everyone ────────
		policy("pol_e2e_two", `environment == "dev" && inputs.role == "twolevels"`, `[
			{"name": "Network engineers", "approvers": ["usr_afavre"], "mode": "any"},
			{"name": "Security board", "approvers": ["usr_mkeller", "usr_cdubois"], "mode": "all"}]`)
		two := submit("lmartin", draft("dev", "twolevels", "S"))
		first := h.waitInbox("afavre", 1).Items[0]
		if first.Level.Index != 1 || first.Level.Count != 2 {
			t.Fatalf("first level = %+v", first.Level)
		}
		h.waitInbox("mkeller", 0) // level 2 is not open yet
		h.json("POST", "/api/v1/approvals/"+first.Id+"/delegate", "afavre",
			api.ApprovalDelegation{UserId: "usr_sbrunner", Comment: strPtr("Out until Monday")}, 200, nil)
		h.json("POST", "/api/v1/approvals/"+first.Id+"/delegate", "afavre", api.ApprovalDelegation{UserId: "usr_lmartin"}, 422, nil)
		decide("sbrunner", h.waitInbox("sbrunner", 1).Items[0].Id, approve("Ports are fine"), 200)
		second := h.waitInbox("mkeller", 1).Items[0]
		if second.Level.Index != 2 || second.Level.Name != "Security board" {
			t.Fatalf("second level = %+v", second.Level)
		}
		if a := decide("mkeller", second.Id, approve("ok"), 200); a.Status != api.ApprovalStatusPending {
			t.Fatalf("mode all after one approval = %s", a.Status)
		}
		h.waitInbox("mkeller", 0)
		a := decide("cdubois", h.waitInbox("cdubois", 1).Items[0].Id, approve("ok"), 200)
		if a.Status != api.ApprovalStatusApproved || *a.Outcome != "Approved by Marc Keller and Claire Dubois" {
			t.Fatalf("mode all after both = %s %v", a.Status, a.Outcome)
		}
		h.waitStatus(two.Id, "lmartin", api.RequestStatusSucceeded)
		if s := policyStep("lmartin", two.Id); s.Title != "Approved by Sophie Brunner; Approved by Marc Keller and Claire Dubois" {
			t.Fatalf("two-level step = %q", s.Title)
		}

		// ── Mode all: a delegate approves for the person who delegated ────────
		policy("pol_e2e_board", `environment == "dev" && inputs.role == "board"`, `[
			{"name": "Change board", "approvers": ["usr_afavre", "usr_cdubois"], "mode": "all"}]`)
		board := submit("lmartin", draft("dev", "board", "S"))
		bl := h.waitInbox("afavre", 1).Items[0].Id
		h.json("POST", "/api/v1/approvals/"+bl+"/delegate", "afavre", api.ApprovalDelegation{UserId: "usr_sbrunner"}, 200, nil)
		decide("sbrunner", bl, approve("for Alain"), 200)
		if a := decide("cdubois", bl, approve("ok"), 200); a.Status != api.ApprovalStatusApproved {
			t.Fatalf("mode all with a delegate = %s", a.Status)
		}
		h.waitStatus(board.Id, "lmartin", api.RequestStatusSucceeded)

		// ── Mode all: once escalated, the escalation approver decides ─────────
		policy("pol_e2e_esc_all", `environment == "dev" && inputs.role == "escall"`, `[
			{"name": "Change board", "approvers": ["usr_afavre", "usr_cdubois"], "mode": "all",
			 "timeout": "1s", "on_timeout": "escalate", "escalate_to": {"approvers": ["usr_mkeller"]}}]`)
		escAll := submit("lmartin", draft("dev", "escall", "S"))
		ea := h.waitInbox("mkeller", 1).Items[0]
		if a := decide("mkeller", ea.Id, approve("Alain is away"), 200); a.Status != api.ApprovalStatusApproved ||
			*a.Outcome != "Approved by Marc Keller after escalation" {
			t.Fatalf("escalated mode all = %s %v", a.Status, a.Outcome)
		}
		h.waitStatus(escAll.Id, "lmartin", api.RequestStatusSucceeded)

		// ── No answer: escalate after the timeout, then reject ────────────────
		policy("pol_e2e_slow", `environment == "dev" && inputs.role == "slow"`, `[
			{"name": "Infrastructure leads", "group": "rvav-infra-leads", "mode": "any",
			 "timeout": "1s", "on_timeout": "escalate", "escalate_to": {"approvers": ["usr_cdubois"]}}]`)
		slow := submit("lmartin", draft("dev", "slow", "S"))
		h.waitInbox("mkeller", 1) // through the rvav-infra-leads group
		esc := h.waitInbox("cdubois", 1).Items[0]
		if esc.Escalated == nil || !*esc.Escalated {
			t.Fatalf("escalated level = %+v", esc)
		}
		// Claire is in no project, but may read the request she is asked about.
		h.json("GET", "/api/v1/requests/"+slow.Id, "cdubois", nil, 200, nil)
		h.json("GET", "/api/v1/requests/"+slow.Id, "sbrunner", nil, 404, nil)
		h.waitStatus(slow.Id, "lmartin", api.RequestStatusRejected)
		if s := policyStep("lmartin", slow.Id); !strings.HasPrefix(s.Title, "Rejected automatically: no answer within 1s") {
			t.Fatalf("timed-out step = %q", s.Title)
		}

		// ── An infrastructure lead's own production VM approves itself ────────
		own := submit("mkeller", draft("prd", "ops", "M"))
		if own.Status == api.RequestStatusPendingApproval {
			t.Fatalf("auto-approved request = %s", own.Status)
		}
		h.waitStatus(own.Id, "mkeller", api.RequestStatusSucceeded)
		if s := policyStep("mkeller", own.Id); s.Title != "Approved automatically" {
			t.Fatalf("auto-approved step = %q", s.Title)
		}

		// ── Rejected with a reason ───────────────────────────────────────────
		no := submit("tnguyen", draft("prd", "batch", "XL"))
		decide("mkeller", h.waitInbox("mkeller", 1).Items[0].Id,
			api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionReject, Comment: strPtr("Use the batch cluster in tst first.")}, 200)
		h.waitStatus(no.Id, "tnguyen", api.RequestStatusRejected)
		if s := policyStep("tnguyen", no.Id); s.Title != "Rejected by Marc Keller" || !strings.Contains(deref(s.Detail), "batch cluster") {
			t.Fatalf("rejected step = %q / %q", s.Title, deref(s.Detail))
		}

		// ── Cancelled while waiting: its level closes ─────────────────────────
		gone := submit("tnguyen", draft("prd", "gone", "XL"))
		h.waitInbox("mkeller", 1)
		h.json("POST", "/api/v1/requests/"+gone.Id+"/cancel", "tnguyen", nil, 202, nil)
		h.waitInbox("mkeller", 0)
		h.json("GET", "/api/v1/requests/"+gone.Id+"/approvals", "tnguyen", nil, 200, &history)
		if history.Items[0].Status != api.ApprovalStatusCancelled {
			t.Fatalf("cancelled request's level = %s", history.Items[0].Status)
		}
	})
}

package e2e

import (
	"context"
	"maps"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
)

// webFarm is web servers kept on separate hosts; its second version adds
// a disk and a monitoring job to each.
func webFarm(v2 bool) api.CatalogVersionDefinition {
	fields := []string{"app", "web_nodes", "size", "cluster", "network"}
	props := map[string]any{}
	ui := map[string]api.UiField{}
	for _, f := range fields {
		props[f] = map[string]any{"type": "string", "minLength": 1}
		ui[f] = api.UiField{Widget: api.UiWidgetText, Label: f}
	}
	props["web_nodes"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 3}
	web := map[string]any{"key": "web", "title": "Web servers", "count": "inputs.web_nodes", "anti_affinity": true,
		"bindings": map[string]any{"cluster": "inputs.cluster", "datastore": `"vsan-gva-dev-cl01"`, "post_config": `["linux-baseline"]`}}
	if v2 {
		web["disks"] = []any{map[string]any{"size_gb": 20}}
		web["bindings"].(map[string]any)["post_config"] = `["linux-baseline", "monitoring-agent"]`
	}
	def := api.CatalogVersionDefinition{
		FormSchema:    map[string]any{"type": "object", "required": fields, "properties": props},
		UiSchema:      api.UiSchema{Fields: ui},
		InitialValues: map[string]any{},
		Blueprint: map[string]any{
			"kind": "vm", "naming": "{project}-{environment}-{inputs.app}{component}{seq:02}",
			"deployment_name": "{project}-{environment}-{inputs.app}",
			"template":        "tpl-rhel9.4-2026.10", "guest": map[string]any{"os_family": "linux", "domain": "corp.example"},
			"sizes": map[string]any{"S": map[string]any{"label": "Small", "vcpu": 2, "memory_gb": 4, "disk_gb": 50},
				"M": map[string]any{"label": "Medium", "vcpu": 4, "memory_gb": 8, "disk_gb": 50}},
			"bindings": map[string]any{"vcpu": "sizes[inputs.size].vcpu", "memory_gb": "sizes[inputs.size].memory_gb",
				"disk_gb": "sizes[inputs.size].disk_gb", "network": "inputs.network"},
			"awx_templates": map[string]any{
				"linux-baseline":   map[string]any{"title": "Linux baseline", "job_template": "linux-baseline"},
				"monitoring-agent": map[string]any{"title": "Monitoring", "job_template": "monitoring-agent"}},
			"components": []any{web},
		},
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "Farm", Fields: fields}}
	return def
}

// TestUpdateKeepsFilesAndEmptied: a deployment whose item has a file
// field and a default the requester emptied. An update with its own
// inputs keeps its file and leaves the default out, so nothing changes;
// one with a new file attaches it to the update request.
func TestUpdateKeepsFilesAndEmptied(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner = "mkeller", "tnguyen"
		def := webFarm(false)
		props := def.FormSchema["properties"].(map[string]any)
		props["spec"] = map[string]any{"type": "object"}
		props["proxy"] = map[string]any{"type": "string"}
		def.UiSchema.Fields["spec"] = api.UiField{Widget: api.UiWidgetFile, Label: "Spec", Accept: &[]string{".csv"}, MaxSizeMb: new(1)}
		def.UiSchema.Fields["proxy"] = api.UiField{Widget: api.UiWidgetText, Label: "Proxy", Rules: &api.UiRules{Default: new(`"proxy.corp:3128"`)}}
		def.UiSchema.Layout.Sections[0].Fields = append(def.UiSchema.Layout.Sections[0].Fields, "spec", "proxy")
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "web-farm", Name: "Web farm",
			Summary: "Web servers.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/web-farm/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		v1 := item.DraftVersion.Id
		upload := func(content string) map[string]any {
			t.Helper()
			var ref api.FileRef
			h.json("POST", "/api/v1/forms/files?"+url.Values{"version_id": {v1}, "project_id": {"prj_web"}, "field": {"spec"}, "name": {"spec.csv"}}.Encode(),
				owner, rawYAML(content), 201, &ref, "Content-Type", "text/csv")
			return map[string]any{"id": ref.Id, "name": ref.Name, "size": ref.Size, "content_type": ref.ContentType, "sha256": ref.Sha256}
		}
		first := upload("port,source\n443,10.0.0.0/8\n")
		var built api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: v1, ProjectId: "prj_web",
			Environment: "dev", Inputs: map[string]any{"app": "farm", "web_nodes": 1, "size": "S", "cluster": "GVA-DEV-CL01",
				"network": "dvpg-app-dev-vl310", "spec": first, "proxy": nil}}, 202, &built, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(built.Id, nil)
		h.waitStatus(built.Id, owner, api.RequestStatusSucceeded)
		depID := (*built.DeploymentIds)[0]
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+depID, owner, nil, 200, &dep)
		if _, ok := dep.Inputs["proxy"]; ok {
			t.Fatalf("the deployment has the default the requester emptied: %v", dep.Inputs)
		}

		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/deployments/"+depID+"/update", owner, api.UpdateDraft{VersionId: v1, Inputs: dep.Inputs}, 422, &problem,
			"Idempotency-Key", uuid.NewString())
		if len(problem.Errors) != 1 || deref(problem.Errors[0].Code) != "no_change" {
			t.Fatalf("an update with the deployment's own inputs = %+v", problem.Errors)
		}

		in := maps.Clone(dep.Inputs)
		in["spec"] = upload("port,source\n8443,10.0.0.0/8\n")
		var upd api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/update", owner, api.UpdateDraft{VersionId: v1, Inputs: in}, 202, &upd,
			"Idempotency-Key", uuid.NewString())
		for id, want := range map[string]string{first["id"].(string): built.Id, in["spec"].(map[string]any)["id"].(string): upd.Id} {
			var reqID *string
			if err := h.store.Pool.QueryRow(ctx, `select request_id from request_files where id = $1`, id).Scan(&reqID); err != nil || reqID == nil || *reqID != want {
				t.Errorf("file %s is the request %v's, want %s (%v)", id, reqID, want, err)
			}
		}
		if got := *upd.Inputs; got["proxy"] != nil {
			t.Errorf("the update gave the emptied field its default: %v", got)
		}
	})
}

// TestUpdateDeployment updates a deployment (X1.4, ADR-0042): an input
// that resizes its VMs, a new version that adds a disk and a job, more web
// servers then fewer, a change rvav can't make, an update approved by a
// policy and one over quota, and a failed deployment updated to ready.
func TestUpdateDeployment(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web = "mkeller", "tnguyen", "prj_web"
		def := webFarm(false)
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "web-farm", Name: "Web farm",
			Summary: "Web servers on separate hosts.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/web-farm/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		v1 := item.DraftVersion.Id

		inputs := map[string]any{"app": "farm", "web_nodes": 2, "size": "S", "cluster": "GVA-DEV-CL01", "network": "dvpg-app-dev-vl310"}
		var built api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: v1, ProjectId: web,
			Environment: "dev", Inputs: inputs}, 202, &built, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(built.Id, nil)
		h.waitStatus(built.Id, owner, api.RequestStatusSucceeded)
		depID := (*built.DeploymentIds)[0]

		with := func(changes map[string]any) map[string]any {
			out := map[string]any{}
			for k, v := range inputs {
				out[k] = v
			}
			for k, v := range changes {
				out[k] = v
			}
			return out
		}
		preview := func(version string, in map[string]any) api.UpdatePreview {
			t.Helper()
			var pv api.UpdatePreview
			h.json("POST", "/api/v1/deployments/"+depID+"/update/preview", owner, api.UpdateDraft{VersionId: version, Inputs: in}, 200, &pv)
			return pv
		}
		ask := func(version string, in map[string]any, want int) (api.Request, []api.FieldError) {
			t.Helper()
			var req api.Request
			var problem struct{ Errors []api.FieldError }
			out := any(&req)
			if want != 202 {
				out = &problem
			}
			h.json("POST", "/api/v1/deployments/"+depID+"/update", owner, api.UpdateDraft{VersionId: version, Inputs: in}, want, out,
				"Idempotency-Key", uuid.NewString())
			return req, problem.Errors
		}
		update := func(version string, in map[string]any) api.Request {
			t.Helper()
			req, _ := ask(version, in, 202)
			h.runWorkflow(req.Id, nil)
			req = h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
			inputs = in
			return req
		}
		kinds := func(pv api.UpdatePreview) []string {
			var out []string
			for _, c := range pv.Changes {
				out = append(out, string(c.Kind))
			}
			return out
		}
		adding := func(pv api.UpdatePreview, res api.QuotaResource) int {
			for _, q := range pv.Quota {
				if q.Environment == nil && q.Resource == res {
					return q.Adding
				}
			}
			t.Fatalf("no %s in %+v", res, pv.Quota)
			return 0
		}
		deployment := func() (api.DeploymentDetail, map[string]api.VmProperties, []api.DrsRuleResource) {
			t.Helper()
			var dep api.DeploymentDetail
			h.json("GET", "/api/v1/deployments/"+depID, owner, nil, 200, &dep)
			vms := map[string]api.VmProperties{}
			var rules []api.DrsRuleResource
			for _, r := range dep.Resources {
				switch v, _ := r.ValueByDiscriminator(); v := v.(type) {
				case api.VmResource:
					vms[v.Name] = v.Vm
				case api.DrsRuleResource:
					rules = append(rules, v)
				}
			}
			return dep, vms, rules
		}
		records := func(typ string) []string {
			var names []string
			_ = h.store.Pool.QueryRow(ctx, `select coalesce(array_agg(name order by name), '{}') from resources
				where deployment_id = $1 and type = $2`, depID, typ).Scan(&names)
			return names
		}

		// The same version and inputs change nothing.
		pv := preview(v1, inputs)
		if !pv.Allowed || !pv.Unchanged || len(pv.Changes) != 0 || len(pv.Refused) != 0 || pv.FromVersion.Id != v1 || pv.ToVersion.Id != v1 {
			t.Fatalf("unchanged preview = %+v", pv)
		}
		if _, errs := ask(v1, inputs, 422); len(errs) != 1 || deref(errs[0].Code) != "no_change" {
			t.Fatalf("an update that changes nothing = %+v", errs)
		}

		// A bigger size resizes each web server in place.
		pv = preview(v1, with(map[string]any{"size": "M"}))
		if !slices.Equal(kinds(pv), []string{"resize", "resize"}) || adding(pv, api.Vcpu) != 4 || adding(pv, api.MemoryGb) != 8 {
			t.Fatalf("resize preview = %v, quota %+v", kinds(pv), pv.Quota)
		}
		req := update(v1, with(map[string]any{"size": "M"}))
		if req.Kind != api.RequestKindUpdate || req.Title != "Update · web-dev-farm" || deref(req.DeploymentId) != depID {
			t.Fatalf("update request = %s %q %v", req.Kind, req.Title, req.DeploymentId)
		}
		dep, vms, _ := deployment()
		for _, name := range []string{"web-dev-farmweb01", "web-dev-farmweb02"} {
			if vms[name].CpuCount != 4 || vms[name].MemoryMb != 8192 {
				t.Fatalf("%s = %d vCPU, %d MB", name, vms[name].CpuCount, vms[name].MemoryMb)
			}
		}
		if dep.Status != api.DeploymentStatusReady || dep.Inputs["size"] != "M" {
			t.Fatalf("updated deployment = %s, inputs %v", dep.Status, dep.Inputs)
		}

		// The second version adds a disk to each, and the job it adds runs
		// on both; the one they ran when built doesn't run again.
		next := webFarm(true)
		var draft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions", admin, api.CatalogItemVersionCreate{Definition: &next}, 201, &draft)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+draft.Id+"/publish", admin, nil, 200, nil)
		v2 := draft.Id
		if _, errs := ask(v1, inputs, 422); !slices.Contains(fieldsOf(errs), "/version_id") {
			t.Fatalf("an update to the version before = %+v", errs)
		}
		pv = preview(v2, inputs)
		if !slices.Equal(kinds(pv), []string{"disk_add", "disk_add", "job"}) || adding(pv, api.StorageGb) != 40 || pv.ToVersion.Number != draft.Number {
			t.Fatalf("new version preview = %v, quota %+v", kinds(pv), pv.Quota)
		}
		req = update(v2, inputs)
		steps := timelineOf(h, owner, req.Id)
		if s := steps["awx.job:monitoring-agent@web"]; s.Status != api.StepStatusDone || deref(s.Detail) != "limit=web-dev-farmweb01,web-dev-farmweb02" {
			t.Fatalf("monitoring job = %+v (steps %v)", s, keysOf(steps))
		}
		if _, ok := steps["awx.job:linux-baseline@web"]; ok {
			t.Fatal("the job they ran when built runs again")
		}
		dep, vms, _ = deployment()
		if dep.Version.Id != v2 || len(vms["web-dev-farmweb02"].Disks) != 2 || vms["web-dev-farmweb02"].Disks[1].CapacityGb != 20 {
			t.Fatalf("deployment of version %s, disks %+v", dep.Version.Number, vms["web-dev-farmweb02"].Disks)
		}

		// A third web server: named after the others, its own address,
		// built as the new version builds them, and kept apart from them.
		pv = preview(v2, with(map[string]any{"web_nodes": 3}))
		if !slices.Equal(kinds(pv), []string{"vm_add"}) || adding(pv, api.VmCount) != 1 || adding(pv, api.Vcpu) != 4 {
			t.Fatalf("scale out preview = %v, quota %+v", kinds(pv), pv.Quota)
		}
		update(v2, with(map[string]any{"web_nodes": 3}))
		_, vms, rules := deployment()
		third, ok := vms["web-dev-farmweb03"]
		if !ok || third.CpuCount != 4 || len(third.Disks) != 2 || deref(third.Component) != "web" {
			t.Fatalf("VMs after scale out = %v", slices.Sorted(mapKeys(vms)))
		}
		addrs := records("ipam.address")
		if len(addrs) != 3 || len(slices.Compact(slices.Clone(addrs))) != 3 {
			t.Fatalf("addresses = %v", addrs)
		}
		if len(rules) != 1 || !slices.Equal(rules[0].Rule.Vms, []string{"web-dev-farmweb01", "web-dev-farmweb02", "web-dev-farmweb03"}) {
			t.Fatalf("DRS rule = %+v", rules)
		}
		inVC, err := h.vc.AntiAffinityRule(ctx, "GVA-DEV-CL01", rules[0].Name)
		if err != nil || len(inVC) != 3 {
			t.Fatalf("rule in vCenter = %v (%v)", inVC, err)
		}

		// Down to one: the last two go with their addresses and records,
		// the rule with them, and the quota gets them back.
		pv = preview(v2, with(map[string]any{"web_nodes": 1}))
		if !slices.Equal(kinds(pv), []string{"vm_remove", "vm_remove"}) || adding(pv, api.VmCount) != -2 || !pv.Changes[0].Destructive ||
			deref(pv.Changes[0].Vm) != "web-dev-farmweb02" {
			t.Fatalf("scale in preview = %+v, quota %+v", pv.Changes, pv.Quota)
		}
		update(v2, with(map[string]any{"web_nodes": 1}))
		_, vms, rules = deployment()
		if len(vms) != 1 || len(rules) != 0 || vmExists(ctx, c, "web-dev-farmweb02") || vmExists(ctx, c, "web-dev-farmweb03") {
			t.Fatalf("after scale in: VMs %v, rules %+v", slices.Sorted(mapKeys(vms)), rules)
		}
		if got := records("ipam.address"); len(got) != 1 || !slices.ContainsFunc(records("dns.record"), func(n string) bool {
			return strings.Contains(n, "web-dev-farmweb01")
		}) || slices.ContainsFunc(records("dns.record"), func(n string) bool { return strings.Contains(n, "web-dev-farmweb03") }) {
			t.Fatalf("addresses %v, DNS records %v", got, records("dns.record"))
		}
		if got, _ := h.vc.AntiAffinityRule(ctx, "GVA-DEV-CL01", "rvav-"+depID+"-web"); got != nil {
			t.Fatalf("the rule is still in vCenter: %v", got)
		}
		// A name given stays given: the next web server is the fourth.
		update(v2, with(map[string]any{"web_nodes": 2}))
		if _, vms, _ = deployment(); !slices.Equal(slices.Sorted(mapKeys(vms)), []string{"web-dev-farmweb01", "web-dev-farmweb04"}) {
			t.Fatalf("VMs after scaling out again = %v", slices.Sorted(mapKeys(vms)))
		}

		// Another network for the first adapter: refused, with its reason.
		pv = preview(v2, with(map[string]any{"network": "dvpg-db-dev-vl320"}))
		if len(pv.Refused) != 1 || deref(pv.Refused[0].Setting) != "network" || !strings.Contains(pv.Refused[0].Reason, "doesn't move") {
			t.Fatalf("refused = %+v", pv.Refused)
		}
		if _, errs := ask(v2, with(map[string]any{"network": "dvpg-db-dev-vl320"}), 422); len(errs) != 1 || deref(errs[0].Code) != "refused" {
			t.Fatalf("a refused update = %+v", errs)
		}

		// Approval policies read the update; quotas count its net change.
		var policy api.ApprovalPolicy
		h.json("POST", "/api/v1/admin/approval-policies", admin, api.ApprovalPolicyWrite{Name: "More VMs on an update",
			Condition: `request.kind == "update" && update.vm_count > 0`,
			Levels:    []api.ApprovalLevel{{Name: "Infrastructure leads", Group: new("rvav-infra-leads")}}}, 201, &policy)
		var quotas api.QuotaLimitList
		h.json("GET", "/api/v1/admin/projects/"+web+"/quotas", admin, nil, 200, &quotas)
		var used int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from resources r join deployments d on d.id = r.deployment_id
			where d.project_id = $1 and r.type = 'vsphere.vm' and d.environment = 'dev' and d.status <> 'deleted'`, web).Scan(&used)
		h.json("PUT", "/api/v1/admin/projects/"+web+"/quotas", admin, api.QuotaLimitList{Items: append(slices.Clone(quotas.Items),
			api.QuotaLimit{Environment: "dev", Resource: api.VmCount, Limit: new(used), Enforcement: api.QuotaLimitEnforcementHard})}, 200, nil)
		pv = preview(v2, with(map[string]any{"web_nodes": 3}))
		var policies []string
		for _, p := range pv.Approval.Policies {
			policies = append(policies, p.PolicyId)
		}
		if !pv.Approval.Required || !slices.Contains(policies, policy.Id) || !slices.Contains(codesOf(pv.Errors), "quota_exceeded") {
			t.Fatalf("over quota, approved: %+v, errors %+v", pv.Approval, pv.Errors)
		}
		if _, errs := ask(v2, with(map[string]any{"web_nodes": 3}), 422); !slices.Contains(codesOf(errs), "quota_exceeded") {
			t.Fatalf("an update over quota = %+v", errs)
		}
		// Fewer VMs fit, and need no approval.
		if pv = preview(v2, with(map[string]any{"web_nodes": 1})); pv.Approval.Required || len(pv.Errors) != 0 {
			t.Fatalf("scale in under the quota = %+v / %+v", pv.Approval, pv.Errors)
		}
		h.json("PUT", "/api/v1/admin/projects/"+web+"/quotas", admin, quotas, 200, nil)
		req, _ = ask(v2, with(map[string]any{"web_nodes": 3}), 202)
		if req.Status != api.RequestStatusPendingApproval {
			t.Fatalf("an update that needs approval = %s", req.Status)
		}
		h.runWorkflow(req.Id, func(env *testsuite.TestWorkflowEnvironment) {
			env.RegisterDelayedCallback(func() {
				var inbox api.ApprovalPage
				h.json("GET", "/api/v1/approvals", admin, nil, 200, &inbox)
				for _, a := range inbox.Items {
					if a.Request.Id == req.Id {
						h.json("POST", "/api/v1/approvals/"+a.Id+"/decision", admin,
							api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionApprove}, 200, nil)
					}
				}
				h.wf.deliver(env)
			}, time.Hour)
		})
		h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
		if _, vms, _ = deployment(); len(vms) != 3 {
			t.Fatalf("VMs after the approved update = %v", slices.Sorted(mapKeys(vms)))
		}
		// New requests don't match it.
		var newPV api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: v2, ProjectId: web,
			Environment: "dev", Inputs: with(map[string]any{"app": "other", "web_nodes": 1})}, 200, &newPV)
		if newPV.Approval.Required {
			t.Fatalf("a new request matches the update's policy: %+v", newPV.Approval)
		}
	})
}

// TestUpdateFailedDeployment updates a deployment whose post-configuration
// failed (A19): its VM resized and every job run again, it is ready.
func TestUpdateFailedDeployment(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const owner, web = "tnguyen", "prj_web"
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+web, owner, nil, 200, &item)
		var form api.RequestForm
		h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+web, owner, nil, 200, &form)
		inputs := map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline", "idm-join"}}
		var built api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: web,
			Environment: "dev", Inputs: inputs}, 202, &built, "Idempotency-Key", uuid.NewString())
		h.awx.FailTask = "idm : join realm"
		h.runWorkflow(built.Id, nil)
		h.awx.FailTask = ""
		h.waitStatus(built.Id, owner, api.RequestStatusFailed)
		depID := (*built.DeploymentIds)[0]

		kindsOf := func(inputs map[string]any) (api.UpdatePreview, []string) {
			var pv api.UpdatePreview
			h.json("POST", "/api/v1/deployments/"+depID+"/update/preview", owner, api.UpdateDraft{VersionId: form.VersionId, Inputs: inputs}, 200, &pv)
			var kinds []string
			for _, c := range pv.Changes {
				kinds = append(kinds, string(c.Kind))
			}
			return pv, kinds
		}
		// With its own inputs, an update repairs it: its jobs run again.
		if pv, kinds := kindsOf(inputs); !pv.Allowed || pv.Unchanged || !slices.Equal(kinds, []string{"job", "job"}) {
			t.Fatalf("failed deployment's repair = %+v", pv)
		}
		inputs["size"] = "M"
		if pv, kinds := kindsOf(inputs); !pv.Allowed || !slices.Equal(kinds, []string{"resize", "job", "job"}) {
			t.Fatalf("failed deployment's preview = %+v", pv)
		}
		var req api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/update", owner, api.UpdateDraft{VersionId: form.VersionId, Inputs: inputs}, 202, &req,
			"Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
		steps := timelineOf(h, owner, req.Id)
		if steps["awx.job:idm-join@vm"].Status != api.StepStatusDone || steps["awx.job:linux-baseline@vm"].Status != api.StepStatusDone {
			t.Fatalf("steps = %v", keysOf(steps))
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+depID, owner, nil, 200, &dep)
		if dep.Status != api.DeploymentStatusReady || dep.Inputs["size"] != "M" {
			t.Fatalf("deployment = %s, inputs %v", dep.Status, dep.Inputs)
		}
		// Its history lists the update.
		var history struct{ Items []api.Request }
		h.json("GET", "/api/v1/deployments/"+depID+"/history", owner, nil, 200, &history)
		if len(history.Items) != 2 || history.Items[0].Kind != api.RequestKindUpdate {
			t.Fatalf("history = %+v", history.Items)
		}
	})
}

func timelineOf(h *harness, user, id string) map[string]api.Step {
	h.t.Helper()
	var steps struct{ Items []api.Step }
	h.json("GET", "/api/v1/requests/"+id+"/steps", user, nil, 200, &steps)
	out := map[string]api.Step{}
	for _, s := range steps.Items {
		out[s.Key] = s
		for _, ch := range s.Children {
			out[ch.Key] = ch
		}
	}
	return out
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func codesOf(errs []api.FieldError) []string {
	var out []string
	for _, e := range errs {
		out = append(out, deref(e.Code))
	}
	return out
}

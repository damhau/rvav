package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/awxcatalog"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vra"
)

// a1Person is someone the acceptance signs in as, made for it: no seeded
// role or group but the ones it gives.
type a1Person struct{ login, name string }

func (p a1Person) id() string    { return seed.UserID(p.login) }
func (p a1Person) email() string { return p.login + "@corp.example" }

// TestA1Acceptance is the custom roles' acceptance (A1.4, ADR-0034), through
// the API, a real Temporal dev server and worker, and vcsim. An
// administrator makes an operator who may only power VMs on and off in
// Web Platform, given to one person directly and to another through a group:
// each powers a real VM off and on there, is refused every other day-2
// action, can't request, and sees no other project. An administrator makes a
// compliance auditor holding the built-in auditor's permissions, given for
// all of rvav: on every operation of api/openapi.yaml, it answers what the
// built-in auditor does, every read the same, and no change succeeds.
func TestA1Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, queue = "mkeller", "rvav-a1-e2e"
		tc := ts.Client()
		h.svc.Workflows = &provision.Temporal{Client: tc, TaskQueue: queue}
		w := provision.NewWorker(tc, queue, h.acts)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		defer w.Stop()
		// The AWX day-2 action is one more the operator must be refused.
		h.svc.AWXCatalog = &awxcatalog.Sync{Store: h.store, AWX: h.awx, Log: h.acts.Log,
			Filter: awx.TemplateFilter{Labels: []string{"catalog"}}, ActionLabels: []string{"day2"}}
		h.json("POST", "/api/v1/admin/awx/templates/sync", admin, nil, 200, nil)

		direct, grouped := a1Person{"ojansen", "Olivia Jansen"}, a1Person{"pweber", "Paul Weber"}
		builtin, custom := a1Person{"ahuber", "Anna Huber"}, a1Person{"rrossi", "Rita Rossi"}
		for _, p := range []a1Person{direct, grouped, builtin, custom} {
			if _, err := h.store.Pool.Exec(ctx, `insert into users (id, subject, name, email) values ($1, $2, $3, $4)`,
				p.id(), "seed:"+p.login, p.name, p.email()); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.store.SetUserGroups(ctx, grouped.id(), []string{"rvav-operators"}); err != nil {
			t.Fatal(err)
		}

		web := a1Build(h, c, "tnguyen", "prj_web")
		data := a1Build(h, c, "sbrunner", "prj_data")

		// The operator, made by an administrator and given in Web Platform, directly and through a group.
		var operator api.Role
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "vm-operator", Name: "VM operator",
			Description: new("Powers the project's VMs on and off, nothing else."),
			Permissions: []string{"project.see", "day2.power:power_on", "day2.power:power_off"}}, 201, &operator)
		if operator.Scope != api.RoleScopeAny {
			t.Fatalf("the operator role = %+v", operator)
		}
		h.json("PUT", "/api/v1/projects/prj_web/members/"+direct.id(), admin, api.ProjectMemberWrite{Roles: &[]string{"vm-operator"}}, 200, nil)
		var mapping api.GroupMapping
		h.json("POST", "/api/v1/admin/group-mappings", admin, api.GroupMappingWrite{OidcGroup: "rvav-operators",
			ProjectId: new("prj_web"), Role: "vm-operator"}, 201, &mapping)

		for _, op := range []a1Person{direct, grouped} {
			var me api.Me
			h.json("GET", "/api/v1/me", op.login, nil, 200, &me)
			if len(me.Projects) != 1 || me.Projects[0].Project.Id != "prj_web" || !slices.Equal(roleKeys(me.Projects[0].Roles), []string{"vm-operator"}) ||
				!slices.Equal(me.Projects[0].Permissions, []string{"day2.power:power_off", "day2.power:power_on", "project.see"}) ||
				len(me.Permissions) != 0 {
				t.Fatalf("%s's /me = %+v", op.login, me)
			}

			// Powered off and on, for real.
			web.power(op.login, "power_off", types.VirtualMachinePowerStatePoweredOff)
			web.power(op.login, "power_on", types.VirtualMachinePowerStatePoweredOn)

			// Every other action: refused in the list and when run.
			others := web.refusedBut(op.login, "power_on", "power_off")
			if len(others) < 10 || !slices.Contains(others, "awx.monitoring-agent") || !slices.Contains(others, "delete") {
				t.Fatalf("%s is refused only %v", op.login, others)
			}

			// No request in Web Platform, and no other project.
			h.json("POST", "/api/v1/requests", op.login, web.draft, 403, nil, "Idempotency-Key", uuid.NewString())
			h.json("POST", "/api/v1/me/drafts", op.login, api.SavedDraftWrite{Draft: web.draft}, 422, nil)
			var deployments api.DeploymentPage
			h.json("GET", "/api/v1/deployments", op.login, nil, 200, &deployments)
			if !slices.ContainsFunc(deployments.Items, func(d api.Deployment) bool { return d.Id == web.id }) ||
				slices.ContainsFunc(deployments.Items, func(d api.Deployment) bool { return d.Project.Id != "prj_web" }) {
				t.Fatalf("%s's deployments = %+v", op.login, deployments.Items)
			}
			h.json("GET", "/api/v1/projects/prj_data", op.login, nil, 404, nil)
			h.json("GET", "/api/v1/catalog/items?project_id=prj_data", op.login, nil, 404, nil)
			h.json("GET", "/api/v1/deployments/"+data.id, op.login, nil, 404, nil)
			h.json("GET", "/api/v1/deployments/"+data.id+"/actions", op.login, nil, 404, nil)
			data.run(op.login, "power_off", 404)
		}

		// The compliance auditor: the built-in auditor's permissions, made by
		// an administrator and given for all of rvav; Anna holds the built-in one.
		var auditor api.Role
		h.json("GET", "/api/v1/admin/roles/auditor", admin, nil, 200, &auditor)
		h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: "compliance-auditor", Name: "Compliance auditor",
			Description: new("Sees everything, changes nothing."), Permissions: auditor.Permissions}, 201, nil)
		var given api.RoleAssignment
		h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: custom.id(), Role: "compliance-auditor"}, 201, &given)
		h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: builtin.id(), Role: "auditor"}, 201, nil)

		walk := a1Walk{t: t, h: h, builtin: builtin, custom: custom, ids: a1IDs(t, h, web, given), draft: web.draft}
		walk.ids["group-mappings/{id}"] = mapping.Id
		walk.compare()
		if a := web.actions(custom.login); slices.ContainsFunc(a, func(a api.Day2Action) bool { return a.Enabled }) {
			t.Fatalf("the compliance auditor's actions = %+v", a)
		}
	})
}

// a1VM is a dev VM built through a real Temporal worker.
type a1VM struct {
	h        *harness
	c        *vim25.Client
	id       string
	resource string
	moref    string
	draft    api.RequestDraft
}

func a1Build(h *harness, c *vim25.Client, user, project string) *a1VM {
	h.t.Helper()
	var item api.CatalogItem
	h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id="+project, user, nil, 200, &item)
	var form api.RequestForm
	h.json("GET", "/api/v1/catalog/items/rhel9-server/form?project_id="+project, user, nil, 200, &form)
	d := api.RequestDraft{CatalogItemId: item.Id, VersionId: form.VersionId, ProjectId: project, Environment: "dev",
		Inputs: map[string]any{"role": "app", "size": "S", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310", "post_config": []string{"linux-baseline"}}}
	var r api.Request
	h.json("POST", "/api/v1/requests", user, d, 202, &r, "Idempotency-Key", uuid.NewString())
	r = h.waitStatus(r.Id, user, api.RequestStatusSucceeded)
	vm := &a1VM{h: h, c: c, id: (*r.DeploymentIds)[0], draft: d}
	for _, a := range vm.actions(user) {
		if a.Key == "power_off" {
			vm.resource = *a.ResourceId
		}
	}
	var dep struct {
		Resources []struct {
			Type string
			VM   api.VmProperties `json:"vm"`
		}
	}
	h.json("GET", "/api/v1/deployments/"+vm.id, user, nil, 200, &dep)
	for _, res := range dep.Resources {
		if res.Type == "vsphere.vm" {
			vm.moref = res.VM.Moref
		}
	}
	if vm.resource == "" || vm.moref == "" {
		h.t.Fatalf("no VM in %s's deployment %s", project, vm.id)
	}
	return vm
}

func (v *a1VM) actions(user string) []api.Day2Action {
	v.h.t.Helper()
	var out struct{ Items []api.Day2Action }
	v.h.json("GET", "/api/v1/deployments/"+v.id+"/actions", user, nil, 200, &out)
	return out.Items
}

func (v *a1VM) run(user, action string, want int) api.Request {
	v.h.t.Helper()
	var r api.Request
	var out any
	if want == 202 {
		out = &r
	}
	v.h.json("POST", "/api/v1/deployments/"+v.id+"/actions", user, api.Day2Draft{Action: action, ResourceId: &v.resource,
		Inputs: map[string]any{}}, want, out, "Idempotency-Key", uuid.NewString())
	return r
}

// power runs a power action through the worker, and finds the VM in that state in vcsim.
func (v *a1VM) power(user, action string, want types.VirtualMachinePowerState) {
	v.h.t.Helper()
	r := v.run(user, action, 202)
	v.h.waitStatus(r.Id, user, api.RequestStatusSucceeded)
	got, err := object.NewVirtualMachine(v.c, types.ManagedObjectReference{Type: "VirtualMachine", Value: v.moref}).PowerState(v.h.ctx)
	if err != nil {
		v.h.t.Fatal(err)
	}
	if got != want {
		v.h.t.Fatalf("after %s's %s, the VM is %s", user, action, got)
	}
}

// refusedBut checks that every action but those allowed is disabled in the
// list and refused when run, and returns their keys.
func (v *a1VM) refusedBut(user string, allowed ...string) []string {
	v.h.t.Helper()
	var keys []string
	for _, a := range v.actions(user) {
		if slices.Contains(allowed, a.Key) {
			continue
		}
		keys = append(keys, a.Key)
		if a.Enabled || deref(a.DisabledReason) == "" {
			v.h.t.Errorf("%s may %s on %s: %+v", user, a.Key, deref(a.ResourceName), a)
		}
		if a.Key == "console" {
			v.h.json("POST", "/api/v1/deployments/"+v.id+"/resources/"+v.resource+"/console", user, nil, 403, nil)
			continue
		}
		if a.Key == "update" {
			v.h.json("POST", "/api/v1/deployments/"+v.id+"/update", user, api.UpdateDraft{VersionId: "ver_none", Inputs: map[string]any{}},
				403, nil, "Idempotency-Key", uuid.NewString())
			continue
		}
		v.h.json("POST", "/api/v1/deployments/"+v.id+"/actions", user, api.Day2Draft{Action: a.Key, ResourceId: a.ResourceId,
			Inputs: map[string]any{}}, 403, nil, "Idempotency-Key", uuid.NewString())
	}
	return keys
}

// a1IDs are real objects for every path parameter of the spec, by the
// segment before it: an administrator makes those the seed doesn't have.
func a1IDs(t *testing.T, h *harness, vm *a1VM, given api.RoleAssignment) map[string]string {
	t.Helper()
	const admin = "mkeller"
	type listed struct {
		Items []struct {
			Id, Key string
			HasLog  bool `json:"has_log"`
		}
	}
	first := func(path string) listed {
		t.Helper()
		var l listed
		h.json("GET", path, admin, nil, 200, &l)
		if len(l.Items) == 0 {
			t.Fatalf("GET %s: nothing to walk with", path)
		}
		return l
	}

	var item api.AdminCatalogItem
	h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &item)
	var steps struct{ Items []api.Step }
	h.json("GET", "/api/v1/requests/"+vm.h.firstRequest(vm.id)+"/steps", admin, nil, 200, &steps)
	logged := slices.IndexFunc(steps.Items, func(s api.Step) bool { return s.HasLog })
	if logged < 0 {
		t.Fatalf("no step with a log: %+v", steps.Items)
	}

	// Waiting for approval: a production VM.
	prd := vm.draft
	prd.Environment, prd.Inputs = "prd", map[string]any{"role": "front", "size": "XL", "cluster": "GVA-PRD-CL02",
		"datastore": "vsan-gva-prd-cl02", "network": "dvpg-web-prd-vl210", "post_config": []string{"linux-baseline"}}
	var waiting api.Request
	h.json("POST", "/api/v1/requests", "tnguyen", prd, 202, &waiting, "Idempotency-Key", uuid.NewString())
	h.waitStatus(waiting.Id, "tnguyen", api.RequestStatusPendingApproval)
	inbox := h.waitInbox(admin, 1)

	// A file the VM's request keeps, which only its requester, approvers
	// and project admins download.
	const attached = "fil_a1rules"
	if _, err := h.store.Pool.Exec(h.ctx, `insert into request_files (id, owner_id, request_id, name, content_type, size, sha256, content)
		select $1, requester_id, id, 'rules.csv', 'text/csv', 4, 'a1', 'a,b
' from requests where id = $2`, attached, vm.h.firstRequest(vm.id)); err != nil {
		t.Fatal(err)
	}

	const picture = "img_a1zones"
	if err := h.store.PutFormImage(h.ctx, store.FormImage{ID: picture, Name: "zones.png", ContentType: "image/png", Size: 8, SHA256: "a1"},
		[]byte("\x89PNG\r\n\x1a\n")); err != nil {
		t.Fatal(err)
	}

	if err := h.store.SetBrandLogo(h.ctx, h.store.Pool, false, &store.BrandLogo{ContentType: "image/png",
		Content: []byte("\x89PNG\r\n\x1a\n")}, nil); err != nil {
		t.Fatal(err)
	}

	var draft api.SavedDraft
	h.json("POST", "/api/v1/me/drafts", "tnguyen", api.SavedDraftWrite{Draft: vm.draft}, 201, &draft)
	var view api.SavedView
	h.json("POST", "/api/v1/me/views", "tnguyen", api.SavedViewWrite{List: api.ViewListDeployments, Name: "Production",
		Query: map[string]string{"environment": "prd"}}, 201, &view)
	var token api.ApiTokenCreated
	h.json("POST", "/api/v1/tokens", "tnguyen", api.ApiTokenCreate{Name: "Thomas's pipeline"}, 201, &token)
	h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "a1-token", Kind: api.SecretWriteKindToken, Value: new("x")}, 201, nil)
	h.json("POST", "/api/v1/admin/property-groups", admin, api.PropertyGroupWrite{Key: new("a1_group"), Name: "A1 group",
		Inputs: []api.PropertyGroupInput{}, Constants: map[string]any{"domain": "corp.example"}}, 201, nil)
	h.json("POST", "/api/v1/admin/form-library", admin, api.FormLibraryEntryWrite{Key: new("a1_check"), Name: "A1 check",
		Use: api.Validation, Kind: api.Cel, Params: map[string]any{"check": "true"}, Parameters: []api.FormLibraryParameter{}}, 201, nil)
	fin := &[]api.Offer{{ProjectId: new("prj_fin")}}
	var naming api.NamingPolicy
	h.json("POST", "/api/v1/admin/naming-policies", admin, api.NamingPolicyWrite{Name: "A1 names",
		Template: "{project}-{environment}-{role}{seq:03}", Offers: fin}, 201, &naming)
	var leaseRule api.LeaseRule
	h.json("POST", "/api/v1/admin/lease-rules", admin, api.LeaseRuleWrite{Name: "A1 leases", Condition: `item.slug == "a1"`,
		ProjectId: new("prj_fin"), DefaultDays: new(7), MaxDays: new(14), GraceDays: 7, ExpiryAction: api.PowerOffThenDelete}, 201, &leaseRule)
	var limits api.DeploymentLimitPolicy
	h.json("POST", "/api/v1/admin/deployment-limit-policies", admin, api.DeploymentLimitPolicyWrite{Name: "A1 limits",
		ProjectId: new("prj_fin"), DeploymentsPerUser: new(50)}, 201, &limits)
	h.json("POST", "/api/v1/admin/templates", admin, api.VmTemplateWrite{Key: "ubuntu", Label: "Ubuntu 24.04",
		Sources: sourceOn("", "tpl-ubuntu24.04-2026.10"), OsFamily: api.VmTemplateWriteOsFamilyLinux, Offers: fin}, 201, nil)
	h.json("POST", "/api/v1/admin/sizes", admin, api.SizePresetWrite{Key: "M", Label: "Medium", Vcpu: 4, MemoryGb: 16, DiskGb: 80,
		Offers: fin}, 201, nil)
	h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new("a1-report"), Title: "Report",
		WorkflowType: "a1-report.v1", TaskQueue: "platform-workflows", Usages: []api.WorkflowUsage{api.WorkflowUsageCompositeStep}}, 201, nil)
	h.json("POST", "/api/v1/admin/workflows", admin, api.WorkflowRegistrationWrite{Key: new("a1-lb"), Title: "Load balancer",
		WorkflowType: "a1-lb.v1", TaskQueue: "platform-workflows", Usages: []api.WorkflowUsage{api.WorkflowUsageResourceType}}, 201, nil)
	lb := api.ResourceTypeDriver{Kind: api.ResourceDriverWorkflow, Name: "a1-lb"}
	h.json("POST", "/api/v1/admin/resource-types", admin, api.ResourceTypeWrite{Key: new("a1-lb"), Title: "Load balancer",
		Create: lb, Delete: lb}, 201, nil)
	h.json("POST", "/api/v1/admin/variables", admin, api.VariableCreate{Group: "a1", Name: "site", Value: "gva"}, 201, nil)
	h.svc.ScheduleControl = &scheduleFake{specs: map[string]provision.ScheduleSpec{}, paused: map[string]string{}}
	var schedule api.Schedule
	h.json("POST", "/api/v1/schedules", "tnguyen", hourly(cleanupItem(h), map[string]any{"target": "/tmp"}), 201, &schedule)

	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(hook.Close)
	h.allowWebhooks(hook.URL)
	var target api.SubscriptionTarget
	_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+hook.URL+`/rvav", "secret": "a1-token"}}`), &target)
	var sub api.Subscription
	h.json("POST", "/api/v1/admin/subscriptions", admin, api.SubscriptionWrite{Name: "Tell the CMDB", Topic: api.ProvisioningPost,
		Target: target}, 201, &sub)
	var run api.SubscriptionRunDetail
	h.json("POST", "/api/v1/admin/subscriptions/"+sub.Id+"/test", admin, nil, 200, &run)

	var plan api.OnboardingPlan
	h.json("POST", "/api/v1/admin/onboarding/plans", admin, api.OnboardingPlanWrite{Name: "Legacy ERP",
		Filter: api.OnboardingFilter{Q: new("erp-legacy")}, Target: api.OnboardingTarget{ProjectId: "prj_web", Environment: new("dev"),
			OwnerId: new("usr_tnguyen"), CatalogItemId: new(item.Id)}}, 201, &plan)
	var planRun api.OnboardingRun
	h.json("POST", "/api/v1/admin/onboarding/plans/"+plan.Id+"/runs", admin, nil, 202, &planRun)

	fake := httptest.NewServer(vra.NewFakeServer(vra.SampleEstate(time.Now())))
	t.Cleanup(fake.Close)
	h.svc.Config.VRA.URL, h.svc.Config.VRA.Token = fake.URL, vra.FakeRefreshToken
	h.json("POST", "/api/v1/admin/vra/snapshots", admin, nil, 201, nil)
	h.svc.Config.VCenter.Name, h.svc.Config.VCenter.URL = "vc01.corp.example", "https://vc01.corp.example/sdk"
	h.json("POST", "/api/v1/admin/nsx-managers", admin, api.NsxManagerWrite{Name: "nsx02.corp.example", Url: "https://nsx02.corp.example",
		Username: new("rvav"), Password: new("a1-nsx-password")}, 201, nil)

	// Off, so that what the walk audits queues nothing and its state holds still.
	var auditDest api.AuditDestination
	h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: "SIEM", Enabled: new(false),
		Categories: &[]api.AuditCategory{api.AuditCategoryCatalog}, Syslog: &api.AuditSyslog{Host: "siem.corp.example"}}, 201, &auditDest)

	return map[string]string{
		"drafts/{id}":                draft.Id,
		"views/{id}":                 view.Id,
		"projects/{projectId}":       "prj_web",
		"items/{itemId}":             "rhel9-server",
		"requests/{requestId}":       vm.h.firstRequest(vm.id),
		"steps/{stepId}":             steps.Items[logged].Id,
		"approvals/{approvalId}":     inbox.Items[0].Id,
		"deployments/{deploymentId}": vm.id,
		"resources/{resourceId}":     vm.resource,
		// A console link is a one-time token openConsole makes, which
		// neither auditor may call; the stream refuses any token but
		// through a WebSocket.
		"console/{token}":                "not-a-console-link",
		"actions/{key}":                  "awx.monitoring-agent",
		"day2-forms/{action}":            "awx.monitoring-agent",
		"versions/{versionId}":           item.PublishedVersion.Id,
		"categories/{key}":               "compute",
		"secrets/{name}":                 "a1-token",
		"property-groups/{key}":          "a1_group",
		"form-library/{key}":             "a1_check",
		"members/{userId}":               seed.UserID("tnguyen"),
		"roles/{key}":                    "vm-operator",
		"role-assignments/{id}":          deref(given.Id),
		"environments/{key}":             "dev",
		"placement-rules/{id}":           first("/api/v1/admin/placement-rules?project_id=prj_web").Items[0].Id,
		"network-mappings/{network}":     "dvpg-app-dev-vl310",
		"templates/{key}":                first("/api/v1/admin/templates").Items[0].Key,
		"sizes/{key}":                    first("/api/v1/admin/sizes").Items[0].Key,
		"naming-policies/{id}":           naming.Id,
		"lease-rules/{id}":               leaseRule.Id,
		"approval-policies/{id}":         first("/api/v1/admin/approval-policies").Items[0].Id,
		"day2-policies/{id}":             first("/api/v1/admin/day2-policies").Items[0].Id,
		"deployment-limit-policies/{id}": limits.Id,
		"integrations/{kind}":            "awx",
		"projects/{vraProjectId}":        "vra-prj-web",
		"vcenters/{name}":                "vc01.corp.example",
		"nsx-managers/{name}":            "nsx02.corp.example",
		"subscriptions/{id}":             sub.Id,
		"subscription-runs/{id}":         run.Id,
		"audit-destinations/{id}":        auditDest.Id,
		"workflows/{key}":                "a1-report",
		"resource-types/{key}":           "a1-lb",
		"variables/{key}":                "a1.site",
		"schedules/{scheduleId}":         schedule.Id,
		"signal/{name}":                  "a1-signal",
		"tokens/{id}":                    token.Id,
		"files/{fileId}":                 attached,
		"images/{imageId}":               picture,
		"notification-templates/{kind}":  "request_failed",
		"logos/{variant}":                "light",
		"plans/{planId}":                 plan.Id,
		"runs/{runId}":                   planRun.Id,
	}
}

// firstRequest is the request that built a deployment.
func (h *harness) firstRequest(deploymentID string) string {
	h.t.Helper()
	var id string
	if err := h.store.Pool.QueryRow(h.ctx, `select created_by_request_id from deployments where id = $1`, deploymentID).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

// a1Walk calls every operation of the spec as the built-in auditor and as
// the compliance auditor.
type a1Walk struct {
	t               *testing.T
	h               *harness
	builtin, custom a1Person
	ids             map[string]string
	draft           api.RequestDraft
}

// a1Op is one operation of the spec, its path filled with real objects.
type a1Op struct {
	method, template, path, id string
	open                       bool // no bearer: its own credential decides (a password, a webhook's signature)
}

func (o a1Op) String() string { return o.method + " " + o.template }

// Writes that change nothing of rvav's: anyone's about themselves, or a read
// sent as a POST. Each answers the two auditors the same.
var (
	a1Own = map[string]bool{
		"markNotificationsRead": true, "setNotificationPreferences": true, "testNotificationChannel": true, "setMyLocale": true,
		"addFavourite": true, "removeFavourite": true, "createApiToken": true, "changePassword": true, "signOut": true,
		"createSavedView": true,
	}
	a1Reads = map[string]bool{"resolveFieldValues": true, "previewRequest": true, "explainPolicies": true,
		"previewCatalogRequesters": true}
)

func (w a1Walk) operations() []a1Op {
	w.t.Helper()
	spec, err := api.GetSwagger()
	if err != nil {
		w.t.Fatal(err)
	}
	var ops []a1Op
	for _, template := range slices.Sorted(maps.Keys(spec.Paths.Map())) {
		item := spec.Paths.Value(template)
		for method, op := range item.Operations() {
			// The embedded spec has its operation IDs as the generated code's names.
			id := strings.ToLower(op.OperationID[:1]) + op.OperationID[1:]
			ops = append(ops, a1Op{method: method, template: template, id: id,
				path: "/api/v1" + w.fill(template, append(item.Parameters, op.Parameters...)),
				open: op.Security != nil && len(*op.Security) == 0})
		}
	}
	// What only those who see everything are shown.
	ops = append(ops, a1Op{method: http.MethodGet, template: "/approvals?view=all", path: "/api/v1/approvals?view=all", id: "listApprovals"},
		a1Op{method: http.MethodGet, template: "/tokens?all=true", path: "/api/v1/tokens?all=true", id: "listApiTokens"})
	slices.SortFunc(ops, func(a, b a1Op) int { return strings.Compare(a.String(), b.String()) })
	return ops
}

// fill puts a real object in each path parameter and a value in each
// required query parameter.
func (w a1Walk) fill(template string, params openapi3.Parameters) string {
	w.t.Helper()
	segments := strings.Split(template, "/")
	for i, s := range segments {
		if strings.HasPrefix(s, "{") {
			id, ok := w.ids[segments[i-1]+"/"+s]
			if !ok {
				w.t.Fatalf("%s: no real object for %s/%s", template, segments[i-1], s)
			}
			segments[i] = id
		}
	}
	values := map[string]string{"project_id": "prj_web", "user_id": seed.UserID("tnguyen"), "environment": "dev",
		"from": w.ids["versions/{versionId}"], "to": w.ids["versions/{versionId}"], "topics": "deployments",
		"version_id": w.ids["versions/{versionId}"], "field": "cluster", "name": "rules.csv"}
	var query []string
	for _, ref := range params {
		p := ref.Value
		if p.In != openapi3.ParameterInQuery || !p.Required {
			continue
		}
		v, ok := values[p.Name]
		if !ok && p.Schema != nil && len(p.Schema.Value.Enum) > 0 {
			v, ok = fmt.Sprint(p.Schema.Value.Enum[0]), true
		}
		if !ok {
			w.t.Fatalf("%s: no value for the query parameter %s", template, p.Name)
		}
		query = append(query, p.Name+"="+v)
	}
	path := strings.Join(segments, "/")
	if len(query) > 0 {
		path += "?" + strings.Join(query, "&")
	}
	return path
}

// call sends one operation as a user, with a body for a write.
func (w a1Walk) call(o a1Op, user string, body any) (int, string) {
	w.t.Helper()
	if o.id == "streamEvents" {
		return w.stream(o.path, user), ""
	}
	if body == nil && o.method != http.MethodGet && o.method != http.MethodDelete {
		body = map[string]any{}
	}
	code, b, _ := w.h.do(o.method, o.path, user, body, "Idempotency-Key", uuid.NewString())
	return code, w.anonymous(a1Steady(b))
}

var a1Time = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)$`)

// a1Steady is an answer without what differs between two calls made a moment
// apart: the request's own id, and times taken from the clock.
func a1Steady(body []byte) string {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return string(body)
	}
	var steady func(any) any
	steady = func(v any) any {
		switch v := v.(type) {
		case map[string]any:
			delete(v, "request_id")
			for k, x := range v {
				v[k] = steady(x)
			}
		case []any:
			for i, x := range v {
				v[i] = steady(x)
			}
		case string:
			if a1Time.MatchString(v) {
				return "<time>"
			}
		}
		return v
	}
	b, _ := json.Marshal(steady(v))
	return string(b)
}

// stream opens the live events and keeps only the status: a stream has no end to compare.
func (w a1Walk) stream(path, user string) int {
	ctx, cancel := context.WithTimeout(w.h.ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, w.h.srv.URL+path, nil)
	req.Header.Set(auth.DevUserHeader, user)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatalf("GET %s: %v", path, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// anonymous puts the same words in both auditors' answers where they name themselves.
func (w a1Walk) anonymous(s string) string {
	for _, p := range []a1Person{w.builtin, w.custom} {
		s = strings.NewReplacer(p.id(), "<auditor's id>", p.name, "<auditor's name>", p.email(), "<auditor's email>").Replace(s)
	}
	return s
}

// compare walks every operation: reads first, then the writes, the reads
// again to show nothing changed, then each one's own.
func (w a1Walk) compare() {
	t := w.t
	ops := w.operations()
	reads := map[string]string{}
	var gets, seen, writes, own int
	for _, o := range ops {
		if o.method != http.MethodGet {
			continue
		}
		gets++
		code, b := w.call(o, w.builtin.login, nil)
		cc, cb := w.call(o, w.custom.login, nil)
		if o.id == "getMe" {
			b, cb = w.withoutRoles(b, "auditor"), w.withoutRoles(cb, "compliance-auditor")
		}
		if code != cc || b != cb {
			t.Errorf("%s: the built-in auditor gets %d %s, the compliance auditor %d %s", o, code, b, cc, cb)
		}
		if cc == http.StatusOK {
			seen++
		}
		if slices.Contains([]string{"/approvals?view=all", "/audit", "/admin/roles", "/admin/day2-policies", "/admin/projects/{projectId}/members"}, o.template) &&
			cc != http.StatusOK {
			t.Errorf("%s: the compliance auditor gets %d %s", o, cc, cb)
		}
		if code == http.StatusNotFound && o.id != "getRequestDraft" {
			t.Errorf("%s: 404 for an auditor, who sees everything: %s", o, b)
		}
		reads[o.String()] = b
	}
	for _, o := range ops {
		if o.method == http.MethodGet || a1Own[o.id] || a1Reads[o.id] {
			continue
		}
		writes++
		code, b := w.call(o, w.builtin.login, w.body(o))
		cc, cb := w.call(o, w.custom.login, w.body(o))
		if code != cc || b != cb {
			t.Errorf("%s: the built-in auditor gets %d %s, the compliance auditor %d %s", o, code, b, cc, cb)
		}
		switch {
		case o.open:
			if cc < 400 {
				t.Errorf("%s with no credential of its own = %d %s", o, cc, cb)
			}
		case cc == http.StatusNotFound && slices.Contains([]string{"updateRequestDraft", "deleteRequestDraft", "revokeApiToken",
			"updateSavedView", "deleteSavedView"}, o.id):
			// Someone else's draft, token or view: an auditor doesn't see them.
		case cc == http.StatusUnprocessableEntity && slices.Contains([]string{"createRequestDraft", "updateRequestDraft"}, o.id) &&
			strings.Contains(cb, `"field":"/draft/project_id"`) && strings.Count(cb, `"field"`) == 1:
			// A draft is refused, as the request would be, for a project one doesn't request in.
		case cc != http.StatusForbidden:
			t.Errorf("%s: the compliance auditor gets %d %s, want 403", o, cc, cb)
		}
	}
	for _, o := range ops {
		if o.method != http.MethodGet {
			continue
		}
		code, b := w.call(o, w.builtin.login, nil)
		switch o.id {
		case "getMe":
			b = w.withoutRoles(b, "auditor")
		case "listAuditEvents":
			b = w.withoutFailedSignIns(b)
			reads[o.String()] = w.withoutFailedSignIns(reads[o.String()])
		}
		if b != reads[o.String()] {
			t.Errorf("%s changed after the writes were refused: %d %s, was %s", o, code, b, reads[o.String()])
		}
	}
	for _, o := range ops {
		if !a1Own[o.id] && !a1Reads[o.id] {
			continue
		}
		own++
		code, b := w.call(o, w.builtin.login, w.body(o))
		cc, cb := w.call(o, w.custom.login, w.body(o))
		switch o.id {
		case "createApiToken":
			b, cb = w.without(b, "id", "token", "created_at"), w.without(cb, "id", "token", "created_at")
		case "createSavedView":
			b, cb = w.without(b, "id", "created_at", "updated_at"), w.without(cb, "id", "created_at", "updated_at")
		case "explainPolicies":
			// Day-2 policies may name a custom role (ADR-0034), so the
			// explanation lists it among the caller's roles here; they
			// name no built-in role but admin's.
			cb = strings.ReplaceAll(cb, ", compliance-auditor.", ".")
		}
		if code != cc || b != cb {
			t.Errorf("%s: the built-in auditor gets %d %s, the compliance auditor %d %s", o, code, b, cc, cb)
		}
	}
	t.Logf("walked %d operations: %d reads (%d seen), %d writes refused, %d of one's own or reads sent as a POST", len(ops), gets, seen, writes, own)
}

// body is a valid write where the API checks the body before the caller.
func (w a1Walk) body(o a1Op) any {
	switch o.id {
	case "markNotificationsRead":
		return api.NotificationsRead{All: new(true)}
	case "setNotificationPreferences":
		return api.NotificationPreferencesWrite{Locale: "en", Rules: []api.NotificationRule{}}
	case "setMyLocale":
		return api.MyLocale{Locale: "en"}
	case "testNotificationChannel":
		return map[string]any{"channel": "email"}
	case "createApiToken":
		return api.ApiTokenCreate{Name: "My reports"}
	case "resolveFieldValues":
		return api.FieldValuesQuery{Field: "cluster", Inputs: map[string]any{}, ProjectId: "prj_web", VersionId: w.ids["versions/{versionId}"],
			Environment: new(api.EnvironmentKey("dev"))}
	case "explainPolicies":
		return api.PolicyExplainQuery{Environment: "dev", ProjectId: "prj_web", Action: new("power_off")}
	case "createRequest":
		return w.draft
	case "createSchedule":
		return api.ScheduleWrite{Name: "A1", CatalogItemId: w.draft.CatalogItemId, VersionId: w.draft.VersionId, ProjectId: w.draft.ProjectId,
			Environment: w.draft.Environment, Inputs: w.draft.Inputs, EveryMinutes: new(60)}
	case "signalRequest":
		return map[string]any{"by": "A1"}
	case "createRequestDraft", "updateRequestDraft":
		return api.SavedDraftWrite{Draft: w.draft}
	case "createSavedView", "updateSavedView":
		return api.SavedViewWrite{List: api.ViewListDeployments, Name: "Expired", Query: map[string]string{"status": "expired"}}
	case "runDeploymentAction":
		return api.Day2Draft{Action: "power_off", ResourceId: new(w.ids["resources/{resourceId}"]), Inputs: map[string]any{}}
	case "previewDeploymentAction":
		return api.Day2FormQuery{Action: "power_off", ResourceId: new(w.ids["resources/{resourceId}"]), Inputs: map[string]any{}}
	case "resolveDeploymentActionValues":
		return api.Day2FieldValuesQuery{Action: "power_off", ResourceId: new(w.ids["resources/{resourceId}"]), Field: "reason",
			Inputs: map[string]any{}}
	case "saveDay2Form":
		var def api.Day2FormDefinition
		def.FormSchema, def.InitialValues = map[string]any{"type": "object"}, map[string]any{}
		def.UiSchema.Fields, def.UiSchema.Layout.Kind, def.UiSchema.Layout.Sections = map[string]api.UiField{}, api.Page, []api.UiSection{}
		return api.Day2FormWrite{Definition: def}
	case "previewDay2Form":
		return api.Day2FormDesignerQuery{DeploymentId: w.ids["deployments/{deploymentId}"], Inputs: map[string]any{}}
	case "resolveDay2FormValues":
		return api.Day2FormDesignerValuesQuery{DeploymentId: w.ids["deployments/{deploymentId}"], Field: "reason", Inputs: map[string]any{}}
	}
	return nil
}

func (w a1Walk) withoutRoles(body, key string) string {
	w.t.Helper()
	var me map[string]any
	if err := json.Unmarshal([]byte(body), &me); err != nil {
		w.t.Fatalf("/me: %v: %s", err, body)
	}
	roles, _ := json.Marshal(me["roles"])
	var refs []api.RoleRef
	_ = json.Unmarshal(roles, &refs)
	if !slices.Equal(roleKeys(refs), []string{"user", key}) {
		w.t.Errorf("/me's roles = %s, want %s", roles, key)
	}
	delete(me, "roles")
	b, _ := json.Marshal(me)
	return string(b)
}

// withoutFailedSignIns is the audit log without the refused sign-ins the
// walk itself makes: the log records them, as it should.
func (w a1Walk) withoutFailedSignIns(body string) string {
	var log map[string]any
	if json.Unmarshal([]byte(body), &log) != nil {
		return body
	}
	items, _ := log["items"].([]any)
	log["items"] = slices.DeleteFunc(items, func(e any) bool {
		m, _ := e.(map[string]any)
		return m["action"] == "auth.sign_in_failed"
	})
	b, _ := json.Marshal(log)
	return string(b)
}

// without is a JSON object without the keys that differ each time it is made.
func (w a1Walk) without(body string, keys ...string) string {
	var made map[string]any
	if json.Unmarshal([]byte(body), &made) != nil {
		return body
	}
	for _, k := range keys {
		delete(made, k)
	}
	b, _ := json.Marshal(made)
	return string(b)
}

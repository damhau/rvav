package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/seed"
)

// TestWritesWithoutTheirRead: a change allowed by its own permission
// answers what was changed, whether or not its holder may read it
// (ADR-0034): never a refusal after the change was made. A project is
// changed by its code as by its ID, and the setup checklist, read and
// changed with system.manage, needs nothing else.
func TestWritesWithoutTheirRead(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, user = "mkeller", "cdubois"
		hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}))
		defer hook.Close()
		h.allowWebhooks(hook.URL)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "w-hook", Kind: api.SecretWriteKindToken,
			Value: new("hook-secret")}, 201, nil)
		var target api.SubscriptionTarget
		_ = json.Unmarshal([]byte(`{"webhook": {"url": "`+hook.URL+`/rvav", "secret": "w-hook"}}`), &target)
		var sub api.Subscription
		var workflow api.WorkflowRegistration
		registration := api.WorkflowRegistrationWrite{Key: new("w-flow"), Title: "Decommission", WorkflowType: "decommission.v1",
			TaskQueue: "platform-workflows", Usages: []api.WorkflowUsage{api.WorkflowUsageCatalogItem}}

		quotas := []api.QuotaLimit{{Environment: "", Resource: api.Vcpu, Limit: new(200), Enforcement: api.QuotaLimitEnforcementHard}}
		cases := []struct {
			perms        []string
			project      string // "" for all of rvav
			method, path string
			body         any
			want         int
			out          any
		}{
			{[]string{"project.create"}, "", "POST", "/api/v1/admin/projects",
				api.AdminProjectCreate{Code: "wone", Name: "W one", Environments: []string{"dev"}}, 201, &api.AdminProject{}},
			{[]string{"project.create"}, "", "POST", "/api/v1/admin/projects",
				api.AdminProjectCreate{Code: "wtwo", Name: "W two", Environments: []string{"dev"},
					ProjectAdmins: &[]string{seed.UserID(user)}}, 201, &api.AdminProject{}},
			{[]string{"project.see", "project.update"}, "prj_web", "PATCH", "/api/v1/admin/projects/prj_web",
				api.AdminProjectUpdate{Name: new("Web 2")}, 200, &api.AdminProject{}},
			{[]string{"project.see", "project.quotas.manage"}, "prj_web", "PUT", "/api/v1/admin/projects/prj_web/quotas",
				api.QuotaLimitList{Items: quotas}, 200, &api.QuotaLimitList{}},
			{[]string{"access.manage"}, "", "POST", "/api/v1/admin/roles",
				api.RoleCreate{Key: "w-made", Name: "Made", Permissions: []string{"policies.read"}}, 201, &api.Role{}},
			{[]string{"access.manage"}, "", "PATCH", "/api/v1/admin/roles/w-made",
				api.RoleUpdate{Name: new("Made again")}, 200, &api.Role{}},
			{[]string{"infrastructure.manage"}, "", "POST", "/api/v1/admin/templates",
				api.VmTemplateWrite{Key: "w-tpl", Label: "W template", VcenterTemplate: "tpl-w", OsFamily: api.VmTemplateWriteOsFamilyLinux},
				201, &api.VmTemplate{}},
			{[]string{"infrastructure.manage"}, "", "PUT", "/api/v1/admin/templates/w-tpl",
				api.VmTemplateWrite{Key: "w-tpl", Label: "W template 2", VcenterTemplate: "tpl-w", OsFamily: api.VmTemplateWriteOsFamilyLinux},
				200, &api.VmTemplate{}},
			{[]string{"infrastructure.manage"}, "", "POST", "/api/v1/admin/sizes",
				api.SizePresetWrite{Key: "w-size", Label: "W size", Vcpu: 2, MemoryGb: 4, DiskGb: 40, Offers: &[]api.Offer{}},
				201, &api.SizePreset{}},
			{[]string{"infrastructure.manage"}, "", "PUT", "/api/v1/admin/sizes/w-size",
				api.SizePresetWrite{Key: "w-size", Label: "W size 2", Vcpu: 2, MemoryGb: 4, DiskGb: 40, Offers: &[]api.Offer{}},
				200, &api.SizePreset{}},
			{[]string{"extensibility.manage"}, "", "POST", "/api/v1/admin/subscriptions",
				api.SubscriptionWrite{Name: "W hook", Topic: api.ProvisioningPost, Target: target}, 201, &sub},
			{[]string{"extensibility.manage"}, "", "PUT", "/api/v1/admin/subscriptions/{sub}",
				api.SubscriptionWrite{Name: "W hook 2", Topic: api.ProvisioningPost, Target: target}, 200, &api.Subscription{}},
			{[]string{"extensibility.manage"}, "", "POST", "/api/v1/admin/subscriptions/{sub}/test", nil, 200, &api.SubscriptionRunDetail{}},
			{[]string{"extensibility.manage"}, "", "POST", "/api/v1/admin/workflows", registration, 201, &workflow},
			{[]string{"extensibility.manage"}, "", "PUT", "/api/v1/admin/workflows/w-flow",
				api.WorkflowRegistrationWrite{Title: "Decommission 2", WorkflowType: "decommission.v1", TaskQueue: "platform-workflows",
					Usages: []api.WorkflowUsage{api.WorkflowUsageCatalogItem}}, 200, &api.WorkflowRegistration{}},
			{[]string{"project.see", "project.update"}, "prj_web", "PATCH", "/api/v1/admin/projects/web",
				api.AdminProjectUpdate{Name: new("Web 3")}, 200, &api.AdminProject{}},
			{[]string{"project.see", "project.quotas.manage"}, "prj_web", "PUT", "/api/v1/admin/projects/web/quotas",
				api.QuotaLimitList{Items: quotas}, 200, &api.QuotaLimitList{}},
			{[]string{"system.manage"}, "", "GET", "/api/v1/admin/setup", nil, 200, &api.Setup{}},
			{[]string{"system.manage"}, "", "PUT", "/api/v1/admin/setup", api.SetupWrite{Hidden: true}, 200, &api.Setup{}},
		}
		for i, tc := range cases {
			key := fmt.Sprintf("w-role-%d", i)
			h.json("POST", "/api/v1/admin/roles", admin, api.RoleCreate{Key: key, Name: key, Permissions: tc.perms}, 201, nil)
			var given api.RoleAssignment
			if tc.project == "" {
				h.json("POST", "/api/v1/admin/role-assignments", admin, api.RoleAssignmentWrite{UserId: seed.UserID(user), Role: key}, 201, &given)
			} else {
				h.json("PUT", "/api/v1/projects/"+tc.project+"/members/"+seed.UserID(user), admin,
					api.ProjectMemberWrite{Roles: &[]string{key}}, 200, nil)
			}
			path := strings.ReplaceAll(tc.path, "{sub}", sub.Id)
			if code, b, _ := h.do(tc.method, path, user, tc.body); code != tc.want {
				t.Errorf("%s %s holding %v = %d, want %d: %s", tc.method, path, tc.perms, code, tc.want, b)
			} else if err := json.Unmarshal(b, tc.out); err != nil {
				t.Errorf("%s %s: %v", tc.method, path, err)
			}
			if tc.project == "" {
				h.json("DELETE", "/api/v1/admin/role-assignments/"+deref(given.Id), admin, nil, 204, nil)
			} else {
				h.json("DELETE", "/api/v1/projects/"+tc.project+"/members/"+seed.UserID(user), admin, nil, 204, nil)
			}
		}

		if t.Failed() {
			return
		}
		// Each answer is what was written.
		created, named := cases[0].out.(*api.AdminProject), cases[1].out.(*api.AdminProject)
		if created.Code != "wone" || !slices.Equal(*created.MyPermissions, []string{"project.create"}) {
			t.Errorf("created project = %+v", created)
		}
		if named.Code != "wtwo" || !slices.Contains(*named.MyPermissions, "project.members.manage") {
			t.Errorf("a project made naming its creator an admin = %+v, my permissions %v", named, *named.MyPermissions)
		}
		if p := cases[2].out.(*api.AdminProject); p.Name != "Web 2" {
			t.Errorf("updated project = %+v", p)
		}
		if q := cases[3].out.(*api.QuotaLimitList); len(q.Items) != 1 || q.Items[0].Resource != api.Vcpu || *q.Items[0].Limit != 200 {
			t.Errorf("quotas = %+v", q.Items)
		}
		if r := cases[4].out.(*api.Role); r.Key != "w-made" || !slices.Equal(r.Permissions, []string{"policies.read"}) {
			t.Errorf("made role = %+v", r)
		}
		if r := cases[5].out.(*api.Role); r.Name != "Made again" {
			t.Errorf("changed role = %+v", r)
		}
		if x := cases[6].out.(*api.VmTemplate); x.Key != "w-tpl" || x.Label != "W template" {
			t.Errorf("made template = %+v", x)
		}
		if x := cases[7].out.(*api.VmTemplate); x.Label != "W template 2" {
			t.Errorf("changed template = %+v", x)
		}
		if x := cases[8].out.(*api.SizePreset); x.Key != "w-size" || x.Vcpu != 2 {
			t.Errorf("made size = %+v", x)
		}
		if x := cases[9].out.(*api.SizePreset); x.Label != "W size 2" {
			t.Errorf("changed size = %+v", x)
		}
		if sub.Name != "W hook" {
			t.Errorf("made subscription = %+v", sub)
		}
		if x := cases[11].out.(*api.Subscription); x.Id != sub.Id || x.Name != "W hook 2" {
			t.Errorf("changed subscription = %+v", x)
		}
		if x := cases[12].out.(*api.SubscriptionRunDetail); !x.Test || x.SubscriptionId != sub.Id || len(x.History) != 1 {
			t.Errorf("test run = %+v", x)
		}
		if workflow.Key != "w-flow" {
			t.Errorf("registered workflow = %+v", workflow)
		}
		if x := cases[14].out.(*api.WorkflowRegistration); x.Title != "Decommission 2" {
			t.Errorf("changed workflow = %+v", x)
		}
		if p := cases[15].out.(*api.AdminProject); p.Id != "prj_web" || p.Name != "Web 3" {
			t.Errorf("project updated by its code = %+v", p)
		}
		if q := cases[16].out.(*api.QuotaLimitList); len(q.Items) != 1 {
			t.Errorf("quotas set by the project's code = %+v", q.Items)
		}
		if st := cases[17].out.(*api.Setup); len(st.Steps) != 8 {
			t.Errorf("setup checklist = %+v", st)
		}
		if st := cases[18].out.(*api.Setup); !st.Hidden || len(st.Steps) != 8 {
			t.Errorf("setup checklist hidden = %+v", st)
		}
	})
}

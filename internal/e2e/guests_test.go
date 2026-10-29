package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"go.temporal.io/sdk/testsuite"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/provision"
)

// guestItem is a single-VM item with its guest from the blueprint.
func guestItem(template string, guest map[string]any) api.CatalogVersionDefinition {
	fields := []string{"role", "cluster", "datastore", "network"}
	props := map[string]any{}
	ui := map[string]api.UiField{}
	for _, f := range fields {
		props[f] = map[string]any{"type": "string", "minLength": 1}
		ui[f] = api.UiField{Widget: api.UiWidgetText, Label: f}
	}
	def := api.CatalogVersionDefinition{
		FormSchema: map[string]any{"type": "object", "required": fields, "properties": props},
		UiSchema:   api.UiSchema{Fields: ui}, InitialValues: map[string]any{},
		Blueprint: map[string]any{"kind": "vm", "naming": "{project}-{environment}-{inputs.role}{seq:02}", "template": template,
			"guest": guest, "bindings": map[string]any{"vcpu": "2", "memory_gb": "4", "disk_gb": "60",
				"cluster": "inputs.cluster", "datastore": "inputs.datastore", "network": "inputs.network"}},
	}
	def.UiSchema.Layout.Kind = api.Page
	def.UiSchema.Layout.Sections = []api.UiSection{{Key: "main", Title: "VM", Fields: fields}}
	return def
}

// TestGuests: a Windows VM joins the domain with the account of a stored
// secret and gets its local administrator's password from another, on a
// vCenter specification; a Linux VM gets its name, address and user data
// through guestinfo for cloud-init. Secrets stay out of the plan, the
// timeline and the logs (S12.4, ADR-0024).
func TestGuests(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "ad-join", Kind: api.SecretWriteKindBasic,
			Username: present.Ptr("svc-join@corp.example"), Value: present.Ptr("J0in-S3cret!")}, 201, nil)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "win-admin", Kind: api.SecretWriteKindToken,
			Value: present.Ptr("L0cal-S3cret!")}, 201, nil)

		publish := func(slug string, def api.CatalogVersionDefinition, want int) api.AdminCatalogItem {
			t.Helper()
			var item api.AdminCatalogItem
			h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: slug, Name: slug, Summary: slug,
				Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
			h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", admin, nil, want, nil)
			h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
			return item
		}
		request := func(item api.AdminCatalogItem, role string) api.Request {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": role, "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}, 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
			return req
		}
		steps := func(id string) map[string]api.Step {
			var list struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+id+"/steps", "tnguyen", nil, 200, &list)
			out := map[string]api.Step{}
			for _, s := range list.Items {
				out[s.Key] = s
			}
			return out
		}
		vmOf := func(id string) mo.VirtualMachine {
			t.Helper()
			var moref string
			_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where request_id = $1 and type = 'vsphere.vm'`, id).Scan(&moref)
			var vm mo.VirtualMachine
			if err := property.DefaultCollector(c).RetrieveOne(ctx, types.ManagedObjectReference{Type: "VirtualMachine", Value: moref},
				[]string{"name", "guest", "config.extraConfig"}, &vm); err != nil {
				t.Fatal(err)
			}
			return vm
		}

		// A size the bindings can't work out (no sizes here) is refused,
		// rather than a VM of the template's size.
		var problem struct{ Errors []api.FieldError }
		sizeless := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		sizeless.Blueprint["bindings"].(map[string]any)["vcpu"] = "sizes[inputs.role].vcpu"
		odd := publish("sizeless", sizeless, 200)
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: odd.Id, VersionId: odd.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "odd", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}, 422, &problem, "Idempotency-Key", uuid.NewString())
		if len(problem.Errors) != 1 || deref(problem.Errors[0].Code) != "invalid_size" {
			t.Fatalf("a size not worked out = %+v", problem.Errors)
		}

		// A tag that can't be attached (its category is missing) fails the
		// clone after the VM was made: it is recorded, and rolled back.
		tagged := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		tagged.Blueprint["tags"], tagged.Blueprint["rollback_on_failure"] = []any{"nosuchcategory/x"}, true
		tagItem := publish("tagged", tagged, 200)
		if req := request(tagItem, "tag"); req.Status != api.RequestStatusFailed || !strings.Contains(errorOf(req), "nosuchcategory") {
			t.Fatalf("a missing tag category = %s %s", req.Status, errorOf(req))
		}
		if vmExists(ctx, c, "web-dev-tag01") {
			t.Fatal("the VM whose tags failed is still in vCenter")
		}

		// A secret that doesn't exist, or of the wrong kind, is refused.
		bad := guestItem("tpl-win2025-std-2026.09", map[string]any{"os_family": "windows", "domain": "corp.example",
			"domain_join": map[string]any{"domain": "corp.example", "credential": "win-admin"}, "admin_password": "nope"})
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "win-bad", Name: "w", Summary: "w",
			Category: "compute", Type: api.VmBlueprint, Definition: &bad}, 422, &problem)
		if got := fieldsOf(problem.Errors); len(got) != 2 || !strings.HasSuffix(got[0], "/guest/admin_password") ||
			!strings.HasSuffix(got[1], "/guest/domain_join/credential") {
			t.Fatalf("bad secrets = %v", problem.Errors)
		}

		// Windows, joined to the domain, on vcsim's specification.
		win := publish("win-joined", guestItem("tpl-win2025-std-2026.09", map[string]any{"os_family": "windows", "domain": "corp.example",
			"customization_spec": "vcsim-windows-static", "admin_password": "win-admin",
			"domain_join": map[string]any{"domain": "corp.example", "ou": "OU=Servers,DC=corp,DC=example", "credential": "ad-join"}}), 200)
		req := request(win, "ad")
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("Windows request = %s %s", req.Status, errorOf(req))
		}
		st := steps(req.Id)["vsphere.customize"]
		if st.Status != api.StepStatusDone || !strings.Contains(deref(st.Detail), "joins corp.example in OU=Servers,DC=corp,DC=example") ||
			!strings.Contains(deref(st.Detail), "from specification vcsim-windows-static") {
			t.Fatalf("customize step = %s %q", st.Status, deref(st.Detail))
		}
		if vm := vmOf(req.Id); vm.Guest == nil || vm.Guest.HostName != "web-dev-ad01" {
			t.Fatalf("guest host name = %+v", vm.Guest)
		}
		var leaked int
		_ = h.store.Pool.QueryRow(ctx, `select (select count(*) from requests where plan::text like '%S3cret%')
			+ (select count(*) from request_steps where detail like '%S3cret%' or title like '%S3cret%' or coalesce(error, '') like '%S3cret%')
			+ (select count(*) from step_log_lines where text like '%S3cret%')`).Scan(&leaked)
		if leaked != 0 {
			t.Fatalf("a secret is in %d plans, steps or logs", leaked)
		}

		// Linux by cloud-init: its metadata and user data in guestinfo.
		// A secret input reaches the user data, and only it (S14.6).
		ciDef := guestItem("tpl-ubuntu24.04-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example",
			"cloud_init": map[string]any{"user_data": "#cloud-config\nfqdn: {{ .vm.fqdn }}\nfinal_message: {{ printf \"%s for %s\" .inputs.role .request.number | json }}\npassword: {{ .inputs.admin_password | json }}"}})
		ciDef.FormSchema["properties"].(map[string]any)["admin_password"] = map[string]any{"type": "string"}
		ciDef.UiSchema.Fields["admin_password"] = api.UiField{Widget: api.UiWidgetSecret, Label: "Admin password"}
		ciDef.UiSchema.Layout.Sections[0].Fields = append(ciDef.UiSchema.Layout.Sections[0].Fields, "admin_password")
		ubuntu := publish("ubuntu-ci", ciDef, 200)
		// vcsim has no cloud-init to set the guest's address: the test does,
		// as the guest would once it booted.
		var ci api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: ubuntu.Id, VersionId: ubuntu.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "ci", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "admin_password": "Cl0ud-pw"}},
			202, &ci, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(ci.Id, cloudInitGuest(h, c))
		h.json("GET", "/api/v1/requests/"+ci.Id, "tnguyen", nil, 200, &ci)
		if ci.Status != api.RequestStatusSucceeded {
			t.Fatalf("cloud-init request = %s %s", ci.Status, errorOf(ci))
		}
		if st := steps(ci.Id)["vsphere.customize"]; st.Title != "Wrote cloud-init metadata and user data" {
			t.Fatalf("customize step = %q", st.Title)
		}
		info := map[string]string{}
		for _, o := range vmOf(ci.Id).Config.ExtraConfig {
			ov := o.GetOptionValue()
			if s, ok := ov.Value.(string); ok {
				info[ov.Key] = s
			}
		}
		user, _ := base64.StdEncoding.DecodeString(info["guestinfo.userdata"])
		rawMeta, _ := base64.StdEncoding.DecodeString(info["guestinfo.metadata"])
		var meta struct {
			Local   string `json:"local-hostname"`
			Network struct {
				Ethernets map[string]struct{ Addresses []string }
			}
		}
		_ = json.Unmarshal(rawMeta, &meta)
		var ip string
		_ = h.store.Pool.QueryRow(ctx, `select name from resources where request_id = $1 and type = 'ipam.address'`, ci.Id).Scan(&ip)
		if string(user) != "#cloud-config\nfqdn: web-dev-ci01.corp.example\nfinal_message: \"ci for "+ci.Number+"\"\npassword: \"Cl0ud-pw\"" ||
			meta.Local != "web-dev-ci01" || len(meta.Network.Ethernets["nic0"].Addresses) != 1 ||
			meta.Network.Ethernets["nic0"].Addresses[0] != ip+"/24" {
			t.Fatalf("user data %q, metadata %s (address %s)", user, rawMeta, ip)
		}
		var inPlan int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from requests where id = $1 and (inputs::text like '%Cl0ud-pw%' or plan::text like '%Cl0ud-pw%')`,
			ci.Id).Scan(&inPlan)
		if inPlan != 0 {
			t.Fatal("the secret input is in the request's inputs or plan")
		}
	})
}

// cloudInitGuest stands in for cloud-init in vcsim: once a VM is powered
// on, its guest address is set (vcsim sets guest.ipAddress from the extra
// config key SET.guest.ipAddress), as the guest would from the metadata.
func cloudInitGuest(h *harness, c *vim25.Client) func(env *testsuite.TestWorkflowEnvironment) {
	return func(env *testsuite.TestWorkflowEnvironment) {
		env.OnActivity(h.acts.PowerOn, mock.Anything, mock.Anything).Return(func(ctx context.Context, in provision.VMInput) error {
			vm := object.NewVirtualMachine(c, types.ManagedObjectReference{Type: "VirtualMachine", Value: in.Moref})
			if task, err := vm.PowerOn(ctx); err != nil {
				return err
			} else if err := task.Wait(ctx); err != nil {
				return err
			}
			task, err := vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{
				ExtraConfig: []types.BaseOptionValue{&types.OptionValue{Key: "SET.guest.ipAddress", Value: in.IP}}})
			if err == nil {
				err = task.Wait(ctx)
			}
			if err != nil {
				return err
			}
			return h.acts.PowerOn(ctx, in)
		})
	}
}

package e2e

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/secrets"
)

// TestProjectSecrets: a project's own secret shadows the global one of the
// same name for its requests only; one only a project has can't be opened
// by another project's request; a project's own may go while a global one
// remains; key rotation reseals a project's under its scope (T1.5,
// ADR-0038).
func TestProjectSecrets(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		put := func(name, project, value string) {
			t.Helper()
			w := api.SecretWrite{Name: name, Kind: api.SecretWriteKindToken, Value: present.Ptr(value)}
			if project != "" {
				w.ProjectId = &project
			}
			h.json("POST", "/api/v1/admin/secrets", admin, w, 201, nil)
		}
		put("ci-token", "", "global-value")
		put("ci-token", "prj_web", "web-value")
		put("web-only", "prj_web", "web-only-value")
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "ci-token", Kind: api.SecretWriteKindToken,
			ProjectId: present.Ptr("prj_web"), Value: present.Ptr("again")}, 409, nil)
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "x-token", Kind: api.SecretWriteKindToken,
			ProjectId: present.Ptr("prj_nope"), Value: present.Ptr("v")}, 422, &problem)
		if f := fieldsOf(problem.Errors); len(f) != 1 || f[0] != "/project_id" {
			t.Fatalf("an unknown project = %+v", problem.Errors)
		}

		var list api.SecretList
		h.json("GET", "/api/v1/admin/secrets", admin, nil, 200, &list)
		var scopes []string
		for _, s := range list.Items {
			scopes = append(scopes, s.Name+"@"+deref(s.ProjectId))
		}
		if strings.Join(scopes, ",") != "ci-token@,ci-token@prj_web,web-only@prj_web" {
			t.Fatalf("secrets = %v", scopes)
		}

		ud := "#cloud-config\nwrite_files:\n  - path: /etc/app/token\n    content: {{ secret \"ci-token\" | json }}"
		item := publishItem(h, "ci-scoped", guestItem("tpl-ubuntu24.04-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example",
			"cloud_init": map[string]any{"user_data": ud}}))
		userData := func(requestID string) string {
			t.Helper()
			var name string
			if err := h.store.Pool.QueryRow(ctx, `select name from resources where request_id = $1 and type = 'vsphere.vm'`, requestID).Scan(&name); err != nil {
				t.Fatal(err)
			}
			var vm mo.VirtualMachine
			if err := property.DefaultCollector(c).RetrieveOne(ctx, vmRefByName(t, ctx, c, name), []string{"config.extraConfig"}, &vm); err != nil {
				t.Fatal(err)
			}
			for _, o := range vm.Config.ExtraConfig {
				if ov := o.GetOptionValue(); ov.Key == "guestinfo.userdata" {
					b, _ := base64.StdEncoding.DecodeString(ov.Value.(string))
					return string(b)
				}
			}
			return ""
		}
		request := func(user, project, network string, itemID, versionID string, want int) api.Request {
			t.Helper()
			var req api.Request
			var out any = &req
			if want != 202 {
				out = &problem
			}
			h.json("POST", "/api/v1/requests", user, api.RequestDraft{CatalogItemId: itemID, VersionId: versionID,
				ProjectId: project, Environment: "dev", Inputs: map[string]any{"role": "tok", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": network}}, want, out, "Idempotency-Key", uuid.NewString())
			return req
		}

		// prj_web's requests open its own; prj_data's the global one.
		web := request("tnguyen", "prj_web", "dvpg-app-dev-vl310", item.Id, item.DraftVersion.Id, 202)
		h.runWorkflow(web.Id, cloudInitGuest(h, c))
		h.waitStatus(web.Id, "tnguyen", api.RequestStatusSucceeded)
		if got := userData(web.Id); !strings.Contains(got, `content: "web-value"`) {
			t.Fatalf("prj_web's user data = %q", got)
		}
		data := request("sbrunner", "prj_data", "dvpg-db-dev-vl320", item.Id, item.DraftVersion.Id, 202)
		h.runWorkflow(data.Id, cloudInitGuest(h, c))
		h.waitStatus(data.Id, "sbrunner", api.RequestStatusSucceeded)
		if got := userData(data.Id); !strings.Contains(got, `content: "global-value"`) {
			t.Fatalf("prj_data's user data = %q", got)
		}

		// A secret only prj_web has: prj_data can't request what opens it.
		only := publishItem(h, "ci-web-only", guestItem("tpl-ubuntu24.04-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example",
			"cloud_init": map[string]any{"user_data": "#cloud-config\na: {{ secret \"web-only\" | json }}"}}))
		request("sbrunner", "prj_data", "dvpg-db-dev-vl320", only.Id, only.DraftVersion.Id, 422)
		if len(problem.Errors) != 1 || !strings.Contains(problem.Errors[0].Message, `opens the secret "web-only", which`) {
			t.Fatalf("prj_data opening prj_web's secret = %+v", problem.Errors)
		}

		// prj_web's ci-token may go while the global one remains; then the
		// global one may not, an item using it.
		h.json("DELETE", "/api/v1/admin/secrets/ci-token?project_id=prj_web", admin, nil, 204, nil)
		h.json("DELETE", "/api/v1/admin/secrets/ci-token", admin, nil, 409, nil)
		h.json("DELETE", "/api/v1/admin/secrets/web-only", admin, nil, 404, nil)
		h.json("PUT", "/api/v1/admin/secrets/web-only?project_id=prj_web", admin, api.SecretWrite{Name: "web-only",
			Kind: api.SecretWriteKindToken, Value: present.Ptr("new")}, 200, nil)

		// Rotation reseals each in its scope: a project's opens only as its own.
		oldKey := base64.StdEncoding.EncodeToString([]byte("e2e-only-secrets-key-32-bytes!!!"))
		newKey := base64.StdEncoding.EncodeToString([]byte("another-e2e-secrets-key-32-bytes"))
		both, err := secrets.New(newKey, oldKey)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := core.RotateSecrets(ctx, h.store, both); err != nil || n < 2 {
			t.Fatalf("rotated %d, %v", n, err)
		}
		onlyNew, _ := secrets.New(newKey)
		v, err := h.store.ScopedSecret(ctx, "prj_web", "web-only")
		if err != nil {
			t.Fatal(err)
		}
		if plain, err := onlyNew.Open(v.SealName(), v.KeyID, v.Nonce, v.Ciphertext); err != nil || string(plain) != "new" {
			t.Fatalf("after rotation: %q %v", plain, err)
		}
		if _, err := onlyNew.Open(v.Name, v.KeyID, v.Nonce, v.Ciphertext); err == nil {
			t.Fatal("a project's secret opens as a global one")
		}
	})
}

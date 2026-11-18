package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/crypto/ssh"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/present"
)

// userDataParts are a multipart user data's parts, by file name.
func userDataParts(t *testing.T, doc string) map[string]string {
	t.Helper()
	header, body, ok := strings.Cut(doc, "\r\n\r\n")
	if !ok {
		t.Fatalf("not multipart: %q", doc)
	}
	_, params, err := mime.ParseMediaType(strings.TrimPrefix(strings.Split(header, "\r\n")[0], "Content-Type: "))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	r := multipart.NewReader(strings.NewReader(body), params["boundary"])
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		out[p.FileName()] = string(b)
	}
}

// TestCloudbaseInit: a Windows template marked as using cloudbase-init
// gets its name, network, user and user data through guestinfo; a
// password leaves it once read. Without cloudbase-init, a Windows VM's
// remote access is Administrator's password, on vCenter's customization
// (ADR-0039).
func TestCloudbaseInit(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := accessHarness{setup(t, ctx, c), t}
		const admin = "mkeller"
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "win-pw", Kind: api.SecretWriteKindToken,
			Value: present.Ptr("W1n-Acc3ss!")}, 201, nil)

		// cloudbase-init is for a Windows template without a customization
		// specification.
		var problem struct{ Errors []api.FieldError }
		tpl := api.VmTemplateWrite{Key: "win-cbi", Label: "Windows Server 2025 (cloudbase-init)", OsFamily: api.VmTemplateWriteOsFamilyWindows,
			CloudbaseInit: present.Ptr(true), Customization: present.Ptr("vcsim-windows-static"), Offers: &[]api.Offer{{}},
			Sources: sourceOn("", "tpl-win2025-std-2026.09"), UserData: present.Ptr("#ps1_sysnative\nWrite-Host {{ .vm.name | json }}")}
		h.json("POST", "/api/v1/admin/templates", admin, tpl, 422, &problem)
		if got := fieldsOf(problem.Errors); len(got) != 1 || got[0] != "/cloudbase_init" {
			t.Fatalf("cloudbase-init with a specification = %+v", problem.Errors)
		}
		tpl.Customization = nil
		var saved api.VmTemplate
		h.json("POST", "/api/v1/admin/templates", admin, tpl, 201, &saved)
		if !saved.CloudbaseInit {
			t.Fatal("the template doesn't say it uses cloudbase-init")
		}
		h.json("POST", "/api/v1/admin/templates", admin, api.VmTemplateWrite{Key: "win-sysprep", Label: "Windows Server 2025",
			OsFamily: api.VmTemplateWriteOsFamilyWindows, Offers: &[]api.Offer{{}}, Sources: sourceOn("", "tpl-win2025-std-2026.09")}, 201, nil)

		guestOf := func(id string) (map[string]any, string) {
			t.Helper()
			var moref string
			_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where request_id = $1 and type = 'vsphere.vm'`, id).Scan(&moref)
			info, err := guestInfoOf(ctx, c, types.ManagedObjectReference{Type: "VirtualMachine", Value: moref})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := base64.StdEncoding.DecodeString(info["guestinfo.metadata"])
			user, _ := base64.StdEncoding.DecodeString(info["guestinfo.userdata"])
			var meta map[string]any
			if err := json.Unmarshal(raw, &meta); err != nil {
				t.Fatalf("metadata %q: %v", raw, err)
			}
			return meta, string(user)
		}
		run := func(item api.AdminCatalogItem, role string) (api.Request, api.Step) {
			t.Helper()
			req := h.submit(item, role, nil)
			h.runWorkflow(req.Id, cloudInitGuest(h.harness, c))
			h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
			if req.Status != api.RequestStatusSucceeded {
				t.Fatalf("%s request = %s %s", role, req.Status, errorOf(req))
			}
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &steps)
			for _, s := range steps.Items {
				if s.Key == "vsphere.customize" {
					return req, s
				}
			}
			t.Fatal("no customize step")
			return req, api.Step{}
		}

		// The deployment's key and the item's user data, through
		// cloudbase-init: no vCenter customization.
		keyed := imageItem(h.harness, "win-key", "win-cbi", map[string]any{"os_family": "windows", "domain": "corp.example",
			"remote_access": map[string]any{"kind": "generated_key", "username": "ops"},
			"cloud_init":    map[string]any{"user_data": "#ps1_sysnative\nWrite-Host {{ .inputs.role | json }}"}})
		req, step := run(keyed, "key")
		if step.Title != "Wrote cloudbase-init metadata and user data" || !strings.Contains(deref(step.Detail), "user ops with the deployment's key SHA256:") ||
			!strings.Contains(deref(step.Detail), "the template's user data merged with the item's") {
			t.Fatalf("customize step = %q %q", step.Title, deref(step.Detail))
		}
		meta, user := guestOf(req.Id)
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(meta["public-keys-data"].(string)))
		if err != nil || meta["admin-username"] != "ops" || meta["admin-password"] != nil || !strings.HasPrefix(meta["local-hostname"].(string), "web-dev-key") {
			t.Fatalf("metadata = %v (%v)", meta, err)
		}
		var key string
		_ = h.store.Pool.QueryRow(ctx, `select fingerprint from deployment_keys k join resources r on r.deployment_id = k.deployment_id
			where r.request_id = $1 and r.type = 'guest.access'`, req.Id).Scan(&key)
		if ssh.FingerprintSHA256(pub) != key {
			t.Fatalf("the VM's key %s, the deployment's %s", ssh.FingerprintSHA256(pub), key)
		}
		if eth := meta["network"].(map[string]any)["ethernets"].(map[string]any)["nic0"].(map[string]any); eth["addresses"] == nil {
			t.Fatalf("network = %v", meta["network"])
		}
		parts := userDataParts(t, user)
		if len(parts) != 2 || !strings.HasPrefix(parts["part-001.ps1"], "#ps1_sysnative\nWrite-Host \"web-dev-key") ||
			parts["part-002.ps1"] != "#ps1_sysnative\nWrite-Host \"key\"" {
			t.Fatalf("user data parts = %v", parts)
		}

		// A password from a stored secret: in the metadata, which the last
		// part writes again without it.
		pw := imageItem(h.harness, "win-pw", "win-cbi", map[string]any{"os_family": "windows", "domain": "corp.example",
			"remote_access": map[string]any{"kind": "password", "username": "ops", "secret": "win-pw"}})
		req, step = run(pw, "pw")
		if !strings.Contains(deref(step.Detail), "user ops with a password") {
			t.Fatalf("customize step = %q", deref(step.Detail))
		}
		meta, user = guestOf(req.Id)
		if meta["admin-username"] != "ops" || meta["admin-password"] != "W1n-Acc3ss!" {
			t.Fatalf("metadata = %v", meta)
		}
		parts = userDataParts(t, user)
		clearing := parts["part-002.ps1"]
		_, encoded, _ := strings.Cut(clearing, "info-set guestinfo.metadata ")
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimSpace(encoded), `"`))
		var cleared map[string]any
		if err := json.Unmarshal(raw, &cleared); err != nil || cleared["admin-username"] != "ops" || cleared["admin-password"] != nil ||
			cleared["local-hostname"] != meta["local-hostname"] {
			t.Fatalf("the clearing part = %q (%v)", clearing, err)
		}
		if n := h.leaks(ctx, "W1n-Acc3ss!"); n != 0 {
			t.Fatalf("the password is kept in %d places", n)
		}

		// Without cloudbase-init: Administrator's password on vCenter's
		// customization; a key or another user is refused.
		sysprep := imageItem(h.harness, "win-admin", "win-sysprep", map[string]any{"os_family": "windows", "domain": "corp.example",
			"remote_access": map[string]any{"kind": "password", "username": "Administrator", "secret": "win-pw"}})
		req, step = run(sysprep, "adm")
		if step.Title != "Applied guest customization" || !strings.Contains(deref(step.Detail), "user Administrator with a password") {
			t.Fatalf("customize step = %q %q", step.Title, deref(step.Detail))
		}
		var kind string
		_ = h.store.Pool.QueryRow(ctx, `select properties->>'kind' from resources where request_id = $1 and type = 'guest.access'`, req.Id).Scan(&kind)
		if kind != "password" {
			t.Fatalf("guest.access kind = %q", kind)
		}
		for slug, guest := range map[string]map[string]any{
			"win-sysprep-key": {"os_family": "windows", "remote_access": map[string]any{"kind": "generated_key", "username": "ops"}},
			"win-sysprep-ud":  {"os_family": "windows", "cloud_init": map[string]any{"user_data": "#ps1_sysnative\nWrite-Host hi"}},
		} {
			item := imageItem(h.harness, slug, "win-sysprep", guest)
			h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "no", "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}, 422, &problem, "Idempotency-Key", uuid.NewString())
			if len(problem.Errors) != 1 || !strings.Contains(problem.Errors[0].Message, "Windows Server 2025 doesn't use cloudbase-init") {
				t.Errorf("%s = %+v", slug, problem.Errors)
			}
		}
	})
}

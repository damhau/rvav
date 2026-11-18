package e2e

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/ssh"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
)

// guestInfoOf is a VM's extra config, by key.
func guestInfoOf(ctx context.Context, c *vim25.Client, ref types.ManagedObjectReference) (map[string]string, error) {
	var vm mo.VirtualMachine
	if err := property.DefaultCollector(c).RetrieveOne(ctx, ref, []string{"config.extraConfig"}, &vm); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, o := range vm.Config.ExtraConfig {
		ov := o.GetOptionValue()
		if s, ok := ov.Value.(string); ok {
			out[ov.Key] = s
		}
	}
	return out, nil
}

func newPublicKey(t *testing.T, comment string) (string, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := ssh.NewPublicKey(pub)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) + " " + comment, ssh.FingerprintSHA256(k)
}

// accessItem is a Linux item by cloud-init with remote access, its key in
// input ssh_key or its password in secret input pw.
func accessItem(access map[string]any) api.CatalogVersionDefinition {
	def := guestItem("tpl-ubuntu24.04-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example", "remote_access": access})
	def.FormSchema["properties"].(map[string]any)["ssh_key"] = map[string]any{"type": "string"}
	def.FormSchema["properties"].(map[string]any)["pw"] = map[string]any{"type": "string"}
	def.UiSchema.Fields["ssh_key"] = api.UiField{Widget: api.UiWidgetTextarea, Label: "SSH key"}
	def.UiSchema.Fields["pw"] = api.UiField{Widget: api.UiWidgetSecret, Label: "Password"}
	def.UiSchema.Layout.Sections[0].Fields = append(def.UiSchema.Layout.Sections[0].Fields, "ssh_key", "pw")
	return def
}

type accessHarness struct {
	*harness
	t *testing.T
}

func (h accessHarness) publish(slug string, def api.CatalogVersionDefinition) api.AdminCatalogItem {
	h.t.Helper()
	var item api.AdminCatalogItem
	h.json("POST", "/api/v1/admin/catalog/items", "mkeller", api.CatalogItemCreate{Slug: slug, Name: slug, Summary: slug,
		Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
	h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", "mkeller", nil, 200, nil)
	h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", "mkeller", api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
	return item
}

func (h accessHarness) submit(item api.AdminCatalogItem, role string, more map[string]any) api.Request {
	h.t.Helper()
	inputs := map[string]any{"role": role, "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}
	for k, v := range more {
		inputs[k] = v
	}
	var req api.Request
	h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
		ProjectId: "prj_web", Environment: "dev", Inputs: inputs}, 202, &req, "Idempotency-Key", uuid.NewString())
	return req
}

// userData is the cloud-config document a request's VM got, and its
// metadata.
func (h accessHarness) userData(ctx context.Context, c *vim25.Client, requestID string) (map[string]any, map[string]any) {
	h.t.Helper()
	var moref string
	_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where request_id = $1 and type = 'vsphere.vm'`,
		requestID).Scan(&moref)
	info, err := guestInfoOf(ctx, c, types.ManagedObjectReference{Type: "VirtualMachine", Value: moref})
	if err != nil {
		h.t.Fatal(err)
	}
	user, _ := base64.StdEncoding.DecodeString(info["guestinfo.userdata"])
	meta, _ := base64.StdEncoding.DecodeString(info["guestinfo.metadata"])
	if !strings.HasPrefix(string(user), "#cloud-config\n") {
		h.t.Fatalf("user data %q", user)
	}
	var u, m map[string]any
	if err := yaml.Unmarshal(user, &u); err != nil {
		h.t.Fatal(err)
	}
	_ = json.Unmarshal(meta, &m)
	return u, m
}

// leaks counts where a value is kept that must not be: the request's
// inputs and plan, its steps, their logs, events and audit entries.
func (h accessHarness) leaks(ctx context.Context, value string) int {
	h.t.Helper()
	var n int
	if err := h.store.Pool.QueryRow(ctx, `select (select count(*) from requests where plan::text like '%' || $1 || '%' or inputs::text like '%' || $1 || '%')
		+ (select count(*) from request_steps where detail like '%' || $1 || '%' or title like '%' || $1 || '%' or coalesce(error, '') like '%' || $1 || '%')
		+ (select count(*) from step_log_lines where text like '%' || $1 || '%')
		+ (select count(*) from events where payload::text like '%' || $1 || '%')
		+ (select count(*) from audit_events where detail::text like '%' || $1 || '%')
		+ (select count(*) from resources where properties::text like '%' || $1 || '%')`, value).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// TestRemoteAccess: a Linux VM gets the user people sign in as through
// cloud-init, with the key a requester gave or a password from a secret
// input, which stays out of the request and leaves guestinfo once read
// (ADR-0039).
func TestRemoteAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := accessHarness{setup(t, ctx, c), t}

		// A Windows VM gets one administrator, and a password must be a
		// secret input's.
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/secrets", "mkeller", api.SecretWrite{Name: "win-admin", Kind: api.SecretWriteKindToken,
			Value: present.Ptr("L0cal-S3cret!")}, 201, nil)
		win := guestItem("tpl-win2025-std-2026.09", map[string]any{"os_family": "windows", "domain": "corp.example", "admin_password": "win-admin",
			"remote_access": map[string]any{"kind": "password", "username": "ops", "secret": "win-admin"}})
		h.json("POST", "/api/v1/admin/catalog/items", "mkeller", api.CatalogItemCreate{Slug: "win-access", Name: "w", Summary: "w",
			Category: "compute", Type: api.VmBlueprint, Definition: &win}, 422, &problem)
		if got := fieldsOf(problem.Errors); len(got) != 1 || !strings.HasSuffix(got[0], "/guest/remote_access") {
			t.Fatalf("Windows remote access and admin_password = %v", problem.Errors)
		}
		plain := accessItem(map[string]any{"kind": "password", "username": "ops", "password": "${inputs.role}"})
		h.json("POST", "/api/v1/admin/catalog/items", "mkeller", api.CatalogItemCreate{Slug: "plain-pw", Name: "p", Summary: "p",
			Category: "compute", Type: api.VmBlueprint, Definition: &plain}, 422, &problem)
		if got := fieldsOf(problem.Errors); len(got) != 1 || !strings.HasSuffix(got[0], "/guest/remote_access/password") {
			t.Fatalf("a password from a plain input = %v", problem.Errors)
		}

		// The requester's key.
		key, fingerprint := newPublicKey(t, "ops@laptop")
		keyed := h.publish("ssh-key", accessItem(map[string]any{"kind": "ssh_key", "username": "ops", "public_key": "${inputs.ssh_key}"}))
		req := h.submit(keyed, "key", map[string]any{"ssh_key": key})
		h.runWorkflow(req.Id, cloudInitGuest(h.harness, c))
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("ssh_key request = %s %s", req.Status, errorOf(req))
		}
		user, meta := h.userData(ctx, c, req.Id)
		users, _ := user["users"].([]any)
		if len(users) != 2 || users[0] != "default" {
			t.Fatalf("users = %v", user["users"])
		}
		ops, _ := users[1].(map[string]any)
		if keys, _ := ops["ssh_authorized_keys"].([]any); ops["name"] != "ops" || len(keys) != 1 || keys[0] != key || ops["sudo"] == nil {
			t.Fatalf("the user = %v", ops)
		}
		if _, ok := meta["cleanup-guestinfo"]; ok {
			t.Fatal("a key's user data is cleared from guestinfo")
		}
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &steps)
		for _, s := range steps.Items {
			if s.Key == "vsphere.customize" && !strings.Contains(deref(s.Detail), "user ops with key "+fingerprint) {
				t.Fatalf("customize step = %q", deref(s.Detail))
			}
		}
		var kind, username, fp string
		if err := h.store.Pool.QueryRow(ctx, `select properties->>'kind', properties->>'username', properties->>'fingerprint'
			from resources where request_id = $1 and type = 'guest.access'`, req.Id).Scan(&kind, &username, &fp); err != nil {
			t.Fatal(err)
		}
		if kind != "ssh_key" || username != "ops" || fp != fingerprint {
			t.Fatalf("guest.access = %s %s %s", kind, username, fp)
		}

		// A password from a secret input: in the user data only, which
		// cloud-init clears from guestinfo once read.
		pw := h.publish("password", accessItem(map[string]any{"kind": "password", "username": "ops", "password": "${inputs.pw}"}))
		req = h.submit(pw, "pw", map[string]any{"pw": "Acc3ss-S3cret!"})
		h.runWorkflow(req.Id, cloudInitGuest(h.harness, c))
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("password request = %s %s", req.Status, errorOf(req))
		}
		user, meta = h.userData(ctx, c, req.Id)
		ops, _ = user["users"].([]any)[1].(map[string]any)
		if ops["plain_text_passwd"] != "Acc3ss-S3cret!" || ops["lock_passwd"] != false || user["ssh_pwauth"] != true {
			t.Fatalf("the user = %v", user)
		}
		if cl, _ := meta["cleanup-guestinfo"].([]any); len(cl) != 1 || cl[0] != "userdata" {
			t.Fatalf("metadata = %v", meta)
		}
		if n := h.leaks(ctx, "Acc3ss-S3cret!"); n != 0 {
			t.Fatalf("the password is kept in %d places", n)
		}

		// A stored secret's key for every VM of the item.
		stored, storedFP := newPublicKey(t, "team@corp")
		h.json("POST", "/api/v1/admin/secrets", "mkeller", api.SecretWrite{Name: "team-key", Kind: api.SecretWriteKindToken,
			Value: present.Ptr(stored)}, 201, nil)
		team := h.publish("team-key", accessItem(map[string]any{"kind": "ssh_key", "username": "team", "secret": "team-key"}))
		req = h.submit(team, "team", nil)
		h.runWorkflow(req.Id, cloudInitGuest(h.harness, c))
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("stored key request = %s %s", req.Status, errorOf(req))
		}
		user, _ = h.userData(ctx, c, req.Id)
		ops, _ = user["users"].([]any)[1].(map[string]any)
		if keys, _ := ops["ssh_authorized_keys"].([]any); len(keys) != 1 || keys[0] != stored {
			t.Fatalf("the stored key's user = %v (%s)", ops, storedFP)
		}

		// User data holding a stored secret or a secret input leaves
		// guestinfo once read too; user data without either stays (T1.7).
		for slug, ud := range map[string]string{
			"ud-secret": "#cloud-config\nwrite_files:\n  - path: /etc/app/key\n    content: {{ secret \"team-key\" | json }}",
			"ud-input":  "#cloud-config\nwrite_files:\n  - path: /etc/app/pw\n    content: {{ .inputs.pw | json }}",
			"ud-plain":  "#cloud-config\nwrite_files:\n  - path: /etc/app/role\n    content: {{ .inputs.role | json }}",
		} {
			def := accessItem(map[string]any{"kind": "ssh_key", "username": "ops", "public_key": "${inputs.ssh_key}"})
			def.Blueprint["guest"].(map[string]any)["cloud_init"] = map[string]any{"user_data": ud}
			req = h.submit(h.publish(slug, def), strings.TrimPrefix(slug, "ud-"), map[string]any{"ssh_key": key, "pw": "Ud-S3cret!"})
			h.runWorkflow(req.Id, cloudInitGuest(h.harness, c))
			h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
			if req.Status != api.RequestStatusSucceeded {
				t.Fatalf("%s request = %s %s", slug, req.Status, errorOf(req))
			}
			_, meta = h.userData(ctx, c, req.Id)
			if _, cleared := meta["cleanup-guestinfo"]; cleared != (slug != "ud-plain") {
				t.Errorf("%s: metadata = %v", slug, meta)
			}
		}

		// A build failing once its key is made, before its VM has it: the
		// rollback drops the key (T1.7).
		def := accessItem(map[string]any{"kind": "generated_key", "username": "ops"})
		def.Blueprint["guest"].(map[string]any)["cloud_init"] = map[string]any{
			"user_data": "#cloud-config\n{{ if eq .inputs.ssh_key \"list\" }}- a list{{ else }}a: map{{ end }}"}
		def.Blueprint["rollback_on_failure"] = true
		req = h.submit(h.publish("gen-broken", def), "broken", map[string]any{"ssh_key": "list"})
		h.runWorkflow(req.Id, cloudInitGuest(h.harness, c))
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		var keys int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from deployment_keys`).Scan(&keys); err != nil {
			t.Fatal(err)
		}
		if req.Status != api.RequestStatusFailed || keys != 0 {
			t.Fatalf("broken build = %s %s, %d keys kept", req.Status, errorOf(req), keys)
		}
	})
}

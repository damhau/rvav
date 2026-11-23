package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
)

// variableUpload is a multipart body with a file part, when name isn't
// empty, and a description part, when one is given.
func variableUpload(name, content string, description ...string) (rawYAML, string) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if name != "" {
		part, _ := w.CreateFormFile("file", name)
		_, _ = part.Write([]byte(content))
	}
	for _, d := range description {
		_ = w.WriteField("description", d)
	}
	_ = w.Close()
	return rawYAML(b.String()), w.FormDataContentType()
}

// TestVariableFiles: a shared variable holds a text file (E2.1, ADR-0057),
// uploaded as multipart and downloaded as an attachment. A composite's REST
// call reads its text, and an AWX job gets it in its extra variables while
// the step's run data keeps the file's marker; cloud-init user data reads
// it with {{ file }} into the VM's guestinfo, which publishing and a
// template's save check. A file over the cap, empty, binary or holding a
// private key is refused, and one read is kept.
func TestVariableFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, requester, auditor = "mkeller", "tnguyen", "svc-awx"
		cmdb := newRESTCMDB(t)
		h.svc.REST.Hosts = []string{cmdb.URL}

		put := func(key, user string, body rawYAML, ct string, want int) (api.Variable, fileProblem) {
			t.Helper()
			code, b, _ := h.do("PUT", "/api/v1/admin/variables/"+key+"/file", user, body, "Content-Type", ct)
			if code != want {
				t.Fatalf("PUT %s's file = %d, want %d: %s", key, code, want, b)
			}
			var v api.Variable
			var p fileProblem
			if code < 300 {
				_ = json.Unmarshal(b, &v)
			} else {
				_ = json.Unmarshal(b, &p)
			}
			return v, p
		}
		upload := func(key, name, content string, want int, description ...string) (api.Variable, fileProblem) {
			t.Helper()
			body, ct := variableUpload(name, content, description...)
			return put(key, admin, body, ct, want)
		}

		const motd = "Welcome to {{ .vm.name }}: this text isn't a template.\n\"Quoted\" and Ünicode.\n"
		v, _ := upload("linux.motd", "motd.txt", motd, 201, "The message of the day.")
		if v.Type != api.VariableTypeFile || v.File == nil || v.File.Name != "motd.txt" || v.File.Size != int64(len(motd)) ||
			v.File.ContentType != "text/plain" || len(v.File.Sha256) != 64 || v.Description != "The message of the day." || len(v.UsedBy) != 0 {
			t.Fatalf("the file variable = %+v %+v", v, v.File)
		}
		if rec, _ := v.Value.(map[string]any); rec["name"] != "motd.txt" || rec["sha256"] != v.File.Sha256 || rec["text"] != nil {
			t.Fatalf("its value = %v", v.Value)
		}
		h.json("POST", "/api/v1/admin/variables", admin, api.VariableCreate{Group: "cmdb", Name: "site", Value: "gva"}, 201, nil)

		// What isn't taken.
		body, ct := variableUpload("motd.txt", motd)
		put("linux.other", requester, body, ct, 403)
		put("linux.other", auditor, body, ct, 403)
		if _, p := upload("linux.big", "big.txt", strings.Repeat("a", 256<<10+1), 413); !strings.Contains(p.Detail, "256 KiB") {
			t.Fatalf("a file over the cap = %+v", p)
		}
		upload("linux.empty", "empty.txt", "", 400)
		upload("linux.binary", "blob.bin", "\x00\x01\x02", 415)
		upload("linux.latin", "latin.txt", "caf\xe9", 415)
		_, p := upload("linux.deploy", "deploy.pem", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blbn\n-----END OPENSSH PRIVATE KEY-----\n", 422)
		if len(p.Errors) == 0 || p.Errors[0].Field != "/file" || valueOf(p.Errors[0].Code) != "secret" {
			t.Fatalf("a private key = %+v", p)
		}
		if _, p := upload("linux.api_token", "t.txt", "x", 422); len(p.Errors) == 0 || p.Errors[0].Field != "/name" {
			t.Fatalf("a secret-like name = %+v", p)
		}
		upload("linux.nofile", "", "", 422, "No file.")
		upload("cmdb.site", "site.txt", "zrh", 409)
		put("linux.json", admin, rawYAML(`{"value": "x"}`), "application/json", 400)
		h.json("PUT", "/api/v1/admin/variables/linux.motd", admin, api.VariableUpdate{Value: "x"}, 409, nil)

		// A download, never shown in the browser; for auditors too.
		code, got, hdr := h.do("GET", "/api/v1/admin/variables/linux.motd/file", auditor, nil)
		if code != 200 || string(got) != motd || hdr.Get("Content-Disposition") != `attachment; filename=motd.txt` ||
			hdr.Get("Content-Type") != "application/octet-stream" || hdr.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("the download = %d %q %v", code, got, hdr)
		}
		h.json("GET", "/api/v1/admin/variables/linux.motd/file", requester, nil, 403, nil)
		h.json("GET", "/api/v1/admin/variables/cmdb.site/file", admin, nil, 404, nil)

		// A composite: its REST call reads the text, its AWX job gets it.
		def := restDefinition(cmdb.URL)
		def.Blueprint = map[string]any{"kind": "composite", "steps": []any{
			map[string]any{"key": "register", "title": "Register", "rest": map[string]any{
				"method": "POST", "url": cmdb.URL + "/api/ci/${inputs.name}?site=${vars.cmdb.site}",
				"body": "{'name': inputs.name, 'motd': vars.linux.motd.text, 'sha256': vars.linux.motd.sha256}", "accept": []any{201}}},
			map[string]any{"key": "job", "title": "Message of the day", "awx": map[string]any{"job_template": "monitoring-agent"},
				"with": map[string]any{"target": "inputs.name", "motd": "vars.linux.motd.text", "size": "vars.linux.motd.size"}},
		}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "motd", Name: "Message of the day",
			Summary: "Sets the message of the day.", Category: "compute", Type: api.Composite, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/motd/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/motd/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		run := func(text string) map[string]any {
			t.Helper()
			var req api.Request
			h.json("POST", "/api/v1/requests", requester, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"name": "web01", "api_password": restInput}},
				202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.json("GET", "/api/v1/requests/"+req.Id, requester, nil, 200, &req)
			if req.Status != api.RequestStatusSucceeded {
				t.Fatalf("the request = %s %s", req.Status, errorOf(req))
			}
			var list struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+req.Id+"/steps", requester, nil, 200, &list)
			i := slices.IndexFunc(list.Items, func(s api.Step) bool { return s.Key == "step:job" })
			job := list.Items[i]
			if job.ExternalRef == nil {
				t.Fatalf("the step ran no job: %+v", job)
			}
			id, _ := strconv.Atoi(job.ExternalRef.Id)
			var d api.StepData
			h.json("GET", "/api/v1/requests/"+req.Id+"/steps/"+job.Id+"/data", admin, nil, 200, &d)
			if in := valueOf(d.Inputs); !jsonEqual(in["motd"], map[string]any{"$file": "linux.motd"}) || in["size"] != float64(len(text)) {
				t.Fatalf("the job's run data = %v", in)
			}
			return h.awx.Launched(id).ExtraVars
		}
		extra := run(motd)
		cmdb.mu.Lock()
		call := cmdb.calls[len(cmdb.calls)-1]
		cmdb.mu.Unlock()
		if call.body["motd"] != motd || call.body["sha256"] != v.File.Sha256 || call.query != "site=gva" {
			t.Fatalf("the call = %+v", call)
		}
		if extra["motd"] != motd || extra["target"] != "web01" {
			t.Fatalf("the job's extra variables = %v", extra)
		}
		var leaked int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from requests where plan::text like '%this text isn''t a template%'`).Scan(&leaked)
		if leaked != 0 {
			t.Fatalf("the file is in %d plans", leaked)
		}

		// Replaced, the next run reads the new file; a description alone keeps it.
		const motd2 = "Maintenance on Sunday.\n"
		v, _ = upload("linux.motd", "motd-sunday.txt", motd2, 200)
		if v.File.Name != "motd-sunday.txt" || v.Description != "The message of the day." || !slices.Equal(v.UsedBy, []string{"item motd"}) {
			t.Fatalf("the replaced variable = %+v", v)
		}
		v, _ = upload("linux.motd", "", "", 200, "Shown at log-in.")
		if v.File.Name != "motd-sunday.txt" || v.Description != "Shown at log-in." {
			t.Fatalf("the described variable = %+v", v)
		}
		if extra = run(motd2); extra["motd"] != motd2 {
			t.Fatalf("the job after the change got %v", extra)
		}

		// Cloud-init reads it into guestinfo; publishing checks the name.
		vm := func(file string) api.CatalogVersionDefinition {
			return guestItem("tpl-ubuntu24.04-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example",
				"cloud_init": map[string]any{"user_data": "#cloud-config\nwrite_files:\n  - path: /etc/motd\n    content: {{ file \"" + file + "\" | json }}\n"}})
		}
		nope := vm("linux.nope")
		var ubuntu api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "ubuntu-motd", Name: "Ubuntu", Summary: "Ubuntu.",
			Category: "compute", Type: api.VmBlueprint, Definition: &nope}, 201, &ubuntu)
		draft := "/api/v1/admin/catalog/items/ubuntu-motd/versions/" + ubuntu.DraftVersion.Id
		var refused struct{ Errors []api.FieldError }
		h.json("POST", draft+"/publish", admin, nil, 422, &refused)
		if len(refused.Errors) != 1 || refused.Errors[0].Field != "/definition/blueprint/guest/cloud_init/user_data" ||
			valueOf(refused.Errors[0].Code) != "unknown_variable" || !strings.Contains(refused.Errors[0].Message, "linux.nope") {
			t.Fatalf("publishing with an unknown file = %+v", refused.Errors)
		}
		var saved api.CatalogItemVersionDetail
		h.json("PUT", draft, admin, api.CatalogItemVersionUpdate{Definition: vm("cmdb.site"), BaseRevision: &ubuntu.DraftVersion.Revision}, 200, &saved)
		h.json("POST", draft+"/publish", admin, nil, 422, nil)
		h.json("PUT", draft, admin, api.CatalogItemVersionUpdate{Definition: vm("linux.motd"), BaseRevision: &saved.Revision}, 200, nil)
		h.json("POST", draft+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/ubuntu-motd/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		var ci api.Request
		h.json("POST", "/api/v1/requests", requester, api.RequestDraft{CatalogItemId: ubuntu.Id, VersionId: ubuntu.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "motd", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}, 202, &ci, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(ci.Id, cloudInitGuest(h, c))
		h.json("GET", "/api/v1/requests/"+ci.Id, requester, nil, 200, &ci)
		if ci.Status != api.RequestStatusSucceeded {
			t.Fatalf("the cloud-init request = %s %s", ci.Status, errorOf(ci))
		}
		var moref string
		_ = h.store.Pool.QueryRow(ctx, `select properties->'vm'->>'moref' from resources where request_id = $1 and type = 'vsphere.vm'`, ci.Id).Scan(&moref)
		var machine mo.VirtualMachine
		if err := property.DefaultCollector(c).RetrieveOne(ctx, types.ManagedObjectReference{Type: "VirtualMachine", Value: moref},
			[]string{"config.extraConfig"}, &machine); err != nil {
			t.Fatal(err)
		}
		var user []byte
		for _, o := range machine.Config.ExtraConfig {
			if ov := o.GetOptionValue(); ov.Key == "guestinfo.userdata" {
				user, _ = base64.StdEncoding.DecodeString(ov.Value.(string))
			}
		}
		if want, _ := json.Marshal(motd2); !strings.Contains(string(user), "content: "+string(want)+"\n") {
			t.Fatalf("the user data = %q", user)
		}

		// A template's user data is checked when saved, and keeps the file too.
		tpl := api.VmTemplateWrite{Key: "ubuntu-motd", Label: "Ubuntu with a message", Sources: sourceOn("", "tpl-ubuntu24.04-2026.10"),
			OsFamily: api.VmTemplateWriteOsFamilyLinux, UserData: new("#cloud-config\nbootcmd:\n  - echo {{ file \"linux.gone\" | json }}\n")}
		h.json("POST", "/api/v1/admin/templates", admin, tpl, 422, &refused)
		if len(refused.Errors) != 1 || refused.Errors[0].Field != "/user_data" || !strings.Contains(refused.Errors[0].Message, "linux.gone") {
			t.Fatalf("a template that reads an unknown file = %+v", refused.Errors)
		}
		tpl.UserData = new("#cloud-config\nbootcmd:\n  - echo {{ file \"linux.motd\" | json }}\n")
		h.json("POST", "/api/v1/admin/templates", admin, tpl, 201, nil)
		h.json("GET", "/api/v1/admin/variables/linux.motd", auditor, nil, 200, &v)
		if !slices.Equal(v.UsedBy, []string{"item motd", "item ubuntu-motd", "template ubuntu-motd"}) {
			t.Fatalf("linux.motd is used by %v", v.UsedBy)
		}
		var conflict api.Problem
		h.json("DELETE", "/api/v1/admin/variables/linux.motd", admin, nil, 409, &conflict)
		if !strings.Contains(valueOf(conflict.Detail), "user data") {
			t.Fatalf("removing one a template reads = %+v", conflict)
		}

		// Audited with the file's record, never its content.
		var audit []string
		rows, err := h.store.Pool.Query(ctx, `select action || ' ' || detail::text from audit_events where target_id = 'linux.motd' order by id`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var a string
			if err := rows.Scan(&a); err != nil {
				t.Fatal(err)
			}
			audit = append(audit, a)
		}
		if len(audit) != 3 || !strings.HasPrefix(audit[0], "variable.create ") || !strings.Contains(audit[1], `"was": {`) ||
			!strings.Contains(audit[2], `"description": "Shown at log-in."`) || strings.Contains(strings.Join(audit, " "), "Maintenance") {
			t.Fatalf("the audit = %q", audit)
		}
	})
}

// fileProblem is a problem as an upload's checks read it.
type fileProblem struct {
	Detail string
	Errors []api.FieldError
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

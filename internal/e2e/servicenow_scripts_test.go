package e2e

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/servicenow"
)

// harnessOut is what integrations/servicenow/test/harness.js writes.
type harnessOut struct {
	Tables map[string][]map[string]any `json:"tables"`
	Calls  []struct {
		Method, Path, Authorization string
		Body                        json.RawMessage
	} `json:"calls"`
	Events  []map[string]any  `json:"events"`
	Log     []string          `json:"log"`
	Answers []json.RawMessage `json:"answers"`
}

func (o harnessOut) rows(table string, match func(map[string]any) bool) []map[string]any {
	var out []map[string]any
	for _, r := range o.Tables[table] {
		if match(r) {
			out = append(out, r)
		}
	}
	return out
}

// leaSysID is Léa Martin's sys_user in the harness's fixture, who orders
// for others.
const leaSysID = "a8f98bb0eb32010045e1a5115206fe3a"

// runHarness runs ServiceNow's scripts under Node, with rvav's answers in
// responses.
func runHarness(t *testing.T, responses map[string]any, steps ...map[string]any) harnessOut {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node to run ServiceNow's scripts with")
	}
	fixture, _ := json.Marshal(map[string]any{
		"properties": map[string]string{},
		"aliases":    map[string]any{"global.rvav": map[string]string{"connection_url": "https://rvav.corp.example/", "api_key": "rvav_pat_x"}},
		"user":       map[string]string{"sys_id": "6816f79cc0a8016401c5a33be04be441", "email": "thomas.nguyen@corp.example", "user_name": "tnguyen"},
		"tables": map[string]any{"sc_catalog": []map[string]string{{"sys_id": "e0d08b13c3330100c8b837659bba8fb4", "title": "Service Catalog"}},
			"sys_user": []map[string]string{{"sys_id": leaSysID, "email": "lea.martin@corp.example", "user_name": "lmartin"}}},
		"responses": responses,
		"steps":     steps,
	})
	file := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(file, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join("..", "..", "integrations", "servicenow", "test", "harness.js"), file)
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("the harness: %v", err)
	}
	var out harnessOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("the harness wrote %s: %v", b, err)
	}
	return out
}

// ServiceNow's side (integrations/servicenow/): the catalog import, the
// catalog form's projects and an order, against what rvav answers.
func TestServiceNowScripts(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		snow, srv := snowInstance(t)
		h.svc.Config.ServiceNow = config.ServiceNowConfig{URL: srv.URL, Auth: servicenow.AuthBasic, Username: snow.Username,
			Secret: snow.Password, RequestsPerMinute: 30}
		var made api.ServiceNowTokenMade
		h.json("POST", "/api/v1/admin/integrations/servicenow/token", "mkeller", nil, 201, &made)
		call := func(method, path string, body any, want int, out any) {
			t.Helper()
			h.json(method, "/api/v1/servicenow"+path, "", body, want, out, "Authorization", "Bearer "+made.Secret)
		}
		var catalog, entitlements json.RawMessage
		call("GET", "/catalog", nil, 200, &catalog)
		call("GET", "/entitlements?user=thomas.nguyen@corp.example", nil, 200, &entitlements)
		var cat api.ServiceNowCatalog
		_ = json.Unmarshal(catalog, &cat)
		const rhel = "itm_rhel9-server"
		answers := map[string]any{
			"GET /catalog": map[string]any{"status": 200, "body": catalog},
			"GET /entitlements?user=thomas.nguyen%40corp.example": map[string]any{"status": 200, "body": entitlements},
			"POST /requests": map[string]any{"status": 202, "body": map[string]any{}},
		}
		order := map[string]any{"do": "order", "item": rhel, "number": "RITM0010046", "variables": map[string]string{
			"rvav_project": "prj_web", "rvav_environment": "dev", "rvav_justification": "The web shop's new front end",
			"rvav_deployment_name": "", "rvav_accept_warnings": "false",
			"rvav_in_role": "app", "rvav_in_size": "S", "rvav_in_cluster": "GVA-DEV-CL01",
			"rvav_in_datastore": "vsan-gva-dev-cl01", "rvav_in_network": "dvpg-app-dev-vl310"}}
		out := runHarness(t, answers, map[string]any{"do": "import"}, map[string]any{"do": "projects", "item": rhel}, order)

		// Every item rvav offers is a catalog item, with rvav's variable set
		// and a variable for each input.
		items := out.rows("sc_cat_item", func(r map[string]any) bool { return strings.HasPrefix(r["meta"].(string), "rvav:") })
		if len(items) != len(cat.Items) || len(items) == 0 {
			t.Fatalf("%d catalog items made for %d", len(items), len(cat.Items))
		}
		item := out.rows("sc_cat_item", func(r map[string]any) bool { return r["meta"] == "rvav:"+rhel })[0]
		if item["name"] != "RHEL 9 server" || item["active"] != true || item["sc_catalogs"] != "e0d08b13c3330100c8b837659bba8fb4" {
			t.Errorf("the item = %v", item)
		}
		vars := map[string]map[string]any{}
		for _, v := range out.rows("item_option_new", func(r map[string]any) bool { return r["cat_item"] == item["sys_id"] }) {
			vars[v["name"].(string)] = v
		}
		size, cluster := vars["rvav_in_size"], vars["rvav_in_cluster"]
		if size == nil || size["type"] != "5" || cluster == nil || cluster["type"] != "6" ||
			!strings.Contains(cluster["help_text"].(string), "checks it") || vars["rvav_in_post_config"] != nil {
			t.Fatalf("the variables = %v", vars)
		}
		if n := len(out.rows("question_choice", func(r map[string]any) bool { return r["question"] == size["sys_id"] })); n != 4 {
			t.Errorf("the size has %d choices", n)
		}
		set := out.rows("item_option_new_set", func(r map[string]any) bool { return r["internal_name"] == "rvav" })
		if len(set) != 1 || len(out.rows("io_set_item", func(r map[string]any) bool {
			return r["sc_cat_item"] == item["sys_id"] && r["variable_set"] == set[0]["sys_id"]
		})) != 1 || len(out.rows("item_option_new", func(r map[string]any) bool { return r["variable_set"] == set[0]["sys_id"] })) != 5 {
			t.Errorf("the variable set = %v, %v", set, out.Tables["io_set_item"])
		}

		// The order form offers the projects the user may request it in.
		var projects struct {
			Projects []struct {
				Value        string
				Environments []api.ServiceNowChoice
			}
		}
		_ = json.Unmarshal(out.Answers[1], &projects)
		if len(projects.Projects) != 1 || projects.Projects[0].Value != "prj_web" || len(projects.Projects[0].Environments) != 3 {
			t.Fatalf("the projects = %s", out.Answers[1])
		}

		// An order queues rvav.order, whose script action submits it on behalf
		// of requested_for; rvav takes what it sent.
		if len(out.Events) != 1 || out.Events[0]["name"] != "rvav.order" {
			t.Fatalf("the events = %v", out.Events)
		}
		post := out.Calls[len(out.Calls)-1]
		if post.Method != "POST" || post.Path != "/requests" || post.Authorization != "Bearer rvav_pat_x" {
			t.Fatalf("the calls = %+v", out.Calls)
		}
		var draft api.ServiceNowRequestDraft
		if err := json.Unmarshal(post.Body, &draft); err != nil {
			t.Fatal(err)
		}
		if draft.User != "thomas.nguyen@corp.example" || draft.OrderedBy != "thomas.nguyen@corp.example" || draft.CatalogItemId != rhel || draft.RitmNumber != "RITM0010046" ||
			draft.ProjectId != "prj_web" || draft.DeploymentName != nil || draft.AcceptWarnings != nil {
			t.Fatalf("the draft = %s", post.Body)
		}
		var req api.ServiceNowRequest
		call("POST", "/requests", draft, 202, &req)
		var got api.Request
		h.json("GET", "/api/v1/requests/"+req.RequestId, "tnguyen", nil, 200, &got)
		if got.Inputs == nil || (*got.Inputs)["size"] != "S" || (*got.Inputs)["cluster"] != "GVA-DEV-CL01" ||
			!strings.HasPrefix(*got.Justification, "The web shop's new front end") {
			t.Errorf("the request = %+v", got)
		}
		var ritm map[string]any
		_ = json.Unmarshal(out.Answers[2], &ritm)
		if ritm["state"] != "1" || ritm["work_notes"] != nil {
			t.Errorf("a request rvav took = %v: rvav writes its progress", ritm)
		}

		// Ordered by someone else: who opened it is sent, and rvav takes it
		// from Léa, Web Platform's admin. Opened by no one rvav can name,
		// it is refused.
		forThomas := maps.Clone(order)
		forThomas["number"], forThomas["opened_by"] = "RITM0010047", leaSysID
		nobody := maps.Clone(order)
		nobody["number"], nobody["opened_by"] = "RITM0010048", "0000000000000000000000000000dead"
		out = runHarness(t, answers, map[string]any{"do": "import"}, forThomas, nobody)
		post = out.Calls[len(out.Calls)-1]
		var byLea api.ServiceNowRequestDraft
		if err := json.Unmarshal(post.Body, &byLea); err != nil || byLea.User != "thomas.nguyen@corp.example" ||
			byLea.OrderedBy != "lea.martin@corp.example" {
			t.Fatalf("ordered by Léa = %s, %v", post.Body, err)
		}
		// The harness numbers its records the same each run.
		byLea.RitmSysId = strings.Repeat("e", 32)
		var leas api.ServiceNowRequest
		call("POST", "/requests", byLea, 202, &leas)
		if leas.OrderedBy == nil || leas.OrderedBy.Id != "usr_lmartin" || leas.User.Id != "usr_tnguyen" {
			t.Fatalf("rvav's request = %+v", leas)
		}
		_ = json.Unmarshal(out.Answers[2], &ritm)
		if ritm["state"] != "4" || !strings.Contains(ritm["work_notes"].(string), "the person who ordered it is unknown") {
			t.Errorf("opened by no one = %v", ritm)
		}

		// A refusal closes the requested item incomplete, saying why.
		answers["POST /requests"] = map[string]any{"status": 422, "body": map[string]any{"title": "Invalid request",
			"status": 422, "request_id": "req_1", "errors": []map[string]string{{"field": "/inputs/size", "message": "One of S, M, L or XL."}}}}
		out = runHarness(t, answers, map[string]any{"do": "import"}, order)
		_ = json.Unmarshal(out.Answers[1], &ritm)
		if ritm["state"] != "4" || !strings.Contains(ritm["work_notes"].(string), "/inputs/size: One of S, M, L or XL.") {
			t.Errorf("a refused request = %v", ritm)
		}

		// rvav unreachable: the order stays open, and the retry job submits
		// it again once rvav says it has no request for it.
		answers["POST /requests"] = map[string]any{"status": 503, "body": map[string]any{"title": "Unavailable"}}
		out = runHarness(t, answers, map[string]any{"do": "import"}, order, map[string]any{"do": "retry"})
		_ = json.Unmarshal(out.Answers[1], &ritm)
		var paths []string
		posts := 0
		for _, c := range out.Calls {
			paths = append(paths, c.Method+" "+c.Path)
			if c.Method == "POST" {
				posts++
			}
		}
		if ritm["state"] != "1" || !slices.Contains(paths, "GET /requests/"+ritm["sys_id"].(string)) || posts != 2 {
			t.Errorf("rvav down = %v, calls %v", ritm, paths)
		}
	})
}

package awx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLaunchPrompts: a launch on some hosts or on another inventory checks
// the template prompts for them first, and a job whose limit or inventory
// AWX ignored anyway is cancelled; survey defaults fill what AWX needs.
func TestLaunchPrompts(t *testing.T) {
	var launched map[string]any
	var cancelled []string
	prompts := map[string]string{
		"7":  `{"ask_limit_on_launch": true, "ask_inventory_on_launch": true}`,
		"8":  `{"ask_limit_on_launch": true, "ask_inventory_on_launch": false}`,
		"9":  `{"ask_limit_on_launch": false, "ask_inventory_on_launch": true}`,
		"10": `{"ask_limit_on_launch": true, "ask_inventory_on_launch": true}`,
		"11": `{"ask_limit_on_launch": true, "ask_inventory_on_launch": true, "ask_credential_on_launch": false}`,
		"12": `{"variables_needed_to_start": ["agent", "port", "given", "tags"]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // api v2 job_templates <id> launch
		switch {
		case r.Method == "GET" && len(parts) == 5 && parts[4] == "launch":
			_, _ = w.Write([]byte(prompts[parts[3]]))
		case r.Method == "POST" && len(parts) == 5 && parts[4] == "launch":
			_ = json.NewDecoder(r.Body).Decode(&launched)
			w.WriteHeader(http.StatusCreated)
			if parts[3] == "10" { // an AWX that ignores the limit all the same
				_, _ = w.Write([]byte(`{"id": 42, "job": 42, "status": "pending", "ignored_fields": {"limit": "web-dev-app001"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"id": 41, "job": 41, "status": "pending"}`))
		case r.Method == "GET" && len(parts) == 5 && parts[4] == "survey_spec":
			_, _ = w.Write([]byte(`{"spec": [{"variable": "agent", "default": "node_exporter", "required": true},
				{"variable": "port", "type": "integer", "default": 9100, "required": true},
				{"variable": "given", "default": "x", "required": true}, {"variable": "other", "default": "y"},
				{"variable": "tags", "type": "multiselect", "default": "a\nb", "required": true}]}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/cancel/"):
			cancelled = append(cancelled, r.URL.Path)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/inventory_sources/3/"):
			_, _ = w.Write([]byte(`{"id": 3, "inventory": 12}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := NewHTTPClient(srv.URL, "token", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	inv, err := c.InventoryOfSource(ctx, 3)
	if err != nil || inv != 12 {
		t.Fatalf("inventory of source = %d, %v", inv, err)
	}
	req := LaunchRequest{Limit: "web-dev-app001", Inventory: inv}
	if _, err := c.Launch(ctx, 7, false, req); err != nil {
		t.Fatal(err)
	}
	if launched["inventory"] != float64(12) || launched["limit"] != "web-dev-app001" {
		t.Fatalf("launched with %v", launched)
	}
	launched = nil
	if _, err := c.Launch(ctx, 7, false, LaunchRequest{Credentials: []int{4}}); !errors.Is(err, ErrNoCredentialPrompt) || launched != nil {
		t.Fatalf("credentials on a template that doesn't prompt for them = %v", err)
	}
	prompts["7"] = `{"ask_limit_on_launch": true, "ask_inventory_on_launch": true, "ask_credential_on_launch": true}`
	if _, err := c.Launch(ctx, 7, false, LaunchRequest{Credentials: []int{4, 5}}); err != nil || fmt.Sprint(launched["credentials"]) != "[4 5]" {
		t.Fatalf("launched with credentials %v, %v", launched["credentials"], err)
	}
	for id, want := range map[int]error{8: ErrNoInventoryPrompt, 9: ErrNoLimitPrompt} {
		launched = nil
		if _, err := c.Launch(ctx, id, false, req); !errors.Is(err, want) || launched != nil {
			t.Fatalf("template %d = %v (launched %v)", id, err, launched)
		}
	}
	// Required survey questions not answered get their defaults (AWX
	// doesn't apply them through its API); extra variables are never null.
	launched = nil
	if _, err := c.Launch(ctx, 12, false, LaunchRequest{ExtraVars: map[string]any{"given": "mine"}}); err != nil ||
		fmt.Sprint(launched["extra_vars"]) != "map[agent:node_exporter given:mine port:9100 tags:[a b]]" {
		t.Fatalf("launched with %v, %v", launched["extra_vars"], err)
	}
	launched = nil
	if _, err := c.Launch(ctx, 7, false, LaunchRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, sent := launched["extra_vars"]; sent {
		t.Fatalf("no variables sent as %v", launched["extra_vars"])
	}
	if _, err := c.Launch(ctx, 10, false, req); !errors.Is(err, ErrPromptIgnored) || len(cancelled) != 1 || !strings.HasSuffix(cancelled[0], "/jobs/42/cancel/") {
		t.Fatalf("limit ignored = %v (cancelled %v)", err, cancelled)
	}
}

// TestWorkflowNodeKinds: a node's job type says whether it has events.
func TestWorkflowNodeKinds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results": [
			{"id": 1, "job": 50, "summary_fields": {"unified_job_template": {"name": "sync rvav"}, "job": {"status": "successful", "type": "inventory_update"}}},
			{"id": 2, "job": 51, "summary_fields": {"unified_job_template": {"name": "linux-baseline"}, "job": {"status": "running", "type": "job"}}}]}`))
	}))
	defer srv.Close()
	c, _ := NewHTTPClient(srv.URL, "token", false)
	nodes, err := c.WorkflowNodes(context.Background(), 9)
	if err != nil || len(nodes) != 2 || nodes[0].Kind != "inventory_update" || nodes[1].Kind != "job" || nodes[1].JobID != 51 {
		t.Fatalf("nodes = %+v, %v", nodes, err)
	}
}

// A job AWX doesn't know (404) is ErrJobNotFound; another error isn't.
func TestGetJobNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/jobs/7/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail": "Not found."}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, _ := NewHTTPClient(srv.URL, "token", false)
	if _, err := c.GetJob(context.Background(), 7, false); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("404 = %v", err)
	}
	if _, err := c.GetJob(context.Background(), 8, false); err == nil || errors.Is(err, ErrJobNotFound) {
		t.Fatalf("500 = %v", err)
	}
}

// TestSetRvavCredential: the token goes in the source's rvav API credential,
// keeping the address AWX has; a source without one gets the organisation's,
// made with the address given, or else the default.
func TestSetRvavCredential(t *testing.T) {
	type call struct {
		method, path string
		body         map[string]any
	}
	run := func(t *testing.T, sourceCred string, haveNamed bool, rvavURL string) (RvavCredentialSet, []call, error) {
		var calls []call
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			path := strings.TrimPrefix(r.URL.Path, "/api/v2")
			calls = append(calls, call{r.Method, path, body})
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == "GET" && path == "/inventory_sources/3/":
				_, _ = fmt.Fprintf(w, `{"id": 3, "inventory": 12, "credential": %s}`, sourceCred)
			case r.Method == "GET" && path == "/credential_types/":
				_, _ = w.Write([]byte(`{"results": [{"id": 30, "name": "rvav API"}]}`))
			case r.Method == "GET" && path == "/credentials/40/":
				_, _ = w.Write([]byte(`{"id": 40, "name": "rvav (lab)", "credential_type": 30,
					"inputs": {"url": "http://rvav-api:8080", "token": "$encrypted$"}}`))
			case r.Method == "GET" && path == "/credentials/41/":
				_, _ = w.Write([]byte(`{"id": 41, "name": "Machine", "credential_type": 1, "inputs": {}}`))
			case r.Method == "GET" && path == "/inventories/12/":
				_, _ = w.Write([]byte(`{"id": 12, "organization": 5}`))
			case r.Method == "GET" && path == "/credentials/":
				if haveNamed && r.URL.Query().Get("organization") == "5" {
					_, _ = w.Write([]byte(`{"results": [{"id": 40, "name": "rvav API"}]}`))
					return
				}
				_, _ = w.Write([]byte(`{"results": []}`))
			case r.Method == "POST" && path == "/credentials/":
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id": 42, "name": "rvav API"}`))
			case r.Method == "PATCH":
				_, _ = w.Write([]byte(`{}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()
		c, err := NewHTTPClient(srv.URL, "token", false)
		if err != nil {
			t.Fatal(err)
		}
		set, err := c.SetRvavCredential(context.Background(), 3, rvavURL, "https://rvav.corp.example", "rvav_pat_new")
		return set, calls, err
	}
	writes := func(calls []call) []call {
		var out []call
		for _, c := range calls {
			if c.method != "GET" {
				out = append(out, c)
			}
		}
		return out
	}
	inputs := func(c call) map[string]any { m, _ := c.body["inputs"].(map[string]any); return m }

	t.Run("the source's own, keeping its address", func(t *testing.T) {
		set, calls, err := run(t, "40", false, "")
		w := writes(calls)
		if err != nil || set.ID != 40 || set.Attached || set.URL != "http://rvav-api:8080" || len(w) != 1 ||
			w[0].path != "/credentials/40/" || inputs(w[0])["token"] != "rvav_pat_new" || inputs(w[0])["url"] != "http://rvav-api:8080" {
			t.Fatalf("set = %+v, %v; writes %+v", set, err, w)
		}
	})
	t.Run("the source's own, at the address given", func(t *testing.T) {
		set, calls, err := run(t, "40", false, "https://rvav.lab.example")
		w := writes(calls)
		if err != nil || set.URL != "https://rvav.lab.example" || inputs(w[0])["url"] != "https://rvav.lab.example" {
			t.Fatalf("set = %+v, %v; writes %+v", set, err, w)
		}
	})
	t.Run("another kind on the source: the organisation's, set on it", func(t *testing.T) {
		set, calls, err := run(t, "41", true, "")
		w := writes(calls)
		if err != nil || set.ID != 40 || !set.Attached || set.URL != "http://rvav-api:8080" || len(w) != 2 ||
			w[0].path != "/credentials/40/" || w[1].path != "/inventory_sources/3/" || w[1].body["credential"] != float64(40) {
			t.Fatalf("set = %+v, %v; writes %+v", set, err, w)
		}
	})
	t.Run("none: made with the default address", func(t *testing.T) {
		set, calls, err := run(t, "null", false, "")
		w := writes(calls)
		if err != nil || set.ID != 42 || !set.Attached || set.URL != "https://rvav.corp.example" || len(w) != 2 ||
			w[0].method != "POST" || w[0].body["credential_type"] != float64(30) || w[0].body["organization"] != float64(5) ||
			inputs(w[0])["url"] != "https://rvav.corp.example" || w[1].body["credential"] != float64(42) {
			t.Fatalf("set = %+v, %v; writes %+v", set, err, w)
		}
	})
}

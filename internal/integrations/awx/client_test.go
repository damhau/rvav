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
// AWX ignored anyway is cancelled.
func TestLaunchPrompts(t *testing.T) {
	var launched map[string]any
	var cancelled []string
	prompts := map[string]string{
		"7":  `{"ask_limit_on_launch": true, "ask_inventory_on_launch": true}`,
		"8":  `{"ask_limit_on_launch": true, "ask_inventory_on_launch": false}`,
		"9":  `{"ask_limit_on_launch": false, "ask_inventory_on_launch": true}`,
		"10": `{"ask_limit_on_launch": true, "ask_inventory_on_launch": true}`,
		"11": `{"ask_limit_on_launch": true, "ask_inventory_on_launch": true, "ask_credential_on_launch": false}`,
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

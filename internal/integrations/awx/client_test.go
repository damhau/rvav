package awx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLaunchInventory: a launch on another inventory checks the template
// prompts for one, and sends it.
func TestLaunchInventory(t *testing.T) {
	var launched map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/job_templates/7/launch/"):
			_, _ = w.Write([]byte(`{"ask_inventory_on_launch": true}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/job_templates/8/launch/"):
			_, _ = w.Write([]byte(`{"ask_inventory_on_launch": false}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/launch/"):
			_ = json.NewDecoder(r.Body).Decode(&launched)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 41, "job": 41, "status": "pending"}`))
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
	if _, err := c.Launch(ctx, 7, false, LaunchRequest{Limit: "web-dev-app001", Inventory: inv}); err != nil {
		t.Fatal(err)
	}
	if launched["inventory"] != float64(12) || launched["limit"] != "web-dev-app001" {
		t.Fatalf("launched with %v", launched)
	}
	launched = nil
	if _, err := c.Launch(ctx, 8, false, LaunchRequest{Limit: "web-dev-app001", Inventory: inv}); !errors.Is(err, ErrNoInventoryPrompt) || launched != nil {
		t.Fatalf("a template that doesn't prompt for an inventory = %v (launched %v)", err, launched)
	}
}

package vra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func fake(t *testing.T, e Estate) (*FakeServer, string) {
	t.Helper()
	f := NewFakeServer(e)
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

// TestRead: the whole estate read through the API, signed in with a user
// or a refresh token: templates with their content and every version,
// their forms, catalog items, sharing per project, policies, deployments
// with their machines.
func TestRead(t *testing.T) {
	ctx := context.Background()
	want := SampleEstate(time.Now())
	_, url := fake(t, want)
	for name, o := range map[string]Options{
		"user":          {URL: url, Username: "rvav-importer", Password: "vra", Domain: "corp.example"},
		"refresh token": {URL: url, Token: FakeRefreshToken},
	} {
		c, err := New(o)
		if err != nil {
			t.Fatal(err)
		}
		e, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(e.Projects) != 3 || len(e.Blueprints) != 5 || len(e.Forms) != 2 || len(e.CatalogItems) != 6 ||
			len(e.Entitlements) != len(want.Entitlements) || len(e.Policies) != 5 || len(e.Deployments) != 5 {
			t.Fatalf("%s: read %d projects, %d templates, %d forms, %d items, %d entitlements, %d policies, %d deployments", name,
				len(e.Projects), len(e.Blueprints), len(e.Forms), len(e.CatalogItems), len(e.Entitlements), len(e.Policies), len(e.Deployments))
		}
		rhel := e.Blueprints[slices.IndexFunc(e.Blueprints, func(b Blueprint) bool { return b.ID == "bp-rhel9" })]
		if !strings.Contains(rhel.Content, "cloudConfig") || len(rhel.Versions) != 2 || rhel.Versions[0].Content == "" ||
			strings.Contains(rhel.Versions[0].Content, "cloudConfig") {
			t.Fatalf("%s: rhel9 = %+v", name, rhel)
		}
		d := e.Deployments[0]
		if len(d.Resources) != 1 || d.Resources[0].Properties["resourceName"] != "vra-web-001" || d.LeaseExpireAt == nil {
			t.Fatalf("%s: deployment = %+v", name, d)
		}
		// vRO, and what runs it.
		if len(e.Subscriptions) != 4 || !e.Subscriptions[1].Blocking || len(e.ResourceActions) != 1 ||
			e.ResourceActions[0].RunnableItem.ID != "wf-decom" || len(e.Workflows) != 7 || len(e.Actions) != 4 {
			t.Fatalf("%s: %d subscriptions, %d day-2 actions, %d workflows, %d actions", name, len(e.Subscriptions),
				len(e.ResourceActions), len(e.Workflows), len(e.Actions))
		}
		for i, w := range e.Workflows {
			x := want.Workflows[i]
			if IsVROLibrary(x.Category) {
				if w.Category != x.Category || w.Content != "" || w.Runs != 0 {
					t.Fatalf("%s: the library's %+v was read whole", name, w)
				}
				continue
			}
			if w.ID != x.ID || w.Category != x.Category || len(w.Inputs) != len(x.Inputs) || w.Content != x.Content || w.Runs != x.Runs ||
				(x.LastRun == nil) != (w.LastRun == nil) || x.LastRun != nil && !w.LastRun.Equal(*x.LastRun) {
				t.Fatalf("%s: workflow %+v, want %+v", name, w, x)
			}
		}
		if a := e.Actions[0]; a.FQN() != "com.corp.db/getVersions" || a.Inputs[0].Name != "environment" || !strings.Contains(a.Script, "RESTHostManager") {
			t.Fatalf("%s: action = %+v", name, a)
		}
	}
}

// TestPagesAndSignIn: lists longer than a page come whole, in both paging
// styles; an expired token is renewed once; refused credentials say so.
func TestPagesAndSignIn(t *testing.T) {
	ctx := context.Background()
	e := Estate{}
	for i := range 250 {
		e.Projects = append(e.Projects, Project{ID: fmt.Sprintf("p%03d", i), Name: fmt.Sprintf("Project %d", i)})
		e.Deployments = append(e.Deployments, Deployment{ID: fmt.Sprintf("d%03d", i), ProjectID: "p000"})
	}
	f, url := fake(t, e)
	c, err := New(Options{URL: url, Token: FakeRefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := c.Projects(ctx)
	if err != nil || len(ps) != 250 || ps[249].ID != "p249" {
		t.Fatalf("projects: %d, %v", len(ps), err)
	}
	f.Expire()
	ds, err := c.Deployments(ctx)
	if err != nil || len(ds) != 250 {
		t.Fatalf("deployments after the token expired: %d, %v", len(ds), err)
	}
	if form, err := c.RequestForm(ctx, "com.vmw.blueprint", "none"); err != nil || form != nil {
		t.Fatalf("no form = %v, %v", form, err)
	}
	bad, _ := New(Options{URL: url, Username: "rvav-importer", Password: "wrong", Domain: "corp.example"})
	_, err = bad.Projects(ctx)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || !strings.Contains(err.Error(), "sign-in") {
		t.Fatalf("wrong password = %v", err)
	}
	if _, err := New(Options{URL: url}); err == nil {
		t.Fatal("no credentials accepted")
	}
}

// TestPagingWithoutTotals: a list without totalElements, pages smaller
// than asked, vRO's lists, and a server that ignores paging (P2.7).
func TestPagingWithoutTotals(t *testing.T) {
	ctx := context.Background()
	items := func(from, n int) []map[string]string {
		out := []map[string]string{}
		for i := from; i < min(from+n, 250); i++ {
			out = append(out, map[string]string{"id": fmt.Sprintf("x%03d", i), "name": fmt.Sprintf("X %d", i)})
		}
		return out
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /iaas/api/login", func(w http.ResponseWriter, _ *http.Request) { write(w, map[string]string{"token": "t"}) })
	// No totalElements.
	mux.HandleFunc("GET /iaas/api/projects", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"content": items(intParam(r.URL.Query(), "$skip", 0), 100)})
	})
	// Pages of 40, though 100 were asked, with last and totalPages.
	mux.HandleFunc("GET /deployment/api/deployments", func(w http.ResponseWriter, r *http.Request) {
		p := intParam(r.URL.Query(), "page", 0)
		write(w, map[string]any{"content": items(p*40, 40), "last": (p+1)*40 >= 250, "totalPages": 7})
	})
	// Ignores paging: the same page for ever.
	mux.HandleFunc("GET /policy/api/policies", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"content": items(0, 100), "last": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, err := New(Options{URL: srv.URL, Token: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if ps, err := c.Projects(ctx); err != nil || len(ps) != 250 {
		t.Fatalf("without totals: %d, %v", len(ps), err)
	}
	if ds, err := c.Deployments(ctx); err != nil || len(ds) != 250 || ds[249].ID != "x249" {
		t.Fatalf("small pages: %d, %v", len(ds), err)
	}
	if _, err := c.Policies(ctx); !errors.Is(err, errPaging) {
		t.Fatalf("a server ignoring paging: %v", err)
	}
}

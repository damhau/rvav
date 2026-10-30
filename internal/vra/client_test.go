package vra

import (
	"context"
	"errors"
	"fmt"
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

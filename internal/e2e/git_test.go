package e2e

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/gitrepo"
)

// bareRepo is an empty bare repository, served in process (rvav's image
// has no git binary).
func bareRepo(t *testing.T) string {
	t.Helper()
	gitrepo.UseInProcessFiles()
	dir := filepath.Join(t.TempDir(), "catalog.git")
	if _, err := git.PlainInit(dir, true); err != nil {
		t.Fatal(err)
	}
	return "file://" + dir
}

// TestGitIntegration: the catalog's repository set from Administration ›
// Integrations, its token sealed, and tested: empty, then at a commit, then
// on a branch it doesn't have (C1.1, ADR-0029).
func TestGitIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		url := bareRepo(t)

		var it api.Integration
		h.json("PUT", "/api/v1/admin/integrations/git", admin, api.IntegrationWrite{Settings: map[string]any{"url": url,
			"branch": "main", "dir": "catalog", "username": "rvav-sync"}, Secret: new("glpat-s3cret")}, 200, &it)
		if it.Kind != "git" || !it.SecretSet || it.Settings["url"] != url || it.Settings["credential"] != nil {
			t.Fatalf("saved git = %+v", it)
		}
		var list api.IntegrationList
		h.json("GET", "/api/v1/admin/integrations", admin, nil, 200, &list)
		if !slices.ContainsFunc(list.Items, func(x api.Integration) bool { return x.Kind == "git" && x.Configured }) {
			t.Fatalf("integrations = %+v", list.Items)
		}

		var test api.IntegrationTest
		h.json("POST", "/api/v1/admin/integrations/git/test", admin, nil, 200, &test)
		if !test.Ok || len(test.Checks) != 3 || test.Checks[2].Message != "Empty: rvav's first commit makes it." {
			t.Fatalf("an empty repository's test = %+v", test)
		}

		repo, err := gitrepo.New(gitrepo.Options{URL: url, Cache: filepath.Join(t.TempDir(), "clone")})
		if err != nil {
			t.Fatal(err)
		}
		first, err := repo.Commit(ctx, "", []gitrepo.Change{{Path: "catalog/rhel9.yaml", Content: []byte("slug: rhel9\n")}},
			gitrepo.Person{Name: "Léa Martin", Email: "lea.martin@corp.example"}, "Add rhel9")
		if err != nil {
			t.Fatal(err)
		}
		h.json("POST", "/api/v1/admin/integrations/git/test", admin, nil, 200, &test)
		if !test.Ok || test.Checks[2].Message != "At "+first.SHA[:12]+"; items are files in catalog/." {
			t.Fatalf("the test at a commit = %+v", test)
		}
		h.json("POST", "/api/v1/admin/integrations/git/test", admin, api.IntegrationWrite{Settings: map[string]any{"url": url,
			"branch": "release"}}, 200, &test)
		if test.Ok || !strings.Contains(test.Checks[1].Message, "no branch release (it has main)") {
			t.Fatalf("a missing branch's test = %+v", test)
		}
		// Not a setting of Git; the secret is never a setting.
		h.json("PUT", "/api/v1/admin/integrations/git", admin, api.IntegrationWrite{Settings: map[string]any{"credential": "x"}}, 422, nil)
		h.json("PUT", "/api/v1/admin/integrations/git", admin, api.IntegrationWrite{Settings: map[string]any{"interval": "1m"}}, 422, nil)
	})
}

package e2e

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/core"
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

// useGit keeps the catalog in a new local repository; reader is another
// clone of it, to see what rvav committed and to commit as someone else.
func useGit(h *harness) (url string, reader *gitrepo.Repo) {
	h.t.Helper()
	url = bareRepo(h.t)
	h.svc.Config.Git = config.GitConfig{URL: url, Branch: "main", Dir: "catalog", Committer: "rvav@corp.example",
		Interval: 5 * time.Minute, Cache: filepath.Join(h.t.TempDir(), "rvav-clone")}
	reader, err := gitrepo.New(gitrepo.Options{URL: url, Cache: filepath.Join(h.t.TempDir(), "reader")})
	if err != nil {
		h.t.Fatal(err)
	}
	return url, reader
}

// syncGit reads the repository once, as the sync process does.
func (h *harness) syncGit() core.GitSyncReport {
	h.t.Helper()
	rep, err := h.svc.SyncCatalogGit(h.ctx)
	if err != nil {
		h.t.Fatalf("sync the catalog's repository: %v", err)
	}
	return rep
}

// gitItem is the item's Git state.
func (h *harness) gitItem(slug string) api.CatalogItemGit {
	h.t.Helper()
	var it api.AdminCatalogItem
	h.json("GET", "/api/v1/admin/catalog/items/"+slug, "lmartin", nil, 200, &it)
	if it.Git == nil {
		h.t.Fatalf("%s has no Git state", slug)
	}
	return *it.Git
}

// fileDoc is an item's file on the branch, as YAML.
func fileDoc(t *testing.T, snap gitrepo.Snapshot, slug string) map[string]any {
	t.Helper()
	f, ok := snap.Files[slug]
	if !ok {
		t.Fatalf("the repository has no file of %s: %v", slug, slices.Collect(maps.Keys(snap.Files)))
	}
	var doc map[string]any
	if err := yaml.Unmarshal(f.Content, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// lastVersion is the number and state of a file's version.
func lastVersion(doc map[string]any) (string, string) {
	vs, _ := doc["versions"].([]any)
	if len(vs) != 1 {
		return "", fmt.Sprintf("%d versions", len(vs))
	}
	v, _ := vs[0].(map[string]any)
	n, _ := v["number"].(string)
	s, _ := v["state"].(string)
	return n, s
}

// TestCatalogGitCommits: rvav's catalog committed to its repository (C1.2,
// ADR-0029). The first read adds each item it has; publishing,
// deprecating, rolling back, changing details or the slug, and deleting
// commit the item's file, the administrator as author and the changelog as
// message, without waiting for Git. A commit refused because the branch
// moved is made again on its new head; a change of the file there is a
// conflict, never overwritten. A repository that doesn't answer is shown
// on the item, tried again, and audited once.
func TestCatalogGitCommits(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "lmartin"
		url, reader := useGit(h)
		var items api.AdminCatalogItemPage
		h.json("GET", "/api/v1/admin/catalog/items?source=native&state=published&limit=100", admin, nil, 200, &items)
		if len(items.Items) < 2 {
			t.Fatalf("seeded items = %d", len(items.Items))
		}
		var rhel api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server", admin, nil, 200, &rhel)
		first := *rhel.PublishedVersion

		// The first read adds rvav's items, by rvav.
		rep := h.syncGit()
		if rep.Committed != len(items.Items) || rep.Failed != 0 {
			t.Fatalf("first read = %+v, want %d commits", rep, len(items.Items))
		}
		snap, err := reader.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(snap.Files) != len(items.Items) {
			t.Fatalf("the repository has %d files", len(snap.Files))
		}
		if n, s := lastVersion(fileDoc(t, snap, "rhel9-server")); n != first.Number || s != "published" {
			t.Fatalf("rhel9-server's file holds %s %s", n, s)
		}
		g := h.gitItem("rhel9-server")
		if g.State != api.CatalogItemGitStateSynced || g.Path != "catalog/rhel9-server.yaml" || g.LastCommit == nil ||
			g.LastCommit.Author != "rvav" || g.LastCommit.Message != "Add rhel9-server from rvav" {
			t.Fatalf("rhel9-server's Git state = %+v", g)
		}
		if again := h.syncGit(); again.Committed != 0 || again.Head != rep.Head {
			t.Fatalf("a second read committed: %+v", again)
		}
		if due, err := h.store.GitDue(ctx, time.Minute); err != nil || due {
			t.Fatalf("due after a read = %v, %v", due, err)
		}

		// Publishing a new version commits it, by the administrator, with the
		// changelog; a draft is never committed.
		var draft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions", admin, api.CatalogItemVersionCreate{}, 201, &draft)
		if h.syncGit().Committed != 0 {
			t.Fatal("a draft was committed")
		}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions/"+draft.Id+"/publish", admin,
			api.CatalogItemVersionPublish{Changelog: new("Larger root disk.")}, 200, &item)
		if item.Git == nil || item.Git.State != api.CatalogItemGitStateWaiting {
			t.Fatalf("just published, the Git state = %+v", item.Git)
		}
		if due, _ := h.store.GitDue(ctx, time.Minute); !due {
			t.Fatal("a change to commit isn't due")
		}
		h.syncGit()
		snap, _ = reader.Read(ctx)
		if n, s := lastVersion(fileDoc(t, snap, "rhel9-server")); n != draft.Number || s != "published" {
			t.Fatalf("after publishing, the file holds %s %s", n, s)
		}
		if snap.Head.Author != "Léa Martin" || snap.Head.Email != "lea.martin@corp.example" ||
			snap.Head.Message != "Publish rhel9-server "+draft.Number+"\n\nLarger root disk." {
			t.Fatalf("the commit = %+v", snap.Head)
		}
		if g := h.gitItem("rhel9-server"); g.State != api.CatalogItemGitStateSynced || g.LastCommit.Sha != snap.Head.SHA {
			t.Fatalf("after the commit = %+v", g)
		}
		if bad, err := reader.LastCommit(ctx, "catalog/rhel9-server.yaml"); err != nil || bad.SHA != snap.Head.SHA {
			t.Fatalf("the file's last commit = %+v, %v", bad, err)
		}

		// The branch moves between rvav's read and its commit: committed on
		// the new head, the other file kept.
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions/"+draft.Id+"/deprecate", admin, nil, 200, nil)
		moved := false
		h.svc.GitBeforeCommit = func(string) {
			if moved {
				return
			}
			moved = true
			s, _ := reader.Read(ctx)
			if _, err := reader.Commit(ctx, s.Head.SHA, []gitrepo.Change{{Path: "README.md", Content: []byte("# Catalog\n")}},
				gitrepo.Person{Name: "Thomas Nguyen", Email: "thomas.nguyen@corp.example"}, "Add a README"); err != nil {
				t.Error(err)
			}
		}
		if rep := h.syncGit(); rep.Committed != 1 || rep.Conflicts != 0 {
			t.Fatalf("a commit on a moved branch = %+v", rep)
		}
		snap, _ = reader.Read(ctx)
		if n, s := lastVersion(fileDoc(t, snap, "rhel9-server")); n != draft.Number || s != "deprecated" ||
			snap.Head.Message != "Deprecate rhel9-server "+draft.Number {
			t.Fatalf("after deprecating: %s %s, %q", n, s, snap.Head.Message)
		}
		if parent, err := reader.LastCommit(ctx, "README.md"); err != nil || parent.Message != "Add a README" {
			t.Fatalf("the README's commit = %+v, %v", parent, err)
		}

		// Rolled back to the first: committed.
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions/"+first.Id+"/publish", admin, nil, 200, nil)
		h.svc.GitBeforeCommit = nil
		h.syncGit()
		snap, _ = reader.Read(ctx)
		if n, s := lastVersion(fileDoc(t, snap, "rhel9-server")); n != first.Number || s != "published" ||
			snap.Head.Message != "Roll rhel9-server back to "+first.Number {
			t.Fatalf("after the rollback: %s %s, %q", n, s, snap.Head.Message)
		}

		// The file changes in the repository between rvav's read and its
		// commit: a conflict, the repository's change kept.
		h.json("PATCH", "/api/v1/admin/catalog/items/rhel9-server", admin, api.CatalogItemUpdate{Summary: new("RHEL 9, hardened.")}, 200, nil)
		h.svc.GitBeforeCommit = func(string) {
			h.svc.GitBeforeCommit = nil
			s, _ := reader.Read(ctx)
			theirs := strings.Replace(string(s.Files["rhel9-server"].Content), "summary:", "summary: Theirs.\n  was:", 1)
			if _, err := reader.Commit(ctx, s.Head.SHA, []gitrepo.Change{{Path: "catalog/rhel9-server.yaml", Content: []byte(theirs)}},
				gitrepo.Person{Name: "Thomas Nguyen", Email: "thomas.nguyen@corp.example"}, "Their summary"); err != nil {
				t.Error(err)
			}
		}
		if rep := h.syncGit(); rep.Committed != 0 || rep.Conflicts != 1 {
			t.Fatalf("a change on both sides = %+v", rep)
		}
		g = h.gitItem("rhel9-server")
		if g.State != api.CatalogItemGitStateConflict || g.Note == nil || !strings.Contains(*g.Note, "Changed in the repository") ||
			g.LastCommit == nil || g.LastCommit.Message != "Their summary" {
			t.Fatalf("the conflict = %+v", g)
		}
		snap, _ = reader.Read(ctx)
		if snap.Head.Message != "Their summary" || h.syncGit().Committed != 0 {
			t.Fatal("rvav overwrote the repository's change")
		}
		if n := h.auditCount("catalog.git.conflict"); n != 1 {
			t.Fatalf("%d conflicts audited", n)
		}
		// Set aside for C1.3's resolution: the other items go on.
		if _, err := h.store.Pool.Exec(ctx, `delete from catalog_git_files where path = 'catalog/rhel9-server.yaml'`); err != nil {
			t.Fatal(err)
		}

		// The repository doesn't answer: publishing doesn't wait, the
		// failure is shown, tried again later, and audited once.
		slug := items.Items[1].Slug
		if slug == "rhel9-server" {
			slug = items.Items[0].Slug
		}
		dir := strings.TrimPrefix(url, "file://")
		if err := os.Rename(dir, dir+".away"); err != nil {
			t.Fatal(err)
		}
		var d2 api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions", admin, api.CatalogItemVersionCreate{}, 201, &d2)
		h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+d2.Id+"/publish", admin, nil, 200, nil)
		if _, err := h.svc.SyncCatalogGit(ctx); err == nil {
			t.Fatal("a repository that isn't there was read")
		}
		g = h.gitItem(slug)
		if g.State != api.CatalogItemGitStateFailing || g.Error == nil || g.Attempts != 1 || g.NextAttemptAt == nil ||
			!g.NextAttemptAt.After(time.Now()) {
			t.Fatalf("a failed commit = %+v", g)
		}
		if due, _ := h.store.GitDue(ctx, time.Hour); due {
			t.Fatal("a failed commit is tried again at once")
		}
		if _, err := h.store.Pool.Exec(ctx, `update catalog_git_files set next_attempt_at = now()`); err != nil {
			t.Fatal(err)
		}
		_, _ = h.svc.SyncCatalogGit(ctx)
		if g := h.gitItem(slug); g.Attempts != 2 {
			t.Fatalf("tried again = %+v", g)
		}
		if n := h.auditCount("catalog.git.commit_failed"); n != 1 {
			t.Fatalf("%d failures audited, want 1", n)
		}
		if err := os.Rename(dir+".away", dir); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.Pool.Exec(ctx, `update catalog_git_files set next_attempt_at = now()`); err != nil {
			t.Fatal(err)
		}
		if rep := h.syncGit(); rep.Committed != 1 {
			t.Fatalf("once the repository answers = %+v", rep)
		}
		if g := h.gitItem(slug); g.State != api.CatalogItemGitStateSynced || g.Error != nil || g.Attempts != 0 {
			t.Fatalf("committed at last = %+v", g)
		}

		// A new slug moves the file; a deleted item's file is removed.
		h.json("PATCH", "/api/v1/admin/catalog/items/"+slug, admin, api.CatalogItemUpdate{Slug: new(slug + "-v2")}, 200, nil)
		h.syncGit()
		snap, _ = reader.Read(ctx)
		if _, ok := snap.Files[slug]; ok {
			t.Fatalf("%s's old file is still there", slug)
		}
		fileDoc(t, snap, slug+"-v2")
		if g := h.gitItem(slug + "-v2"); g.Path != "catalog/"+slug+"-v2.yaml" {
			t.Fatalf("after the new slug = %+v", g)
		}
		h.json("DELETE", "/api/v1/admin/catalog/items/"+slug+"-v2", admin, nil, 204, nil)
		h.syncGit()
		snap, _ = reader.Read(ctx)
		if _, ok := snap.Files[slug+"-v2"]; ok || snap.Head.Message != "Remove "+slug+"-v2" {
			t.Fatalf("after the deletion: %v, %q", slices.Collect(maps.Keys(snap.Files)), snap.Head.Message)
		}
		if n := h.auditCount("catalog.git.commit"); n < len(items.Items)+5 {
			t.Fatalf("%d commits audited", n)
		}
	})
}

// auditCount is how many events of the action were audited.
func (h *harness) auditCount(action string) int {
	h.t.Helper()
	var n int
	if err := h.store.Pool.QueryRow(h.ctx, `select count(*) from audit_events where action = $1`, action).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

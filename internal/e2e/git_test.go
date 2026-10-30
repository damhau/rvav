package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
		// Settled for rvav: committed over the repository's change.
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/git/resolve", admin, map[string]string{"take": "rvav"}, 200, nil)
		if rep := h.syncGit(); rep.Committed != 1 {
			t.Fatalf("settled for rvav = %+v", rep)
		}
		snap, _ = reader.Read(ctx)
		if strings.Contains(string(snap.Files["rhel9-server"].Content), "Theirs.") || snap.Head.Author != "Léa Martin" ||
			snap.Head.Message != "Keep rhel9-server as rvav has it, over the repository's change" {
			t.Fatalf("after settling: %+v", snap.Head)
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

// editFile changes an item's file as someone editing the repository would.
func editFile(t *testing.T, content []byte, change func(doc map[string]any, version map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatal(err)
	}
	vs := doc["versions"].([]any)
	change(doc, vs[len(vs)-1].(map[string]any))
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// relabel gives the version's first field (by name) another label.
func relabel(label string) func(_, v map[string]any) {
	return func(_, v map[string]any) {
		fields := v["ui_schema"].(map[string]any)["fields"].(map[string]any)
		name := slices.Sorted(maps.Keys(fields))[0]
		fields[name].(map[string]any)["label"] = label
	}
}

// push commits files to the repository as a teammate would.
func push(t *testing.T, ctx context.Context, reader *gitrepo.Repo, message string, changes ...gitrepo.Change) gitrepo.Commit {
	t.Helper()
	snap, err := reader.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := reader.Commit(ctx, snap.Head.SHA, changes, gitrepo.Person{Name: "Thomas Nguyen", Email: "thomas.nguyen@corp.example"}, message)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fileOf is an item's file on the branch.
func fileOf(t *testing.T, ctx context.Context, reader *gitrepo.Repo, slug string) []byte {
	t.Helper()
	snap, err := reader.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return snap.Files[slug].Content
}

// TestCatalogGitDrafts: the repository proposes (C1.3, ADR-0029). A file
// changed there becomes a draft of its item, with its commit, never
// published by itself; changed again, the untouched draft is made again;
// published in rvav, it is committed back. Details changed there follow
// rvav. A new file becomes a new item, a draft; a file the import refuses,
// or whose name isn't its slug, is reported with its problems; a deleted
// file is reported, the item kept, its file added back when it changes in
// rvav. A change on both sides is a conflict, shown with both sides and
// settled either way. The Git server's webhook asks for a read, and an
// administrator can read the repository now.
func TestCatalogGitDrafts(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "lmartin"
		_, reader := useGit(h)
		var items api.AdminCatalogItemPage
		h.json("GET", "/api/v1/admin/catalog/items?source=native&state=published&limit=100", admin, nil, 200, &items)
		var slugs []string
		for _, it := range items.Items {
			if it.Slug != "rhel9-server" {
				slugs = append(slugs, it.Slug)
			}
		}
		if len(slugs) < 3 {
			t.Fatalf("seeded items: %v", slugs)
		}
		h.syncGit()
		item := func(slug string) api.AdminCatalogItem {
			var it api.AdminCatalogItem
			h.json("GET", "/api/v1/admin/catalog/items/"+slug, admin, nil, 200, &it)
			return it
		}
		rhel := item("rhel9-server")

		// A change in the repository: a draft, not published.
		theirs := editFile(t, fileOf(t, ctx, reader, "rhel9-server"), relabel("Their label"))
		commit := push(t, ctx, reader, "Clearer label", gitrepo.Change{Path: "catalog/rhel9-server.yaml", Content: theirs})
		if rep := h.syncGit(); rep.Drafts != 1 || rep.Committed != 0 {
			t.Fatalf("a change in the repository = %+v", rep)
		}
		it := item("rhel9-server")
		if it.DraftVersion == nil || it.PublishedVersion.Id != rhel.PublishedVersion.Id || it.Git.State != api.CatalogItemGitStateDraft ||
			deref(it.Git.DraftVersionId) != it.DraftVersion.Id || it.Git.LastCommit == nil || it.Git.LastCommit.Sha != commit.SHA ||
			it.Git.LastCommit.Author != "Thomas Nguyen" {
			t.Fatalf("after the repository's change = %+v, git %+v", it, it.Git)
		}
		var detail api.CatalogItemGitDetail
		h.json("GET", "/api/v1/admin/catalog/items/rhel9-server/git", admin, nil, 200, &detail)
		if detail.Repository == nil || detail.Rvav == nil || !slices.ContainsFunc(detail.Diff, func(l api.DiffLine) bool {
			return l.Op == api.DiffLineOpAdded && strings.Contains(l.Text, "Their label")
		}) || !slices.ContainsFunc(detail.Diff, func(l api.DiffLine) bool { return l.Op == api.DiffLineOpRemoved }) {
			t.Fatalf("the draft's diff = %+v", detail.Diff)
		}
		if rep := h.syncGit(); rep.Drafts != 0 {
			t.Fatalf("read again = %+v", rep)
		}
		// Changed again: the untouched draft is made again.
		first := it.DraftVersion.Id
		theirs = editFile(t, theirs, relabel("Their better label"))
		push(t, ctx, reader, "Better label", gitrepo.Change{Path: "catalog/rhel9-server.yaml", Content: theirs})
		if rep := h.syncGit(); rep.Drafts != 1 || rep.Conflicts != 0 {
			t.Fatalf("changed again = %+v", rep)
		}
		it = item("rhel9-server")
		if it.DraftVersion == nil || it.DraftVersion.Id == first || it.Git.LastCommit.Message != "Better label" {
			t.Fatalf("the draft made again = %+v", it.DraftVersion)
		}
		// Published in rvav: committed back, and both agree.
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions/"+it.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		if rep := h.syncGit(); rep.Committed != 1 {
			t.Fatalf("publishing the repository's draft = %+v", rep)
		}
		if def, _ := core.SameItemFile(fileOf(t, ctx, reader, "rhel9-server"), theirs); !def || item("rhel9-server").Git.State != api.CatalogItemGitStateSynced {
			t.Fatal("rvav's commit of the published draft isn't the repository's change")
		}
		var synced api.CatalogItemGitDetail
		h.json("GET", "/api/v1/admin/catalog/items/"+slugs[0]+"/git", admin, nil, 200, &synced)
		if synced.Repository != nil || synced.Rvav == nil || len(synced.Diff) != 0 {
			t.Fatalf("a synced file's detail = %+v", synced)
		}

		// Details changed in the repository follow rvav: put back.
		renamed := editFile(t, fileOf(t, ctx, reader, slugs[0]), func(doc, _ map[string]any) {
			doc["metadata"].(map[string]any)["name"] = "Their name"
		})
		push(t, ctx, reader, "Rename it", gitrepo.Change{Path: "catalog/" + slugs[0] + ".yaml", Content: renamed})
		if rep := h.syncGit(); rep.Drafts != 0 || rep.Committed != 1 {
			t.Fatalf("a change of details = %+v", rep)
		}
		if item(slugs[0]).Name == "Their name" || strings.Contains(string(fileOf(t, ctx, reader, slugs[0])), "Their name") {
			t.Fatal("the repository's details were taken")
		}
		if n := h.auditCount("catalog.git.details_kept"); n != 1 {
			t.Fatalf("details kept audited %d times", n)
		}

		// A new file: a new item, a draft. A file the import refuses, and
		// one whose name isn't its slug, are reported.
		copied := editFile(t, fileOf(t, ctx, reader, "rhel9-server"), func(doc, v map[string]any) {
			m := doc["metadata"].(map[string]any)
			m["slug"], m["name"] = "rhel9-hardened", "RHEL 9, hardened"
			v["number"] = "1.0"
		})
		broken := editFile(t, copied, func(doc, _ map[string]any) {
			m := doc["metadata"].(map[string]any)
			m["slug"], m["category"] = "broken", "no-such-category"
		})
		mismatch := editFile(t, copied, func(doc, _ map[string]any) { doc["metadata"].(map[string]any)["slug"] = "other" })
		push(t, ctx, reader, "New items", gitrepo.Change{Path: "catalog/rhel9-hardened.yaml", Content: copied},
			gitrepo.Change{Path: "catalog/broken.yaml", Content: broken}, gitrepo.Change{Path: "catalog/mismatch.yaml", Content: mismatch},
			gitrepo.Change{Path: "catalog/notes.txt", Content: []byte("not an item")})
		if rep := h.syncGit(); rep.Drafts != 1 || rep.Refused != 2 {
			t.Fatalf("new files = %+v", rep)
		}
		hardened := item("rhel9-hardened")
		if hardened.PublishedVersion != nil || hardened.DraftVersion == nil || len(hardened.Versions) != 1 ||
			hardened.Git.State != api.CatalogItemGitStateDraft || hardened.Name != "RHEL 9, hardened" {
			t.Fatalf("the new item = %+v", hardened)
		}
		var status api.CatalogGitStatus
		h.json("GET", "/api/v1/admin/catalog/git", admin, nil, 200, &status)
		byPath := map[string]api.CatalogGitFile{}
		for _, f := range status.Files {
			byPath[f.Path] = f
		}
		if b := byPath["catalog/broken.yaml"]; b.State != api.CatalogGitFileStateRefused || b.Item != nil || b.Problems == nil ||
			!slices.ContainsFunc(*b.Problems, func(e api.FieldError) bool { return strings.Contains(e.Message, "no-such-category") }) {
			t.Fatalf("the refused file = %+v", b)
		}
		if m := byPath["catalog/mismatch.yaml"]; m.State != api.CatalogGitFileStateRefused || m.Problems == nil ||
			!strings.Contains((*m.Problems)[0].Message, "must be the same") {
			t.Fatalf("the misnamed file = %+v", m)
		}
		if _, ok := byPath["catalog/notes.txt"]; ok || !status.Configured || deref(status.Head) == "" || status.ReadAt == nil {
			t.Fatalf("the status = %+v", status)
		}
		// Fixed in the repository: an item; the other removed: forgotten.
		fixed := editFile(t, broken, func(doc, _ map[string]any) { doc["metadata"].(map[string]any)["category"] = rhel.Category })
		push(t, ctx, reader, "Fix and clean up", gitrepo.Change{Path: "catalog/broken.yaml", Content: fixed},
			gitrepo.Change{Path: "catalog/mismatch.yaml"})
		if rep := h.syncGit(); rep.Drafts != 1 || rep.Refused != 0 || rep.Deleted != 0 {
			t.Fatalf("fixed = %+v", rep)
		}
		h.json("GET", "/api/v1/admin/catalog/git", admin, nil, 200, &status)
		if slices.ContainsFunc(status.Files, func(f api.CatalogGitFile) bool { return f.Path == "catalog/mismatch.yaml" }) ||
			item("broken").Git.State != api.CatalogItemGitStateDraft {
			t.Fatalf("after the fix = %+v", status.Files)
		}

		// A file deleted in the repository: reported, the item kept, the
		// file added back when the item changes in rvav.
		gone := slugs[1]
		push(t, ctx, reader, "Remove "+gone, gitrepo.Change{Path: "catalog/" + gone + ".yaml"})
		if rep := h.syncGit(); rep.Deleted != 1 || rep.Committed != 0 {
			t.Fatalf("a deleted file = %+v", rep)
		}
		if g := item(gone); g.PublishedVersion == nil || g.Git.State != api.CatalogItemGitStateDeleted {
			t.Fatalf("the deleted file's item = %+v", g.Git)
		}
		h.json("PATCH", "/api/v1/admin/catalog/items/"+gone, admin, api.CatalogItemUpdate{Summary: new("Back again.")}, 200, nil)
		if rep := h.syncGit(); rep.Committed != 1 || fileOf(t, ctx, reader, gone) == nil {
			t.Fatalf("added back = %+v", rep)
		}

		// rvav has a draft open, and the repository changes the file: a
		// conflict, settled for the repository (rvav's draft discarded).
		both := slugs[2]
		var mine api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/"+both+"/versions", admin, api.CatalogItemVersionCreate{Changelog: new("Mine.")}, 201, &mine)
		push(t, ctx, reader, "Theirs", gitrepo.Change{Path: "catalog/" + both + ".yaml",
			Content: editFile(t, fileOf(t, ctx, reader, both), relabel("Theirs"))})
		if rep := h.syncGit(); rep.Conflicts != 1 || rep.Drafts != 0 {
			t.Fatalf("a draft open on both sides = %+v", rep)
		}
		if g := item(both).Git; g.State != api.CatalogItemGitStateConflict || !strings.Contains(deref(g.Note), "draft of "+both+" open") {
			t.Fatalf("the conflict = %+v", g)
		}
		detail = api.CatalogItemGitDetail{}
		h.json("GET", "/api/v1/admin/catalog/items/"+both+"/git", admin, nil, 200, &detail)
		if detail.Repository == nil || !strings.Contains(*detail.Repository, "Theirs") || detail.Rvav == nil || len(detail.Diff) == 0 {
			t.Fatalf("the conflict's sides = %+v", detail)
		}
		h.json("POST", "/api/v1/admin/catalog/items/"+both+"/git/resolve", admin, map[string]string{"take": "nobody"}, 422, nil)
		h.json("POST", "/api/v1/admin/catalog/items/"+slugs[0]+"/git/resolve", admin, map[string]string{"take": "rvav"}, 409, nil)
		h.json("POST", "/api/v1/admin/catalog/items/"+both+"/git/resolve", "tnguyen", map[string]string{"take": "rvav"}, 403, nil)
		var settled api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items/"+both+"/git/resolve", admin, map[string]string{"take": "repository"}, 200, &settled)
		if settled.DraftVersion == nil || settled.DraftVersion.Id == mine.Id || settled.Git.State != api.CatalogItemGitStateDraft {
			t.Fatalf("settled for the repository = %+v", settled.Git)
		}
		var draft api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/"+both+"/versions/"+settled.DraftVersion.Id, admin, nil, 200, &draft)
		if b, _ := json.Marshal(draft.Definition); !strings.Contains(string(b), "Theirs") {
			t.Fatal("the draft isn't the repository's file")
		}
		if h.syncGit().Drafts != 0 {
			t.Fatal("the settled file was taken again")
		}

		// rvav publishes, and the repository changes the file before rvav
		// commits: a conflict, settled for rvav (committed over theirs).
		h.json("POST", "/api/v1/admin/catalog/items/"+both+"/versions/"+settled.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		push(t, ctx, reader, "Theirs again", gitrepo.Change{Path: "catalog/" + both + ".yaml",
			Content: editFile(t, fileOf(t, ctx, reader, both), relabel("Theirs again"))})
		if rep := h.syncGit(); rep.Conflicts != 1 || rep.Committed != 0 {
			t.Fatalf("published on both sides = %+v", rep)
		}
		h.json("POST", "/api/v1/admin/catalog/items/"+both+"/git/resolve", admin, map[string]string{"take": "rvav"}, 200, &settled)
		if rep := h.syncGit(); rep.Committed != 1 {
			t.Fatalf("settled for rvav = %+v", rep)
		}
		if content := string(fileOf(t, ctx, reader, both)); strings.Contains(content, "Theirs again") || !strings.Contains(content, "Theirs") ||
			item(both).Git.State != api.CatalogItemGitStateSynced {
			t.Fatalf("rvav's file wasn't committed: %s", content)
		}
		if n := h.auditCount("catalog.git.resolve"); n != 2 {
			t.Fatalf("%d settlements audited", n)
		}

		// The Git server's webhook asks for a read.
		hook := "/api/v1/integrations/git/webhook"
		if code, _, _ := h.do("POST", hook, "", map[string]any{"ref": "refs/heads/main"}); code != 404 {
			t.Fatalf("a webhook without a secret = %d", code)
		}
		h.svc.Config.Git.WebhookSecret = "hook-s3cret"
		body := []byte(`{"ref":"refs/heads/main"}`)
		mac := hmac.New(sha256.New, []byte("hook-s3cret"))
		mac.Write(body)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if code, _, _ := h.do("POST", hook, "", rawYAML(body), "X-Hub-Signature-256", "sha256=00"); code != 401 {
			t.Fatalf("a webhook signed with another secret = %d", code)
		}
		if code, _, _ := h.do("POST", hook, "", rawYAML(body), "X-Gitlab-Token", "wrong"); code != 401 {
			t.Fatalf("GitLab's call with another token = %d", code)
		}
		if due, _ := h.store.GitDue(ctx, time.Hour); due {
			t.Fatal("due before the webhook")
		}
		if code, b, _ := h.do("POST", hook, "", rawYAML(body), "X-Hub-Signature-256", sig); code != 202 {
			t.Fatalf("GitHub's call = %d: %s", code, b)
		}
		if due, _ := h.store.GitDue(ctx, time.Hour); !due {
			t.Fatal("the webhook didn't ask for a read")
		}
		h.syncGit()
		if code, _, _ := h.do("POST", hook, "", rawYAML(body), "X-Gitlab-Token", "hook-s3cret"); code != 202 {
			t.Fatalf("GitLab's call = %d", code)
		}

		// Read now: committed and taken at once.
		push(t, ctx, reader, "Theirs once more", gitrepo.Change{Path: "catalog/" + slugs[0] + ".yaml",
			Content: editFile(t, fileOf(t, ctx, reader, slugs[0]), relabel("Read now"))})
		h.json("POST", "/api/v1/admin/catalog/git/read", "tnguyen", nil, 403, nil)
		h.json("POST", "/api/v1/admin/catalog/git/read", admin, nil, 200, &status)
		if status.ReadRequested != nil && *status.ReadRequested || status.Error != nil || item(slugs[0]).Git.State != api.CatalogItemGitStateDraft {
			t.Fatalf("read now = %+v", status)
		}
		h.svc.Config.Git.URL = ""
		h.json("POST", "/api/v1/admin/catalog/git/read", admin, nil, 409, nil)
		h.json("GET", "/api/v1/admin/catalog/git", admin, nil, 200, &status)
		if status.Configured || len(status.Files) != 0 {
			t.Fatalf("without a repository = %+v", status)
		}
	})
}

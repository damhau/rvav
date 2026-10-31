package e2e

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/gitrepo"
)

// restartWith applies the integration's saved settings, as rvav's
// processes do when they start (ADR-0020).
func (h *harness) restartWith(kind string) {
	h.t.Helper()
	stored, err := h.store.StoredIntegrations(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	row, ok := stored[kind]
	if !ok {
		h.t.Fatalf("no %s settings saved", kind)
	}
	var s config.Stored
	if err := json.Unmarshal(row.Settings, &s.Settings); err != nil {
		h.t.Fatal(err)
	}
	if row.SecretCiphertext != nil {
		plain, err := h.svc.Secrets.Open(core.IntegrationSecretName(kind), row.SecretKeyID, row.SecretNonce, row.SecretCiphertext)
		if err != nil {
			h.t.Fatal(err)
		}
		s.Secret = string(plain)
	}
	h.svc.Config.ApplyStored(kind, s)
}

// TestC1Acceptance: the catalog in Git, end to end (C1.5, ADR-0029). An
// administrator sets the repository in Administration › Integrations ›
// Git and tests it; once rvav restarts, its sync process adds the catalog
// to the repository. A catalog admin publishes a new version: committed,
// by her, with its changelog. A teammate merges a change in the
// repository; the Git server's webhook asks for a read, and the change
// becomes a draft, with its commit and diff, not published. The catalog
// admin publishes it: users can request it, and it is committed back, so
// the repository and rvav agree. Changed on both sides, the item is a
// conflict shown with both sides, settled for rvav. Every step is audited.
func TestC1Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, catalogAdmin, user = "mkeller", "lmartin", "tnguyen"
		url := bareRepo(t)

		// The repository, set and tested from the UI.
		h.json("PUT", "/api/v1/admin/integrations/git", admin, api.IntegrationWrite{Settings: map[string]any{"url": url,
			"branch": "main", "dir": "catalog", "committer": "rvav@corp.example"}, Secret: new("glpat-catalog")}, 200, nil)
		var test api.IntegrationTest
		h.json("POST", "/api/v1/admin/integrations/git/test", admin, nil, 200, &test)
		if !test.Ok {
			t.Fatalf("the repository's test = %+v", test)
		}

		// rvav restarts with them; its sync process adds the catalog.
		h.restartWith("git")
		h.svc.Config.Git.Interval, h.svc.Config.Git.Cache = 5*time.Minute, filepath.Join(t.TempDir(), "sync")
		h.svc.Config.Git.WebhookSecret = "hook-s3cret"
		if h.svc.Config.Git.URL != url || h.svc.Config.Git.Credential != "glpat-catalog" {
			t.Fatalf("after the restart, Git = %+v", h.svc.Config.Git)
		}
		loop, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- h.svc.RunCatalogGit(loop) }()
		defer func() {
			stop()
			<-done
		}()
		reader, err := gitrepo.New(gitrepo.Options{URL: url, Cache: filepath.Join(t.TempDir(), "teammate")})
		if err != nil {
			t.Fatal(err)
		}
		var items api.AdminCatalogItemPage
		h.json("GET", "/api/v1/admin/catalog/items?source=native&state=published&limit=100", catalogAdmin, nil, 200, &items)
		var snap gitrepo.Snapshot
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			if snap, err = reader.Read(ctx); err == nil && len(snap.Files) == len(items.Items) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the sync process added %d files of %d (%v)", len(snap.Files), len(items.Items), err)
			}
		}
		var status api.CatalogGitStatus
		h.json("GET", "/api/v1/admin/catalog/git", catalogAdmin, nil, 200, &status)
		if !status.Configured || deref(status.Url) != url || len(status.Files) != len(items.Items) {
			t.Fatalf("the repository's status = %+v", status)
		}

		// A new version published: committed, by the catalog admin.
		var draft api.CatalogItemVersionDetail
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions", catalogAdmin, api.CatalogItemVersionCreate{}, 201, &draft)
		h.json("POST", "/api/v1/admin/catalog/items/rhel9-server/versions/"+draft.Id+"/publish", catalogAdmin,
			api.CatalogItemVersionPublish{Changelog: new("Patched template.")}, 200, nil)
		h.json("POST", "/api/v1/admin/catalog/git/read", catalogAdmin, nil, 200, &status)
		snap, _ = reader.Read(ctx)
		if snap.Head.Author != "Léa Martin" || snap.Head.Message != "Publish rhel9-server "+draft.Number+"\n\nPatched template." {
			t.Fatalf("the publish's commit = %+v", snap.Head)
		}

		// A teammate merges a change; the Git server's webhook calls rvav.
		const slug = "ubuntu-server"
		theirs := editFile(t, snap.Files[slug].Content, func(_, v map[string]any) {
			relabel("Application role")(nil, v)
			v["changelog"] = "Clearer role label."
		})
		merge := push(t, ctx, reader, "Merge !42: clearer role label", gitrepo.Change{Path: "catalog/" + slug + ".yaml", Content: theirs})
		body := []byte(`{"ref":"refs/heads/main","after":"` + merge.SHA + `"}`)
		if code, b, _ := h.do("POST", "/api/v1/integrations/git/webhook", "", rawYAML(body), "X-Gitlab-Token", "hook-s3cret"); code != 202 {
			t.Fatalf("the webhook = %d: %s", code, b)
		}
		var it api.AdminCatalogItem
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			h.json("GET", "/api/v1/admin/catalog/items/"+slug, catalogAdmin, nil, 200, &it)
			if it.Git != nil && it.Git.State == api.CatalogItemGitStateDraft {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the merge didn't become a draft: %+v", it.Git)
			}
		}
		if it.DraftVersion == nil || it.DraftVersion.Changelog != "Clearer role label." || it.Git.LastCommit == nil ||
			it.Git.LastCommit.Sha != merge.SHA || it.Git.LastCommit.Author != "Thomas Nguyen" {
			t.Fatalf("the draft of the merge = %+v, git %+v", it.DraftVersion, it.Git)
		}
		published := it.PublishedVersion.Id
		var user1 api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/"+slug+"?project_id=prj_web", user, nil, 200, &user1)
		if user1.Version.Id != published {
			t.Fatal("users see the repository's change before it is published")
		}
		var detail api.CatalogItemGitDetail
		h.json("GET", "/api/v1/admin/catalog/items/"+slug+"/git", catalogAdmin, nil, 200, &detail)
		added := 0
		for _, l := range detail.Diff {
			if l.Op == api.DiffLineOpAdded && strings.Contains(l.Text, "Application role") {
				added++
			}
		}
		if added != 1 {
			t.Fatalf("the draft's diff = %+v", detail.Diff)
		}

		// Published in rvav: users get it, and it is committed back.
		h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+it.DraftVersion.Id+"/publish", catalogAdmin, nil, 200, nil)
		var user2 api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/"+slug+"?project_id=prj_web", user, nil, 200, &user2)
		if user2.Version.Id != it.DraftVersion.Id {
			t.Fatalf("users get version %s, want the published draft", user2.Version.Number)
		}
		h.json("POST", "/api/v1/admin/catalog/git/read", catalogAdmin, nil, 200, &status)
		snap, _ = reader.Read(ctx)
		if def, details := core.SameItemFile(snap.Files[slug].Content, theirs); !def || !details || snap.Head.Author != "Léa Martin" {
			t.Fatalf("rvav's commit of the merge = %+v", snap.Head)
		}
		h.json("GET", "/api/v1/admin/catalog/items/"+slug, catalogAdmin, nil, 200, &it)
		if it.Git.State != api.CatalogItemGitStateSynced || status.Error != nil {
			t.Fatalf("after publishing the merge = %+v, %+v", it.Git, status)
		}
		// Changed on both sides: the teammate pushes (no webhook this
		// time) and the catalog admin publishes before rvav commits. A
		// conflict, shown with both sides, nothing overwritten; the admin
		// keeps rvav's, committed over the teammate's change.
		snap, _ = reader.Read(ctx)
		mine := editFile(t, snap.Files[slug].Content, func(_, v map[string]any) { relabel("Teammate's label")(nil, v) })
		theirsAgain := push(t, ctx, reader, "Teammate's label", gitrepo.Change{Path: "catalog/" + slug + ".yaml", Content: mine})
		h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions", catalogAdmin, api.CatalogItemVersionCreate{}, 201, &draft)
		h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+draft.Id+"/publish", catalogAdmin,
			api.CatalogItemVersionPublish{Changelog: new("Ours.")}, 200, nil)
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			h.json("GET", "/api/v1/admin/catalog/items/"+slug, catalogAdmin, nil, 200, &it)
			if it.Git != nil && it.Git.State == api.CatalogItemGitStateConflict {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no conflict: %+v", it.Git)
			}
		}
		var both api.CatalogItemGitDetail
		h.json("GET", "/api/v1/admin/catalog/items/"+slug+"/git", catalogAdmin, nil, 200, &both)
		if both.Rvav == nil || both.Repository == nil || !strings.Contains(*both.Repository, "Teammate's label") ||
			strings.Contains(*both.Rvav, "Teammate's label") || len(both.Diff) == 0 || both.File.LastCommit == nil ||
			both.File.LastCommit.Sha != theirsAgain.SHA {
			t.Fatalf("the conflict's sides = %+v", both)
		}
		if snap, _ = reader.Read(ctx); snap.Head.SHA != theirsAgain.SHA {
			t.Fatalf("rvav committed over the conflict: %+v", snap.Head)
		}
		h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/git/resolve", catalogAdmin, map[string]string{"take": "rvav"}, 200, nil)
		h.json("POST", "/api/v1/admin/catalog/git/read", catalogAdmin, nil, 200, &status)
		snap, _ = reader.Read(ctx)
		if strings.Contains(string(snap.Files[slug].Content), "Teammate's label") || snap.Head.Author != "Léa Martin" {
			t.Fatalf("after keeping rvav's = %+v", snap.Head)
		}
		h.json("GET", "/api/v1/admin/catalog/items/"+slug, catalogAdmin, nil, 200, &it)
		if it.Git.State != api.CatalogItemGitStateSynced {
			t.Fatalf("after settling = %+v", it.Git)
		}

		for _, action := range []string{"catalog.git.commit", "catalog.git.draft", "catalog.git.conflict", "catalog.git.resolve",
			"catalog.version.publish"} {
			if h.auditCount(action) == 0 {
				t.Errorf("%s isn't audited", action)
			}
		}
	})
}

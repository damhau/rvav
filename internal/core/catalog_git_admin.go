package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/utils/diff"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/integrations/gitrepo"
	"github.com/damhau/rvav/internal/store"
)

// The catalog's repository as administrators see it (ADR-0029): its files,
// a read on demand, a conflict settled, and the Git server's webhook.

// ErrGitWebhook: a call that isn't the Git server's.
var ErrGitWebhook = errors.New("not a call from the catalog's Git server")

// errNoGit: rvav doesn't keep its catalog in Git.
var errNoGit = &ConflictError{Msg: "rvav doesn't keep its catalog in Git: set the repository in Administration › Integrations › Git."}

// CatalogGitStatus is the repository as rvav last read it, and every file
// it keeps track of.
func (s *Service) CatalogGitStatus(ctx context.Context, p *auth.Principal) (api.CatalogGitStatus, error) {
	if !canAdminCatalog(p) {
		return api.CatalogGitStatus{}, ErrForbidden
	}
	c := s.Config.Git
	out := api.CatalogGitStatus{Configured: c.Configured(), Files: []api.CatalogGitFile{}}
	if !out.Configured {
		return out, nil
	}
	out.Url, out.Branch, out.Dir = &c.URL, new(c.Branch), new(strings.Trim(c.Dir, "/"))
	repo, err := s.Store.GetGitRepo(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	if err == nil {
		out.Head, out.ReadAt, out.Error = repo.Head, repo.ReadAt, repo.Error
		out.ReadRequested = new(repo.ReadRequestedAt != nil)
	}
	files, err := s.Store.GitFiles(ctx)
	if err != nil {
		return out, err
	}
	for _, f := range files {
		g := presentGitFile(f)
		file := api.CatalogGitFile{Path: g.Path, State: api.CatalogGitFileState(g.State), Attempts: g.Attempts, Error: g.Error,
			NextAttemptAt: g.NextAttemptAt, Note: g.Note, LastCommit: g.LastCommit, DraftVersionId: g.DraftVersionId, Problems: g.Problems}
		if f.ItemID != nil {
			if it, err := s.Store.GetAdminItem(ctx, *f.ItemID); err == nil {
				file.Item = &api.ItemRef{Id: it.ID, Slug: it.Slug, Name: it.Name}
			}
		}
		out.Files = append(out.Files, file)
	}
	return out, nil
}

// ReadCatalogGit reads the repository now and commits what waits, waiting
// for it a minute at most.
func (s *Service) ReadCatalogGit(ctx context.Context, p *auth.Principal) (api.CatalogGitStatus, error) {
	if !canAdminCatalog(p) {
		return api.CatalogGitStatus{}, ErrForbidden
	}
	if !s.Config.Git.Configured() {
		return api.CatalogGitStatus{}, errNoGit
	}
	rctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if _, err := s.SyncCatalogGit(rctx); err != nil {
		// The read's own failure is in the status; the database's isn't.
		if _, gerr := s.Store.GetGitRepo(ctx); gerr != nil {
			return api.CatalogGitStatus{}, err
		}
	}
	return s.CatalogGitStatus(ctx, p)
}

// CatalogItemGit is the item's file in the repository and rvav's, and the
// lines from one to the other: the repository's change rvav didn't take
// (a conflict, a refused file), or the draft it made of it.
func (s *Service) CatalogItemGit(ctx context.Context, p *auth.Principal, idOrSlug string) (api.CatalogItemGitDetail, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.CatalogItemGitDetail{}, err
	}
	if !s.gitTracks(it) {
		return api.CatalogItemGitDetail{}, ErrNotFound
	}
	f, err := s.Store.GitFileOfItem(ctx, it.ID)
	if errors.Is(err, store.ErrNotFound) {
		return api.CatalogItemGitDetail{}, ErrNotFound
	} else if err != nil {
		return api.CatalogItemGitDetail{}, err
	}
	out := api.CatalogItemGitDetail{File: *presentGitFile(f), Diff: []api.DiffLine{}}
	ours, _, err := s.gitFile(ctx, it)
	if err != nil {
		return out, err
	}
	var theirs []byte
	switch {
	case f.RepoContent != nil:
		theirs = []byte(*f.RepoContent)
	case f.State == "draft" && f.DraftVersionID != nil:
		if theirs, err = s.itemYAML(ctx, it, func(r store.VersionRow) bool { return r.ID == *f.DraftVersionID }); err != nil &&
			!errors.Is(err, ErrNotFound) {
			return out, err
		}
	}
	if ours != nil {
		out.Rvav = new(string(ours))
	}
	if theirs != nil {
		out.Repository = new(string(theirs))
		out.Diff = diffLines(string(ours), string(theirs))
	}
	return out, nil
}

// diffLines are the lines from a to b.
func diffLines(a, b string) []api.DiffLine {
	out := []api.DiffLine{}
	for _, d := range diff.Do(a, b) {
		op := api.DiffLineOpSame
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			op = api.DiffLineOpAdded
		case diffmatchpatch.DiffDelete:
			op = api.DiffLineOpRemoved
		}
		for _, line := range strings.SplitAfter(d.Text, "\n") {
			if line != "" {
				out = append(out, api.DiffLine{Op: op, Text: strings.TrimSuffix(line, "\n")})
			}
		}
	}
	return out
}

// ResolveCatalogItemGit settles a conflict of the item's file: "rvav"
// commits rvav's file over the repository's change, "repository" makes a
// draft of the repository's file (rvav's open draft discarded).
func (s *Service) ResolveCatalogItemGit(ctx context.Context, p *auth.Principal, idOrSlug, take string) (api.AdminCatalogItem, error) {
	it, err := s.adminItem(ctx, p, idOrSlug)
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	if !s.gitTracks(it) {
		return api.AdminCatalogItem{}, errNoGit
	}
	if take != "rvav" && take != "repository" {
		return api.AdminCatalogItem{}, &ValidationError{Errors: []api.FieldError{fieldErr("/take", "enum", "rvav or repository.")}}
	}
	err = s.Store.WithLock(ctx, store.LockGitSync, func() error {
		f, err := s.Store.GitFileOfItem(ctx, it.ID)
		if errors.Is(err, store.ErrNotFound) || err == nil && f.State != "conflict" {
			return &ConflictError{Msg: it.Name + "'s file isn't in conflict: there is nothing to settle."}
		} else if err != nil {
			return err
		}
		if take == "rvav" {
			if _, err := s.Store.GitTakeRvav(ctx, f.Path, p.User.ID, "Keep "+it.Slug+" as rvav has it, over the repository's change"); err != nil {
				return err
			}
			s.auditCatalog(ctx, p, "catalog.git.resolve", it.ID, map[string]any{"take": take, "path": f.Path})
			return nil
		}
		if f.RepoContent == nil || f.RepoBlob == nil || *f.RepoBlob == "" {
			return &ConflictError{Msg: "The repository deleted " + f.Path + ": there is no file of it to take."}
		}
		versions, err := s.Store.ItemVersions(ctx, it.ID)
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(versions, func(v store.VersionRow) bool { return v.State == "draft" }); i >= 0 {
			if err := s.Store.DeleteDraft(ctx, versions[i].ID); err != nil {
				return err
			}
			s.auditCatalog(ctx, p, "catalog.version.delete", it.ID, map[string]any{"version_id": versions[i].ID, "for": "git"})
		}
		res, err := s.importItem(ctx, p, []byte(*f.RepoContent), it.ID, false)
		if err != nil {
			return err
		}
		versions, err = s.Store.ItemVersions(ctx, res.ItemId)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(versions, func(v store.VersionRow) bool { return v.State == "draft" })
		if i < 0 {
			return errors.New("the import made no draft")
		}
		var commit *gitrepo.Commit
		_ = json.Unmarshal(f.LastCommit, &commit)
		if err := s.Store.GitDraft(ctx, f.Path, it.ID, *f.RepoBlob, versions[i].ID, versions[i].Revision, commit, ""); err != nil {
			return err
		}
		s.auditCatalog(ctx, p, "catalog.git.resolve", it.ID, map[string]any{"take": take, "path": f.Path, "version": versions[i].Number})
		return nil
	})
	if err != nil {
		return api.AdminCatalogItem{}, err
	}
	if err := s.Store.RequestGitRead(ctx); err != nil {
		s.Log.Warn("catalog in Git: ask for a read", "err", err)
	}
	return s.AdminCatalogItem(ctx, p, it.ID)
}

// GitWebhook checks the Git server's call and asks for a read of the
// repository: ErrNotFound when rvav has no repository or no webhook
// secret, ErrGitWebhook when the call isn't signed with it.
func (s *Service) GitWebhook(ctx context.Context, h http.Header, body []byte) error {
	secret := s.Config.Git.WebhookSecret
	if !s.Config.Git.Configured() || secret == "" {
		return ErrNotFound
	}
	if !gitSigned(h, body, secret) {
		return ErrGitWebhook
	}
	return s.Store.RequestGitRead(ctx)
}

// gitSigned says whether the call carries the secret: GitHub's and
// Gitea's HMAC of the body, or GitLab's token.
func gitSigned(h http.Header, body []byte, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	for _, sig := range []string{strings.TrimPrefix(h.Get("X-Hub-Signature-256"), "sha256="), h.Get("X-Gitea-Signature")} {
		if got, err := hex.DecodeString(sig); err == nil && sig != "" && hmac.Equal(got, want) {
			return true
		}
	}
	token := h.Get("X-Gitlab-Token")
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
}

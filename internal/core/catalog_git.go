package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/integrations/gitrepo"
	"github.com/damhau/rvav/internal/store"
)

// The catalog in Git (ADR-0029). rvav publishes, the repository proposes:
// what an administrator publishes, deprecates or rolls back is marked to be
// committed, and the sync process commits it, never while the
// administrator waits; never over a change of the repository's rvav hasn't
// seen.

// gitPoll is how often the sync process looks for something to do.
var gitPoll = 5 * time.Second

// gitTries is how many times a commit refused because the branch moved is
// made again on its new head.
const gitTries = 3

// gitTracks says whether the item's file is kept in the repository: rvav
// keeps its catalog in Git, and the item isn't AWX's (its sync owns it).
func (s *Service) gitTracks(it store.AdminItem) bool {
	return s.Config.Git.Configured() && it.SourceKind == "native"
}

// gitPath is the file of the item of that slug in the repository.
func (s *Service) gitPath(slug string) string {
	return path.Join(strings.Trim(cmp.Or(s.Config.Git.Dir, "catalog"), "/"), slug+".yaml")
}

// markGit says rvav has a change of the item to commit, by p. Publishing
// doesn't wait for Git, nor fail with it: should this fail, the next read
// finds the difference.
func (s *Service) markGit(ctx context.Context, p *auth.Principal, itemID, message string) {
	it, err := s.Store.GetAdminItem(ctx, itemID)
	if err != nil || !s.gitTracks(it) {
		return
	}
	if err := s.Store.MarkGitPending(ctx, it.ID, s.gitPath(it.Slug), p.User.ID, message); err != nil {
		s.Log.Error("catalog in Git: mark the item's change", "item", it.ID, "err", err)
	}
}

// gitMessage is a commit's message: a line, then the changelog.
func gitMessage(line, changelog string) string {
	if changelog = strings.TrimSpace(changelog); changelog != "" {
		return line + "\n\n" + changelog
	}
	return line
}

// gitVersion is the version the item's file holds: the published one, or,
// when none is, the one deprecated last. False when it has neither.
func gitVersion(it store.AdminItem, rows []store.VersionRow) (store.VersionRow, bool) {
	var last store.VersionRow
	for _, r := range rows {
		switch {
		case it.PublishedVersionID != nil && r.ID == *it.PublishedVersionID:
			return r, true
		case r.State == "deprecated" && r.DeprecatedAt != nil && (last.DeprecatedAt == nil || r.DeprecatedAt.After(*last.DeprecatedAt)):
			last = r
		}
	}
	return last, last.ID != ""
}

// gitFile is the item's file as rvav would commit it, nil when there is
// nothing to commit (it has only a draft).
func (s *Service) gitFile(ctx context.Context, it store.AdminItem) ([]byte, store.VersionRow, error) {
	rows, err := s.Store.ItemVersions(ctx, it.ID)
	if err != nil {
		return nil, store.VersionRow{}, err
	}
	v, ok := gitVersion(it, rows)
	if !ok {
		return nil, v, nil
	}
	out, err := s.itemYAML(ctx, it, func(r store.VersionRow) bool { return r.ID == v.ID })
	return out, v, err
}

// sameFile compares two files of an item: the same definition (the last
// version's), and the same metadata.
func sameFile(a, b []byte) (definition, metadata bool) {
	var x, y itemDoc
	if yaml.Unmarshal(a, &x) != nil || yaml.Unmarshal(b, &y) != nil || len(x.Versions) == 0 || len(y.Versions) == 0 {
		return false, false
	}
	def := func(d itemDoc) string {
		v := d.Versions[len(d.Versions)-1]
		raw, _ := json.Marshal(map[string]any{"form_schema": orEmpty(v.FormSchema), "ui_schema": orEmpty(v.UISchema),
			"initial_values": orEmpty(v.InitialValues), "blueprint": orEmpty(v.Blueprint)})
		return string(raw)
	}
	norm := func(m itemMetadata) itemMetadata {
		if len(m.Tags) == 0 {
			m.Tags = nil
		}
		return m
	}
	return def(x) == def(y), reflect.DeepEqual(norm(x.Metadata), norm(y.Metadata))
}

// presentGitFile is an item's file as administrators see it.
func presentGitFile(f store.GitFile) *api.CatalogItemGit {
	out := &api.CatalogItemGit{Path: f.Path, State: api.CatalogItemGitState(f.State), Attempts: f.Attempts, Error: f.Error}
	switch {
	case f.State == "conflict":
	case f.Pending && f.Error != nil:
		out.State, out.NextAttemptAt = api.CatalogItemGitStateFailing, f.NextAttemptAt
	case f.Pending:
		out.State = api.CatalogItemGitStateWaiting
	}
	if f.Note != "" {
		out.Note = &f.Note
	}
	var c gitrepo.Commit
	if json.Unmarshal(f.LastCommit, &c) == nil && c.SHA != "" {
		out.LastCommit = &api.GitCommit{Sha: c.SHA, Message: c.Message, Author: c.Author, When: c.When}
		if c.Email != "" {
			out.LastCommit.Email = &c.Email
		}
	}
	return out
}

// GitSyncReport is what one read of the repository did.
type GitSyncReport struct {
	Head      string
	Committed int
	Conflicts int
	Failed    int
}

// RunCatalogGit keeps the catalog and its repository in step until ctx is
// done: it reads the repository when it is due (every interval, when asked
// to, or when a change waits to be committed).
func (s *Service) RunCatalogGit(ctx context.Context) error {
	s.Log.Info("catalog in Git started", "repository", s.Config.Git.URL, "interval", s.Config.Git.Interval.String())
	for {
		due, err := s.Store.GitDue(ctx, s.Config.Git.Interval)
		if err == nil && due {
			_, err = s.SyncCatalogGit(ctx)
		}
		if err != nil && ctx.Err() == nil {
			s.Log.Warn("catalog in Git: the sync failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(gitPoll):
		}
	}
}

// SyncCatalogGit reads the repository and commits rvav's changes, one sync
// at a time in any process.
func (s *Service) SyncCatalogGit(ctx context.Context) (GitSyncReport, error) {
	var rep GitSyncReport
	if !s.Config.Git.Configured() {
		return rep, nil
	}
	err := s.Store.WithLock(ctx, store.LockGitSync, func() error {
		var err error
		rep, err = s.syncGit(ctx)
		return err
	})
	return rep, err
}

// gitSync is one read: the repository, its files, and rvav's items.
type gitSync struct {
	s      *Service
	repo   *gitrepo.Repo
	snap   gitrepo.Snapshot
	byPath map[string]gitrepo.File
	items  map[string]store.AdminItem
	rep    GitSyncReport
}

func (g *gitSync) take(snap gitrepo.Snapshot) {
	g.snap, g.byPath = snap, map[string]gitrepo.File{}
	for _, f := range snap.Files {
		g.byPath[f.Path] = f
	}
	g.rep.Head = snap.Head.SHA
}

// blobAt is the blob of the file at p on the branch, "" when it has none.
func (g *gitSync) blobAt(p string) string { return g.byPath[p].Hash }

func (s *Service) syncGit(ctx context.Context) (GitSyncReport, error) {
	c := s.Config.Git
	repo, err := GitRepo(c, c.Cache)
	if err != nil {
		return GitSyncReport{}, err
	}
	if err := s.Store.StartGitRepo(ctx, c.URL+" "+repo.Branch()+" "+repo.Dir()); err != nil {
		return GitSyncReport{}, err
	}
	started := time.Now()
	g := &gitSync{s: s, repo: repo, items: map[string]store.AdminItem{}}
	snap, err := repo.Read(ctx)
	if err != nil {
		_ = s.Store.GitRead(ctx, "", err, started)
		return g.rep, g.failDue(ctx, err)
	}
	g.take(snap)
	page, err := s.Store.AdminItems(ctx, store.AdminItemFilter{Source: "native", Limit: 100000})
	if err != nil {
		return g.rep, err
	}
	for _, it := range page.Items {
		g.items[it.ID] = it
	}
	if err := g.adopt(ctx); err != nil {
		return g.rep, err
	}
	if err := g.commitPending(ctx); err != nil {
		return g.rep, err
	}
	if err := g.catchUp(ctx); err != nil {
		return g.rep, err
	}
	return g.rep, s.Store.GitRead(ctx, g.rep.Head, nil, started)
}

// failDue records a failed read on each change waiting to be committed.
func (g *gitSync) failDue(ctx context.Context, cause error) error {
	files, err := g.s.Store.GitFiles(ctx)
	if err != nil {
		return errors.Join(cause, err)
	}
	for _, f := range files {
		if f.Pending && f.State != "conflict" && (f.NextAttemptAt == nil || !f.NextAttemptAt.After(time.Now())) {
			g.failed(ctx, f, cause)
		}
	}
	return cause
}

// adopt keeps track of the items rvav has and the repository doesn't know
// of yet: those it has a file of are seen as they are, the others are to
// be added.
func (g *gitSync) adopt(ctx context.Context) error {
	files, err := g.s.Store.GitFiles(ctx)
	if err != nil {
		return err
	}
	known, paths := map[string]bool{}, map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
		if f.ItemID != nil {
			known[*f.ItemID] = true
		}
	}
	for _, it := range g.items {
		if known[it.ID] {
			continue
		}
		content, _, err := g.s.gitFile(ctx, it)
		if err != nil {
			return err
		}
		p := g.repo.FilePath(it.Slug)
		if content == nil || paths[p] {
			continue
		}
		if f, ok := g.byPath[p]; ok {
			// The repository has a file of it: the same as rvav's, seen;
			// another, a change it proposes.
			var blob *string
			if def, meta := sameFile(content, f.Content); def && meta {
				blob = &f.Hash
			}
			if err := g.s.Store.AddGitFile(ctx, p, &it.ID, blob); err != nil {
				return err
			}
			continue
		}
		if err := g.s.Store.MarkGitPending(ctx, it.ID, p, SystemUserID, "Add "+it.Slug+" from rvav"); err != nil {
			return err
		}
	}
	return nil
}

// commitPending commits rvav's changes that are due, oldest first, each
// with its author and message.
func (g *gitSync) commitPending(ctx context.Context) error {
	files, err := g.s.Store.GitFiles(ctx)
	if err != nil {
		return err
	}
	at := func(f store.GitFile) int64 {
		if f.PendingAt == nil {
			return 0
		}
		return f.PendingAt.UnixNano()
	}
	slices.SortFunc(files, func(a, b store.GitFile) int { return cmp.Compare(at(a), at(b)) })
	for _, f := range files {
		if !f.Pending || f.State == "conflict" || f.NextAttemptAt != nil && f.NextAttemptAt.After(time.Now()) {
			continue
		}
		if err := g.commit(ctx, f); err != nil {
			return err
		}
	}
	return nil
}

// commit commits one file's change. It returns only the database's errors:
// Git's are recorded on the file, and the change tried again.
func (g *gitSync) commit(ctx context.Context, f store.GitFile) error {
	st := g.s.Store
	it, hasItem := store.AdminItem{}, false
	if f.ItemID != nil {
		it, hasItem = g.items[*f.ItemID]
	}
	var content []byte
	to := f.Path
	if hasItem && !f.Remove {
		var err error
		if content, _, err = g.s.gitFile(ctx, it); err != nil {
			return err
		}
		if content == nil {
			// Only a draft: nothing to commit.
			return st.GitAgreed(ctx, f.Path, deref(f.SeenBlob), nil, f.Note, f.Revision)
		}
		to = g.repo.FilePath(it.Slug)
	}
	if !hasItem || f.Remove {
		if g.blobAt(f.Path) == "" {
			return st.DeleteGitFile(ctx, f.Path, f.Revision)
		}
	}
	for try := 1; ; try++ {
		// A change of the repository's rvav hasn't seen: the same as
		// rvav's, they agree; another, a conflict.
		if g.blobAt(f.Path) != deref(f.SeenBlob) {
			cur, ok := g.byPath[f.Path]
			if def, meta := sameFile(content, cur.Content); ok && content != nil && def && meta {
				return st.GitAgreed(ctx, f.Path, cur.Hash, g.lastCommit(ctx, f.Path), "", f.Revision)
			}
			return g.conflict(ctx, f, it, "Changed in the repository since rvav last read it.")
		}
		if to != f.Path && g.blobAt(to) != "" {
			return g.conflict(ctx, f, it, fmt.Sprintf("The repository already has %s.", to))
		}
		changes := []gitrepo.Change{{Path: to, Content: content}}
		if to != f.Path && g.blobAt(f.Path) != "" {
			changes = append(changes, gitrepo.Change{Path: f.Path})
		}
		author := gitrepo.Person{Name: deref(f.PendingByName), Email: deref(f.PendingByEmail)}
		if g.s.GitBeforeCommit != nil {
			g.s.GitBeforeCommit(f.Path)
		}
		commit, err := g.repo.Commit(ctx, g.snap.Head.SHA, changes, author, f.PendingMessage)
		if errors.Is(err, gitrepo.ErrRejected) && try < gitTries {
			// The branch moved: read it again, and commit on its new head
			// if this file didn't change there.
			snap, rerr := g.repo.Read(ctx)
			if rerr == nil {
				g.take(snap)
				continue
			}
			err = rerr
		}
		if err != nil {
			g.failed(ctx, f, err)
			return nil
		}
		g.rep.Head = commit.SHA
		g.snap.Head = commit
		if content == nil {
			delete(g.byPath, f.Path)
			err = st.DeleteGitFile(ctx, f.Path, f.Revision)
		} else {
			blob := gitrepo.BlobHash(content)
			delete(g.byPath, f.Path)
			g.byPath[to] = gitrepo.File{Path: to, Hash: blob, Content: content}
			err = st.GitCommitted(ctx, f.Path, to, blob, commit, f.Revision)
		}
		if err != nil {
			return err
		}
		g.rep.Committed++
		target := it.ID
		if target == "" {
			target = deref(f.ItemID)
		}
		g.s.auditGit(ctx, "catalog.git.commit", target, map[string]any{"path": to, "commit": commit.SHA,
			"message": firstLine(f.PendingMessage)})
		return nil
	}
}

// conflict records that rvav and the repository both changed the file.
func (g *gitSync) conflict(ctx context.Context, f store.GitFile, it store.AdminItem, note string) error {
	cur := g.byPath[f.Path]
	if err := g.s.Store.GitConflict(ctx, f.Path, cur.Hash, string(cur.Content), note, g.lastCommit(ctx, f.Path)); err != nil {
		return err
	}
	g.rep.Conflicts++
	g.s.auditGit(ctx, "catalog.git.conflict", cmp.Or(it.ID, deref(f.ItemID)), map[string]any{"path": f.Path, "note": note})
	return nil
}

// failed records a failed commit: tried again later, the first failure
// audited.
func (g *gitSync) failed(ctx context.Context, f store.GitFile, cause error) {
	g.rep.Failed++
	wait := min(30*time.Second<<min(f.Attempts, 10), max(g.s.Config.Git.Interval, time.Minute))
	if err := g.s.Store.GitCommitFailed(ctx, f.Path, cause.Error(), time.Now().Add(wait)); err != nil {
		g.s.Log.Error("catalog in Git: record a failed commit", "path", f.Path, "err", err)
	}
	if f.Attempts == 0 {
		g.s.auditGit(ctx, "catalog.git.commit_failed", deref(f.ItemID), map[string]any{"path": f.Path, "error": cause.Error()})
	}
}

// catchUp commits what rvav has and the repository's unchanged file
// doesn't, when no change was marked for it: one whose mark failed, or a
// change of the repository's set aside in rvav.
func (g *gitSync) catchUp(ctx context.Context) error {
	files, err := g.s.Store.GitFiles(ctx)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.Pending || f.State != "synced" || f.ItemID == nil || f.SeenBlob == nil || g.blobAt(f.Path) != *f.SeenBlob {
			continue
		}
		it, ok := g.items[*f.ItemID]
		if !ok {
			continue
		}
		content, _, err := g.s.gitFile(ctx, it)
		if err != nil {
			return err
		}
		if content == nil {
			continue
		}
		if def, meta := sameFile(content, g.byPath[f.Path].Content); def && meta && g.repo.FilePath(it.Slug) == f.Path {
			continue
		}
		if err := g.s.Store.MarkGitPending(ctx, it.ID, f.Path, SystemUserID, "Update "+it.Slug+" from rvav"); err != nil {
			return err
		}
		f.Pending, f.PendingMessage, f.Revision = true, "Update "+it.Slug+" from rvav", f.Revision+1
		if err := g.commit(ctx, f); err != nil {
			return err
		}
	}
	return nil
}

// lastCommit is the last commit that changed the file, nil when it isn't
// known.
func (g *gitSync) lastCommit(ctx context.Context, p string) *gitrepo.Commit {
	c, err := g.repo.LastCommit(ctx, p)
	if err != nil || c.SHA == "" {
		return nil
	}
	return &c
}

func (s *Service) auditGit(ctx context.Context, action, itemID string, detail map[string]any) {
	if itemID == "" {
		return
	}
	s.auditCatalog(ctx, system(), action, itemID, detail)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

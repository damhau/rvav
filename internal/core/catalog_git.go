package core

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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
	case f.State == "draft" && f.DraftVersionID == nil && !f.Pending:
		// Its draft discarded: rvav's file is committed at the next read.
		out.State = api.CatalogItemGitStateWaiting
	case f.Pending && f.Error != nil:
		out.State, out.NextAttemptAt = api.CatalogItemGitStateFailing, f.NextAttemptAt
	case f.Pending:
		out.State = api.CatalogItemGitStateWaiting
	}
	if f.Note != "" {
		out.Note = &f.Note
	}
	out.DraftVersionId = f.DraftVersionID
	var problems []api.FieldError
	if json.Unmarshal(f.Problems, &problems) == nil && len(problems) > 0 {
		out.Problems = &problems
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

// SameItemFile says whether two files of an item hold the same
// definition, and the same details.
func SameItemFile(a, b []byte) (definition, details bool) { return sameFile(a, b) }

// GitSyncReport is what one read of the repository did.
type GitSyncReport struct {
	Head      string
	Committed int
	Conflicts int
	Failed    int
	// Drafts made of the repository's changes, files refused, files
	// deleted there.
	Drafts, Refused, Deleted int
}

// RunCatalogGit keeps the catalog and its repository in step until ctx is
// done: it reads the repository when it is due (every interval, when asked
// to, or when a change waits to be committed).
func (s *Service) RunCatalogGit(ctx context.Context) error {
	s.Log.Info("catalog in Git started", "repository", s.Config.Git.URL, "interval", s.Config.Git.Interval.String())
	for {
		due, err := s.Store.GitDue(ctx, s.liveConfig(ctx).Git.Interval)
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
	// fileErrs: files of the repository not taken, tried again next read.
	fileErrs []string
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
		// Settings rvav can't use (from the environment): shown as the
		// read's error.
		if serr := s.Store.StartGitRepo(ctx, c.URL+" "+c.Branch+" "+c.Dir); serr == nil {
			_ = s.Store.GitRead(ctx, "", err, time.Now())
		}
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
	// Whatever stops the read is its error, so it shows, and the read
	// waits for its interval.
	fail := func(err error) (GitSyncReport, error) {
		_ = s.Store.GitRead(context.WithoutCancel(ctx), g.rep.Head, err, started)
		return g.rep, err
	}
	page, err := s.Store.AdminItems(ctx, store.AdminItemFilter{Source: "native", Limit: 100000})
	if err != nil {
		return fail(err)
	}
	for _, it := range page.Items {
		g.items[it.ID] = it
	}
	for _, step := range []func(context.Context) error{g.adopt, g.takeChanges, g.commitPending, g.catchUp} {
		if err := step(ctx); err != nil {
			return fail(err)
		}
	}
	var readErr error
	if len(g.fileErrs) > 0 {
		readErr = fmt.Errorf("%d file(s) weren't taken, tried again at the next read: %s", len(g.fileErrs), strings.Join(g.fileErrs, "; "))
	}
	return g.rep, s.Store.GitRead(ctx, g.rep.Head, readErr, started)
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
			// Only a draft: nothing to commit. A file the repository
			// doesn't have isn't kept track of.
			if f.SeenBlob == nil && g.blobAt(f.Path) == "" {
				return st.DeleteGitFile(ctx, f.Path, f.Revision)
			}
			return st.GitNothingToCommit(ctx, f.Path, f.Revision)
		}
		to = g.repo.FilePath(it.Slug)
	}
	if !hasItem || f.Remove {
		if g.blobAt(f.Path) == "" {
			return st.DeleteGitFile(ctx, f.Path, f.Revision)
		}
	}
	for try := 1; ; try++ {
		if (!hasItem || f.Remove) && g.blobAt(f.Path) != deref(f.SeenBlob) {
			// Deleted in rvav, changed in the repository: nothing to
			// settle it on. The file is a new one, a new item's draft.
			if err := st.Audit(ctx, st.Pool, store.AuditEvent{ActorID: new(SystemUserID), Action: "catalog.git.removal_dropped",
				TargetType: "catalog_git_file", TargetID: f.Path, Detail: map[string]any{"item_id": deref(f.ItemID)}}); err != nil {
				g.s.Log.Error("audit", "action", "catalog.git.removal_dropped", "err", err)
			}
			return st.ForgetGitFile(ctx, f.Path)
		}
		// A change of the repository's rvav hasn't seen: the same as
		// rvav's, they agree; another, a conflict.
		if g.blobAt(f.Path) != deref(f.SeenBlob) {
			cur, ok := g.byPath[f.Path]
			if def, meta := sameFile(content, cur.Content); ok && content != nil && def && meta {
				return st.GitAgreed(ctx, f.Path, cur.Hash, g.lastCommit(ctx, f.Path), "", f.Revision)
			}
			if !ok {
				return g.conflict(ctx, f, it, "Deleted in the repository since rvav last read it.")
			}
			return g.conflict(ctx, f, it, "Changed in the repository since rvav last read it.")
		}
		if to != f.Path && g.blobAt(to) != "" {
			// Another file has the item's new name: tried again until it
			// is renamed or removed there, or the item renamed again.
			g.failed(ctx, f, fmt.Errorf("the repository already has %s: rename or remove it there, or give the item another slug", to))
			return nil
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
	if err := g.s.Store.GitConflict(ctx, f.Path, cur.Hash, storable(cur.Content), note, g.lastCommit(ctx, f.Path)); err != nil {
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
	wait := min(30*time.Second<<min(f.Attempts, 10), max(g.s.liveConfig(ctx).Git.Interval, time.Minute))
	if err := g.s.Store.GitCommitFailed(ctx, f.Path, cause.Error(), time.Now().Add(wait)); err != nil {
		g.s.Log.Error("catalog in Git: record a failed commit", "path", f.Path, "err", err)
	}
	if f.Attempts == 0 {
		g.s.auditGit(ctx, "catalog.git.commit_failed", deref(f.ItemID), map[string]any{"path": f.Path, "error": cause.Error()})
	}
}

// takeChanges takes what changed in the repository since rvav last read
// it: a changed file becomes a draft of its item, a new file a new item
// (never published by themselves); a deleted file, or one the import
// refuses, is reported. A file rvav has a change of to commit is left to
// commit: the same as rvav's, they agree; another, a conflict.
func (g *gitSync) takeChanges(ctx context.Context) error {
	files, err := g.s.Store.GitFiles(ctx)
	if err != nil {
		return err
	}
	rows := map[string]store.GitFile{}
	for _, f := range files {
		rows[f.Path] = f
	}
	paths := slices.Sorted(maps.Keys(g.byPath))
	for _, p := range paths {
		file := g.byPath[p]
		row, known := rows[p]
		switch {
		case known && deref(row.SeenBlob) == file.Hash:
		case known && row.Pending && row.State != "conflict":
			// commit sees it.
		case known && row.State == "conflict":
			if deref(row.RepoBlob) != file.Hash {
				// Changed again: the conflict shows the repository's last.
				if err := g.s.Store.GitConflict(ctx, p, file.Hash, storable(file.Content), row.Note, g.lastCommit(ctx, p)); err != nil {
					return err
				}
			}
		default:
			var r *store.GitFile
			if known {
				r = &row
			}
			if err := g.proposed(ctx, r, file); err != nil {
				// Tried again at the next read; the others go on.
				g.s.Log.Warn("catalog in Git: a file of the repository wasn't taken", "path", p, "err", err)
				g.fileErrs = append(g.fileErrs, fmt.Sprintf("%s: %v", p, err))
			}
		}
	}
	for _, row := range files {
		if g.blobAt(row.Path) != "" || row.SeenBlob == nil || row.Pending || row.State == "deleted" {
			continue
		}
		if row.ItemID == nil {
			// A file no item came from, gone: nothing to report.
			if err := g.s.Store.ForgetGitFile(ctx, row.Path); err != nil {
				return err
			}
			continue
		}
		if err := g.s.Store.GitDeleted(ctx, row.Path,
			"Deleted in the repository: rvav keeps the item, and adds its file back when it next commits a change of it."); err != nil {
			return err
		}
		g.rep.Deleted++
		g.s.auditGit(ctx, "catalog.git.deleted", *row.ItemID, map[string]any{"path": row.Path})
	}
	return nil
}

// proposed takes a file the repository changed, or added (row nil when
// rvav doesn't know of it).
func (g *gitSync) proposed(ctx context.Context, row *store.GitFile, file gitrepo.File) error {
	st, commit := g.s.Store, g.lastCommit(ctx, file.Path)
	it, hasItem := store.AdminItem{}, false
	if row != nil && row.ItemID != nil {
		it, hasItem = g.items[*row.ItemID]
	}
	switch {
	case file.TooLarge:
		return g.refused(ctx, row, file, nil, commit, []api.FieldError{fieldErr("", "too_large",
			fmt.Sprintf("The file is over %d MiB: an item's file isn't that large.", gitrepo.MaxFileSize>>20))})
	case storable(file.Content) == "" && len(file.Content) > 0:
		return g.refused(ctx, row, file, nil, commit, []api.FieldError{fieldErr("", "encoding",
			"The file isn't UTF-8 text.")})
	}
	revision := int64(0)
	if row != nil {
		revision = row.Revision
	}
	if !hasItem {
		found, err := st.GetAdminItem(ctx, file.Slug)
		switch {
		case err == nil && found.SourceKind != "native":
			return g.refused(ctx, row, file, nil, commit, []api.FieldError{fieldErr("/metadata/slug", "awx",
				fmt.Sprintf("%s is an item imported from AWX: AWX's sync owns it, not the repository.", found.Slug))})
		case err == nil:
			if other, err := st.GitFileOfItem(ctx, found.ID); err == nil && other.Path != file.Path {
				// Not the item's file: rvav keeps it elsewhere.
				return g.refused(ctx, row, file, nil, commit, []api.FieldError{fieldErr("/metadata/slug", "duplicate",
					fmt.Sprintf("rvav keeps %s in %s.", found.Slug, other.Path))})
			}
			it, hasItem = found, true
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
	}
	var doc itemDoc
	if yaml.Unmarshal(file.Content, &doc) == nil && doc.Metadata.Slug != "" && doc.Metadata.Slug != file.Slug {
		var id *string
		if hasItem {
			id = &it.ID
		}
		return g.refused(ctx, row, file, id, commit, []api.FieldError{fieldErr("/metadata/slug", "invalid",
			fmt.Sprintf("The file is %s.yaml, its slug %s: they must be the same.", file.Slug, doc.Metadata.Slug))})
	}
	if !hasItem {
		// A new item, a draft.
		res, err := g.s.importItem(ctx, system(), file.Content, "", true)
		if problems, ok := importProblems(err); ok {
			return g.refused(ctx, row, file, nil, commit, problems)
		} else if err != nil {
			return err
		}
		return g.drafted(ctx, file, res.ItemId, commit, "A new item, from the repository.", revision)
	}

	if row == nil {
		if err := st.AddGitFile(ctx, file.Path, &it.ID, nil); err != nil {
			return err
		}
		row = &store.GitFile{Path: file.Path, ItemID: &it.ID}
	}
	ours, _, err := g.s.gitFile(ctx, it)
	if err != nil {
		return err
	}
	def, meta := sameFile(ours, file.Content)
	note := ""
	if ours != nil && !meta {
		note = "Its details (name, summary, category...) follow rvav: the repository's aren't taken."
	}
	if ours != nil && def {
		// The same definition: agreed. Details changed there are put
		// back as rvav has them.
		if err := st.GitAgreed(ctx, file.Path, file.Hash, commit, note, row.Revision); err != nil {
			return err
		}
		if !meta {
			g.s.auditGit(ctx, "catalog.git.details_kept", it.ID, map[string]any{"path": file.Path})
			return st.MarkGitPending(ctx, it.ID, file.Path, SystemUserID, "Keep "+it.Slug+"'s details from rvav")
		}
		return nil
	}
	versions, err := st.ItemVersions(ctx, it.ID)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(versions, func(v store.VersionRow) bool { return v.State == "draft" }); i >= 0 {
		d := versions[i]
		if deref(row.DraftVersionID) != d.ID || row.DraftRevision == nil || *row.DraftRevision != d.Revision {
			// A draft of rvav's is open: both changed the item.
			return g.conflict(ctx, *row, it,
				"rvav has a draft of "+it.Slug+" open ("+d.Number+"): publish or discard it, or take the repository's change.")
		}
		// The draft rvav made of the repository's earlier change,
		// untouched: made again of this one.
		if err := st.DeleteDraft(ctx, d.ID); err != nil && !errors.Is(err, store.ErrVersionState) {
			return err
		}
	}
	res, err := g.s.importItem(ctx, system(), file.Content, it.ID, false)
	if problems, ok := importProblems(err); ok {
		return g.refused(ctx, row, file, &it.ID, commit, problems)
	} else if err != nil {
		return err
	}
	return g.drafted(ctx, file, res.ItemId, commit, note, row.Revision)
}

// importProblems are why the import refused a file.
func importProblems(err error) ([]api.FieldError, bool) {
	var v *ValidationError
	var c *ConflictError
	switch {
	case errors.As(err, &v):
		return v.Errors, true
	case errors.As(err, &c):
		return []api.FieldError{fieldErr("", "conflict", c.Msg)}, true
	}
	return nil, false
}

// drafted records the draft made of the repository's file.
func (g *gitSync) drafted(ctx context.Context, file gitrepo.File, itemID string, commit *gitrepo.Commit, note string,
	fileRevision int64) error {
	versions, err := g.s.Store.ItemVersions(ctx, itemID)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(versions, func(v store.VersionRow) bool { return v.State == "draft" })
	if i < 0 {
		return fmt.Errorf("the import of %s made no draft", file.Path)
	}
	d := versions[i]
	if ok, err := g.s.Store.GitDraft(ctx, file.Path, itemID, file.Hash, d.ID, d.Revision, commit, note, fileRevision); err != nil {
		return err
	} else if !ok {
		// rvav marked a change of the item meanwhile: its commit finds the
		// repository's change, a conflict.
		return nil
	}
	g.rep.Drafts++
	detail := map[string]any{"path": file.Path, "version": d.Number}
	if commit != nil {
		detail["commit"] = commit.SHA
	}
	g.s.auditGit(ctx, "catalog.git.draft", itemID, detail)
	return nil
}

// refused records a file the import refused, with its problems.
func (g *gitSync) refused(ctx context.Context, row *store.GitFile, file gitrepo.File, itemID *string, commit *gitrepo.Commit,
	problems []api.FieldError) error {
	if row != nil && row.ItemID != nil {
		itemID = row.ItemID
	}
	if err := g.s.Store.GitRefused(ctx, file.Path, itemID, file.Hash, storable(file.Content), problems, commit); err != nil {
		return err
	}
	g.rep.Refused++
	if itemID != nil {
		g.s.auditGit(ctx, "catalog.git.refused", *itemID, map[string]any{"path": file.Path, "problems": len(problems)})
	}
	return nil
}

// catchUp commits what rvav has and the repository's unchanged file
// doesn't, when no change was marked for it: one whose mark failed, or a
// change of the repository's set aside in rvav (its draft discarded).
func (g *gitSync) catchUp(ctx context.Context) error {
	files, err := g.s.Store.GitFiles(ctx)
	if err != nil {
		return err
	}
	for _, f := range files {
		setAside := f.State == "draft" && f.DraftVersionID == nil
		if f.Pending || f.State != "synced" && !setAside || f.ItemID == nil || f.SeenBlob == nil || g.blobAt(f.Path) != *f.SeenBlob {
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
		message := "Update " + it.Slug + " from rvav"
		if setAside {
			message = "Keep " + it.Slug + " as rvav has it: the repository's change was discarded there"
		}
		if err := g.s.Store.MarkGitPending(ctx, it.ID, f.Path, SystemUserID, message); err != nil {
			return err
		}
		f.Pending, f.PendingMessage, f.Revision = true, message, f.Revision+1
		f.PendingBy, f.PendingByName, f.PendingByEmail = new(SystemUserID), new("rvav"), nil
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

// storable is a file's content as the database keeps it: "" when it isn't
// UTF-8 text.
func storable(b []byte) string {
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return ""
	}
	return string(b)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

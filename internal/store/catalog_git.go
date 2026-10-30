package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// The catalog in Git (ADR-0029): each item's file in the repository, what
// rvav waits to commit, and what the repository proposed.

// LockGitSync keeps one read of the catalog's repository at a time, in any
// process.
const LockGitSync int64 = 0x7276_6176_0006

// GitFile is an item's file in the repository, as rvav keeps track of it.
type GitFile struct {
	Path           string          `db:"path"`
	ItemID         *string         `db:"item_id"`
	State          string          `db:"state"`
	SeenBlob       *string         `db:"seen_blob"`
	RepoBlob       *string         `db:"repo_blob"`
	RepoContent    *string         `db:"repo_content"`
	Problems       json.RawMessage `db:"problems"`
	Note           string          `db:"note"`
	LastCommit     json.RawMessage `db:"last_commit"`
	DraftVersionID *string         `db:"draft_version_id"`
	DraftRevision  *int            `db:"draft_revision"`
	Pending        bool            `db:"pending"`
	Remove         bool            `db:"remove"`
	PendingBy      *string         `db:"pending_by"`
	PendingByName  *string         `db:"pending_by_name"`
	PendingByEmail *string         `db:"pending_by_email"`
	PendingMessage string          `db:"pending_message"`
	PendingAt      *time.Time      `db:"pending_at"`
	Revision       int64           `db:"revision"`
	Attempts       int             `db:"attempts"`
	NextAttemptAt  *time.Time      `db:"next_attempt_at"`
	Error          *string         `db:"error"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

const gitFileSelect = `select f.path, f.item_id, f.state, f.seen_blob, f.repo_blob, f.repo_content, f.problems, f.note,
	f.last_commit, f.draft_version_id, f.draft_revision, f.pending, f.remove, f.pending_by, u.name as pending_by_name,
	u.email as pending_by_email, f.pending_message, f.pending_at, f.revision, f.attempts, f.next_attempt_at, f.error, f.updated_at
	from catalog_git_files f left join users u on u.id = f.pending_by`

// GitFiles are every file rvav keeps track of, by path.
func (s *Store) GitFiles(ctx context.Context) ([]GitFile, error) {
	rows, _ := s.Pool.Query(ctx, gitFileSelect+` order by f.path`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[GitFile])
}

// GitFileOfItem is an item's file, ErrNotFound when it has none yet.
func (s *Store) GitFileOfItem(ctx context.Context, itemID string) (GitFile, error) {
	rows, _ := s.Pool.Query(ctx, gitFileSelect+` where f.item_id = $1`, itemID)
	f, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[GitFile])
	return f, notFound(err)
}

// MarkGitPending says rvav has a change of the item to commit to its file
// at path: by whom, and the commit's message. A newer change replaces one
// not committed yet.
func (s *Store) MarkGitPending(ctx context.Context, itemID, path, by, message string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		set := `pending = true, pending_by = $2, pending_message = $3, pending_at = now(), revision = revision + 1,
			attempts = 0, next_attempt_at = now(), error = null, updated_at = now()`
		tag, err := tx.Exec(ctx, `update catalog_git_files set `+set+` where item_id = $1`, itemID, by, message)
		if err != nil || tag.RowsAffected() > 0 {
			return err
		}
		// A file of that path no item came from (one the import refused)
		// becomes the item's.
		tag, err = tx.Exec(ctx, `update catalog_git_files set item_id = $1, remove = false, `+set+`
			where path = $4 and item_id is null`, itemID, by, message, path)
		if err != nil || tag.RowsAffected() > 0 {
			return err
		}
		_, err = tx.Exec(ctx, `insert into catalog_git_files (path, item_id, pending, pending_by, pending_message, pending_at,
			revision, next_attempt_at) values ($4, $1, true, $2, $3, now(), 1, now())`, itemID, by, message, path)
		return err
	})
}

// MarkGitRemove says the file at path is to be removed: its item was
// deleted.
func (s *Store) MarkGitRemove(ctx context.Context, path, by, message string) error {
	_, err := s.Pool.Exec(ctx, `update catalog_git_files set pending = true, remove = true, pending_by = $2, pending_message = $3,
		pending_at = now(), revision = revision + 1, attempts = 0, next_attempt_at = now(), error = null, updated_at = now()
		where path = $1`, path, by, message)
	return err
}

// GitCommitted records a commit of the file: now at path (the item's slug
// may have changed), with that blob. A change marked since revision stays
// to be committed.
func (s *Store) GitCommitted(ctx context.Context, oldPath, path, blob string, commit any, revision int64) error {
	b, _ := json.Marshal(commit)
	_, err := s.Pool.Exec(ctx, `update catalog_git_files set path = $2, seen_blob = $3, last_commit = $4, state = 'synced',
		repo_blob = null, repo_content = null, problems = null, note = '', error = null, attempts = 0,
		draft_version_id = null, draft_revision = null,
		pending = revision <> $5, next_attempt_at = case when revision <> $5 then now() end, updated_at = now()
		where path = $1`, oldPath, path, blob, b, revision)
	return err
}

// GitAgreed records that the repository's file is what rvav would commit:
// nothing to commit, unless a change was marked since revision.
func (s *Store) GitAgreed(ctx context.Context, path, blob string, commit any, note string, revision int64) error {
	b, _ := json.Marshal(commit)
	_, err := s.Pool.Exec(ctx, `update catalog_git_files set seen_blob = $2, last_commit = coalesce($3, last_commit), state = 'synced',
		repo_blob = null, repo_content = null, problems = null, note = $4, error = null, attempts = 0,
		draft_version_id = null, draft_revision = null,
		pending = pending and revision <> $5, updated_at = now() where path = $1`, path, blob, jsonOrNil(b), note, revision)
	return err
}

// GitCommitFailed records a failed commit, tried again at next.
func (s *Store) GitCommitFailed(ctx context.Context, path, msg string, next time.Time) error {
	_, err := s.Pool.Exec(ctx, `update catalog_git_files set error = $2, attempts = attempts + 1, next_attempt_at = $3,
		updated_at = now() where path = $1`, path, msg, next)
	return err
}

// GitConflict records that the repository changed the file rvav has to
// commit: what the repository has, and why.
func (s *Store) GitConflict(ctx context.Context, path, blob, content, note string, commit any) error {
	b, _ := json.Marshal(commit)
	_, err := s.Pool.Exec(ctx, `update catalog_git_files set state = 'conflict', repo_blob = $2, repo_content = $3, note = $4,
		last_commit = coalesce($5, last_commit), error = null, updated_at = now() where path = $1`, path, blob, content, note, jsonOrNil(b))
	return err
}

// GitDraft records the draft made of the repository's file at path, seen
// as blob: of the item, made by that commit. rvav's change not committed
// yet, if any, is dropped: the draft takes its place.
func (s *Store) GitDraft(ctx context.Context, path, itemID, blob, versionID string, revision int, commit any, note string) error {
	b, _ := json.Marshal(commit)
	_, err := s.Pool.Exec(ctx, `insert into catalog_git_files (path, item_id, state, seen_blob, draft_version_id, draft_revision,
			last_commit, note) values ($1, $2, 'draft', $3, $4, $5, $6, $7)
		on conflict (path) do update set item_id = $2, state = 'draft', seen_blob = $3, draft_version_id = $4, draft_revision = $5,
			last_commit = coalesce($6, catalog_git_files.last_commit), note = $7, repo_blob = null, repo_content = null,
			problems = null, pending = false, remove = false, error = null, attempts = 0, revision = catalog_git_files.revision + 1,
			updated_at = now()`, path, itemID, blob, versionID, revision, jsonOrNil(b), note)
	return err
}

// GitRefused records that the import refused the repository's file at
// path (of the item, if it has one): the file, and its problems.
func (s *Store) GitRefused(ctx context.Context, path string, itemID *string, blob, content string, problems, commit any) error {
	p, _ := json.Marshal(problems)
	b, _ := json.Marshal(commit)
	_, err := s.Pool.Exec(ctx, `insert into catalog_git_files (path, item_id, state, seen_blob, repo_blob, repo_content, problems,
			last_commit) values ($1, $2, 'refused', $3, $3, $4, $5, $6)
		on conflict (path) do update set state = 'refused', seen_blob = $3, repo_blob = $3, repo_content = $4, problems = $5,
			last_commit = coalesce($6, catalog_git_files.last_commit), note = '', error = null, updated_at = now()`,
		path, itemID, blob, content, p, jsonOrNil(b))
	return err
}

// GitDeleted records that the repository deleted the file: rvav keeps
// its item, and adds the file back when it commits a change of it.
func (s *Store) GitDeleted(ctx context.Context, path, note string) error {
	_, err := s.Pool.Exec(ctx, `update catalog_git_files set state = 'deleted', seen_blob = null, repo_blob = null,
		repo_content = null, problems = null, note = $2, draft_version_id = null, draft_revision = null, updated_at = now()
		where path = $1`, path, note)
	return err
}

// ForgetGitFile forgets the file at path, whatever it was.
func (s *Store) ForgetGitFile(ctx context.Context, path string) error {
	_, err := s.Pool.Exec(ctx, `delete from catalog_git_files where path = $1`, path)
	return err
}

// GitTakeRvav resolves a conflict for rvav: the repository's file, as the
// administrator saw it, is replaced by rvav's. False when it is no longer a
// conflict.
func (s *Store) GitTakeRvav(ctx context.Context, path, by, message string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update catalog_git_files set state = 'synced', seen_blob = repo_blob, repo_blob = null,
		repo_content = null, problems = null, note = '', pending = true, remove = false, pending_by = $2,
		pending_message = $3, pending_at = now(), revision = revision + 1, attempts = 0, next_attempt_at = now(), error = null,
		draft_version_id = null, draft_revision = null, updated_at = now() where path = $1 and state = 'conflict'`, path, by, message)
	return tag.RowsAffected() == 1, err
}

// AddGitFile keeps track of a file the repository has, seen as blob (nil:
// not seen yet).
func (s *Store) AddGitFile(ctx context.Context, path string, itemID *string, blob *string) error {
	_, err := s.Pool.Exec(ctx, `insert into catalog_git_files (path, item_id, seen_blob) values ($1, $2, $3)
		on conflict (path) do nothing`, path, itemID, blob)
	return err
}

// DeleteGitFile forgets a file: its removal is committed.
func (s *Store) DeleteGitFile(ctx context.Context, path string, revision int64) error {
	_, err := s.Pool.Exec(ctx, `delete from catalog_git_files where path = $1 and revision = $2`, path, revision)
	return err
}

// GitRepo is the repository as last read.
type GitRepo struct {
	Identity        string     `db:"identity"`
	Head            *string    `db:"head"`
	ReadAt          *time.Time `db:"read_at"`
	Error           *string    `db:"error"`
	ReadRequestedAt *time.Time `db:"read_requested_at"`
}

// GetGitRepo is the repository as last read; ErrNotFound before the first
// read.
func (s *Store) GetGitRepo(ctx context.Context) (GitRepo, error) {
	rows, _ := s.Pool.Query(ctx, `select identity, head, read_at, error, read_requested_at from catalog_git_repo`)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[GitRepo])
	return r, notFound(err)
}

// StartGitRepo makes identity the repository rvav reads. Another one than
// before starts afresh: what rvav knew of the files was of the other.
func (s *Store) StartGitRepo(ctx context.Context, identity string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		var was string
		err := tx.QueryRow(ctx, `select identity from catalog_git_repo for update`).Scan(&was)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `insert into catalog_git_repo (identity) values ($1)`, identity)
			return err
		}
		if err != nil || was == identity {
			return err
		}
		if _, err := tx.Exec(ctx, `update catalog_git_repo set identity = $1, head = null, read_at = null, error = null`, identity); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `delete from catalog_git_files`)
		return err
	})
}

// GitRead records a read of the repository: its head, or why it failed.
func (s *Store) GitRead(ctx context.Context, head string, readErr error, requestedBefore time.Time) error {
	var msg *string
	if readErr != nil {
		m := readErr.Error()
		msg = &m
	}
	_, err := s.Pool.Exec(ctx, `update catalog_git_repo set head = case when $2::text is null then nullif($1, '') else head end,
		read_at = now(), error = $2, read_requested_at = case when read_requested_at <= $3 then null else read_requested_at end`,
		head, msg, requestedBefore)
	return err
}

// RequestGitRead asks the sync process to read the repository now.
func (s *Store) RequestGitRead(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `update catalog_git_repo set read_requested_at = now()`)
	return err
}

// GitDue says whether the repository is to be read: never read, read
// longer ago than interval, asked for, or a change waits to be committed.
func (s *Store) GitDue(ctx context.Context, interval time.Duration) (bool, error) {
	var due bool
	err := s.Pool.QueryRow(ctx, `select not exists (select 1 from catalog_git_repo)
		or exists (select 1 from catalog_git_repo where read_at is null or read_at < now() - make_interval(secs => $1)
			or read_requested_at is not null)
		or exists (select 1 from catalog_git_files where pending and state <> 'conflict' and next_attempt_at <= now())`,
		interval.Seconds()).Scan(&due)
	return due, err
}

// jsonOrNil is nil for JSON's null: SQL's null, so coalesce keeps a value.
func jsonOrNil(b []byte) []byte {
	if string(b) == "null" {
		return nil
	}
	return b
}

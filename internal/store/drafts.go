package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// RequestDraft is a request saved to finish later: its owner's only.
type RequestDraft struct {
	ID          string          `db:"id"`
	UserID      string          `db:"user_id"`
	ItemID      string          `db:"item_id"`
	ItemSlug    string          `db:"item_slug"`
	ItemName    string          `db:"item_name"`
	ProjectID   string          `db:"project_id"`
	ProjectCode string          `db:"project_code"`
	ProjectName string          `db:"project_name"`
	Name        string          `db:"name"`
	Draft       json.RawMessage `db:"draft"`
	CreatedAt   time.Time       `db:"created_at"`
	UpdatedAt   time.Time       `db:"updated_at"`
}

const draftSelect = `select d.id, d.user_id, d.item_id, i.slug as item_slug, i.name as item_name, d.project_id,
	p.code as project_code, p.name as project_name, d.name, d.draft, d.created_at, d.updated_at
	from request_drafts d join catalog_items i on i.id = d.item_id join projects p on p.id = d.project_id`

// RequestDrafts are a user's drafts, last changed first.
func (s *Store) RequestDrafts(ctx context.Context, userID string) ([]RequestDraft, error) {
	rows, _ := s.Pool.Query(ctx, draftSelect+` where d.user_id = $1 order by d.updated_at desc, d.id desc`, userID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[RequestDraft])
}

// GetRequestDraft is one of a user's drafts (ErrNotFound for anyone else's).
func (s *Store) GetRequestDraft(ctx context.Context, userID, id string) (RequestDraft, error) {
	rows, _ := s.Pool.Query(ctx, draftSelect+` where d.user_id = $1 and d.id = $2`, userID, id)
	d, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[RequestDraft])
	return d, notFound(err)
}

// SaveRequestDraft creates or replaces one of a user's drafts.
func (s *Store) SaveRequestDraft(ctx context.Context, d RequestDraft) error {
	tag, err := s.Pool.Exec(ctx, `insert into request_drafts (id, user_id, item_id, project_id, name, draft)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (id) do update set item_id = excluded.item_id, project_id = excluded.project_id, name = excluded.name,
			draft = excluded.draft, updated_at = now()
		where request_drafts.user_id = excluded.user_id`, d.ID, d.UserID, d.ItemID, d.ProjectID, d.Name, d.Draft)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteRequestDraft discards one of a user's drafts.
func (s *Store) DeleteRequestDraft(ctx context.Context, userID, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from request_drafts where user_id = $1 and id = $2`, userID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

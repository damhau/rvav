package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// SavedView is a list's query saved under a name: its owner's only.
type SavedView struct {
	ID        string          `db:"id"`
	UserID    string          `db:"user_id"`
	List      string          `db:"list"`
	Name      string          `db:"name"`
	Query     json.RawMessage `db:"query"`
	CreatedAt time.Time       `db:"created_at"`
	UpdatedAt time.Time       `db:"updated_at"`
}

const savedViewCols = `id, user_id, list, name, query, created_at, updated_at`

// SavedViews are a user's views of a list, by name.
func (s *Store) SavedViews(ctx context.Context, userID, list string) ([]SavedView, error) {
	rows, _ := s.Pool.Query(ctx, `select `+savedViewCols+` from saved_views where user_id = $1 and list = $2
		order by lower(name), id`, userID, list)
	return pgx.CollectRows(rows, pgx.RowToStructByName[SavedView])
}

// GetSavedView is one of a user's views (ErrNotFound for anyone else's).
func (s *Store) GetSavedView(ctx context.Context, userID, id string) (SavedView, error) {
	rows, _ := s.Pool.Query(ctx, `select `+savedViewCols+` from saved_views where user_id = $1 and id = $2`, userID, id)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[SavedView])
	return v, notFound(err)
}

// SaveView creates or replaces one of a user's views; a name the user
// already gave another view of the list is a unique violation.
func (s *Store) SaveView(ctx context.Context, v SavedView) error {
	tag, err := s.Pool.Exec(ctx, `insert into saved_views (id, user_id, list, name, query) values ($1, $2, $3, $4, $5)
		on conflict (id) do update set list = excluded.list, name = excluded.name, query = excluded.query, updated_at = now()
		where saved_views.user_id = excluded.user_id`, v.ID, v.UserID, v.List, v.Name, v.Query)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteSavedView deletes one of a user's views.
func (s *Store) DeleteSavedView(ctx context.Context, userID, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from saved_views where user_id = $1 and id = $2`, userID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// CountSavedViews is how many views of a list a user has.
func (s *Store) CountSavedViews(ctx context.Context, userID, list string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) from saved_views where user_id = $1 and list = $2`, userID, list).Scan(&n)
	return n, err
}

package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Day2Form is a form an administrator designed for a custom day-2 action
// (ADR-0050).
type Day2Form struct {
	Action        string          `db:"action"`
	FormSchema    json.RawMessage `db:"form_schema"`
	UISchema      json.RawMessage `db:"ui_schema"`
	InitialValues json.RawMessage `db:"initial_values"`
	Active        bool            `db:"active"`
	Revision      int             `db:"revision"`
	UpdatedBy     *string         `db:"updated_by"`
	UpdatedByName *string         `db:"updated_by_name"`
	UpdatedAt     time.Time       `db:"updated_at"`
}

// ErrFormChanged: the designed form was saved elsewhere since the revision given.
var ErrFormChanged = errors.New("the form changed since it was read")

const day2FormCols = `f.action, f.form_schema, f.ui_schema, f.initial_values, f.active, f.revision, f.updated_by,
	u.name as updated_by_name, f.updated_at`

// Day2Forms lists the designed forms, by action.
func (s *Store) Day2Forms(ctx context.Context) ([]Day2Form, error) {
	rows, _ := s.Pool.Query(ctx, `select `+day2FormCols+` from day2_forms f left join users u on u.id = f.updated_by
		order by f.action`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Day2Form])
}

// GetDay2Form is the form designed for an action (ErrNotFound when none).
func (s *Store) GetDay2Form(ctx context.Context, action string) (Day2Form, error) {
	rows, _ := s.Pool.Query(ctx, `select `+day2FormCols+` from day2_forms f left join users u on u.id = f.updated_by
		where f.action = $1`, action)
	f, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Day2Form])
	return f, notFound(err)
}

// SaveDay2Form saves an action's form on base, the revision it was
// changed from (0: there was none; nil: whatever there is), and returns the
// new revision. active nil keeps it as it is, and a new form inactive.
func (s *Store) SaveDay2Form(ctx context.Context, f Day2Form, active *bool, base *int, by string) (int, error) {
	args := []any{f.Action, f.FormSchema, f.UISchema, jsonOr(f.InitialValues, "{}"), active, by, base}
	q := `insert into day2_forms as f (action, form_schema, ui_schema, initial_values, active, updated_by)
		values ($1, $2, $3, $4, coalesce($5, false), $6)
		on conflict (action) do update set form_schema = excluded.form_schema, ui_schema = excluded.ui_schema,
			initial_values = excluded.initial_values, active = coalesce($5, f.active), updated_by = excluded.updated_by,
			updated_at = now(), revision = f.revision + 1
		where $7::int is null or f.revision = $7
		returning revision`
	if base != nil && *base > 0 {
		// Saved on a form that was there: one dropped since is a conflict too.
		q = `update day2_forms set form_schema = $2, ui_schema = $3, initial_values = $4, active = coalesce($5, active),
			updated_by = $6, updated_at = now(), revision = revision + 1
			where action = $1 and revision = $7 returning revision`
	}
	var revision int
	err := s.Pool.QueryRow(ctx, q, args...).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrFormChanged
	}
	return revision, err
}

// DeleteDay2Form drops an action's designed form, if it has one.
func (s *Store) DeleteDay2Form(ctx context.Context, action string) error {
	_, err := s.Pool.Exec(ctx, `delete from day2_forms where action = $1`, action)
	return err
}

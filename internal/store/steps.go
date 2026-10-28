package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/ids"
)

const stepCols = `id, request_id, parent_id, position, key, title, detail, system, status, started_at,
	finished_at, error, external_kind, external_id, external_url, has_log, outputs`

// InsertSteps creates the pending top-level steps of a request.
func (s *Store) InsertSteps(ctx context.Context, db DB, steps []Step) error {
	for _, st := range steps {
		_, err := db.Exec(ctx, `insert into request_steps (id, request_id, parent_id, position, key, title, detail,
			system, status, started_at, finished_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			st.ID, st.RequestID, st.ParentID, st.Position, st.Key, st.Title, st.Detail, st.System, st.Status,
			st.StartedAt, st.FinishedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListSteps(ctx context.Context, requestID string) ([]Step, error) {
	rows, _ := s.Pool.Query(ctx, `select `+stepCols+` from request_steps where request_id = $1
		order by parent_id nulls first, position, id`, requestID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Step])
}

func (s *Store) GetStep(ctx context.Context, requestID, stepID string) (Step, error) {
	rows, _ := s.Pool.Query(ctx, `select `+stepCols+` from request_steps where request_id = $1 and id = $2`, requestID, stepID)
	st, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Step])
	return st, notFound(err)
}

func (s *Store) GetStepByKey(ctx context.Context, requestID, key string) (Step, error) {
	rows, _ := s.Pool.Query(ctx, `select `+stepCols+` from request_steps where request_id = $1 and key = $2`, requestID, key)
	st, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Step])
	return st, notFound(err)
}

// StepUpdate changes a step. Nil fields are left as they are.
type StepUpdate struct {
	Status       string
	Title        *string
	Detail       *string
	Error        *string
	ExternalKind *string
	ExternalID   *string
	ExternalURL  *string
	HasLog       *bool
}

// UpdateStep applies u and sets started_at/finished_at from the status.
func (s *Store) UpdateStep(ctx context.Context, db DB, requestID, key string, u StepUpdate) (Step, error) {
	rows, _ := db.Query(ctx, `update request_steps set
		status = coalesce(nullif($3, ''), status),
		title = coalesce($4, title),
		detail = coalesce($5, detail),
		error = case when $3 = 'running' then null else coalesce($6, error) end,
		external_kind = coalesce($7, external_kind),
		external_id = coalesce($8, external_id),
		external_url = coalesce($9, external_url),
		has_log = coalesce($10, has_log),
		started_at = case when $3 = 'running' then coalesce(started_at, now()) else started_at end,
		finished_at = case when $3 in ('done', 'failed', 'skipped', 'cancelled') then now()
		                   when $3 = 'running' then null else finished_at end
		where request_id = $1 and key = $2
		returning `+stepCols,
		requestID, key, u.Status, u.Title, u.Detail, u.Error, u.ExternalKind, u.ExternalID, u.ExternalURL, u.HasLog)
	st, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Step])
	return st, notFound(err)
}

// UpsertChildStep creates or updates a child step (for example one AWX task).
func (s *Store) UpsertChildStep(ctx context.Context, db DB, requestID, parentKey, key, title, status string, errText *string) (Step, error) {
	rows, _ := db.Query(ctx, `insert into request_steps (id, request_id, parent_id, position, key, title, status,
			system, started_at, finished_at, error)
		select $1, $2, p.id,
		       (select count(*) from request_steps c where c.parent_id = p.id),
		       $4, $5, $6, p.system,
		       case when $6 <> 'pending' then now() end,
		       case when $6 in ('done', 'failed', 'skipped', 'cancelled') then now() end,
		       $7
		from request_steps p where p.request_id = $2 and p.key = $3
		on conflict (request_id, key) do update set
		       status = excluded.status,
		       title = excluded.title,
		       error = coalesce(excluded.error, request_steps.error),
		       finished_at = case when excluded.status in ('done', 'failed', 'skipped', 'cancelled')
		                          then coalesce(request_steps.finished_at, now()) end
		returning `+stepCols,
		ids.New(ids.Step), requestID, parentKey, key, title, status, errText)
	st, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Step])
	return st, notFound(err)
}

// FinishOpenChildren marks running children of a step with status.
func (s *Store) FinishOpenChildren(ctx context.Context, db DB, requestID, parentKey, status string) error {
	_, err := db.Exec(ctx, `update request_steps c set status = $3, finished_at = now()
		from request_steps p
		where p.request_id = $1 and p.key = $2 and c.parent_id = p.id and c.status in ('running', 'pending')`,
		requestID, parentKey, status)
	return err
}

// MarkRemainingSteps sets every pending or running top-level step to status.
func (s *Store) MarkRemainingSteps(ctx context.Context, db DB, requestID, status string) error {
	_, err := db.Exec(ctx, `update request_steps set status = $2,
		finished_at = case when status = 'running' then now() else finished_at end
		where request_id = $1 and status in ('pending', 'running')`, requestID, status)
	return err
}

// AppendLogLines adds lines after the last stored line and returns the new length.
func (s *Store) AppendLogLines(ctx context.Context, db DB, stepID string, lines []LogLine) (int, error) {
	var next int
	if err := db.QueryRow(ctx, `select coalesce(max(n) + 1, 0) from step_log_lines where step_id = $1`, stepID).Scan(&next); err != nil {
		return 0, err
	}
	if len(lines) == 0 {
		return next, nil
	}
	batch := &pgx.Batch{}
	for i, l := range lines {
		batch.Queue(`insert into step_log_lines (step_id, n, text, kind) values ($1, $2, $3, $4)
			on conflict do nothing`, stepID, next+i, l.Text, l.Kind)
	}
	batch.Queue(`update request_steps set has_log = true where id = $1`, stepID)
	if err := db.SendBatch(ctx, batch).Close(); err != nil {
		return 0, err
	}
	return next + len(lines), nil
}

func (s *Store) LogLines(ctx context.Context, stepID string, offset, limit int) ([]LogLine, error) {
	rows, _ := s.Pool.Query(ctx, `select n, text, kind from step_log_lines where step_id = $1 and n >= $2
		order by n limit $3`, stepID, offset, limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[LogLine])
}

// Now is the database clock, so step timestamps and API times agree.
func (s *Store) Now(ctx context.Context) (time.Time, error) {
	var t time.Time
	err := s.Pool.QueryRow(ctx, `select now()`).Scan(&t)
	return t, err
}

// ReplacePendingSteps swaps the top-level steps that have not started for
// new ones, after an approver changed what the request will do.
func (s *Store) ReplacePendingSteps(ctx context.Context, db DB, requestID string, steps []Step) error {
	if _, err := db.Exec(ctx, `delete from request_steps where request_id = $1 and parent_id is null
		and status = 'pending' and key not in ('submit', 'policy')`, requestID); err != nil {
		return err
	}
	return s.InsertSteps(ctx, db, steps)
}

// ResetForRetry makes a failed request runnable again: its failed, running
// and cancelled steps, with their sub-steps, are pending again, or every
// step after the approval when what it built was rolled back. A rollback
// step goes. The request is queued, without its error.
func (s *Store) ResetForRetry(ctx context.Context, db DB, requestID string, all bool) error {
	reset := `request_id = $1 and parent_id is null and status in ('failed', 'running', 'cancelled')`
	if all {
		reset = `request_id = $1 and parent_id is null and key not in ('submit', 'policy')`
	}
	// A rollback's step, done or failed, belongs to the run before.
	if _, err := db.Exec(ctx, `delete from request_steps where request_id = $1 and key = 'rollback'`, requestID); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, `delete from request_steps where parent_id in (select id from request_steps where `+reset+`)`, requestID); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, `delete from step_log_lines where step_id in (select id from request_steps where `+reset+`)`, requestID); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, `update request_steps set status = 'pending', started_at = null, finished_at = null, error = null,
		external_kind = null, external_id = null, external_url = null, has_log = false where `+reset, requestID); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `update requests set status = 'queued', error_step_id = null, error_message = null, finished_at = null
		where id = $1`, requestID)
	return err
}

// InsertStepBeforePending adds a top-level step just before the first one
// still pending (a step added while the request runs, such as the checks of
// blocking subscriptions); an existing one is set running again.
func (s *Store) InsertStepBeforePending(ctx context.Context, db DB, requestID, key, title, detail, system string) (Step, error) {
	if _, err := db.Exec(ctx, `with at as (
			select coalesce(min(position), (select count(*) from request_steps where request_id = $1 and parent_id is null)) as p
			from request_steps where request_id = $1 and parent_id is null and status = 'pending')
		update request_steps set position = position + 1
		where request_id = $1 and parent_id is null and position >= (select p from at)
			and not exists (select 1 from request_steps where request_id = $1 and key = $2)`, requestID, key); err != nil {
		return Step{}, err
	}
	rows, _ := db.Query(ctx, `insert into request_steps (id, request_id, position, key, title, detail, system, status, started_at)
		select $3, $1, coalesce((select min(position) - 1 from request_steps where request_id = $1 and parent_id is null and status = 'pending'),
			(select count(*) from request_steps where request_id = $1 and parent_id is null)), $2, $4, $5, $6, 'running', now()
		on conflict (request_id, key) do update set status = 'running', error = null, finished_at = null
		returning `+stepCols, requestID, key, ids.New(ids.Step), title, detail, system)
	st, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Step])
	return st, notFound(err)
}

// SetStepOutputs keeps what a composite step gave.
func (s *Store) SetStepOutputs(ctx context.Context, requestID, key string, outputs json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `update request_steps set outputs = $3 where request_id = $1 and key = $2`, requestID, key, outputs)
	return err
}

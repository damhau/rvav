package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Custom workflows (ADR-0022).

// WorkflowRegistration is a custom Temporal workflow rvav may start.
type WorkflowRegistration struct {
	Key            string          `db:"key"`
	Title          string          `db:"title"`
	Description    string          `db:"description"`
	WorkflowType   string          `db:"workflow_type"`
	TaskQueue      string          `db:"task_queue"`
	InputSchema    json.RawMessage `db:"input_schema"`
	TimeoutSeconds int             `db:"timeout_seconds"`
	ProjectIDs     []string        `db:"project_ids"`
	Usages         []string        `db:"usages"`
	Day2           json.RawMessage `db:"day2"`
	Enabled        bool            `db:"enabled"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

const workflowCols = `key, title, description, workflow_type, task_queue, input_schema, timeout_seconds, project_ids,
	usages, day2, enabled, created_at, updated_at`

func (s *Store) WorkflowRegistrations(ctx context.Context) ([]WorkflowRegistration, error) {
	rows, _ := s.Pool.Query(ctx, `select `+workflowCols+` from workflow_registrations order by key`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[WorkflowRegistration])
}

func (s *Store) GetWorkflowRegistration(ctx context.Context, key string) (WorkflowRegistration, error) {
	rows, _ := s.Pool.Query(ctx, `select `+workflowCols+` from workflow_registrations where key = $1`, key)
	w, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[WorkflowRegistration])
	if errors.Is(err, pgx.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

// SaveWorkflowRegistration inserts or replaces one.
func (s *Store) SaveWorkflowRegistration(ctx context.Context, w WorkflowRegistration) error {
	_, err := s.Pool.Exec(ctx, `insert into workflow_registrations (key, title, description, workflow_type, task_queue,
			input_schema, timeout_seconds, project_ids, usages, day2, enabled) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		on conflict (key) do update set title = excluded.title, description = excluded.description,
			workflow_type = excluded.workflow_type, task_queue = excluded.task_queue, input_schema = excluded.input_schema,
			timeout_seconds = excluded.timeout_seconds, project_ids = excluded.project_ids, usages = excluded.usages,
			day2 = excluded.day2, enabled = excluded.enabled, updated_at = now()`,
		w.Key, w.Title, w.Description, w.WorkflowType, w.TaskQueue, w.InputSchema, w.TimeoutSeconds, nonNil(w.ProjectIDs),
		w.Usages, nullJSON(w.Day2), w.Enabled)
	return err
}

func (s *Store) DeleteWorkflowRegistration(ctx context.Context, key string) error {
	tag, err := s.Pool.Exec(ctx, `delete from workflow_registrations where key = $1`, key)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// WorkflowUse is what uses a registration, and how many of its runs aren't
// over.
type WorkflowUse struct {
	Running int
	UsedBy  []string
}

// WorkflowUses says, for each registration, what uses it: catalog items
// (published or draft versions), subscriptions, and its runs not ended.
func (s *Store) WorkflowUses(ctx context.Context) (map[string]WorkflowUse, error) {
	out := map[string]WorkflowUse{}
	rows, err := s.Pool.Query(ctx, `select w.key,
			(select count(*) from workflow_runs r where r.registration_key = w.key and r.status = 'running'),
			array(select distinct 'item ' || i.slug from catalog_item_versions v join catalog_items i on i.id = v.item_id
				where v.state in ('draft', 'published') and (v.blueprint->>'workflow' = w.key
					or jsonb_path_exists(v.blueprint, '$.**.workflow ? (@ == $k)', jsonb_build_object('k', w.key)))
				union select 'subscription ' || s.name from subscriptions s where s.target->'workflow'->>'key' = w.key)
		from workflow_registrations w`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var u WorkflowUse
		if err := rows.Scan(&key, &u.Running, &u.UsedBy); err != nil {
			return nil, err
		}
		out[key] = u
	}
	return out, rows.Err()
}

// WorkflowRun is a run of a custom workflow rvav started.
type WorkflowRun struct {
	WorkflowID      string  `db:"workflow_id"`
	RegistrationKey string  `db:"registration_key"`
	RequestID       *string `db:"request_id"`
	ProjectID       *string `db:"project_id"`
	StepKey         *string `db:"step_key"`
	Status          string  `db:"status"`
}

// StartWorkflowRun records a run (again, on a retry).
func (s *Store) StartWorkflowRun(ctx context.Context, workflowID, key string, requestID, stepKey *string) error {
	_, err := s.Pool.Exec(ctx, `insert into workflow_runs (workflow_id, registration_key, request_id, step_key)
		values ($1, $2, $3, $4) on conflict (workflow_id) do update set registration_key = excluded.registration_key,
			request_id = excluded.request_id, step_key = excluded.step_key, status = 'running', result = null, error = null,
			started_at = now(), finished_at = null`, workflowID, key, requestID, stepKey)
	return err
}

// FinishWorkflowRun records how a run ended.
func (s *Store) FinishWorkflowRun(ctx context.Context, workflowID, status string, result json.RawMessage, errText *string) error {
	_, err := s.Pool.Exec(ctx, `update workflow_runs set status = $2, result = $3, error = $4, finished_at = now()
		where workflow_id = $1`, workflowID, status, nullJSON(result), errText)
	return err
}

// GetWorkflowRun finds the run a contract activity's caller is.
func (s *Store) GetWorkflowRun(ctx context.Context, workflowID string) (WorkflowRun, error) {
	rows, _ := s.Pool.Query(ctx, `select w.workflow_id, w.registration_key, w.request_id, r.project_id, w.step_key, w.status
		from workflow_runs w left join requests r on r.id = w.request_id where w.workflow_id = $1`, workflowID)
	run, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[WorkflowRun])
	if errors.Is(err, pgx.ErrNoRows) {
		return run, ErrNotFound
	}
	return run, err
}

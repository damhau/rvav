package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Schedule runs an item as its owner's request on a cron expression or
// every so many minutes (ADR-0057).
type Schedule struct {
	ID             string          `db:"id"`
	Name           string          `db:"name"`
	ItemID         string          `db:"item_id"`
	VersionID      string          `db:"version_id"`
	ProjectID      string          `db:"project_id"`
	Environment    string          `db:"environment"`
	OwnerID        string          `db:"owner_id"`
	Inputs         json.RawMessage `db:"inputs"`
	Justification  string          `db:"justification"`
	AcceptWarnings bool            `db:"accept_warnings"`
	Cron           *string         `db:"cron"`
	EveryMinutes   *int            `db:"every_minutes"`
	Timezone       string          `db:"timezone"`
	Paused         bool            `db:"paused"`
	PausedNote     string          `db:"paused_note"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

// ScheduleRow is a schedule with the names shown next to it and its last run.
type ScheduleRow struct {
	Schedule
	ItemSlug      string     `db:"item_slug"`
	ItemName      string     `db:"item_name"`
	ItemType      string     `db:"item_type"`
	VersionNumber string     `db:"version_number"`
	ProjectCode   string     `db:"project_code"`
	ProjectName   string     `db:"project_name"`
	OwnerName     string     `db:"owner_name"`
	OwnerEmail    string     `db:"owner_email"`
	LastRunAt     *time.Time `db:"last_run_at"`
	LastOutcome   *string    `db:"last_outcome"`
	LastReason    *string    `db:"last_reason"`
	LastRequestID *string    `db:"last_request_id"`
	LastNumber    *string    `db:"last_number"`
	LastStatus    *string    `db:"last_status"`
}

// ScheduleRun is what one firing of a schedule did.
type ScheduleRun struct {
	FireID        string    `db:"fire_id"`
	ScheduleID    string    `db:"schedule_id"`
	At            time.Time `db:"at"`
	RequestID     *string   `db:"request_id"`
	RequestNumber *string   `db:"request_number"`
	RequestStatus *string   `db:"request_status"`
	Outcome       string    `db:"outcome"`
	Reason        string    `db:"reason"`
}

const scheduleSelect = `
select s.id, s.name, s.item_id, s.version_id, s.project_id, s.environment, s.owner_id, s.inputs, s.justification,
       s.accept_warnings, s.cron, s.every_minutes, s.timezone, s.paused, s.paused_note, s.created_at, s.updated_at,
       i.slug as item_slug, i.name as item_name, i.type as item_type, v.number as version_number,
       p.code as project_code, p.name as project_name, u.name as owner_name, u.email as owner_email,
       lr.at as last_run_at, lr.outcome as last_outcome, lr.reason as last_reason,
       lr.request_id as last_request_id, r.number as last_number, r.status as last_status
from schedules s
join catalog_items i on i.id = s.item_id
join catalog_item_versions v on v.id = s.version_id
join projects p on p.id = s.project_id
join users u on u.id = s.owner_id
left join lateral (select * from schedule_runs x where x.schedule_id = s.id order by x.at desc limit 1) lr on true
left join requests r on r.id = lr.request_id
where s.deleted_at is null`

// Schedules lists the schedules not deleted, the newest first.
func (s *Store) Schedules(ctx context.Context) ([]ScheduleRow, error) {
	rows, _ := s.Pool.Query(ctx, scheduleSelect+` order by s.created_at desc`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ScheduleRow])
}

// GetSchedule finds a schedule not deleted.
func (s *Store) GetSchedule(ctx context.Context, id string) (ScheduleRow, error) {
	rows, _ := s.Pool.Query(ctx, scheduleSelect+` and s.id = $1`, id)
	sc, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[ScheduleRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return sc, ErrNotFound
	}
	return sc, err
}

func (s *Store) InsertSchedule(ctx context.Context, sc Schedule) error {
	_, err := s.Pool.Exec(ctx, `insert into schedules (id, name, item_id, version_id, project_id, environment, owner_id,
		inputs, justification, accept_warnings, cron, every_minutes, timezone)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		sc.ID, sc.Name, sc.ItemID, sc.VersionID, sc.ProjectID, sc.Environment, sc.OwnerID, jsonOr(sc.Inputs, "{}"),
		sc.Justification, sc.AcceptWarnings, sc.Cron, sc.EveryMinutes, sc.Timezone)
	return err
}

// RemoveSchedule drops a schedule whose Temporal schedule couldn't be made.
func (s *Store) RemoveSchedule(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `delete from schedules where id = $1`, id)
	return err
}

func (s *Store) SetSchedulePaused(ctx context.Context, id string, paused bool, note string) error {
	tag, err := s.Pool.Exec(ctx, `update schedules set paused = $2, paused_note = $3, updated_at = now()
		where id = $1 and deleted_at is null`, id, paused, note)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteSchedule marks a schedule deleted: the requests it started still
// name it.
func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `update schedules set deleted_at = now(), updated_at = now()
		where id = $1 and deleted_at is null`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ScheduleName is a schedule's name, deleted or not.
func (s *Store) ScheduleName(ctx context.Context, id string) (string, error) {
	var name string
	err := s.Pool.QueryRow(ctx, `select name from schedules where id = $1`, id).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// InsertScheduleRun records a firing; false when it was recorded already.
func (s *Store) InsertScheduleRun(ctx context.Context, db DB, r ScheduleRun) (bool, error) {
	tag, err := db.Exec(ctx, `insert into schedule_runs (fire_id, schedule_id, at, request_id, outcome, reason)
		values ($1, $2, $3, $4, $5, $6) on conflict (fire_id) do nothing`,
		r.FireID, r.ScheduleID, r.At, r.RequestID, r.Outcome, r.Reason)
	return err == nil && tag.RowsAffected() == 1, err
}

// GetScheduleRun is a firing as recorded.
func (s *Store) GetScheduleRun(ctx context.Context, fireID string) (ScheduleRun, error) {
	rows, _ := s.Pool.Query(ctx, scheduleRunSelect+` where x.fire_id = $1`, fireID)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[ScheduleRun])
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

const scheduleRunSelect = `select x.fire_id, x.schedule_id, x.at, x.request_id, r.number as request_number,
	r.status as request_status, x.outcome, x.reason
	from schedule_runs x left join requests r on r.id = x.request_id`

// ScheduleRuns are a schedule's last firings, the newest first.
func (s *Store) ScheduleRuns(ctx context.Context, scheduleID string, limit int) ([]ScheduleRun, error) {
	rows, _ := s.Pool.Query(ctx, scheduleRunSelect+` where x.schedule_id = $1 order by x.at desc limit $2`, scheduleID, limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ScheduleRun])
}

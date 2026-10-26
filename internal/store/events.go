package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// EventsChannel is the Postgres NOTIFY channel carrying new event IDs.
const EventsChannel = "rvav_events"

// InsertEvent writes an event to the outbox and notifies listeners when the
// surrounding transaction commits.
func (s *Store) InsertEvent(ctx context.Context, db DB, typ string, projectID *string, topics []string, payload any) (int64, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := db.QueryRow(ctx, `insert into events (type, project_id, topics, payload) values ($1, $2, $3, $4) returning id`,
		typ, projectID, topics, b).Scan(&id); err != nil {
		return 0, err
	}
	_, err = db.Exec(ctx, `select pg_notify($1, $2)`, EventsChannel, strconv.FormatInt(id, 10))
	return id, err
}

func (s *Store) GetEvent(ctx context.Context, id int64) (Event, error) {
	rows, _ := s.Pool.Query(ctx, `select id, type, project_id, topics, payload, created_at from events where id = $1`, id)
	e, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Event])
	return e, notFound(err)
}

// EventsAfter returns up to limit events with an ID greater than after.
func (s *Store) EventsAfter(ctx context.Context, after int64, limit int) ([]Event, error) {
	rows, _ := s.Pool.Query(ctx, `select id, type, project_id, topics, payload, created_at from events
		where id > $1 order by id limit $2`, after, limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Event])
}

// OldestEventID is the smallest retained event ID, or 0 when there are none.
func (s *Store) OldestEventID(ctx context.Context) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `select coalesce(min(id), 0) from events`).Scan(&id)
	return id, err
}

func (s *Store) LatestEventID(ctx context.Context) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `select coalesce(max(id), 0) from events`).Scan(&id)
	return id, err
}

func (s *Store) PurgeEvents(ctx context.Context, before time.Time) error {
	_, err := s.Pool.Exec(ctx, `delete from events where created_at < $1`, before)
	return err
}

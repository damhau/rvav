package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// SignalWait is a composite step waiting for a signal or an event
// (ADR-0057).
type SignalWait struct {
	RequestID  string          `db:"request_id"`
	StepKey    string          `db:"step_key"`
	Name       *string         `db:"name"`
	Topic      *string         `db:"topic"`
	Condition  string          `db:"condition"`
	Waiting    json.RawMessage `db:"waiting"`
	AfterSeq   int64           `db:"after_seq"`
	DueAt      time.Time       `db:"due_at"`
	Payload    json.RawMessage `db:"payload"`
	ReceivedAt *time.Time      `db:"received_at"`
	ClosedAt   *time.Time      `db:"closed_at"`
}

// SignalTarget is an open wait for an event, with its request's project
// and the workflow to tell.
type SignalTarget struct {
	SignalWait
	ProjectID  string  `db:"project_id"`
	WorkflowID *string `db:"workflow_id"`
}

const signalCols = `request_id, step_key, name, topic, condition, waiting, after_seq, due_at, payload, received_at, closed_at`

// OpenSignalWait opens a step's wait, or gives the one it opened: one that
// timed out or was stopped opens again (a Retry), one that received stays.
func (s *Store) OpenSignalWait(ctx context.Context, w SignalWait) (SignalWait, error) {
	rows, _ := s.Pool.Query(ctx, `insert into signal_waits (request_id, step_key, name, topic, condition, waiting, after_seq, due_at)
		values ($1, $2, $3, $4, $5, $6, (select coalesce(max(seq), 0) from integration_events), $7)
		on conflict (request_id, step_key) do update set
			name = excluded.name, topic = excluded.topic, condition = excluded.condition, waiting = excluded.waiting,
			after_seq = excluded.after_seq, due_at = excluded.due_at, payload = null, received_at = null, closed_at = null
			where signal_waits.closed_at is not null and signal_waits.received_at is null
		returning `+signalCols, w.RequestID, w.StepKey, w.Name, w.Topic, w.Condition, w.Waiting, w.DueAt)
	got, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[SignalWait])
	if errors.Is(err, pgx.ErrNoRows) {
		return s.SignalWaitFor(ctx, w.RequestID, w.StepKey)
	}
	return got, err
}

// SignalWaitFor is a step's wait.
func (s *Store) SignalWaitFor(ctx context.Context, requestID, stepKey string) (SignalWait, error) {
	rows, _ := s.Pool.Query(ctx, `select `+signalCols+` from signal_waits where request_id = $1 and step_key = $2`, requestID, stepKey)
	w, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[SignalWait])
	if errors.Is(err, pgx.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

// ReceiveSignal gives a request's open wait for the signal name what came;
// ErrNotFound when none waits for it.
func (s *Store) ReceiveSignal(ctx context.Context, requestID, name string, payload json.RawMessage) (SignalWait, error) {
	rows, _ := s.Pool.Query(ctx, `update signal_waits set payload = $3, received_at = now()
		where request_id = $1 and name = $2 and received_at is null and closed_at is null returning `+signalCols, requestID, name, payload)
	w, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[SignalWait])
	if errors.Is(err, pgx.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

// CloseSignalWait ends a step's wait that received nothing: it timed out or
// was stopped.
func (s *Store) CloseSignalWait(ctx context.Context, requestID, stepKey string) error {
	_, err := s.Pool.Exec(ctx, `update signal_waits set closed_at = now()
		where request_id = $1 and step_key = $2 and received_at is null and closed_at is null`, requestID, stepKey)
	return err
}

// EventWaits are the open waits for an event of the topic, of requests
// still running.
func (s *Store) EventWaits(ctx context.Context, tx pgx.Tx, topic string) ([]SignalTarget, error) {
	rows, _ := tx.Query(ctx, `select w.request_id, w.step_key, w.name, w.topic, w.condition, w.waiting, w.after_seq, w.due_at,
			w.payload, w.received_at, w.closed_at, r.project_id, r.workflow_id
		from signal_waits w join requests r on r.id = w.request_id
		where w.topic = $1 and w.received_at is null and w.closed_at is null and r.status = 'running'
		order by w.created_at for update of w skip locked`, topic)
	return pgx.CollectRows(rows, pgx.RowToStructByName[SignalTarget])
}

// ReceiveEvent gives an open wait the event it waited for.
func (s *Store) ReceiveEvent(ctx context.Context, tx pgx.Tx, requestID, stepKey string, payload json.RawMessage) error {
	_, err := tx.Exec(ctx, `update signal_waits set payload = $3, received_at = now()
		where request_id = $1 and step_key = $2 and received_at is null and closed_at is null`, requestID, stepKey, payload)
	return err
}

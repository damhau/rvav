package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Events and subscriptions (ADR-0021).

// Subscription sends the events of a topic to a target.
type Subscription struct {
	ID             string          `db:"id"`
	Name           string          `db:"name"`
	Description    string          `db:"description"`
	Topic          string          `db:"topic"`
	Condition      string          `db:"condition"`
	Blocking       bool            `db:"blocking"`
	OnFailure      string          `db:"on_failure"`
	Priority       int             `db:"priority"`
	TimeoutSeconds int             `db:"timeout_seconds"`
	Enabled        bool            `db:"enabled"`
	Target         json.RawMessage `db:"target"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

const subscriptionCols = `id, name, description, topic, condition, blocking, on_failure, priority, timeout_seconds,
	enabled, target, created_at, updated_at`

// Subscriptions lists them by topic and priority.
func (s *Store) Subscriptions(ctx context.Context) ([]Subscription, error) {
	rows, _ := s.Pool.Query(ctx, `select `+subscriptionCols+` from subscriptions order by topic, priority, name, id`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Subscription])
}

// ActiveSubscriptions are the enabled ones, blocking or not, of a topic
// (all topics when empty), by priority.
func (s *Store) ActiveSubscriptions(ctx context.Context, topic string, blocking bool) ([]Subscription, error) {
	rows, _ := s.Pool.Query(ctx, `select `+subscriptionCols+` from subscriptions
		where enabled and blocking = $2 and ($1 = '' or topic = $1) order by priority, created_at, id`, topic, blocking)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Subscription])
}

func (s *Store) GetSubscription(ctx context.Context, id string) (Subscription, error) {
	rows, _ := s.Pool.Query(ctx, `select `+subscriptionCols+` from subscriptions where id = $1`, id)
	sub, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Subscription])
	if errors.Is(err, pgx.ErrNoRows) {
		return sub, ErrNotFound
	}
	return sub, err
}

// SaveSubscription inserts or replaces one.
func (s *Store) SaveSubscription(ctx context.Context, sub Subscription) error {
	_, err := s.Pool.Exec(ctx, `insert into subscriptions (id, name, description, topic, condition, blocking, on_failure,
			priority, timeout_seconds, enabled, target) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		on conflict (id) do update set name = excluded.name, description = excluded.description, topic = excluded.topic,
			condition = excluded.condition, blocking = excluded.blocking, on_failure = excluded.on_failure,
			priority = excluded.priority, timeout_seconds = excluded.timeout_seconds, enabled = excluded.enabled,
			target = excluded.target, updated_at = now()`,
		sub.ID, sub.Name, sub.Description, sub.Topic, sub.Condition, sub.Blocking, sub.OnFailure, sub.Priority,
		sub.TimeoutSeconds, sub.Enabled, sub.Target)
	return err
}

func (s *Store) DeleteSubscription(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from subscriptions where id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SubscriptionRunCounts are a subscription's runs of the last 30 days by status.
func (s *Store) SubscriptionRunCounts(ctx context.Context) (map[string]map[string]int, map[string]time.Time, error) {
	rows, err := s.Pool.Query(ctx, `select subscription_id, status, count(*), max(created_at) from subscription_runs
		where created_at > now() - interval '30 days' group by subscription_id, status`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	counts, last := map[string]map[string]int{}, map[string]time.Time{}
	for rows.Next() {
		var id, status string
		var n int
		var at time.Time
		if err := rows.Scan(&id, &status, &n, &at); err != nil {
			return nil, nil, err
		}
		if counts[id] == nil {
			counts[id] = map[string]int{}
		}
		counts[id][status] = n
		if at.After(last[id]) {
			last[id] = at
		}
	}
	return counts, last, rows.Err()
}

// IntegrationEvent is an event in the outbox.
type IntegrationEvent struct {
	ID        string          `db:"id"`
	Topic     string          `db:"topic"`
	RequestID *string         `db:"request_id"`
	Payload   json.RawMessage `db:"payload"`
	CreatedAt time.Time       `db:"created_at"`
}

// UndispatchedEvents locks the oldest events no run was made for yet.
func (s *Store) UndispatchedEvents(ctx context.Context, tx pgx.Tx, limit int) ([]IntegrationEvent, error) {
	rows, _ := tx.Query(ctx, `select id, topic, request_id, payload, created_at from integration_events
		where dispatched_at is null order by seq limit $1 for update skip locked`, limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[IntegrationEvent])
}

// InsertEvent stores an event made up for a test run.
func (s *Store) InsertTestEvent(ctx context.Context, id, topic string, payload json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `insert into integration_events (id, topic, dedupe_key, payload, dispatched_at)
		values ($1, $2, $1, $3, now())`, id, topic, payload)
	return err
}

func (s *Store) MarkDispatched(ctx context.Context, tx pgx.Tx, ids []string) error {
	_, err := tx.Exec(ctx, `update integration_events set dispatched_at = now() where id = any($1)`, ids)
	return err
}

// SubscriptionRun is one event sent to one subscription.
type SubscriptionRun struct {
	ID             string          `db:"id"`
	SubscriptionID string          `db:"subscription_id"`
	EventID        string          `db:"event_id"`
	Topic          string          `db:"topic"`
	Status         string          `db:"status"`
	Blocking       bool            `db:"blocking"`
	Test           bool            `db:"test"`
	RequestID      *string         `db:"request_id"`
	RequestNumber  *string         `db:"request_number"`
	StepID         *string         `db:"step_id"`
	Attempts       int             `db:"attempts"`
	NextAttemptAt  time.Time       `db:"next_attempt_at"`
	Outcome        *string         `db:"outcome"`
	Reason         *string         `db:"reason"`
	Properties     json.RawMessage `db:"properties"`
	CreatedAt      time.Time       `db:"created_at"`
	FinishedAt     *time.Time      `db:"finished_at"`
}

const runCols = `r.id, r.subscription_id, r.event_id, e.topic, r.status, r.blocking, r.test, r.request_id,
	q.number as request_number, r.step_id, r.attempts, r.next_attempt_at, r.outcome, r.reason, r.properties,
	r.created_at, r.finished_at`
const runFrom = ` from subscription_runs r join integration_events e on e.id = r.event_id
	left join requests q on q.id = r.request_id`

// InsertRun adds a run, queued unless said otherwise; a second one for the
// same subscription and event is ignored.
func (s *Store) InsertRun(ctx context.Context, db DB, r SubscriptionRun) error {
	next := r.NextAttemptAt
	if next.IsZero() {
		next = time.Now()
	}
	_, err := db.Exec(ctx, `insert into subscription_runs (id, subscription_id, event_id, status, blocking, test, request_id,
			step_id, reason, finished_at, next_attempt_at) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		on conflict (subscription_id, event_id) do nothing`,
		r.ID, r.SubscriptionID, r.EventID, r.Status, r.Blocking, r.Test, r.RequestID, r.StepID, r.Reason, r.FinishedAt, next)
	return err
}

// ClaimedRun is a run taken for delivery, with what it needs.
type ClaimedRun struct {
	SubscriptionRun
	Subscription Subscription
	Payload      json.RawMessage
	// LastJobID is the AWX job its last attempt launched.
	LastJobID *int
}

// ClaimRuns takes runs due for an attempt at the given time, and those a
// worker took but didn't finish in time: each is the caller's until its
// lease ends.
func (s *Store) ClaimRuns(ctx context.Context, limit int, now time.Time) ([]ClaimedRun, error) {
	rows, err := s.Pool.Query(ctx, `with due as (
			select r.id from subscription_runs r join subscriptions s on s.id = r.subscription_id
			where (r.status = 'queued' and r.next_attempt_at <= $2) or (r.status = 'delivering' and r.lease_until < $2)
			order by r.next_attempt_at limit $1 for update of r skip locked)
		update subscription_runs r set status = 'delivering', attempts = r.attempts + 1,
			lease_until = $2::timestamptz + make_interval(secs => s.timeout_seconds + 60)
		from due, subscriptions s where r.id = due.id and s.id = r.subscription_id
		returning r.id`, limit, now)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	rows, _ = s.Pool.Query(ctx, `select `+runCols+`, e.payload, `+prefixed("s", subscriptionCols)+`,
			(select a.job_id from subscription_attempts a where a.run_id = r.id and a.job_id is not null order by a.id desc limit 1)`+runFrom+`
		join subscriptions s on s.id = r.subscription_id where r.id = any($1)`, ids)
	defer rows.Close()
	var out []ClaimedRun
	for rows.Next() {
		var c ClaimedRun
		sub := &c.Subscription
		if err := rows.Scan(&c.ID, &c.SubscriptionID, &c.EventID, &c.Topic, &c.Status, &c.Blocking, &c.Test, &c.RequestID,
			&c.RequestNumber, &c.StepID, &c.Attempts, &c.NextAttemptAt, &c.Outcome, &c.Reason, &c.Properties, &c.CreatedAt,
			&c.FinishedAt, &c.Payload, &sub.ID, &sub.Name, &sub.Description, &sub.Topic, &sub.Condition, &sub.Blocking,
			&sub.OnFailure, &sub.Priority, &sub.TimeoutSeconds, &sub.Enabled, &sub.Target, &sub.CreatedAt, &sub.UpdatedAt,
			&c.LastJobID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Attempt is one try at delivering a run.
type Attempt struct {
	At         time.Time `db:"at"`
	OK         bool      `db:"ok"`
	StatusCode *int      `db:"status_code"`
	JobID      *int      `db:"job_id"`
	WorkflowID *string   `db:"workflow_id"`
	Response   *string   `db:"response"`
	Error      *string   `db:"error"`
	DurationMS int       `db:"duration_ms"`
}

// RunUpdate is where a run stands after an attempt.
type RunUpdate struct {
	Status      string
	NextAttempt *time.Time
	Outcome     *string
	Reason      *string
	Properties  json.RawMessage
	StepID      *string
}

// RecordAttempt keeps an attempt and moves the run on.
func (s *Store) RecordAttempt(ctx context.Context, runID string, a Attempt, u RunUpdate) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into subscription_attempts (run_id, at, ok, status_code, job_id, workflow_id,
				response, error, duration_ms) values ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			runID, a.At, a.OK, a.StatusCode, a.JobID, a.WorkflowID, a.Response, a.Error, a.DurationMS); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `update subscription_runs set status = $2, lease_until = null,
				next_attempt_at = coalesce($3, next_attempt_at), outcome = coalesce($4, outcome), reason = $5,
				properties = coalesce($6, properties), step_id = coalesce($7, step_id),
				finished_at = case when $2 in ('delivered', 'failed', 'blocked') then now() end
			where id = $1`, runID, u.Status, u.NextAttempt, u.Outcome, u.Reason, nullJSON(u.Properties), u.StepID)
		return err
	})
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// RunFilter selects a subscription's runs.
type RunFilter struct {
	SubscriptionID string
	Status         string
	Before         *struct {
		At time.Time
		ID string
	}
	Limit int
}

// SubscriptionRuns lists runs newest first.
func (s *Store) SubscriptionRuns(ctx context.Context, f RunFilter) ([]SubscriptionRun, error) {
	var beforeAt *time.Time
	beforeID := ""
	if f.Before != nil {
		beforeAt, beforeID = &f.Before.At, f.Before.ID
	}
	rows, _ := s.Pool.Query(ctx, `select `+runCols+runFrom+`
		where r.subscription_id = $1 and ($2 = '' or r.status = $2)
			and ($3::timestamptz is null or (r.created_at, r.id) < ($3, $4))
		order by r.created_at desc, r.id desc limit $5`, f.SubscriptionID, f.Status, beforeAt, beforeID, f.Limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[SubscriptionRun])
}

func (s *Store) GetSubscriptionRun(ctx context.Context, id string) (SubscriptionRun, json.RawMessage, error) {
	var payload json.RawMessage
	rows, _ := s.Pool.Query(ctx, `select `+runCols+runFrom+` where r.id = $1`, id)
	run, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[SubscriptionRun])
	if errors.Is(err, pgx.ErrNoRows) {
		return run, nil, ErrNotFound
	} else if err != nil {
		return run, nil, err
	}
	err = s.Pool.QueryRow(ctx, `select payload from integration_events where id = $1`, run.EventID).Scan(&payload)
	return run, payload, err
}

func (s *Store) RunAttempts(ctx context.Context, runID string) ([]Attempt, error) {
	rows, _ := s.Pool.Query(ctx, `select at, ok, status_code, job_id, workflow_id, response, error, duration_ms
		from subscription_attempts where run_id = $1 order by id`, runID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Attempt])
}

// RetryRun queues a failed run again.
func (s *Store) RetryRun(ctx context.Context, id string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update subscription_runs set status = 'queued', next_attempt_at = now(), finished_at = null,
		reason = null where id = $1 and status = 'failed' and not blocking`, id)
	return tag.RowsAffected() == 1, err
}

// PruneRuns removes runs finished before a time, and events nothing waits
// for any more.
func (s *Store) PruneRuns(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `delete from subscription_runs where finished_at < $1`, before)
	if err != nil {
		return 0, err
	}
	_, err = s.Pool.Exec(ctx, `delete from integration_events e where e.created_at < $1 and e.dispatched_at is not null
		and not exists (select 1 from subscription_runs r where r.event_id = e.id)`, before)
	return tag.RowsAffected(), err
}

// SetDeploymentProperties merges properties set by a blocking subscription.
func (s *Store) SetDeploymentProperties(ctx context.Context, db DB, requestID string, props map[string]string) error {
	b, err := json.Marshal(props)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `update deployments set properties = properties || $2::jsonb
		where created_by_request_id = $1 or id = (select deployment_id from requests where id = $1)`, requestID, b)
	return err
}

// prefixed qualifies a column list with a table alias.
func prefixed(alias, cols string) string {
	out := ""
	for i, c := range splitCols(cols) {
		if i > 0 {
			out += ", "
		}
		out += alias + "." + c
	}
	return out
}

func splitCols(cols string) []string {
	var out []string
	cur := ""
	for _, ch := range cols {
		switch ch {
		case ',':
			out = append(out, cur)
			cur = ""
		case ' ', '\n', '\t':
		default:
			cur += string(ch)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// LatestEvent is the newest event of a topic about a request.
func (s *Store) LatestEvent(ctx context.Context, requestID, topic string) (IntegrationEvent, error) {
	rows, _ := s.Pool.Query(ctx, `select id, topic, request_id, payload, created_at from integration_events
		where request_id = $1 and topic = $2 order by seq desc limit 1`, requestID, topic)
	ev, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[IntegrationEvent])
	if errors.Is(err, pgx.ErrNoRows) {
		return ev, ErrNotFound
	}
	return ev, err
}

// RequestProperties are the properties of a request's deployments, merged.
func (s *Store) RequestProperties(ctx context.Context, requestID string) (map[string]string, error) {
	out := map[string]string{}
	rows, err := s.Pool.Query(ctx, `select properties from deployments
		where created_by_request_id = $1 or id = (select deployment_id from requests where id = $1)`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m map[string]string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		for k, v := range m {
			out[k] = v
		}
	}
	return out, rows.Err()
}

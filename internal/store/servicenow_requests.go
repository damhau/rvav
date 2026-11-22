package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ServiceNowRequest is the requested item a request was made for
// (ADR-0065).
type ServiceNowRequest struct {
	RequestID  string    `db:"request_id"`
	RITMSysID  string    `db:"ritm_sys_id"`
	RITMNumber string    `db:"ritm_number"`
	UserGiven  string    `db:"user_given"`
	TokenID    *string   `db:"token_id"`
	CreatedAt  time.Time `db:"created_at"`
}

// ErrRITMTaken: a request was made for the requested item already.
var ErrRITMTaken = errors.New("a request was made for this requested item already")

// InsertServiceNowRequest records it in the transaction that makes the
// request, so that the request's first event finds it.
func (s *Store) InsertServiceNowRequest(ctx context.Context, tx pgx.Tx, r ServiceNowRequest) error {
	_, err := tx.Exec(ctx, `insert into servicenow_requests (request_id, ritm_sys_id, ritm_number, user_given, token_id)
		values ($1, $2, $3, $4, $5)`, r.RequestID, r.RITMSysID, r.RITMNumber, r.UserGiven, r.TokenID)
	if pe := (*pgconn.PgError)(nil); errors.As(err, &pe) && pe.Code == "23505" {
		return ErrRITMTaken
	}
	return err
}

const serviceNowRequestCols = `request_id, ritm_sys_id, ritm_number, user_given, token_id, created_at`

// ServiceNowRequestOf is the requested item a request was made for.
func (s *Store) ServiceNowRequestOf(ctx context.Context, requestID string) (ServiceNowRequest, error) {
	rows, _ := s.Pool.Query(ctx, `select `+serviceNowRequestCols+` from servicenow_requests where request_id = $1`, requestID)
	return one[ServiceNowRequest](rows)
}

// ServiceNowRequestForRITM is the request made for a requested item.
func (s *Store) ServiceNowRequestForRITM(ctx context.Context, sysID string) (ServiceNowRequest, error) {
	rows, _ := s.Pool.Query(ctx, `select `+serviceNowRequestCols+` from servicenow_requests where ritm_sys_id = $1`, sysID)
	return one[ServiceNowRequest](rows)
}

// ServiceNowRequestOfDeployment is the requested item the request that
// made a deployment was made for.
func (s *Store) ServiceNowRequestOfDeployment(ctx context.Context, deploymentID string) (ServiceNowRequest, error) {
	rows, _ := s.Pool.Query(ctx, `select `+serviceNowRequestCols+` from servicenow_requests
		where request_id = (select created_by_request_id from deployments where id = $1)`, deploymentID)
	return one[ServiceNowRequest](rows)
}

func one[T any](rows pgx.Rows) (T, error) {
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[T])
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// OfferedItem is an item ServiceNow's catalog may offer, with its default
// version.
type OfferedItem struct {
	ID            string `db:"id"`
	Slug          string `db:"slug"`
	Name          string `db:"name"`
	Summary       string `db:"summary"`
	Category      string `db:"category"`
	Type          string `db:"type"`
	VersionID     string `db:"version_id"`
	VersionNumber string `db:"version_number"`
}

// ServiceNowCatalogItems are the items published and shared in a project.
func (s *Store) ServiceNowCatalogItems(ctx context.Context) ([]OfferedItem, error) {
	rows, _ := s.Pool.Query(ctx, `select i.id, i.slug, i.name, i.summary, i.category, i.type, v.id as version_id, v.number as version_number
		from catalog_items i join catalog_item_versions v on v.id = i.published_version_id
		where i.all_projects or exists (select 1 from catalog_entitlements e where e.item_id = i.id)
		order by i.name, i.id`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[OfferedItem])
}

// ServiceNowUsersMatching are the users ServiceNow may request for whose
// email is this one, any case, or, for a name without @, whose email's
// part before the @ is it: never the local administrator or one of rvav's
// own.
func (s *Store) ServiceNowUsersMatching(ctx context.Context, given string) ([]User, error) {
	rows, _ := s.Pool.Query(ctx, `select `+userCols+` from users
		where email <> '' and subject not like 'local:%' and subject not like 'system:%'
			and case when position('@' in $1) > 0 then lower(email) = lower($1)
				else lower(split_part(email, '@', 1)) = lower($1) end
		order by id limit 2`, given)
	return pgx.CollectRows(rows, pgx.RowToStructByName[User])
}

// CountServiceNowSubmission counts a submission in the current minute,
// across processes, and says how many there are now.
func (s *Store) CountServiceNowSubmission(ctx context.Context, now time.Time) (int, error) {
	minute := now.UTC().Truncate(time.Minute)
	var n int
	err := s.Pool.QueryRow(ctx, `with gone as (delete from servicenow_rate where minute < $1::timestamptz - interval '1 hour')
		insert into servicenow_rate (minute, n) values ($1, 1)
		on conflict (minute) do update set n = servicenow_rate.n + 1 returning n`, minute).Scan(&n)
	return n, err
}

// Events of a request made from ServiceNow that its requested item hears
// of.
var ritmTopics = []string{"request.submitted", "approval.requested", "approval.decided", "provisioning.post"}

// QueueServiceNowNotes queues a work note for each of these events of a
// request made from ServiceNow, in the transaction that marks them
// dispatched.
func (s *Store) QueueServiceNowNotes(ctx context.Context, tx pgx.Tx, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `insert into servicenow_ritm_notes (event_id, request_id, ritm_sys_id)
		select e.id, r.request_id, r.ritm_sys_id from integration_events e join servicenow_requests r on r.request_id = e.request_id
		where e.id = any($1) and e.topic = any($2) order by e.seq
		on conflict (event_id) do nothing`, eventIDs, ritmTopics)
	return err
}

// ServiceNowNote is a work note owed to a requested item, with the event
// it tells of.
type ServiceNowNote struct {
	ID         int64           `db:"id"`
	RITMSysID  string          `db:"ritm_sys_id"`
	RITMNumber string          `db:"ritm_number"`
	RequestID  string          `db:"request_id"`
	Topic      string          `db:"topic"`
	Payload    json.RawMessage `db:"payload"`
	Attempts   int             `db:"attempts"`
}

// DueServiceNowNotes are the first unsent note of each requested item,
// when it is due: the next wait for it, so the item reads in order.
func (s *Store) DueServiceNowNotes(ctx context.Context, now time.Time, limit int) ([]ServiceNowNote, error) {
	rows, _ := s.Pool.Query(ctx, `select n.id, n.ritm_sys_id, r.ritm_number, n.request_id, coalesce(e.topic, '') as topic,
			coalesce(e.payload, '{}') as payload, n.attempts
		from (select distinct on (ritm_sys_id) * from servicenow_ritm_notes where sent_at is null order by ritm_sys_id, id) n
			join servicenow_requests r on r.request_id = n.request_id
			left join integration_events e on e.id = n.event_id
		where n.due_at <= $1 order by n.id limit $2`, now, limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ServiceNowNote])
}

// SentServiceNowNote marks a note sent.
func (s *Store) SentServiceNowNote(ctx context.Context, id int64, at time.Time) error {
	_, err := s.Pool.Exec(ctx, `update servicenow_ritm_notes set sent_at = $2, last_error = null where id = $1`, id, at)
	return err
}

// GaveUpServiceNowNote marks a note ServiceNow refused for good as done,
// keeping why.
func (s *Store) GaveUpServiceNowNote(ctx context.Context, id int64, at time.Time, why string) error {
	_, err := s.Pool.Exec(ctx, `update servicenow_ritm_notes set sent_at = $2, attempts = attempts + 1, last_error = $3 where id = $1`,
		id, at, why)
	return err
}

// ServiceNowWorkDue says whether a pass has anything to do now: a
// deployment or a note due, or, with the CMDB kept, a reconcile.
func (s *Store) ServiceNowWorkDue(ctx context.Context, now time.Time, cmdb bool, interval time.Duration) (bool, error) {
	var due bool
	err := s.Pool.QueryRow(ctx, `select exists (select 1 from servicenow_ritm_notes where sent_at is null and due_at <= $1)
		or $2 and (exists (select 1 from servicenow_cmdb_queue where due_at <= $1)
			or exists (select 1 from servicenow_state where reconciled_at is null or reconciled_at <= $3))`,
		now, cmdb, now.Add(-interval)).Scan(&due)
	return due, err
}

// RetryServiceNowNote keeps a note until due, with why.
func (s *Store) RetryServiceNowNote(ctx context.Context, id int64, due time.Time, why string) error {
	_, err := s.Pool.Exec(ctx, `update servicenow_ritm_notes set attempts = attempts + 1, due_at = $2, last_error = $3 where id = $1`,
		id, due, why)
	return err
}

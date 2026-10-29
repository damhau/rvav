package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const requestRowSelect = `
select r.id, r.number, r.item_id, r.version_id, r.project_id, r.environment, r.title, r.status,
       r.requester_id, r.on_behalf_of_id, r.owner_id, r.inputs, r.count, r.justification, r.lease_days,
       r.approval_required, r.approval, r.plan, r.submitted_at, r.scheduled_at, r.started_at,
       r.finished_at, r.workflow_id, r.error_step_id, r.error_message,
       r.kind, r.action, r.deployment_id, r.resource_id, r.resource_name,
       i.slug as item_slug, i.name as item_name, i.type as item_type, v.number as version_number,
       p.code as project_code, p.name as project_name,
       u.name as requester_name, u.email as requester_email,
       ob.name as on_behalf_of_name, ob.email as on_behalf_of_email,
       case when r.deployment_id is not null then array[r.deployment_id]
            else coalesce((select array_agg(d.id order by d.created_at) from deployments d
                           where d.created_by_request_id = r.id), '{}') end as deployment_ids,
       (select count(*) from request_steps s where s.request_id = r.id and s.parent_id is null
          and s.status in ('done', 'skipped'))::int as steps_done,
       (select count(*) from request_steps s where s.request_id = r.id and s.parent_id is null)::int as steps_total,
       cs.id as current_step_id, cs.title as current_step_title
from requests r
join catalog_items i on i.id = r.item_id
join catalog_item_versions v on v.id = r.version_id
join projects p on p.id = r.project_id
join users u on u.id = r.requester_id
left join users ob on ob.id = r.on_behalf_of_id
left join lateral (select s.id, s.title from request_steps s
                   where s.request_id = r.id and s.parent_id is null and s.status in ('running', 'failed')
                   order by s.position limit 1) cs on true`

func (s *Store) NextRequestNumber(ctx context.Context, db DB) (string, error) {
	var n int64
	if err := db.QueryRow(ctx, `select nextval('request_number_seq')`).Scan(&n); err != nil {
		return "", err
	}
	return fmt.Sprintf("REQ-%d", n), nil
}

func (s *Store) InsertRequest(ctx context.Context, db DB, r Request) error {
	_, err := db.Exec(ctx, `insert into requests (id, number, item_id, version_id, project_id, environment, title,
		status, requester_id, on_behalf_of_id, owner_id, inputs, count, justification, lease_days,
		approval_required, approval, plan, submitted_at, scheduled_at, workflow_id,
		kind, action, deployment_id, resource_id, resource_name)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)`,
		r.ID, r.Number, r.ItemID, r.VersionID, r.ProjectID, r.Environment, r.Title, r.Status, r.RequesterID,
		r.OnBehalfOfID, r.OwnerID, r.Inputs, r.Count, r.Justification, r.LeaseDays, r.ApprovalRequired,
		jsonOr(r.Approval, "{}"), jsonOr(r.Plan, "{}"), r.SubmittedAt, r.ScheduledAt, r.WorkflowID,
		orDefault(r.Kind, "provision"), r.Action, r.DeploymentID, r.ResourceID, r.ResourceName)
	return err
}

// GetRequest finds a request by ID or number.
func (s *Store) GetRequest(ctx context.Context, idOrNumber string) (RequestRow, error) {
	return s.GetRequestIn(ctx, s.Pool, idOrNumber)
}

// GetRequestIn reads a request in a transaction.
func (s *Store) GetRequestIn(ctx context.Context, db DB, idOrNumber string) (RequestRow, error) {
	rows, _ := db.Query(ctx, requestRowSelect+` where r.id = $1 or r.number = upper($1)`, idOrNumber)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[RequestRow])
	return r, notFound(err)
}

type RequestFilter struct {
	// Projects limits results to these projects; nil means all projects.
	Projects    []string
	ViewerID    string
	ProjectID   string
	Statuses    []string
	RequesterID string
	Search      string
	// DeploymentID lists the requests that created or changed the deployment.
	DeploymentID string
	Limit        int
	After        *Cursor
}

// ListRequests returns requests newest first. A request is visible when it is
// in one of Projects, or the viewer requested or owns it.
func (s *Store) ListRequests(ctx context.Context, f RequestFilter) (Page[RequestRow], error) {
	afterTime, afterID := time.Time{}, ""
	if f.After != nil {
		t, err := time.Parse(time.RFC3339Nano, f.After.Value)
		if err != nil {
			return Page[RequestRow]{}, ErrBadCursor
		}
		afterTime, afterID = t, f.After.ID
	}
	rows, _ := s.Pool.Query(ctx, requestRowSelect+`
		where ($1 or r.project_id = any($2) or r.requester_id = $3 or r.owner_id = $3)
		  and ($4 = '' or r.project_id = $4)
		  and (cardinality($5::text[]) = 0 or r.status = any($5))
		  and ($6 = '' or r.requester_id = $6)
		  and ($7 = '' or r.title ilike $7 or r.number ilike $7)
		  and ($8 = '' or (r.submitted_at, r.id) < ($9, $8))
		  and ($11 = '' or r.deployment_id = $11
		       or r.id = (select created_by_request_id from deployments where id = $11))
		order by r.submitted_at desc, r.id desc limit $10`,
		f.Projects == nil, f.Projects, f.ViewerID, f.ProjectID, nonNil(f.Statuses), f.RequesterID,
		SearchText(f.Search), afterID, afterTime, f.Limit+1, f.DeploymentID)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[RequestRow])
	if err != nil {
		return Page[RequestRow]{}, err
	}
	return paginate(list, f.Limit, func(r RequestRow) Cursor {
		return Cursor{Value: r.SubmittedAt.Format(time.RFC3339Nano), ID: r.ID}
	}), nil
}

// ActiveDeploymentRequest returns the number of a request that has not
// finished on the deployment and is in the way of an action on resourceID,
// or "". Actions on different VMs run side by side; the request that
// created the deployment and actions on the whole deployment are in the way
// of everything, and resourceID "" (a whole-deployment action) waits for
// any request. except leaves one request out. waiting counts requests that
// wait for approval or their start time too; rvav's lease expiry doesn't
// wait for them. Lock the deployment row first (LockDeployment) to make it
// a reliable check.
func (s *Store) ActiveDeploymentRequest(ctx context.Context, db DB, deploymentID, resourceID, except string, waiting bool) (string, error) {
	var number string
	err := db.QueryRow(ctx, `select r.number from requests r
		where (r.deployment_id = $1 or r.id = (select created_by_request_id from deployments where id = $1))
		  and (r.status in ('queued', 'running', 'cancelling') or $4 and r.status in ('pending_approval', 'scheduled'))
		  and ($2 = '' or r.resource_id is null or r.resource_id = $2)
		  and r.id <> $3
		order by r.submitted_at limit 1`, deploymentID, resourceID, except, waiting).Scan(&number)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return number, err
}

// WaitingDeploymentRequests lists the numbers of the requests on the
// deployment that wait for approval or their start time.
func (s *Store) WaitingDeploymentRequests(ctx context.Context, deploymentID string) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select number from requests where deployment_id = $1
		and status in ('pending_approval', 'scheduled') order by submitted_at`, deploymentID)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// OtherRunningOnDeployment reports whether a day-2 request other than
// except is running on the deployment.
func (s *Store) OtherRunningOnDeployment(ctx context.Context, db DB, deploymentID, except string) (bool, error) {
	var n int
	err := db.QueryRow(ctx, `select count(*) from requests where deployment_id = $1 and id <> $2
		and status in ('running', 'cancelling')`, deploymentID, except).Scan(&n)
	return n > 0, err
}

// RecentlyFailedAction reports whether a request by requesterID running
// action on the deployment (and resource, "" for the whole deployment)
// failed since a time; input and value, when set, must match its inputs.
func (s *Store) RecentlyFailedAction(ctx context.Context, requesterID, deploymentID, resourceID, action, input, value string, since time.Time) (bool, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) from requests where requester_id = $1 and deployment_id = $2
		and coalesce(resource_id, '') = $3 and action = $4 and ($5 = '' or inputs->>$5 = $6)
		and status = 'failed' and submitted_at > $7`,
		requesterID, deploymentID, resourceID, action, input, value, since).Scan(&n)
	return n > 0, err
}

// LockDeployment locks a deployment row until the transaction ends.
func (s *Store) LockDeployment(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `select 1 from deployments where id = $1 for update`, id)
	return err
}

// SetRequestStatus updates the status and the matching timestamps.
func (s *Store) SetRequestStatus(ctx context.Context, db DB, id, status string) error {
	_, err := db.Exec(ctx, `update requests set status = $2,
		started_at = case when $2 = 'running' and started_at is null then now() else started_at end,
		finished_at = case when $2 in ('succeeded', 'failed', 'rejected', 'cancelled') then now() else finished_at end
		where id = $1`, id, status)
	if err != nil {
		return err
	}
	return dropSecretInputs(ctx, db, id, status)
}

// dropSecretInputs deletes a request's sealed secret inputs once it can't
// run again: succeeded, rejected or cancelled. A failed request keeps them
// for a retry (S14.6).
func dropSecretInputs(ctx context.Context, db DB, id, status string) error {
	switch status {
	case "succeeded", "rejected", "cancelled":
		_, err := db.Exec(ctx, `delete from request_secrets where request_id = $1`, id)
		return err
	}
	return nil
}

// DeleteUnstartedRequest removes a request whose workflow never started,
// with its steps and the deployments it created, so a failed submit leaves
// nothing behind.
func (s *Store) DeleteUnstartedRequest(ctx context.Context, id string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		// Its events go too: subscribers never hear of a request that wasn't.
		if _, err := tx.Exec(ctx, `delete from integration_events where request_id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from deployments where created_by_request_id = $1`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `delete from requests where id = $1`, id)
		return err
	})
}

func (s *Store) SetRequestError(ctx context.Context, db DB, id, stepID, message string) error {
	_, err := db.Exec(ctx, `update requests set error_step_id = nullif($2, ''), error_message = nullif($3, '') where id = $1`,
		id, stepID, message)
	return err
}

// Idempotency

type IdempotencyRecord struct {
	BodyHash   string
	StatusCode *int
	Response   json.RawMessage
}

// ClaimIdempotencyKey records a new key, or returns the existing record when
// the key was used before (claimed is then false).
func (s *Store) ClaimIdempotencyKey(ctx context.Context, userID, key, bodyHash string) (rec IdempotencyRecord, claimed bool, err error) {
	tag, err := s.Pool.Exec(ctx, `insert into idempotency_keys (user_id, key, body_hash) values ($1, $2, $3)
		on conflict do nothing`, userID, key, bodyHash)
	if err != nil {
		return rec, false, err
	}
	if tag.RowsAffected() == 1 {
		return IdempotencyRecord{BodyHash: bodyHash}, true, nil
	}
	err = s.Pool.QueryRow(ctx, `select body_hash, status_code, response from idempotency_keys
		where user_id = $1 and key = $2`, userID, key).Scan(&rec.BodyHash, &rec.StatusCode, &rec.Response)
	return rec, false, err
}

func (s *Store) CompleteIdempotencyKey(ctx context.Context, userID, key string, status int, response []byte) error {
	_, err := s.Pool.Exec(ctx, `update idempotency_keys set status_code = $3, response = $4
		where user_id = $1 and key = $2`, userID, key, status, response)
	return err
}

func (s *Store) ReleaseIdempotencyKey(ctx context.Context, userID, key string) error {
	_, err := s.Pool.Exec(ctx, `delete from idempotency_keys where user_id = $1 and key = $2 and status_code is null`, userID, key)
	return err
}

func (s *Store) PurgeIdempotencyKeys(ctx context.Context, before time.Time) error {
	_, err := s.Pool.Exec(ctx, `delete from idempotency_keys where created_at < $1`, before)
	return err
}

func jsonOr(b json.RawMessage, def string) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(def)
	}
	return b
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ReplaceInputs stores the inputs and plan of a request that an approver
// changed, and the inputs on the deployments it is creating.
func (s *Store) ReplaceInputs(ctx context.Context, db DB, id string, inputs, plan json.RawMessage) error {
	if _, err := db.Exec(ctx, `update requests set inputs = $2, plan = $3 where id = $1`, id, inputs, plan); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `update deployments set inputs = $2 where created_by_request_id = $1`, id, inputs)
	return err
}

// SetRequestStatusFrom changes the status only from the expected one, and
// reports whether it did.
func (s *Store) SetRequestStatusFrom(ctx context.Context, db DB, id, from, to string) (bool, error) {
	tag, err := db.Exec(ctx, `update requests set status = $3,
		finished_at = case when $3 in ('succeeded', 'failed', 'rejected', 'cancelled') then now() else finished_at end
		where id = $1 and status = $2`, id, from, to)
	if err != nil || tag.RowsAffected() != 1 {
		return false, err
	}
	return true, dropSecretInputs(ctx, db, id, to)
}

// SealedInput is a request's secret input, sealed (S14.6).
type SealedInput struct {
	Field      string `db:"field"`
	KeyID      string `db:"key_id"`
	Nonce      []byte `db:"nonce"`
	Ciphertext []byte `db:"ciphertext"`
}

// PutRequestSecret keeps (or replaces) a request's sealed secret input.
func (s *Store) PutRequestSecret(ctx context.Context, db DB, requestID string, in SealedInput) error {
	_, err := db.Exec(ctx, `insert into request_secrets (request_id, field, key_id, nonce, ciphertext) values ($1,$2,$3,$4,$5)
		on conflict (request_id, field) do update set key_id = excluded.key_id, nonce = excluded.nonce, ciphertext = excluded.ciphertext`,
		requestID, in.Field, in.KeyID, in.Nonce, in.Ciphertext)
	return err
}

// SealedInputRow is a request's sealed secret input, for key rotation.
type SealedInputRow struct {
	RequestID string `db:"request_id"`
	SealedInput
}

// SealedInputsNotWith lists the sealed secret inputs of every request not
// sealed with this key.
func (s *Store) SealedInputsNotWith(ctx context.Context, keyID string) ([]SealedInputRow, error) {
	rows, _ := s.Pool.Query(ctx, `select request_id, field, key_id, nonce, ciphertext from request_secrets where key_id <> $1`, keyID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[SealedInputRow])
}

// ResealRequestSecret replaces a sealed secret input (key rotation), if it
// wasn't changed meanwhile.
func (s *Store) ResealRequestSecret(ctx context.Context, requestID, field string, oldNonce []byte, in SealedInput) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update request_secrets set key_id = $3, nonce = $4, ciphertext = $5
		where request_id = $1 and field = $2 and nonce = $6`, requestID, field, in.KeyID, in.Nonce, in.Ciphertext, oldNonce)
	return tag.RowsAffected() == 1, err
}

// RequestSecrets are a request's sealed secret inputs.
func (s *Store) RequestSecrets(ctx context.Context, requestID string) ([]SealedInput, error) {
	return s.RequestSecretsIn(ctx, s.Pool, requestID)
}

// RequestSecretsIn reads them in a transaction.
func (s *Store) RequestSecretsIn(ctx context.Context, db DB, requestID string) ([]SealedInput, error) {
	rows, _ := db.Query(ctx, `select field, key_id, nonce, ciphertext from request_secrets where request_id = $1`, requestID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[SealedInput])
}

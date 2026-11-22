package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The categories audit events are forwarded by (ADR-0058).
const (
	AuditSignIn         = "sign_in"
	AuditToken          = "token"
	AuditAccess         = "access"
	AuditCatalog        = "catalog"
	AuditPolicy         = "policy"
	AuditInfrastructure = "infrastructure"
	AuditExtensibility  = "extensibility"
	AuditSettings       = "settings"
	AuditRequest        = "request"
	AuditOther          = "other"
)

var auditCategories = map[string]string{
	"auth":    AuditSignIn,
	"token":   AuditToken,
	"project": AuditAccess, "member": AuditAccess, "group_mapping": AuditAccess, "role": AuditAccess,
	"role_assignment": AuditAccess, "environment": AuditAccess,
	"catalog": AuditCatalog, "form_library": AuditCatalog, "form_validation": AuditCatalog, "property_group": AuditCatalog,
	"policy": AuditPolicy, "naming": AuditPolicy,
	"placement": AuditInfrastructure, "network": AuditInfrastructure, "template": AuditInfrastructure,
	"size": AuditInfrastructure, "vcenter": AuditInfrastructure, "nsx_manager": AuditInfrastructure,
	"ip_range": AuditInfrastructure, "ad": AuditInfrastructure,
	"subscription": AuditExtensibility, "workflow": AuditExtensibility, "resource_type": AuditExtensibility,
	"awx_action": AuditExtensibility, "day2_form": AuditExtensibility, "variable": AuditExtensibility,
	"settings": AuditSettings, "secret": AuditSettings, "vra": AuditSettings, "setup": AuditSettings, "awx": AuditSettings,
	"request": AuditRequest, "deployment": AuditRequest, "approval": AuditRequest, "action": AuditRequest,
	"console": AuditRequest, "onboarding_plan": AuditRequest, "wf": AuditRequest, "schedule": AuditRequest,
}

// AuditCategory is the category of an action, by its first word.
func AuditCategory(action string) string {
	switch {
	case strings.HasPrefix(action, "awx.inventory_token"), strings.HasPrefix(action, "servicenow.token"):
		return AuditToken
	case strings.HasPrefix(action, "servicenow.request"):
		return AuditRequest
	case strings.HasPrefix(action, "servicenow."):
		return AuditSettings
	}
	first, _, _ := strings.Cut(action, ".")
	if c, ok := auditCategories[first]; ok {
		return c
	}
	return AuditOther
}

// AuditDestination is a syslog server or webhook audit events go to, with
// its delivery state.
type AuditDestination struct {
	ID            string          `db:"id"`
	Name          string          `db:"name"`
	Enabled       bool            `db:"enabled"`
	Kind          string          `db:"kind"`
	Categories    []string        `db:"categories"`
	Settings      json.RawMessage `db:"settings"`
	Failures      int             `db:"failures"`
	NextAttemptAt time.Time       `db:"next_attempt_at"`
	Delivered     int64           `db:"delivered"`
	LastSuccessAt *time.Time      `db:"last_success_at"`
	LastError     *string         `db:"last_error"`
	LastErrorAt   *time.Time      `db:"last_error_at"`
	LeaseUntil    *time.Time      `db:"lease_until"`
	// The webhook's authentication header's value, sealed.
	AuthKeyID      string `db:"auth_key_id"`
	AuthNonce      []byte `db:"auth_nonce"`
	AuthCiphertext []byte `db:"auth_ciphertext"`
	// Backlog leaves out the events set aside; a claim counts neither.
	Backlog        int       `db:"backlog"`
	SetAside       int       `db:"set_aside"`
	SetAsideReason *string   `db:"set_aside_reason"`
	CreatedAt      time.Time `db:"created_at"`
	UpdatedAt      time.Time `db:"updated_at"`
}

const auditDestinationCols = `d.id, d.name, d.enabled, d.kind, d.categories, d.settings, d.failures, d.next_attempt_at, d.delivered,
	d.last_success_at, d.last_error, d.last_error_at, d.lease_until, d.auth_key_id, d.auth_nonce, d.auth_ciphertext, d.created_at, d.updated_at`

const selectAuditDestinations = `select ` + auditDestinationCols + `, q.backlog, q.set_aside, q.set_aside_reason
	from audit_destinations d cross join lateral (
		select (count(*) filter (where set_aside_at is null))::int as backlog,
			(count(*) filter (where set_aside_at is not null))::int as set_aside,
			(array_agg(set_aside_reason order by set_aside_at desc) filter (where set_aside_at is not null))[1] as set_aside_reason
		from audit_deliveries where destination_id = d.id) q`

func (s *Store) AuditDestinations(ctx context.Context) ([]AuditDestination, error) {
	rows, _ := s.Pool.Query(ctx, selectAuditDestinations+` order by d.name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AuditDestination])
}

func (s *Store) GetAuditDestination(ctx context.Context, id string) (AuditDestination, error) {
	rows, _ := s.Pool.Query(ctx, selectAuditDestinations+` where d.id = $1`, id)
	d, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[AuditDestination])
	return d, notFound(err)
}

// SaveAuditDestination creates or replaces a destination. What waits for
// it, and what was set aside, is tried again at once.
func (s *Store) SaveAuditDestination(ctx context.Context, d AuditDestination) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into audit_destinations (id, name, enabled, kind, categories, settings, auth_key_id,
				auth_nonce, auth_ciphertext)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			on conflict (id) do update set name = excluded.name, enabled = excluded.enabled, kind = excluded.kind,
				categories = excluded.categories, settings = excluded.settings, auth_key_id = excluded.auth_key_id,
				auth_nonce = excluded.auth_nonce, auth_ciphertext = excluded.auth_ciphertext, next_attempt_at = now(),
				updated_at = now()`,
			d.ID, d.Name, d.Enabled, d.Kind, nonNil(d.Categories), d.Settings, d.AuthKeyID, d.AuthNonce, d.AuthCiphertext); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `update audit_deliveries set set_aside_at = null, set_aside_reason = null
			where destination_id = $1 and set_aside_at is not null`, d.ID)
		return err
	})
}

// DeleteAuditDestination removes a destination; what waited for it is
// swept (SweepAuditDeliveries).
func (s *Store) DeleteAuditDestination(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from audit_destinations where id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ClaimAuditDestinations leases the enabled destinations that have events
// waiting and are due, so that one worker sends to each at a time. What
// waits is looked at before, not under, the destinations' locks.
func (s *Store) ClaimAuditDestinations(ctx context.Context, now time.Time, lease time.Duration) ([]AuditDestination, error) {
	rows, _ := s.Pool.Query(ctx, `select d.id from audit_destinations d
		where d.enabled and d.next_attempt_at <= $1 and (d.lease_until is null or d.lease_until <= $1)
		  and exists (select 1 from audit_deliveries q where q.destination_id = d.id and q.set_aside_at is null)`, now)
	waiting, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(waiting) == 0 {
		return nil, err
	}
	rows, _ = s.Pool.Query(ctx, `with due as (
			select d.id from audit_destinations d
			where d.id = any($3) and d.enabled and d.next_attempt_at <= $1 and (d.lease_until is null or d.lease_until <= $1)
			for no key update skip locked)
		update audit_destinations d set lease_until = $1::timestamptz + $2::interval from due where d.id = due.id
		returning `+auditDestinationCols+`, 0 as backlog, 0 as set_aside, null::text as set_aside_reason`, now, lease, waiting)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AuditDestination])
}

// ReleaseAuditDestination ends a destination's lease, unless it ran out
// and another worker holds the destination now.
func (s *Store) ReleaseAuditDestination(ctx context.Context, id string, until time.Time) error {
	_, err := s.Pool.Exec(ctx, `update audit_destinations set lease_until = null where id = $1 and lease_until = $2`, id, until)
	return err
}

// SweepAuditDeliveries drops what waits for destinations removed, at most
// 10,000 at a time: those of the removal, and of the events audited while
// it ran. It says how many it dropped.
func (s *Store) SweepAuditDeliveries(ctx context.Context) (int64, error) {
	// The destinations with something waiting, one index lookup each,
	// rather than a scan of the deliveries.
	tag, err := s.Pool.Exec(ctx, `with recursive waiting as (
			(select destination_id as id from audit_deliveries order by destination_id limit 1)
			union all
			select (select q.destination_id from audit_deliveries q where q.destination_id > w.id
				order by q.destination_id limit 1)
			from waiting w where w.id is not null),
		gone as (
			select q.destination_id, q.event_id from audit_deliveries q
			where q.destination_id in (select w.id from waiting w where w.id is not null
				and not exists (select 1 from audit_destinations d where d.id = w.id))
			limit 10000)
		delete from audit_deliveries q using gone
		where q.destination_id = gone.destination_id and q.event_id = gone.event_id`)
	return tag.RowsAffected(), err
}

// AuditBacklog is the oldest events waiting for a destination, oldest
// first, without those set aside.
func (s *Store) AuditBacklog(ctx context.Context, destinationID string, limit int) ([]AuditRow, error) {
	rows, _ := s.Pool.Query(ctx, `select a.id, a.at, a.actor_id, u.name as actor_name, a.action, a.target_type, a.target_id,
		a.project_id, p.code as project_code, p.name as project_name, a.detail
		from audit_deliveries q join audit_events a on a.id = q.event_id
		left join users u on u.id = a.actor_id left join projects p on p.id = a.project_id
		where q.destination_id = $1 and q.set_aside_at is null order by a.id limit $2`, destinationID, limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AuditRow])
}

// AuditDelivered drops the events sent from a destination's backlog and
// records the success.
func (s *Store) AuditDelivered(ctx context.Context, destinationID string, eventIDs []int64, now time.Time) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `delete from audit_deliveries where destination_id = $1 and event_id = any($2)`, destinationID, eventIDs)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update audit_destinations set delivered = delivered + $2, failures = 0, last_success_at = $3,
			last_error = null, last_error_at = null, next_attempt_at = $3 where id = $1`, destinationID, tag.RowsAffected(), now)
		return err
	})
}

// AuditDeliveryFailed records a failed attempt; what waits is tried again
// at next.
func (s *Store) AuditDeliveryFailed(ctx context.Context, destinationID, why string, now, next time.Time) error {
	_, err := s.Pool.Exec(ctx, `update audit_destinations set failures = failures + 1, last_error = $2, last_error_at = $3,
		next_attempt_at = $4 where id = $1`, destinationID, why, now, next)
	return err
}

// AuditSetAside sets aside an event a destination refuses for good: the
// events after it go, and saving the destination tries it again.
func (s *Store) AuditSetAside(ctx context.Context, destinationID string, eventID int64, why string, now time.Time) error {
	_, err := s.Pool.Exec(ctx, `update audit_deliveries set set_aside_at = $3, set_aside_reason = $4
		where destination_id = $1 and event_id = $2`, destinationID, eventID, now, why)
	return err
}

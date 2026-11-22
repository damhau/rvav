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
	"subscription": AuditExtensibility, "workflow": AuditExtensibility, "resource_type": AuditExtensibility,
	"awx_action": AuditExtensibility, "day2_form": AuditExtensibility,
	"settings": AuditSettings, "secret": AuditSettings, "vra": AuditSettings, "setup": AuditSettings, "awx": AuditSettings,
	"request": AuditRequest, "deployment": AuditRequest, "approval": AuditRequest, "action": AuditRequest,
	"console": AuditRequest, "onboarding_plan": AuditRequest, "wf": AuditRequest,
}

// AuditCategory is the category of an action, by its first word.
func AuditCategory(action string) string {
	if strings.HasPrefix(action, "awx.inventory_token") {
		return AuditToken
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
	Backlog       int             `db:"backlog"`
	CreatedAt     time.Time       `db:"created_at"`
	UpdatedAt     time.Time       `db:"updated_at"`
}

const auditDestinationCols = `d.id, d.name, d.enabled, d.kind, d.categories, d.settings, d.failures, d.next_attempt_at, d.delivered,
	d.last_success_at, d.last_error, d.last_error_at, d.created_at, d.updated_at,
	(select count(*) from audit_deliveries q where q.destination_id = d.id)::int as backlog`

func (s *Store) AuditDestinations(ctx context.Context) ([]AuditDestination, error) {
	rows, _ := s.Pool.Query(ctx, `select `+auditDestinationCols+` from audit_destinations d order by d.name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AuditDestination])
}

func (s *Store) GetAuditDestination(ctx context.Context, id string) (AuditDestination, error) {
	rows, _ := s.Pool.Query(ctx, `select `+auditDestinationCols+` from audit_destinations d where d.id = $1`, id)
	d, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[AuditDestination])
	return d, notFound(err)
}

// SaveAuditDestination creates or replaces a destination. What waits for
// it is tried again at once.
func (s *Store) SaveAuditDestination(ctx context.Context, d AuditDestination) error {
	_, err := s.Pool.Exec(ctx, `insert into audit_destinations (id, name, enabled, kind, categories, settings)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (id) do update set name = excluded.name, enabled = excluded.enabled, kind = excluded.kind,
			categories = excluded.categories, settings = excluded.settings, next_attempt_at = now(), updated_at = now()`,
		d.ID, d.Name, d.Enabled, d.Kind, nonNil(d.Categories), d.Settings)
	return err
}

// DeleteAuditDestination removes a destination and what waits for it.
func (s *Store) DeleteAuditDestination(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from audit_destinations where id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ClaimAuditDestinations leases the enabled destinations that have events
// waiting and are due, so that one worker sends to each at a time.
func (s *Store) ClaimAuditDestinations(ctx context.Context, now time.Time, lease time.Duration) ([]AuditDestination, error) {
	rows, _ := s.Pool.Query(ctx, `with due as (
			select d.id from audit_destinations d
			where d.enabled and d.next_attempt_at <= $1 and (d.lease_until is null or d.lease_until <= $1)
			  and exists (select 1 from audit_deliveries q where q.destination_id = d.id)
			for update skip locked)
		update audit_destinations d set lease_until = $1::timestamptz + $2::interval from due where d.id = due.id
		returning `+auditDestinationCols, now, lease)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AuditDestination])
}

// ReleaseAuditDestination ends a destination's lease.
func (s *Store) ReleaseAuditDestination(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `update audit_destinations set lease_until = null where id = $1`, id)
	return err
}

// AuditBacklog is the oldest events waiting for a destination, oldest first.
func (s *Store) AuditBacklog(ctx context.Context, destinationID string, limit int) ([]AuditRow, error) {
	rows, _ := s.Pool.Query(ctx, `select a.id, a.at, a.actor_id, u.name as actor_name, a.action, a.target_type, a.target_id,
		a.project_id, p.code as project_code, p.name as project_name, a.detail
		from audit_deliveries q join audit_events a on a.id = q.event_id
		left join users u on u.id = a.actor_id left join projects p on p.id = a.project_id
		where q.destination_id = $1 order by a.id limit $2`, destinationID, limit)
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

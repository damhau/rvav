package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Quotas and the lease, approval and day-2 policies, for their
// administration (ADR-0020).

// AdminApprovalPolicy is an approval policy, enabled or not.
type AdminApprovalPolicy struct {
	ApprovalPolicy
	Enabled bool `db:"enabled"`
}

const approvalPolicyCols = `id, name, reason, condition, item_type, project_id, levels, priority, auto_approve, enabled`

// AllApprovalPolicies lists every approval policy in the order they are tried.
func (s *Store) AllApprovalPolicies(ctx context.Context) ([]AdminApprovalPolicy, error) {
	rows, _ := s.Pool.Query(ctx, `select `+approvalPolicyCols+` from approval_policies order by priority, id`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AdminApprovalPolicy])
}

func (s *Store) GetApprovalPolicy(ctx context.Context, id string) (AdminApprovalPolicy, error) {
	rows, _ := s.Pool.Query(ctx, `select `+approvalPolicyCols+` from approval_policies where id = $1`, id)
	p, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[AdminApprovalPolicy])
	return p, notFound(err)
}

func (s *Store) SaveApprovalPolicy(ctx context.Context, p AdminApprovalPolicy) error {
	_, err := s.Pool.Exec(ctx, `insert into approval_policies (id, name, reason, condition, item_type, project_id, levels, priority,
		auto_approve, enabled) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		on conflict (id) do update set name = excluded.name, reason = excluded.reason, condition = excluded.condition,
		item_type = excluded.item_type, project_id = excluded.project_id, levels = excluded.levels, priority = excluded.priority,
		auto_approve = excluded.auto_approve, enabled = excluded.enabled`,
		p.ID, p.Name, p.Reason, p.Condition, p.ItemType, p.ProjectID, jsonOr(p.Levels, "[]"), p.Priority, p.AutoApprove, p.Enabled)
	return err
}

func (s *Store) DeleteApprovalPolicy(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from approval_policies where id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// AdminDay2Policy is a day-2 policy, enabled or not.
type AdminDay2Policy struct {
	Day2Policy
	Enabled bool `db:"enabled"`
}

const day2PolicyCols = `id, name, reason, priority, actions, roles, condition, effect, levels, project_id, enabled`

func (s *Store) AllDay2Policies(ctx context.Context) ([]AdminDay2Policy, error) {
	rows, _ := s.Pool.Query(ctx, `select `+day2PolicyCols+` from day2_policies order by priority, id`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AdminDay2Policy])
}

func (s *Store) GetDay2Policy(ctx context.Context, id string) (AdminDay2Policy, error) {
	rows, _ := s.Pool.Query(ctx, `select `+day2PolicyCols+` from day2_policies where id = $1`, id)
	p, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[AdminDay2Policy])
	return p, notFound(err)
}

func (s *Store) SaveDay2Policy(ctx context.Context, p AdminDay2Policy) error {
	_, err := s.Pool.Exec(ctx, `insert into day2_policies (id, name, reason, priority, actions, roles, condition, effect, levels,
		project_id, enabled) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		on conflict (id) do update set name = excluded.name, reason = excluded.reason, priority = excluded.priority,
		actions = excluded.actions, roles = excluded.roles, condition = excluded.condition, effect = excluded.effect,
		levels = excluded.levels, project_id = excluded.project_id, enabled = excluded.enabled`,
		p.ID, p.Name, p.Reason, p.Priority, nonNil(p.Actions), nonNil(p.Roles), p.Condition, p.Effect, jsonOr(p.Levels, "[]"),
		p.ProjectID, p.Enabled)
	return err
}

func (s *Store) DeleteDay2Policy(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from day2_policies where id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// LeasePolicies lists environments' policies and projects' overrides.
func (s *Store) LeasePolicies(ctx context.Context) ([]LeasePolicy, error) {
	rows, _ := s.Pool.Query(ctx, `select environment, project_id, default_days, max_days, max_extensions, grace_days, expiry_action
		from lease_policies order by environment, project_id nulls first`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[LeasePolicy])
}

// SetLeasePolicy sets an environment's policy or a project's override.
func (s *Store) SetLeasePolicy(ctx context.Context, l LeasePolicy) error {
	_, err := s.Pool.Exec(ctx, `insert into lease_policies (environment, project_id, default_days, max_days, max_extensions,
		grace_days, expiry_action) values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (environment, (coalesce(project_id, ''))) do update set default_days = excluded.default_days,
		max_days = excluded.max_days, max_extensions = excluded.max_extensions, grace_days = excluded.grace_days,
		expiry_action = excluded.expiry_action`,
		l.Environment, l.ProjectID, l.DefaultDays, l.MaxDays, l.MaxExtensions, l.GraceDays, l.ExpiryAction)
	return err
}

// DeleteLeaseOverride removes a project's override of an environment's policy.
func (s *Store) DeleteLeaseOverride(ctx context.Context, environment, projectID string) error {
	tag, err := s.Pool.Exec(ctx, `delete from lease_policies where environment = $1 and project_id = $2`, environment, projectID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ProjectQuotas lists a project's limits, the project-wide ones first.
func (s *Store) ProjectQuotas(ctx context.Context, projectID string) ([]QuotaLimit, error) {
	rows, _ := s.Pool.Query(ctx, `select environment, resource, limit_value, enforcement from quotas where project_id = $1
		order by environment, resource`, projectID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[QuotaLimit])
}

// ReplaceQuotas replaces a project's limits.
func (s *Store) ReplaceQuotas(ctx context.Context, projectID string, limits []QuotaLimit) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `delete from quotas where project_id = $1`, projectID); err != nil {
			return err
		}
		for _, q := range limits {
			if _, err := tx.Exec(ctx, `insert into quotas (project_id, environment, resource, limit_value, enforcement)
				values ($1, $2, $3, $4, $5)`, projectID, q.Environment, q.Resource, q.Limit, q.Enforcement); err != nil {
				return err
			}
		}
		return nil
	})
}

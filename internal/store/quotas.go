package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// UserQuota limits what each user owns in a project, or across rvav when
// ProjectID is nil (ADR-0047).
type UserQuota struct {
	ProjectID   *string `db:"project_id"`
	Resource    string  `db:"resource"`
	Limit       *int    `db:"limit_value"`
	Enforcement string  `db:"enforcement"`
	SetByAdmin  bool    `db:"set_by_admin"`
}

// UserQuotas lists each user's limits in a project, or across rvav for nil.
func (s *Store) UserQuotas(ctx context.Context, projectID *string) ([]UserQuota, error) {
	rows, _ := s.Pool.Query(ctx, `select project_id, resource, limit_value, enforcement, set_by_admin from user_quotas
		where project_id is not distinct from $1 order by resource`, projectID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[UserQuota])
}

// ReplaceUserQuotas replaces each member's limits in a project.
func (s *Store) ReplaceUserQuotas(ctx context.Context, projectID string, limits []UserQuota) error {
	return s.Tx(ctx, func(tx pgx.Tx) error { return replaceUserQuotas(ctx, tx, &projectID, limits) })
}

func replaceUserQuotas(ctx context.Context, tx pgx.Tx, projectID *string, limits []UserQuota) error {
	if _, err := tx.Exec(ctx, `delete from user_quotas where project_id is not distinct from $1`, projectID); err != nil {
		return err
	}
	for _, q := range limits {
		if _, err := tx.Exec(ctx, `insert into user_quotas (project_id, resource, limit_value, enforcement, set_by_admin)
			values ($1, $2, $3, $4, $5)`, projectID, q.Resource, q.Limit, q.Enforcement, q.SetByAdmin); err != nil {
			return err
		}
	}
	return nil
}

// GlobalQuotas lists the limits of all of rvav together.
func (s *Store) GlobalQuotas(ctx context.Context) ([]QuotaLimit, error) {
	rows, _ := s.Pool.Query(ctx, `select '' as environment, resource, limit_value, enforcement from global_quotas order by resource`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[QuotaLimit])
}

// ReplaceGlobalQuotas replaces the limits of all of rvav together and those
// of each user across it.
func (s *Store) ReplaceGlobalQuotas(ctx context.Context, whole []QuotaLimit, perUser []UserQuota) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `delete from global_quotas`); err != nil {
			return err
		}
		for _, q := range whole {
			if _, err := tx.Exec(ctx, `insert into global_quotas (resource, limit_value, enforcement) values ($1, $2, $3)`,
				q.Resource, q.Limit, q.Enforcement); err != nil {
				return err
			}
		}
		return replaceUserQuotas(ctx, tx, nil, perUser)
	})
}

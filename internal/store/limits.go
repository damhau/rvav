package store

import (
	"cmp"
	"context"

	"github.com/jackc/pgx/v5"
)

// DeploymentLimitPolicy caps how many deployments each user, or a project,
// may have, and the vCPU and memory of each deployment (ADR-0047). A nil
// limit is one the policy doesn't set.
type DeploymentLimitPolicy struct {
	ID                    string  `db:"id"`
	Name                  string  `db:"name"`
	ProjectID             *string `db:"project_id"`
	Environment           *string `db:"environment"`
	Priority              int     `db:"priority"`
	Enabled               bool    `db:"enabled"`
	Enforcement           string  `db:"enforcement"`
	DeploymentsPerUser    *int    `db:"deployments_per_user"`
	DeploymentsPerProject *int    `db:"deployments_per_project"`
	VCPUPerDeployment     *int    `db:"vcpu_per_deployment"`
	MemoryGBPerDeployment *int    `db:"memory_gb_per_deployment"`
	SetByAdmin            bool    `db:"set_by_admin"`
}

const limitColumns = `id, name, project_id, environment, priority, enabled, enforcement, deployments_per_user,
	deployments_per_project, vcpu_per_deployment, memory_gb_per_deployment, set_by_admin`

// DeploymentLimitPolicies lists the policies in the order they are tried:
// by priority, global ones first on a tie, then by name.
func (s *Store) DeploymentLimitPolicies(ctx context.Context, db DB) ([]DeploymentLimitPolicy, error) {
	rows, _ := db.Query(ctx, `select `+limitColumns+` from deployment_limit_policies
		order by priority, project_id nulls first, name, id`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[DeploymentLimitPolicy])
}

func (s *Store) GetDeploymentLimitPolicy(ctx context.Context, id string) (DeploymentLimitPolicy, error) {
	rows, _ := s.Pool.Query(ctx, `select `+limitColumns+` from deployment_limit_policies where id = $1`, id)
	pol, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[DeploymentLimitPolicy])
	return pol, notFound(err)
}

func (s *Store) SaveDeploymentLimitPolicy(ctx context.Context, pol DeploymentLimitPolicy) error {
	_, err := s.Pool.Exec(ctx, `insert into deployment_limit_policies (`+limitColumns+`)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		on conflict (id) do update set name = excluded.name, project_id = excluded.project_id,
		environment = excluded.environment, priority = excluded.priority, enabled = excluded.enabled,
		enforcement = excluded.enforcement, deployments_per_user = excluded.deployments_per_user,
		deployments_per_project = excluded.deployments_per_project, vcpu_per_deployment = excluded.vcpu_per_deployment,
		memory_gb_per_deployment = excluded.memory_gb_per_deployment, set_by_admin = excluded.set_by_admin`,
		pol.ID, pol.Name, pol.ProjectID, pol.Environment, pol.Priority, pol.Enabled, cmp.Or(pol.Enforcement, EnforcementHard),
		pol.DeploymentsPerUser, pol.DeploymentsPerProject, pol.VCPUPerDeployment, pol.MemoryGBPerDeployment, pol.SetByAdmin)
	return err
}

func (s *Store) DeleteDeploymentLimitPolicy(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from deployment_limit_policies where id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeploymentCount is how many deployments a project has in an
// environment, and how many of them a user owns.
type DeploymentCount struct {
	Environment string `db:"environment"`
	All         int    `db:"all_count"`
	Owned       int    `db:"owned"`
}

// DeploymentCounts counts a project's deployments not deleted, by
// environment, and those ownerID owns: failed ones and those of requests
// not done yet count, since they hold their place (ADR-0047). So do the
// deployments a move not done yet brings to the project, and those a
// handover not done yet gives ownerID.
func (s *Store) DeploymentCounts(ctx context.Context, db DB, projectID, ownerID string) ([]DeploymentCount, error) {
	rows, _ := db.Query(ctx, `select environment, sum(n)::int as all_count, sum(o)::int as owned from (
		  select environment, 1 as n, (owner_id = $2)::int as o
		  from deployments where project_id = $1 and status <> 'deleted'
		  union all
		  select d.environment, case when r.plan->'quota_to'->>'project_id' = $1 then 1 else 0 end,
		         case when coalesce(r.plan->'quota_to'->>'owner_id', d.owner_id) = $2 then 1 else 0 end
		  from requests r join deployments d on d.id = r.deployment_id
		  where r.status in ('pending_approval', 'queued', 'scheduled', 'running') and d.status <> 'deleted'
		    and ((r.plan->'quota_to'->>'project_id' = $1 and d.project_id <> $1)
		         or (r.plan->'quota_to'->>'owner_id' = $2 and d.owner_id <> $2 and d.project_id = $1))
		) c group by environment`, projectID, ownerID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[DeploymentCount])
}

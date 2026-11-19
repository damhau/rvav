package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// OnboardingPlan is a saved filter of the inventory's VMs and where what it
// matches goes (X1.5).
type OnboardingPlan struct {
	ID          string          `db:"id"`
	Name        string          `db:"name"`
	Description string          `db:"description"`
	Filter      json.RawMessage `db:"filter"`
	Target      json.RawMessage `db:"target"`
	EveryHours  *int            `db:"every_hours"`
	NextRunAt   *time.Time      `db:"next_run_at"`
	// DeploymentID is the deployment its VMs go into; DeploymentName is
	// empty when it was deleted.
	DeploymentID   *string   `db:"deployment_id"`
	DeploymentName string    `db:"deployment_name"`
	CreatedBy      *string   `db:"created_by"`
	UpdatedBy      *string   `db:"updated_by"`
	UpdatedByName  string    `db:"updated_by_name"`
	CreatedAt      time.Time `db:"created_at"`
	UpdatedAt      time.Time `db:"updated_at"`
}

const planSelect = `select p.id, p.name, p.description, p.filter, p.target, p.every_hours, p.next_run_at, p.deployment_id,
	coalesce((select d.name from deployments d where d.id = p.deployment_id and d.status <> 'deleted'), '') as deployment_name,
	p.created_by, p.updated_by, coalesce(u.name, '') as updated_by_name, p.created_at, p.updated_at
	from onboarding_plans p left join users u on u.id = p.updated_by`

func (s *Store) ListOnboardingPlans(ctx context.Context) ([]OnboardingPlan, error) {
	rows, _ := s.Pool.Query(ctx, planSelect+` order by lower(p.name), p.id`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[OnboardingPlan])
}

func (s *Store) GetOnboardingPlan(ctx context.Context, db DB, id string) (OnboardingPlan, error) {
	rows, _ := db.Query(ctx, planSelect+` where p.id = $1`, id)
	p, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[OnboardingPlan])
	return p, notFound(err)
}

// SaveOnboardingPlan creates a plan or replaces what can be changed of it.
func (s *Store) SaveOnboardingPlan(ctx context.Context, db DB, p OnboardingPlan) error {
	_, err := db.Exec(ctx, `insert into onboarding_plans (id, name, description, filter, target, every_hours, next_run_at,
			deployment_id, created_by, updated_by)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
		on conflict (id) do update set name = excluded.name, description = excluded.description, filter = excluded.filter,
			target = excluded.target, every_hours = excluded.every_hours, next_run_at = excluded.next_run_at,
			deployment_id = excluded.deployment_id, updated_by = excluded.updated_by, updated_at = now()`,
		p.ID, p.Name, p.Description, jsonOr(p.Filter, "{}"), jsonOr(p.Target, "{}"), p.EveryHours, p.NextRunAt, p.DeploymentID,
		p.UpdatedBy)
	return err
}

func (s *Store) DeleteOnboardingPlan(ctx context.Context, db DB, id string) error {
	_, err := db.Exec(ctx, `delete from onboarding_plans where id = $1`, id)
	return err
}

// SetPlanDeployment records the deployment a plan's first run made.
func (s *Store) SetPlanDeployment(ctx context.Context, db DB, planID, deploymentID string) error {
	_, err := db.Exec(ctx, `update onboarding_plans set deployment_id = $2 where id = $1`, planID, deploymentID)
	return err
}

// ClaimDuePlans moves the next run of the plans whose schedule is due on
// by their interval, and returns them: each is started once, whichever
// worker claims it.
func (s *Store) ClaimDuePlans(ctx context.Context, now time.Time) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `update onboarding_plans set next_run_at = $1 + every_hours * interval '1 hour'
		where every_hours is not null and next_run_at <= $1 returning id`, now)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// OnboardingRun is a run of a plan, with how many VMs it adopted, skipped
// and failed to adopt.
type OnboardingRun struct {
	ID            string     `db:"id"`
	PlanID        string     `db:"plan_id"`
	PlanName      string     `db:"plan_name"`
	Trigger       string     `db:"trigger"`
	StartedBy     *string    `db:"started_by"`
	StartedByName string     `db:"started_by_name"`
	Status        string     `db:"status"`
	Error         string     `db:"error"`
	StartedAt     time.Time  `db:"started_at"`
	FinishedAt    *time.Time `db:"finished_at"`
	Adopted       int        `db:"adopted"`
	Skipped       int        `db:"skipped"`
	Failed        int        `db:"failed"`
}

const runSelect = `select r.id, r.plan_id, p.name as plan_name, r.trigger, r.started_by, coalesce(u.name, '') as started_by_name,
	r.status, r.error, r.started_at, r.finished_at,
	(select count(*) from onboarding_run_vms v where v.run_id = r.id and v.outcome = 'adopted')::int as adopted,
	(select count(*) from onboarding_run_vms v where v.run_id = r.id and v.outcome = 'skipped')::int as skipped,
	(select count(*) from onboarding_run_vms v where v.run_id = r.id and v.outcome = 'failed')::int as failed
	from onboarding_runs r join onboarding_plans p on p.id = r.plan_id left join users u on u.id = r.started_by`

// ErrRunning is a plan that is running already.
var ErrRunning = errors.New("a run of the plan goes on")

// StartOnboardingRun records a run of a plan as running; ErrRunning when
// one is.
func (s *Store) StartOnboardingRun(ctx context.Context, db DB, r OnboardingRun) error {
	_, err := db.Exec(ctx, `insert into onboarding_runs (id, plan_id, trigger, started_by) values ($1,$2,$3,$4)`,
		r.ID, r.PlanID, r.Trigger, r.StartedBy)
	if IsUniqueViolation(err) {
		return ErrRunning
	}
	return err
}

func (s *Store) GetOnboardingRun(ctx context.Context, id string) (OnboardingRun, error) {
	rows, _ := s.Pool.Query(ctx, runSelect+` where r.id = $1`, id)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[OnboardingRun])
	return r, notFound(err)
}

// ListOnboardingRuns lists a plan's runs, the latest first.
func (s *Store) ListOnboardingRuns(ctx context.Context, planID string, limit int) ([]OnboardingRun, error) {
	rows, _ := s.Pool.Query(ctx, runSelect+` where r.plan_id = $1 order by r.started_at desc, r.id desc limit $2`, planID, limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[OnboardingRun])
}

// LastOnboardingRuns is each plan's latest run, by plan.
func (s *Store) LastOnboardingRuns(ctx context.Context) (map[string]OnboardingRun, error) {
	rows, _ := s.Pool.Query(ctx, runSelect+` where r.id = (select l.id from onboarding_runs l where l.plan_id = r.plan_id
		order by l.started_at desc, l.id desc limit 1)`)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[OnboardingRun])
	out := make(map[string]OnboardingRun, len(list))
	for _, r := range list {
		out[r.PlanID] = r
	}
	return out, err
}

// FinishOnboardingRun ends a running run; false when it had ended.
func (s *Store) FinishOnboardingRun(ctx context.Context, db DB, id, status, msg string) (bool, error) {
	tag, err := db.Exec(ctx, `update onboarding_runs set status = $2, error = $3, finished_at = now()
		where id = $1 and status = 'running'`, id, status, msg)
	return tag.RowsAffected() == 1, err
}

// PruneOnboardingRuns keeps a plan's latest runs only.
func (s *Store) PruneOnboardingRuns(ctx context.Context, planID string, keep int) error {
	_, err := s.Pool.Exec(ctx, `delete from onboarding_runs where plan_id = $1 and status <> 'running' and id not in
		(select id from onboarding_runs where plan_id = $1 order by started_at desc, id desc limit $2)`, planID, keep)
	return err
}

// OnboardingRunVM is what a run did to a VM its plan matched.
type OnboardingRunVM struct {
	RunID          string  `db:"run_id"`
	VCenter        string  `db:"vcenter"`
	Moref          string  `db:"moref"`
	Name           string  `db:"name"`
	Outcome        string  `db:"outcome"`
	Code           string  `db:"code"`
	Reason         string  `db:"reason"`
	DeploymentID   *string `db:"deployment_id"`
	DeploymentName string  `db:"deployment_name"`
}

func (s *Store) RecordRunVM(ctx context.Context, db DB, v OnboardingRunVM) error {
	_, err := db.Exec(ctx, `insert into onboarding_run_vms (run_id, vcenter, moref, name, outcome, code, reason, deployment_id)
		values ($1,$2,$3,$4,$5,$6,$7,$8)
		on conflict (run_id, vcenter, moref) do update set name = excluded.name, outcome = excluded.outcome, code = excluded.code,
			reason = excluded.reason, deployment_id = excluded.deployment_id`,
		v.RunID, v.VCenter, v.Moref, v.Name, v.Outcome, v.Code, v.Reason, v.DeploymentID)
	return err
}

// RunVMs lists what a run did, VM by VM, by name.
func (s *Store) RunVMs(ctx context.Context, runID string) ([]OnboardingRunVM, error) {
	rows, _ := s.Pool.Query(ctx, `select v.run_id, v.vcenter, v.moref, v.name, v.outcome, v.code, v.reason, v.deployment_id,
		coalesce(d.name, '') as deployment_name
		from onboarding_run_vms v left join deployments d on d.id = v.deployment_id
		where v.run_id = $1 order by v.name, v.vcenter, v.moref`, runID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[OnboardingRunVM])
}

// Seat is a VM of a deployment, and the component of its blueprint it is
// a VM of ("" without components).
type Seat struct {
	Name      string `db:"name"`
	Component string `db:"component"`
}

// DeploymentSeats lists a deployment's VMs, in a transaction that holds
// its lock.
func (s *Store) DeploymentSeats(ctx context.Context, db DB, deploymentID string) ([]Seat, error) {
	rows, _ := db.Query(ctx, `select name, coalesce(properties->'vm'->>'component', '') as component from resources
		where deployment_id = $1 and type = 'vsphere.vm' order by name`, deploymentID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Seat])
}

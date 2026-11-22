package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ServiceNow (ADR-0065): the CIs rvav keeps for VMs, and the deployments
// queued for a look.

// LockServiceNowSync keeps one ServiceNow pass at a time across the sync
// processes.
const LockServiceNowSync int64 = 0x7276_6176_0008

// cmdbTopics are the events that may change a deployment's VMs.
var cmdbTopics = []string{"compute.provision.post", "compute.removal.post", "provisioning.post", "day2.post", "delete.post",
	"lease.expired"}

// QueueForServiceNow queues, in the transaction that marks events
// dispatched, the deployments they may have changed, while rvav keeps
// ServiceNow's CMDB.
func (s *Store) QueueForServiceNow(ctx context.Context, tx pgx.Tx, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `insert into servicenow_cmdb_queue (deployment_id)
		select distinct e.deployment_id from integration_events e
		where e.id = any($1) and e.deployment_id is not null and e.topic = any($2)
			and exists (select 1 from servicenow_state where cmdb)
		on conflict (deployment_id) do update set gen = servicenow_cmdb_queue.gen + 1, queued_at = now()`, eventIDs, cmdbTopics)
	return err
}

// ServiceNowState is what the ServiceNow pass keeps between passes.
type ServiceNowState struct {
	CMDB         bool            `db:"cmdb"`
	ReconciledAt *time.Time      `db:"reconciled_at"`
	Report       json.RawMessage `db:"report"`
}

func (s *Store) ServiceNowState(ctx context.Context) (ServiceNowState, error) {
	rows, _ := s.Pool.Query(ctx, `select cmdb, reconciled_at, report from servicenow_state`)
	st, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[ServiceNowState])
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceNowState{}, nil
	}
	return st, err
}

// SetServiceNowCMDB says whether the CMDB is kept, so that the dispatcher
// queues deployments, or doesn't. Turned on, the next pass reconciles:
// events went unqueued while it was off.
func (s *Store) SetServiceNowCMDB(ctx context.Context, on bool) error {
	_, err := s.Pool.Exec(ctx, `update servicenow_state set cmdb = $1, reconciled_at = case when $1 then null else reconciled_at end
		where cmdb <> $1`, on)
	return err
}

// SaveServiceNowReconcile records a reconcile's report.
func (s *Store) SaveServiceNowReconcile(ctx context.Context, at time.Time, report json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `update servicenow_state set reconciled_at = $1, report = $2`, at, report)
	return err
}

// ReconcileServiceNowNow makes the next pass reconcile.
func (s *Store) ReconcileServiceNowNow(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `update servicenow_state set reconciled_at = null`)
	return err
}

// QueueAllForServiceNow queues every deployment of the projects in scope
// (all when none is named) that has VMs, and every deployment with a CI
// rvav keeps.
func (s *Store) QueueAllForServiceNow(ctx context.Context, projects []string) error {
	_, err := s.Pool.Exec(ctx, `insert into servicenow_cmdb_queue (deployment_id)
		select d.id from deployments d join projects p on p.id = d.project_id
		where d.status <> 'deleted' and (coalesce(cardinality($1::text[]), 0) = 0 or p.id = any($1) or p.code = any($1))
			and exists (select 1 from resources r where r.deployment_id = d.id and r.type = 'vsphere.vm')
		union
		select deployment_id from servicenow_cis where retired_at is null
		on conflict (deployment_id) do update set gen = servicenow_cmdb_queue.gen + 1, queued_at = now()`, projects)
	return err
}

// ServiceNowQueued is a deployment queued for a look.
type ServiceNowQueued struct {
	DeploymentID string  `db:"deployment_id"`
	Gen          int64   `db:"gen"`
	Attempts     int     `db:"attempts"`
	LastError    *string `db:"last_error"`
}

// DueForServiceNow are the deployments due for a look, oldest first.
func (s *Store) DueForServiceNow(ctx context.Context, now time.Time, limit int) ([]ServiceNowQueued, error) {
	rows, _ := s.Pool.Query(ctx, `select deployment_id, gen, attempts, last_error from servicenow_cmdb_queue
		where due_at <= $1 order by due_at, queued_at limit $2`, now, limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ServiceNowQueued])
}

// DoneForServiceNow takes a deployment off the queue, unless an event
// queued it again since it was read.
func (s *Store) DoneForServiceNow(ctx context.Context, q ServiceNowQueued) error {
	_, err := s.Pool.Exec(ctx, `delete from servicenow_cmdb_queue where deployment_id = $1 and gen = $2`, q.DeploymentID, q.Gen)
	return err
}

// RetryForServiceNow keeps a deployment queued until due, with why.
func (s *Store) RetryForServiceNow(ctx context.Context, deploymentID string, due time.Time, why string) error {
	_, err := s.Pool.Exec(ctx, `update servicenow_cmdb_queue set attempts = attempts + 1, due_at = $2, last_error = $3
		where deployment_id = $1`, deploymentID, due, why)
	return err
}

// ServiceNowVM is a VM as its CI is made from.
type ServiceNowVM struct {
	ResourceID     string          `db:"resource_id"`
	Name           string          `db:"name"`
	InstanceUUID   string          `db:"instance_uuid"`
	SyncState      string          `db:"sync_state"`
	Properties     json.RawMessage `db:"properties"`
	DeploymentID   string          `db:"deployment_id"`
	DeploymentName string          `db:"deployment_name"`
	ProjectID      string          `db:"project_id"`
	ProjectCode    string          `db:"project_code"`
	ProjectName    string          `db:"project_name"`
	OwnerName      string          `db:"owner_name"`
	OwnerEmail     string          `db:"owner_email"`
	VCenterUUID    string          `db:"vcenter_uuid"`
}

// ServiceNowVMs are a deployment's VMs, none once it is deleted.
func (s *Store) ServiceNowVMs(ctx context.Context, deploymentID string) ([]ServiceNowVM, error) {
	rows, _ := s.Pool.Query(ctx, `select r.id as resource_id, r.name, r.external_id as instance_uuid, r.sync_state, coalesce(r.properties->'vm', '{}') as properties,
			d.id as deployment_id, d.name as deployment_name, p.id as project_id, p.code as project_code, p.name as project_name,
			coalesce(u.name, '') as owner_name, coalesce(u.email, '') as owner_email, coalesce(v.instance_uuid, '') as vcenter_uuid
		from resources r join deployments d on d.id = r.deployment_id join projects p on p.id = d.project_id
			left join users u on u.id = d.owner_id
			left join inv_vcenters v on v.vcenter = r.properties->'vm'->>'vcenter'
		where r.deployment_id = $1 and r.type = 'vsphere.vm' and d.status <> 'deleted' and coalesce(r.external_id, '') <> ''
		order by r.name`, deploymentID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ServiceNowVM])
}

// ServiceNowCI is the CI rvav keeps for a VM.
type ServiceNowCI struct {
	InstanceUUID string            `db:"instance_uuid"`
	ResourceID   string            `db:"resource_id"`
	DeploymentID string            `db:"deployment_id"`
	SysID        string            `db:"sys_id"`
	Class        string            `db:"class"`
	Sent         map[string]string `db:"sent"`
	RetiredAt    *time.Time        `db:"retired_at"`
	SyncedAt     time.Time         `db:"synced_at"`
}

// ServiceNowCIs are the CIs rvav keeps for a deployment's VMs, retired or not.
func (s *Store) ServiceNowCIs(ctx context.Context, deploymentID string) ([]ServiceNowCI, error) {
	rows, _ := s.Pool.Query(ctx, `select instance_uuid, resource_id, deployment_id, sys_id, class, sent, retired_at, synced_at
		from servicenow_cis where deployment_id = $1`, deploymentID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ServiceNowCI])
}

// ServiceNowCIOf is the CI rvav keeps for a VM, by its instance UUID.
func (s *Store) ServiceNowCIOf(ctx context.Context, instanceUUID string) (ServiceNowCI, error) {
	rows, _ := s.Pool.Query(ctx, `select instance_uuid, resource_id, deployment_id, sys_id, class, sent, retired_at, synced_at
		from servicenow_cis where instance_uuid = $1`, instanceUUID)
	ci, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[ServiceNowCI])
	return ci, notFound(err)
}

// SaveServiceNowCI records the CI a VM has and what it was sent.
func (s *Store) SaveServiceNowCI(ctx context.Context, ci ServiceNowCI) error {
	sent, err := json.Marshal(ci.Sent)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `insert into servicenow_cis (instance_uuid, resource_id, deployment_id, sys_id, class, sent)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (instance_uuid) do update set resource_id = $2, deployment_id = $3, sys_id = $4, class = $5, sent = $6,
			retired_at = null, synced_at = now()`, ci.InstanceUUID, ci.ResourceID, ci.DeploymentID, ci.SysID, ci.Class, sent)
	return err
}

// RetireServiceNowCI records that a VM's CI is retired.
func (s *Store) RetireServiceNowCI(ctx context.Context, instanceUUID string, at time.Time) error {
	_, err := s.Pool.Exec(ctx, `update servicenow_cis set retired_at = $2, synced_at = $2 where instance_uuid = $1`, instanceUUID, at)
	return err
}

// VMAlive says whether a VM is in a deployment not deleted, whichever.
func (s *Store) VMAlive(ctx context.Context, instanceUUID string) (bool, error) {
	var alive bool
	err := s.Pool.QueryRow(ctx, `select exists (select 1 from resources r join deployments d on d.id = r.deployment_id
		where r.type = 'vsphere.vm' and r.external_id = $1 and d.status <> 'deleted')`, instanceUUID).Scan(&alive)
	return alive, err
}

// ServiceNowKept are the sys_ids of the CIs rvav keeps, not retired.
func (s *Store) ServiceNowKept(ctx context.Context) (map[string]bool, error) {
	rows, _ := s.Pool.Query(ctx, `select sys_id from servicenow_cis where retired_at is null`)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

// ServiceNowCounts are how many CIs rvav keeps and has retired, and how
// many deployments are queued and failing.
type ServiceNowCounts struct {
	Kept    int `db:"kept"`
	Retired int `db:"retired"`
	Queued  int `db:"queued"`
	Failing int `db:"failing"`
}

func (s *Store) ServiceNowCounts(ctx context.Context) (ServiceNowCounts, error) {
	rows, _ := s.Pool.Query(ctx, `select
		(select count(*) from servicenow_cis where retired_at is null) as kept,
		(select count(*) from servicenow_cis where retired_at is not null) as retired,
		(select count(*) from servicenow_cmdb_queue) as queued,
		(select count(*) from servicenow_cmdb_queue where attempts > 0) as failing`)
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[ServiceNowCounts])
}

// ServiceNowFailing is a deployment ServiceNow didn't take.
type ServiceNowFailing struct {
	DeploymentID   string    `db:"deployment_id"`
	DeploymentName *string   `db:"deployment_name"`
	Attempts       int       `db:"attempts"`
	LastError      string    `db:"last_error"`
	DueAt          time.Time `db:"due_at"`
}

// ServiceNowFailures are the deployments ServiceNow didn't take, the
// latest tried first.
func (s *Store) ServiceNowFailures(ctx context.Context, limit int) ([]ServiceNowFailing, error) {
	rows, _ := s.Pool.Query(ctx, `select q.deployment_id, d.name as deployment_name, q.attempts, coalesce(q.last_error, '') as last_error,
			q.due_at
		from servicenow_cmdb_queue q left join deployments d on d.id = q.deployment_id
		where q.attempts > 0 order by q.due_at desc limit $1`, limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ServiceNowFailing])
}

package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Administration of projects, members, group mappings and environments
// (ADR-0020).

// AdminProject is a project with what its administration shows.
type AdminProject struct {
	Project
	CreatedAt   time.Time `db:"created_at"`
	Members     int       `db:"members"`
	Deployments int       `db:"deployments"`
}

const adminProjectSelect = `select ` + projectCols + `, created_at,
	(select count(*) from (select m.user_id from project_members m where m.project_id = p.id
		union select g.user_id from user_groups g join group_mappings gm on gm.oidc_group = g.group_name
		where gm.project_id = p.id) x)::int as members,
	(select count(*) from deployments d where d.project_id = p.id and d.status <> 'deleted')::int as deployments
	from projects p`

// AdminProjects lists projects by name, archived ones too when asked.
func (s *Store) AdminProjects(ctx context.Context, search string, archived bool) ([]AdminProject, error) {
	rows, _ := s.Pool.Query(ctx, adminProjectSelect+`
		where ($1 = '' or p.name ilike '%' || $1 || '%' or p.code ilike '%' || $1 || '%')
		  and ($2 or p.archived_at is null)
		order by p.name, p.id`, search, archived)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AdminProject])
}

func (s *Store) GetAdminProject(ctx context.Context, idOrCode string) (AdminProject, error) {
	rows, _ := s.Pool.Query(ctx, adminProjectSelect+` where p.id = $1 or p.code = $1`, idOrCode)
	p, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[AdminProject])
	return p, notFound(err)
}

// CreateProject creates a project with its first project admins (ErrInUse
// when the code is taken).
func (s *Store) CreateProject(ctx context.Context, p Project, admins []string) error {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into projects (id, code, name, description, cost_center, environments)
			values ($1, $2, $3, $4, $5, $6)`, p.ID, p.Code, p.Name, p.Description, p.CostCenter, nonNilStrings(p.Environments)); err != nil {
			return err
		}
		for _, u := range admins {
			if _, err := tx.Exec(ctx, `insert into project_members (project_id, user_id, role) values ($1, $2, 'project-admin')
				on conflict do nothing`, p.ID, u); err != nil {
				return err
			}
		}
		return nil
	})
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	return err
}

// ProjectUpdate changes a project; nil fields stay as they are.
type ProjectUpdate struct {
	Name, Description, CostCenter *string
	Environments                  *[]string
	Archived                      *bool
}

func (s *Store) UpdateProject(ctx context.Context, id string, u ProjectUpdate) error {
	var envs []string
	if u.Environments != nil {
		envs = nonNilStrings(*u.Environments)
	}
	tag, err := s.Pool.Exec(ctx, `update projects set name = coalesce($2, name), description = coalesce($3, description),
		cost_center = coalesce($4, cost_center), environments = coalesce($5, environments),
		archived_at = case when $6::bool is null then archived_at when $6 then coalesce(archived_at, now()) end
		where id = $1`, id, u.Name, u.Description, u.CostCenter, envs, u.Archived)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// MemberRole is a user's role in a project, directly or through a group.
type MemberRole struct {
	User
	Role  string  `db:"role"`
	Group *string `db:"group_name"` // nil for a direct member
}

// ProjectMemberRoles lists a project's members: one row per direct
// membership and per group that gives them a role.
func (s *Store) ProjectMemberRoles(ctx context.Context, projectID string) ([]MemberRole, error) {
	rows, _ := s.Pool.Query(ctx, `select u.id, u.subject, u.name, u.email, u.title, u.global_roles, m.role, null::text as group_name
		from project_members m join users u on u.id = m.user_id where m.project_id = $1
		union all
		select u.id, u.subject, u.name, u.email, u.title, u.global_roles, gm.role, g.group_name
		from user_groups g join group_mappings gm on gm.oidc_group = g.group_name join users u on u.id = g.user_id
		where gm.project_id = $1
		order by 3, 1, 8 nulls first`, projectID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[MemberRole])
}

// SetMember adds a direct member or changes their role.
func (s *Store) SetMember(ctx context.Context, projectID, userID, role string) error {
	_, err := s.Pool.Exec(ctx, `insert into project_members (project_id, user_id, role) values ($1, $2, $3)
		on conflict (project_id, user_id) do update set role = excluded.role`, projectID, userID, role)
	return err
}

// DirectRole is a user's own role in a project ("" when none).
func (s *Store) DirectRole(ctx context.Context, projectID, userID string) (string, error) {
	var role string
	err := s.Pool.QueryRow(ctx, `select role from project_members where project_id = $1 and user_id = $2`, projectID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func (s *Store) RemoveMember(ctx context.Context, projectID, userID string) error {
	tag, err := s.Pool.Exec(ctx, `delete from project_members where project_id = $1 and user_id = $2`, projectID, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// OtherProjectAdmins counts the ways a project has a project admin besides
// this user's direct membership: other direct ones and group mappings.
func (s *Store) OtherProjectAdmins(ctx context.Context, projectID, userID string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select
		(select count(*) from project_members where project_id = $1 and role = 'project-admin' and user_id <> $2) +
		(select count(*) from group_mappings where project_id = $1 and role = 'project-admin')`, projectID, userID).Scan(&n)
	return n, err
}

// GroupMapping maps an OIDC group to a role, in a project or globally.
type GroupMapping struct {
	ID          string  `db:"id"`
	Group       string  `db:"oidc_group"`
	ProjectID   *string `db:"project_id"`
	ProjectCode *string `db:"project_code"`
	ProjectName *string `db:"project_name"`
	Role        string  `db:"role"`
	Users       int     `db:"users"`
}

const groupMappingSelect = `select gm.id, gm.oidc_group, gm.project_id, p.code as project_code, p.name as project_name, gm.role,
	(select count(*) from user_groups g where g.group_name = gm.oidc_group)::int as users
	from group_mappings gm left join projects p on p.id = gm.project_id`

func (s *Store) GroupMappings(ctx context.Context) ([]GroupMapping, error) {
	rows, _ := s.Pool.Query(ctx, groupMappingSelect+` order by gm.oidc_group, p.name nulls first, gm.role`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[GroupMapping])
}

func (s *Store) GetGroupMapping(ctx context.Context, id string) (GroupMapping, error) {
	rows, _ := s.Pool.Query(ctx, groupMappingSelect+` where gm.id = $1`, id)
	m, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[GroupMapping])
	return m, notFound(err)
}

// CreateGroupMapping adds a mapping (ErrInUse when it exists).
func (s *Store) CreateGroupMapping(ctx context.Context, id, group string, projectID *string, role string) error {
	_, err := s.Pool.Exec(ctx, `insert into group_mappings (id, oidc_group, project_id, role) values ($1, $2, $3, $4)`,
		id, group, projectID, role)
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	return err
}

func (s *Store) DeleteGroupMapping(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from group_mappings where id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// AdminSources counts the ways anyone is an administrator, leaving out one
// group mapping: users with the role themselves and other admin mappings.
func (s *Store) AdminSources(ctx context.Context, exceptMapping string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select
		(select count(*) from users where 'admin' = any(global_roles)) +
		(select count(*) from group_mappings where role = 'admin' and project_id is null and id <> $1)`, exceptMapping).Scan(&n)
	return n, err
}

// AdminEnvironment is an environment with what uses it.
type AdminEnvironment struct {
	Environment
	Projects    int `db:"projects"`
	Deployments int `db:"deployments"`
}

func (s *Store) AdminEnvironments(ctx context.Context) ([]AdminEnvironment, error) {
	rows, _ := s.Pool.Query(ctx, `select e.key, e.name, e.production, e.sort_order, e.snapshot_max_count, e.snapshot_max_age_days,
		(select count(*) from projects p where e.key = any(p.environments) and p.archived_at is null)::int as projects,
		(select count(*) from deployments d where d.environment = e.key and d.status <> 'deleted')::int as deployments
		from environments e order by e.sort_order, e.key`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AdminEnvironment])
}

// CreateEnvironment adds an environment (ErrInUse when the key exists).
func (s *Store) CreateEnvironment(ctx context.Context, e Environment) error {
	_, err := s.Pool.Exec(ctx, `insert into environments (key, name, production, sort_order, snapshot_max_count, snapshot_max_age_days)
		values ($1, $2, $3, $4, $5, $6)`, e.Key, e.Name, e.Production, e.SortOrder, e.SnapshotMaxCount, e.SnapshotMaxAgeDays)
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	return err
}

// EnvironmentUpdate changes an environment; nil fields stay as they are,
// and ClearMaxAge removes the snapshot age limit.
type EnvironmentUpdate struct {
	Name                *string
	Production          *bool
	SortOrder           *int
	SnapshotMaxCount    *int
	SnapshotMaxAgeDays  *int
	ClearSnapshotMaxAge bool
}

func (s *Store) UpdateEnvironment(ctx context.Context, key string, u EnvironmentUpdate) error {
	tag, err := s.Pool.Exec(ctx, `update environments set name = coalesce($2, name), production = coalesce($3, production),
		sort_order = coalesce($4, sort_order), snapshot_max_count = coalesce($5, snapshot_max_count),
		snapshot_max_age_days = case when $7 then null else coalesce($6, snapshot_max_age_days) end
		where key = $1`, key, u.Name, u.Production, u.SortOrder, u.SnapshotMaxCount, u.SnapshotMaxAgeDays, u.ClearSnapshotMaxAge)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteEnvironment removes an environment nothing uses (ErrInUse
// otherwise), with its own lease policy.
func (s *Store) DeleteEnvironment(ctx context.Context, key string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		var used bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from projects where $1 = any(environments))
			or exists (select 1 from placement_rules where environment = $1)
			or exists (select 1 from requests where environment = $1)
			or exists (select 1 from deployments where environment = $1)
			or exists (select 1 from quotas where environment = $1)
			or exists (select 1 from lease_policies where environment = $1 and project_id is not null)`, key).Scan(&used); err != nil {
			return err
		}
		if used {
			return ErrInUse
		}
		if _, err := tx.Exec(ctx, `delete from lease_policies where environment = $1`, key); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `delete from environments where key = $1`, key)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// SearchUsers finds users by name or email, by name.
func (s *Store) SearchUsers(ctx context.Context, q string, limit int) ([]User, error) {
	rows, _ := s.Pool.Query(ctx, `select id, subject, name, email, title, global_roles from users
		where $1 = '' or name ilike '%' || $1 || '%' or email ilike '%' || $1 || '%'
		order by name, id limit $2`, q, limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[User])
}

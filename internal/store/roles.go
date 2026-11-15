package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// StoredRole is a role as the roles table lists it: a built-in one's
// permissions are code (ADR-0034).
type StoredRole struct {
	Key         string    `db:"key"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	Permissions []string  `db:"permissions"`
	BuiltIn     bool      `db:"built_in"`
	Holders     int       `db:"holders"`
	Policies    []string  `db:"policies"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

const roleSelect = `select r.key, r.name, r.description, r.permissions, r.built_in,
	((select count(*) from project_members m where m.role = r.key) + (select count(*) from user_roles a where a.role = r.key) +
	 (select count(*) from group_mappings g where g.role = r.key) + (select count(*) from users u where r.key = any(u.global_roles)))::int as holders,
	array(select p.name from day2_policies p where r.key = any(p.roles) order by p.priority, p.name) as policies,
	r.created_at, r.updated_at from roles r`

func (s *Store) Roles(ctx context.Context) ([]StoredRole, error) {
	rows, _ := s.Pool.Query(ctx, roleSelect+` order by not r.built_in, r.name, r.key`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[StoredRole])
}

func (s *Store) GetRole(ctx context.Context, key string) (StoredRole, error) {
	rows, _ := s.Pool.Query(ctx, roleSelect+` where r.key = $1`, key)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[StoredRole])
	return r, notFound(err)
}

// CreateRole adds a custom role (ErrInUse when the key is taken).
func (s *Store) CreateRole(ctx context.Context, r StoredRole) error {
	_, err := s.Pool.Exec(ctx, `insert into roles (key, name, description, permissions) values ($1, $2, $3, $4)`,
		r.Key, r.Name, r.Description, r.Permissions)
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	return err
}

// UpdateRole changes a custom role; nil fields stay as they are.
func (s *Store) UpdateRole(ctx context.Context, key string, name, description *string, permissions []string) error {
	tag, err := s.Pool.Exec(ctx, `update roles set name = coalesce($2, name), description = coalesce($3, description),
		permissions = coalesce($4, permissions), updated_at = now() where key = $1 and not built_in`, key, name, description, permissions)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteRole removes a custom role nobody holds (ErrInUse otherwise).
func (s *Store) DeleteRole(ctx context.Context, key string) error {
	tag, err := s.Pool.Exec(ctx, `delete from roles where key = $1 and not built_in`, key)
	if IsForeignKeyViolation(err) {
		return ErrInUse
	}
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// CustomRolesWith are the keys of the custom roles holding a permission.
func (s *Store) CustomRolesWith(ctx context.Context, permission string) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select key from roles where not built_in and $1 = any(permissions) order by key`, permission)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// RoleProjects are the projects where a role is given, to users or groups.
func (s *Store) RoleProjects(ctx context.Context, key string) ([]Project, error) {
	rows, _ := s.Pool.Query(ctx, `select `+projectCols+` from projects where id in
		(select project_id from project_members where role = $1 union select project_id from group_mappings where role = $1)
		order by name`, key)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Project])
}

// RvavHolders counts the ways anyone holds one of these roles for all of
// rvav, leaving out an assignment and a group mapping: rvav's own
// (users.global_roles), those given and the groups'.
func (s *Store) RvavHolders(ctx context.Context, roles []string, exceptAssignment, exceptMapping string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select
		(select count(*) from users where global_roles && $1) +
		(select count(*) from user_roles where role = any($1) and id <> $2) +
		(select count(*) from group_mappings where role = any($1) and project_id is null and id <> $3)`,
		roles, exceptAssignment, exceptMapping).Scan(&n)
	return n, err
}

// ProjectHolders counts the ways a project has one of these roles given in
// it, leaving out a user's own and a group mapping.
func (s *Store) ProjectHolders(ctx context.Context, projectID string, roles []string, exceptUser, exceptMapping string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select
		(select count(*) from project_members where project_id = $1 and role = any($2) and user_id <> $3) +
		(select count(*) from group_mappings where project_id = $1 and role = any($2) and id <> $4)`,
		projectID, roles, exceptUser, exceptMapping).Scan(&n)
	return n, err
}

// RoleAssignment is a role a user holds for all of rvav: given (with an
// ID), or set by rvav itself (Fixed).
type RoleAssignment struct {
	ID        *string    `db:"id"`
	UserID    string     `db:"user_id"`
	UserName  string     `db:"user_name"`
	UserEmail string     `db:"user_email"`
	Role      string     `db:"role"`
	RoleName  string     `db:"role_name"`
	Fixed     bool       `db:"fixed"`
	CreatedAt *time.Time `db:"created_at"`
}

// RoleAssignments lists the roles users hold for all of rvav, but user,
// which everyone holds; role and userID filter when set.
func (s *Store) RoleAssignments(ctx context.Context, role, userID string) ([]RoleAssignment, error) {
	rows, _ := s.Pool.Query(ctx, `select a.id, a.user_id, u.name as user_name, u.email as user_email, a.role,
			coalesce(r.name, a.role) as role_name, a.fixed, a.created_at
		from (select id, user_id, role, false as fixed, created_at from user_roles
			union all select null, id, unnest(global_roles), true, null from users) a
		join users u on u.id = a.user_id left join roles r on r.key = a.role
		where a.role <> 'user' and ($1 = '' or a.role = $1) and ($2 = '' or a.user_id = $2)
		order by u.name, a.user_id, a.fixed desc, role_name`, role, userID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[RoleAssignment])
}

func (s *Store) GetRoleAssignment(ctx context.Context, id string) (RoleAssignment, error) {
	rows, _ := s.Pool.Query(ctx, `select a.id, a.user_id, u.name as user_name, u.email as user_email, a.role, r.name as role_name,
		false as fixed, a.created_at from user_roles a join users u on u.id = a.user_id join roles r on r.key = a.role where a.id = $1`, id)
	a, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[RoleAssignment])
	return a, notFound(err)
}

// CreateRoleAssignment gives a user a role for all of rvav (ErrInUse when
// they hold it already, given or set by rvav).
func (s *Store) CreateRoleAssignment(ctx context.Context, id, userID, role string) error {
	tag, err := s.Pool.Exec(ctx, `insert into user_roles (id, user_id, role)
		select $1, $2, $3 where not exists (select 1 from users where id = $2 and $3 = any(global_roles))`, id, userID, role)
	if IsUniqueViolation(err) || err == nil && tag.RowsAffected() == 0 {
		return ErrInUse
	}
	return err
}

func (s *Store) DeleteRoleAssignment(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from user_roles where id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

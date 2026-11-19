package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/ids"
)

const userCols = `id, subject, name, email, title, global_roles`

func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	rows, _ := s.Pool.Query(ctx, `select `+userCols+` from users where id = $1`, id)
	u, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	return u, notFound(err)
}

func (s *Store) GetUserBySubject(ctx context.Context, subject string) (User, error) {
	rows, _ := s.Pool.Query(ctx, `select `+userCols+` from users where subject = $1`, subject)
	u, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	return u, notFound(err)
}

// UpsertOIDCUser finds the user for an OIDC subject, linking a pre-created
// user by email on first login, or creates one. Name, email and manager
// follow the identity provider: a token without a manager clears it.
func (s *Store) UpsertOIDCUser(ctx context.Context, subject, name, email, title, manager string) (User, error) {
	var u User
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `select `+userCols+` from users where subject = $1 for update`, subject)
		found, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
		if err == pgx.ErrNoRows && email != "" {
			rows, _ = tx.Query(ctx, `select `+userCols+` from users
				where lower(email) = lower($1) and subject like 'seed:%' for update`, email)
			found, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
		}
		switch {
		case err == pgx.ErrNoRows:
			rows, _ = tx.Query(ctx, `insert into users (id, subject, name, email, title, manager, last_seen_at)
				values ($1, $2, $3, $4, $5, $6, now()) returning `+userCols,
				ids.New(ids.User), subject, name, email, title, manager)
			u, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
			return err
		case err != nil:
			return err
		}
		if title == "" {
			title = found.Title
		}
		rows, _ = tx.Query(ctx, `update users set subject = $2, name = $3, email = $4, title = $5, manager = $6,
			last_seen_at = now() where id = $1 returning `+userCols, found.ID, subject, name, email, title, manager)
		u, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
		return err
	})
	return u, err
}

// managerOf joins the user m that u's manager claim names, by email,
// subject or ID: never u, nor one of rvav's own system users.
const managerOf = `u.manager <> '' and m.id <> u.id and m.subject not like 'system:%'
	and (lower(m.email) = lower(u.manager) or m.subject = u.manager or m.id = u.manager)`

// Manager returns the user's manager claim as stored at their last sign-in
// and the user it names, "" when it names nobody rvav knows.
func (s *Store) Manager(ctx context.Context, userID string) (claim, managerID string, err error) {
	err = s.Pool.QueryRow(ctx, `select u.manager, coalesce((select m.id from users m where `+managerOf+`
		order by m.last_seen_at desc nulls last, m.id limit 1), '') from users u where u.id = $1`, userID).Scan(&claim, &managerID)
	return claim, managerID, notFound(err)
}

// RoleGrant is a role a user holds, themselves or through a group: in a
// project, or for all of rvav (ProjectID ""). Custom says it is no built-in
// role, whose name and permissions are stored; Known is false for a key
// that is no role. Source says where it comes from: GrantRvav, GrantUser or
// GrantGroup, then Group names the group.
type RoleGrant struct {
	Role        string   `db:"role"`
	ProjectID   string   `db:"project_id"`
	Source      string   `db:"source"`
	Group       string   `db:"oidc_group"`
	Known       bool     `db:"known"`
	Custom      bool     `db:"custom"`
	Name        string   `db:"name"`
	Permissions []string `db:"permissions"`
}

// Where a role a user holds comes from.
const (
	GrantRvav  = "rvav"
	GrantUser  = "user"
	GrantGroup = "group"
)

// RoleGrants lists in one query the roles a user holds (ADR-0034): those
// rvav sets (global, from users.global_roles), those given to them for all
// of rvav and in projects, then their groups' (none for nil), with the
// custom roles' permissions.
func (s *Store) RoleGrants(ctx context.Context, userID string, global, groups []string) ([]RoleGrant, error) {
	rows, _ := s.Pool.Query(ctx, `with held (role, project_id, src, n, source, oidc_group) as (
			select g.role, '', 1, g.n, 'rvav', '' from unnest($2::text[]) with ordinality as g(role, n)
			union all select role, '', 2, 0, 'user', '' from user_roles where user_id = $1
			union all select role, project_id, 3, 0, 'user', '' from project_members where user_id = $1
			union all select role, coalesce(project_id, ''), 4, 0, 'group', oidc_group from group_mappings where oidc_group = any($3))
		select h.role, h.project_id, h.source, h.oidc_group, r.key is not null as known, coalesce(not r.built_in, false) as custom,
			coalesce(r.name, '') as name, coalesce(r.permissions, '{}') as permissions
		from held h left join roles r on r.key = h.role
		order by h.src, h.n, h.role, h.project_id, h.oidc_group`, userID, global, groups)
	return pgx.CollectRows(rows, pgx.RowToStructByName[RoleGrant])
}

// CreateAPIToken stores a token by the SHA-256 hash of its value. A scope
// limits what it may do ("": what its user may).
func (s *Store) CreateAPIToken(ctx context.Context, id, userID, name, hash, scope string, expires *time.Time) error {
	_, err := s.Pool.Exec(ctx, `insert into api_tokens (id, user_id, name, token_hash, scope, expires_at) values ($1, $2, $3, $4, $5, $6)`,
		id, userID, name, hash, scope, expires)
	return err
}

// DeleteAPITokens revokes a user's tokens of this name.
func (s *Store) DeleteAPITokens(ctx context.Context, userID, name string) (int, error) {
	tag, err := s.Pool.Exec(ctx, `delete from api_tokens where user_id = $1 and name = $2`, userID, name)
	return int(tag.RowsAffected()), err
}

// TokenUse is an API token presented, as UseToken found it.
type TokenUse struct {
	ID, UserID, Name, Scope string
	Expired                 bool
	// FirstToday: not used before today (UTC), or, expired, not refused
	// before today; the audit log records it then (ADR-0058).
	FirstToday bool
}

// UseToken finds an API token by its SHA-256 hash and marks it used now,
// or refused when it has expired: ErrNotFound when there is none.
func (s *Store) UseToken(ctx context.Context, db DB, hash string) (TokenUse, error) {
	var t TokenUse
	// for update: of two requests at once, the second sees the first's mark.
	err := db.QueryRow(ctx, `with t as (select id, coalesce(expires_at <= now(), false) as expired,
			last_used_at, last_refused_at from api_tokens where token_hash = $1 for update)
		update api_tokens a set last_used_at = case when t.expired then a.last_used_at else now() end,
			last_refused_at = case when t.expired then now() else a.last_refused_at end
		from t where a.id = t.id
		returning a.id, a.user_id, a.name, a.scope, t.expired,
			coalesce(case when t.expired then t.last_refused_at else t.last_used_at end
				< date_trunc('day', now() at time zone 'UTC') at time zone 'UTC', true)`, hash).
		Scan(&t.ID, &t.UserID, &t.Name, &t.Scope, &t.Expired, &t.FirstToday)
	return t, notFound(err)
}

// FindUsers returns users by ID; missing IDs are skipped.
// UserIDsByLogin finds users by ID or by email, in any case: the IDs of
// those found, by the login given.
func (s *Store) UserIDsByLogin(ctx context.Context, logins []string) (map[string]string, error) {
	rows, _ := s.Pool.Query(ctx, `select distinct on (x.login) x.login, u.id from unnest($1::text[]) x(login)
		join users u on u.id = x.login or lower(u.email) = lower(x.login)
		where x.login <> '' order by x.login, u.id = x.login desc, u.id`, logins)
	out := map[string]string{}
	var login, id string
	_, err := pgx.ForEachRow(rows, []any{&login, &id}, func() error { out[login] = id; return nil })
	return out, err
}

func (s *Store) FindUsers(ctx context.Context, idList []string) (map[string]User, error) {
	rows, _ := s.Pool.Query(ctx, `select `+userCols+` from users where id = any($1)`, idList)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[User])
	if err != nil {
		return nil, err
	}
	out := make(map[string]User, len(list))
	for _, u := range list {
		out[u.ID] = u
	}
	return out, nil
}

// SearchText turns a free-text query into an ILIKE pattern.
func SearchText(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// EnsureSystemUser makes a user rvav acts as itself, for a token it makes
// (ADR-0030): no one signs in as it, and its roles are its own.
func (s *Store) EnsureSystemUser(ctx context.Context, id, subject, name string, roles []string) error {
	_, err := s.Pool.Exec(ctx, `insert into users (id, subject, name, global_roles) values ($1, $2, $3, $4)
		on conflict (id) do nothing`, id, subject, name, roles)
	return err
}

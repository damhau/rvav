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
// user by email on first login, or creates one. Name and email follow the
// identity provider.
func (s *Store) UpsertOIDCUser(ctx context.Context, subject, name, email, title string) (User, error) {
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
			rows, _ = tx.Query(ctx, `insert into users (id, subject, name, email, title, last_seen_at)
				values ($1, $2, $3, $4, $5, now()) returning `+userCols,
				ids.New(ids.User), subject, name, email, title)
			u, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
			return err
		case err != nil:
			return err
		}
		if title == "" {
			title = found.Title
		}
		rows, _ = tx.Query(ctx, `update users set subject = $2, name = $3, email = $4, title = $5, last_seen_at = now()
			where id = $1 returning `+userCols, found.ID, subject, name, email, title)
		u, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
		return err
	})
	return u, err
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

// UserForToken returns the owner of an unexpired API token, by its SHA-256
// hash, and the token's scope.
func (s *Store) UserForToken(ctx context.Context, hash string) (User, string, error) {
	var userID, scope string
	err := s.Pool.QueryRow(ctx, `update api_tokens set last_used_at = now()
		where token_hash = $1 and (expires_at is null or expires_at > now())
		returning user_id, scope`, hash).Scan(&userID, &scope)
	if err != nil {
		return User{}, "", notFound(err)
	}
	u, err := s.GetUser(ctx, userID)
	return u, scope, err
}

// FindUsers returns users by ID; missing IDs are skipped.
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

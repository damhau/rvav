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
			rows, _ = tx.Query(ctx, `insert into users (id, subject, name, email, title)
				values ($1, $2, $3, $4, $5) returning `+userCols,
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

func (s *Store) Memberships(ctx context.Context, userID string) ([]Membership, error) {
	rows, _ := s.Pool.Query(ctx, `select project_id, role from project_members where user_id = $1`, userID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Membership])
}

// GroupRoles maps OIDC groups to global roles and project roles.
func (s *Store) GroupRoles(ctx context.Context, groups []string) (global []string, projects []Membership, err error) {
	if len(groups) == 0 {
		return nil, nil, nil
	}
	rows, err := s.Pool.Query(ctx, `select coalesce(project_id, ''), role from group_mappings where oidc_group = any($1)`, groups)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var project, role string
		if err := rows.Scan(&project, &role); err != nil {
			return nil, nil, err
		}
		if project == "" {
			global = append(global, role)
		} else {
			projects = append(projects, Membership{ProjectID: project, Role: role})
		}
	}
	return global, projects, rows.Err()
}

// CreateAPIToken stores a token by the SHA-256 hash of its value.
func (s *Store) CreateAPIToken(ctx context.Context, id, userID, name, hash string, expires *time.Time) error {
	_, err := s.Pool.Exec(ctx, `insert into api_tokens (id, user_id, name, token_hash, expires_at) values ($1, $2, $3, $4, $5)`,
		id, userID, name, hash, expires)
	return err
}

// UserForToken returns the owner of an unexpired API token, by its SHA-256 hash.
func (s *Store) UserForToken(ctx context.Context, hash string) (User, error) {
	rows, _ := s.Pool.Query(ctx, `update api_tokens set last_used_at = now()
		where token_hash = $1 and (expires_at is null or expires_at > now())
		returning user_id`, hash)
	userID, err := pgx.CollectExactlyOneRow(rows, pgx.RowTo[string])
	if err != nil {
		return User{}, notFound(err)
	}
	return s.GetUser(ctx, userID)
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

package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// API tokens and the audit log, for their pages (ADR-0020).

type APIToken struct {
	ID         string     `db:"id"`
	UserID     string     `db:"user_id"`
	UserName   string     `db:"user_name"`
	Name       string     `db:"name"`
	Scope      string     `db:"scope"`
	CreatedAt  time.Time  `db:"created_at"`
	ExpiresAt  *time.Time `db:"expires_at"`
	LastUsedAt *time.Time `db:"last_used_at"`
}

const tokenSelect = `select t.id, t.user_id, u.name as user_name, t.name, t.scope, t.created_at, t.expires_at, t.last_used_at
	from api_tokens t join users u on u.id = t.user_id`

// APITokens lists a user's tokens ("" for everyone's), newest first.
func (s *Store) APITokens(ctx context.Context, userID string) ([]APIToken, error) {
	rows, _ := s.Pool.Query(ctx, tokenSelect+` where $1 = '' or t.user_id = $1 order by t.created_at desc, t.id`, userID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[APIToken])
}

func (s *Store) GetAPIToken(ctx context.Context, id string) (APIToken, error) {
	rows, _ := s.Pool.Query(ctx, tokenSelect+` where t.id = $1`, id)
	t, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[APIToken])
	return t, notFound(err)
}

func (s *Store) RevokeAPIToken(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `delete from api_tokens where id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// AuditRow is an audit event with its actor's and project's names.
type AuditRow struct {
	ID          int64           `db:"id"`
	At          time.Time       `db:"at"`
	ActorID     *string         `db:"actor_id"`
	ActorName   *string         `db:"actor_name"`
	Action      string          `db:"action"`
	TargetType  string          `db:"target_type"`
	TargetID    string          `db:"target_id"`
	ProjectID   *string         `db:"project_id"`
	ProjectCode *string         `db:"project_code"`
	ProjectName *string         `db:"project_name"`
	Detail      json.RawMessage `db:"detail"`
}

// AuditFilter narrows the audit log. Projects, when not nil, limits it to
// those projects' events (a project admin's view).
type AuditFilter struct {
	Search, ActorID, Action, ProjectID string
	Projects                           []string
	From, To                           *time.Time
	Before                             int64 // events older than this ID (0: from the newest)
	Limit                              int
}

// AuditEvents lists events newest first. An action ending in "." is a
// prefix (catalog.).
func (s *Store) AuditEvents(ctx context.Context, f AuditFilter) ([]AuditRow, error) {
	rows, _ := s.Pool.Query(ctx, `select a.id, a.at, a.actor_id, u.name as actor_name, a.action, a.target_type, a.target_id,
		a.project_id, p.code as project_code, p.name as project_name, a.detail
		from audit_events a left join users u on u.id = a.actor_id left join projects p on p.id = a.project_id
		where ($1 = '' or a.action ilike '%' || $1 || '%' or a.target_id ilike '%' || $1 || '%' or u.name ilike '%' || $1 || '%'
		       or a.detail::text ilike '%' || $1 || '%')
		  and ($2 = '' or a.actor_id = $2)
		  and ($3 = '' or a.action = $3 or (right($3, 1) = '.' and starts_with(a.action, $3)))
		  and ($4 = '' or a.project_id = $4)
		  and ($5::text[] is null or a.project_id = any($5))
		  and ($6::timestamptz is null or a.at >= $6) and ($7::timestamptz is null or a.at < $7)
		  and ($8 = 0 or a.id < $8)
		order by a.id desc limit $9`,
		f.Search, f.ActorID, f.Action, f.ProjectID, f.Projects, f.From, f.To, f.Before, f.Limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[AuditRow])
}

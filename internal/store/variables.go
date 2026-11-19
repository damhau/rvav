package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Variable is a shared variable (ADR-0057): composite steps read it as
// vars.<group>.<name>.
type Variable struct {
	Group         string          `db:"group_key"`
	Name          string          `db:"name"`
	Value         json.RawMessage `db:"value"`
	Description   string          `db:"description"`
	CreatedAt     time.Time       `db:"created_at"`
	UpdatedAt     time.Time       `db:"updated_at"`
	UpdatedBy     *string         `db:"updated_by"`
	UpdatedByName *string         `db:"updated_by_name"`
}

const variableCols = `v.group_key, v.name, v.value, v.description, v.created_at, v.updated_at, v.updated_by,
	u.name as updated_by_name`

const variableFrom = ` from variables v left join users u on u.id = v.updated_by`

func (s *Store) Variables(ctx context.Context) ([]Variable, error) {
	rows, _ := s.Pool.Query(ctx, `select `+variableCols+variableFrom+` order by v.group_key, v.name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Variable])
}

func (s *Store) GetVariable(ctx context.Context, group, name string) (Variable, error) {
	rows, _ := s.Pool.Query(ctx, `select `+variableCols+variableFrom+` where v.group_key = $1 and v.name = $2`, group, name)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Variable])
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// AddVariable adds one, ErrInUse when its group already has the name.
func (s *Store) AddVariable(ctx context.Context, v Variable, by string) error {
	_, err := s.Pool.Exec(ctx, `insert into variables (group_key, name, value, description, updated_by) values ($1, $2, $3, $4, $5)`,
		v.Group, v.Name, v.Value, v.Description, by)
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	return err
}

// SetVariable changes one's value and description.
func (s *Store) SetVariable(ctx context.Context, v Variable, by string) error {
	tag, err := s.Pool.Exec(ctx, `update variables set value = $3, description = $4, updated_by = $5, updated_at = now()
		where group_key = $1 and name = $2`, v.Group, v.Name, v.Value, v.Description, by)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) DeleteVariable(ctx context.Context, group, name string) error {
	tag, err := s.Pool.Exec(ctx, `delete from variables where group_key = $1 and name = $2`, group, name)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// VariableValues are the values by group and name, as steps read them.
func (s *Store) VariableValues(ctx context.Context) (map[string]any, error) {
	rows, err := s.Pool.Query(ctx, `select group_key, name, value from variables`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var group, name string
		var raw json.RawMessage
		if err := rows.Scan(&group, &name, &raw); err != nil {
			return nil, err
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		g, _ := out[group].(map[string]any)
		if g == nil {
			g = map[string]any{}
			out[group] = g
		}
		g[name] = v
	}
	return out, rows.Err()
}

// VariableUsers lists, for each variable as group.name, the items whose
// draft or published version reads it.
func (s *Store) VariableUsers(ctx context.Context) (map[string][]string, error) {
	rows, err := s.Pool.Query(ctx, `select x.group_key || '.' || x.name,
			array(select distinct 'item ' || i.slug from catalog_item_versions v join catalog_items i on i.id = v.item_id
				where v.state in ('draft', 'published')
					and v.blueprint::text ~ ('\mvars\.' || x.group_key || '\.' || x.name || '([^a-z0-9_]|$)')
				order by 1)
		from variables x`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var key string
		var users []string
		if err := rows.Scan(&key, &users); err != nil {
			return nil, err
		}
		out[key] = users
	}
	return out, rows.Err()
}

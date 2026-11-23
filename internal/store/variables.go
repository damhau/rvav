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
	// File is the text file it holds, if it holds one: its value is then
	// the file's record (E2.1).
	FileName   *string `db:"file_name"`
	FileType   *string `db:"file_type"`
	FileSize   *int64  `db:"file_size"`
	FileSHA256 *string `db:"file_sha256"`
}

// HoldsFile says whether it holds a file rather than a value.
func (v Variable) HoldsFile() bool { return v.FileName != nil }

const variableCols = `v.group_key, v.name, v.value, v.description, v.created_at, v.updated_at, v.updated_by,
	u.name as updated_by_name, v.file_name, v.file_type, v.file_size, v.file_sha256`

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

// SetVariable changes the value and description of one that holds a value.
func (s *Store) SetVariable(ctx context.Context, v Variable, by string) error {
	tag, err := s.Pool.Exec(ctx, `update variables set value = $3, description = $4, updated_by = $5, updated_at = now()
		where group_key = $1 and name = $2 and file_name is null`, v.Group, v.Name, v.Value, v.Description, by)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// AddVariableFile adds one that holds a file, ErrInUse when its group
// already has the name.
func (s *Store) AddVariableFile(ctx context.Context, v Variable, content []byte, by string) error {
	_, err := s.Pool.Exec(ctx, `insert into variables (group_key, name, value, description, updated_by,
			file_name, file_type, file_size, file_sha256, file_content)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		v.Group, v.Name, v.Value, v.Description, by, v.FileName, v.FileType, v.FileSize, v.FileSHA256, content)
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	return err
}

// SetVariableFile replaces the file of one that holds a file, and its
// description; a nil content keeps the file.
func (s *Store) SetVariableFile(ctx context.Context, v Variable, content []byte, by string) error {
	tag, err := s.Pool.Exec(ctx, `update variables set description = $3, updated_by = $4, updated_at = now(),
			value = case when $10::bytea is null then value else $5 end,
			file_name = case when $10::bytea is null then file_name else $6 end,
			file_type = case when $10::bytea is null then file_type else $7 end,
			file_size = case when $10::bytea is null then file_size else $8 end,
			file_sha256 = case when $10::bytea is null then file_sha256 else $9 end,
			file_content = coalesce($10::bytea, file_content)
		where group_key = $1 and name = $2 and file_name is not null`,
		v.Group, v.Name, v.Description, by, v.Value, v.FileName, v.FileType, v.FileSize, v.FileSHA256, content)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// VariableFileContent is the content of the file one holds, ErrNotFound
// when it holds none.
func (s *Store) VariableFileContent(ctx context.Context, group, name string) ([]byte, error) {
	var b []byte
	err := s.Pool.QueryRow(ctx, `select file_content from variables where group_key = $1 and name = $2 and file_content is not null`,
		group, name).Scan(&b)
	return b, notFound(err)
}

// VariableFileTexts are the contents of the files that those of keys
// (group.name) which hold one hold, by key.
func (s *Store) VariableFileTexts(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `select group_key || '.' || name, file_content from variables
		where group_key || '.' || name = any($1) and file_content is not null`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var b []byte
		if err := rows.Scan(&key, &b); err != nil {
			return nil, err
		}
		out[key] = string(b)
	}
	return out, rows.Err()
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
// draft or published version reads it, the VM templates whose user data
// reads its file, and the requests still to run or running whose
// deprecated version does. A failed one isn't counted: it would keep the
// variable for ever, and its Retry fails on the step that reads one
// removed. A blueprint reads one as vars.<group>.<name> in its steps or as
// {{ file "<group>.<name>" }} in its user data, which JSON writes with its
// quotes escaped.
func (s *Store) VariableUsers(ctx context.Context) (map[string][]string, error) {
	rows, err := s.Pool.Query(ctx, `with unfinished as (
			select version_id, number from requests where status in ('pending_approval', 'scheduled', 'queued', 'running', 'cancelling')),
		reads as (
			select x.group_key || '.' || x.name as key, v.id, v.state, i.slug
			from variables x, catalog_item_versions v join catalog_items i on i.id = v.item_id
			where (v.state in ('draft', 'published') or exists (select from unfinished q where q.version_id = v.id))
				and (v.blueprint::text ~ ('\mvars\.' || x.group_key || '\.' || x.name || '([^a-z0-9_]|$)')
					or x.file_name is not null and v.blueprint::text ~ ('\mfile\s+\\"' || x.group_key || '\.' || x.name || '\\"')))
		select x.group_key || '.' || x.name, array(select u from (
				select 'item ' || r.slug as u from reads r
				where r.key = x.group_key || '.' || x.name and r.state in ('draft', 'published')
				union
				select 'template ' || t.key from vm_templates t
				where x.file_name is not null and t.user_data ~ ('\mfile\s+"' || x.group_key || '\.' || x.name || '"')
				union
				select 'request ' || q.number from reads r join unfinished q on q.version_id = r.id
				where r.key = x.group_key || '.' || x.name and r.state = 'deprecated'
			) users order by u)
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

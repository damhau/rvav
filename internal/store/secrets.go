package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Secret is a stored secret; Nonce and Ciphertext hold its value, sealed
// by the secrets package. ProjectID is set for a project's own secret
// (ADR-0038), nil for a global one.
type Secret struct {
	Name        string    `db:"name"`
	ProjectID   *string   `db:"project_id"`
	Kind        string    `db:"kind"`
	Description string    `db:"description"`
	Username    string    `db:"username"`
	Header      string    `db:"header"`
	KeyID       string    `db:"key_id"`
	Nonce       []byte    `db:"nonce"`
	Ciphertext  []byte    `db:"ciphertext"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

const secretCols = `name, project_id, kind, description, username, header, key_id, nonce, ciphertext, created_at, updated_at`

// SealName is the name a secret's value is sealed under: its own for a
// global secret, project:<id>:<name> for a project's.
func (v Secret) SealName() string {
	if v.ProjectID != nil {
		return "project:" + *v.ProjectID + ":" + v.Name
	}
	return v.Name
}

// Secrets lists every stored secret, a global one before the projects' of
// the same name.
func (s *Store) Secrets(ctx context.Context) ([]Secret, error) {
	rows, _ := s.Pool.Query(ctx, `select `+secretCols+` from secrets order by name, project_id nulls first`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Secret])
}

// GetSecret gives the global secret of this name.
func (s *Store) GetSecret(ctx context.Context, name string) (Secret, error) {
	return s.ScopedSecret(ctx, "", name)
}

// ScopedSecret gives a project's own secret, or the global one when
// project is "".
func (s *Store) ScopedSecret(ctx context.Context, project, name string) (Secret, error) {
	rows, _ := s.Pool.Query(ctx, `select `+secretCols+` from secrets where name = $1 and project_id is not distinct from nullif($2, '')`,
		name, project)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Secret])
	return v, notFound(err)
}

// SecretFor gives the secret a project's requests open by this name: the
// project's own, else the global one.
func (s *Store) SecretFor(ctx context.Context, project, name string) (Secret, error) {
	rows, _ := s.Pool.Query(ctx, `select `+secretCols+` from secrets where name = $1 and (project_id = $2 or project_id is null)
		order by project_id nulls last limit 1`, name, project)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Secret])
	return v, notFound(err)
}

// SecretsNamed lists the secrets of this name, global and the projects'.
func (s *Store) SecretsNamed(ctx context.Context, name string) ([]Secret, error) {
	rows, _ := s.Pool.Query(ctx, `select `+secretCols+` from secrets where name = $1 order by project_id nulls first`, name)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Secret])
}

// PutSecret creates a secret (create) or replaces one, in its scope:
// ErrInUse when creating a name that exists, ErrNotFound when replacing
// one that doesn't.
func (s *Store) PutSecret(ctx context.Context, v Secret, create bool) error {
	if create {
		_, err := s.Pool.Exec(ctx, `insert into secrets (name, project_id, kind, description, username, header, nonce, ciphertext, key_id)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, v.Name, v.ProjectID, v.Kind, v.Description, v.Username, v.Header, v.Nonce,
			v.Ciphertext, v.KeyID)
		if IsUniqueViolation(err) {
			return ErrInUse
		}
		return err
	}
	tag, err := s.Pool.Exec(ctx, `update secrets set kind = $3, description = $4, username = $5, header = $6, nonce = $7,
		ciphertext = $8, key_id = $9, updated_at = now() where name = $1 and project_id is not distinct from $2`, v.Name, v.ProjectID,
		v.Kind, v.Description, v.Username, v.Header, v.Nonce, v.Ciphertext, v.KeyID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteSecret deletes a project's own secret, or the global one when
// project is "".
func (s *Store) DeleteSecret(ctx context.Context, project, name string) error {
	tag, err := s.Pool.Exec(ctx, `delete from secrets where name = $1 and project_id is not distinct from nullif($2, '')`, name, project)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SecretUsers lists the slugs of items whose versions use the secret: in a
// `rest` value source or a composite's REST call, or that call's undo.
func (s *Store) SecretUsers(ctx context.Context, name string) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select distinct i.slug from catalog_item_versions v join catalog_items i on i.id = v.item_id
		where jsonb_path_exists(v.ui_schema, '$.fields.* ? (@.value_source.kind == "rest" && @.value_source.params.secret == $name)',
			jsonb_build_object('name', $1::text))
		   or jsonb_path_exists(v.blueprint, '$.steps.**.rest ? (@.secret == $name)', jsonb_build_object('name', $1::text))
		order by i.slug`, name)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ItemBlueprint is the blueprint of one of an item's versions.
type ItemBlueprint struct {
	Slug      string          `db:"slug"`
	Blueprint json.RawMessage `db:"blueprint"`
}

// BlueprintsMentioning are the item versions whose blueprint holds text, to
// be read for the secrets they name.
func (s *Store) BlueprintsMentioning(ctx context.Context, text string) ([]ItemBlueprint, error) {
	rows, _ := s.Pool.Query(ctx, `select i.slug, v.blueprint from catalog_item_versions v join catalog_items i on i.id = v.item_id
		where strpos(v.blueprint::text, $1) > 0 order by i.slug`, text)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ItemBlueprint])
}

// ResealSecret replaces a secret's sealed value only (key rotation), if it
// is still the one sealed with oldNonce: false when it changed meanwhile, so
// a value saved during the rotation is never put back to the old one.
func (s *Store) ResealSecret(ctx context.Context, v Secret, oldNonce []byte, keyID string, nonce, ciphertext []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update secrets set key_id = $2, nonce = $3, ciphertext = $4
		where name = $1 and nonce = $5 and project_id is not distinct from $6`, v.Name, keyID, nonce, ciphertext, oldNonce, v.ProjectID)
	return tag.RowsAffected() == 1, err
}

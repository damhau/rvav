package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Secret is a stored secret; Nonce and Ciphertext hold its value, sealed
// by the secrets package.
type Secret struct {
	Name        string    `db:"name"`
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

const secretCols = `name, kind, description, username, header, key_id, nonce, ciphertext, created_at, updated_at`

func (s *Store) Secrets(ctx context.Context) ([]Secret, error) {
	rows, _ := s.Pool.Query(ctx, `select `+secretCols+` from secrets order by name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Secret])
}

func (s *Store) GetSecret(ctx context.Context, name string) (Secret, error) {
	rows, _ := s.Pool.Query(ctx, `select `+secretCols+` from secrets where name = $1`, name)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Secret])
	return v, notFound(err)
}

// PutSecret creates a secret (create) or replaces one: ErrInUse when
// creating a name that exists, ErrNotFound when replacing one that doesn't.
func (s *Store) PutSecret(ctx context.Context, v Secret, create bool) error {
	if create {
		_, err := s.Pool.Exec(ctx, `insert into secrets (name, kind, description, username, header, nonce, ciphertext, key_id)
			values ($1, $2, $3, $4, $5, $6, $7, $8)`, v.Name, v.Kind, v.Description, v.Username, v.Header, v.Nonce, v.Ciphertext, v.KeyID)
		if IsUniqueViolation(err) {
			return ErrInUse
		}
		return err
	}
	tag, err := s.Pool.Exec(ctx, `update secrets set kind = $2, description = $3, username = $4, header = $5, nonce = $6,
		ciphertext = $7, key_id = $8, updated_at = now() where name = $1`, v.Name, v.Kind, v.Description, v.Username, v.Header,
		v.Nonce, v.Ciphertext, v.KeyID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) DeleteSecret(ctx context.Context, name string) error {
	tag, err := s.Pool.Exec(ctx, `delete from secrets where name = $1`, name)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SecretUsers lists the slugs of items whose versions have a `rest` value
// source using the secret.
func (s *Store) SecretUsers(ctx context.Context, name string) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select distinct i.slug from catalog_item_versions v join catalog_items i on i.id = v.item_id
		where jsonb_path_exists(v.ui_schema, '$.fields.* ? (@.value_source.kind == "rest" && @.value_source.params.secret == $name)',
			jsonb_build_object('name', $1::text))
		order by i.slug`, name)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ResealSecret replaces a secret's sealed value only (key rotation), if it
// is still the one sealed with oldNonce: false when it changed meanwhile, so
// a value saved during the rotation is never put back to the old one.
func (s *Store) ResealSecret(ctx context.Context, name string, oldNonce []byte, keyID string, nonce, ciphertext []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update secrets set key_id = $2, nonce = $3, ciphertext = $4 where name = $1 and nonce = $5`,
		name, keyID, nonce, ciphertext, oldNonce)
	return tag.RowsAffected() == 1, err
}

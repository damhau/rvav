package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Integration settings set from the UI (ADR-0020), their password or token
// sealed.

type IntegrationSettings struct {
	Kind             string          `db:"kind"`
	Settings         json.RawMessage `db:"settings"`
	SecretKeyID      string          `db:"secret_key_id"`
	SecretNonce      []byte          `db:"secret_nonce"`
	SecretCiphertext []byte          `db:"secret_ciphertext"`
	Revision         int64           `db:"revision"`
	UpdatedAt        time.Time       `db:"updated_at"`
	UpdatedBy        *string         `db:"updated_by"`
	UpdatedByName    *string         `db:"updated_by_name"`
}

func (s *Store) StoredIntegrations(ctx context.Context) (map[string]IntegrationSettings, error) {
	rows, _ := s.Pool.Query(ctx, `select i.kind, i.settings, i.secret_key_id, i.secret_nonce, i.secret_ciphertext, i.revision,
		i.updated_at, i.updated_by, u.name as updated_by_name
		from integration_settings i left join users u on u.id = i.updated_by`)
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[IntegrationSettings])
	if err != nil {
		return nil, err
	}
	out := map[string]IntegrationSettings{}
	for _, i := range list {
		out[i.Kind] = i
	}
	return out, nil
}

// Sealed is a sealed value and the key that sealed it.
type Sealed struct {
	KeyID             string
	Nonce, Ciphertext []byte
}

// SaveIntegration saves an integration's settings, and its secret when
// given (nil keeps the stored one), and gives the new settings revision.
func (s *Store) SaveIntegration(ctx context.Context, kind string, settings json.RawMessage, secret *Sealed, userID string) (int64, error) {
	var keyID string
	var nonce, ct []byte
	if secret != nil {
		keyID, nonce, ct = secret.KeyID, secret.Nonce, secret.Ciphertext
	}
	var revision int64
	err := s.Pool.QueryRow(ctx, `insert into integration_settings (kind, settings, secret_key_id, secret_nonce, secret_ciphertext,
		updated_by) values ($1, $2, $3, $4, $5, $6)
		on conflict (kind) do update set settings = excluded.settings,
			secret_key_id = case when $7 then excluded.secret_key_id else integration_settings.secret_key_id end,
			secret_nonce = case when $7 then excluded.secret_nonce else integration_settings.secret_nonce end,
			secret_ciphertext = case when $7 then excluded.secret_ciphertext else integration_settings.secret_ciphertext end,
			revision = nextval('settings_revision'), updated_at = now(), updated_by = excluded.updated_by
		returning revision`, kind, settings, keyID, nonce, ct, userID, secret != nil).Scan(&revision)
	return revision, err
}

// SettingsRevision is the revision of the latest saved settings (0 for none).
func (s *Store) SettingsRevision(ctx context.Context) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx, `select coalesce(max(revision), 0) from integration_settings`).Scan(&n)
	return n, err
}

// ResealIntegration replaces an integration's sealed secret only (key
// rotation), if it is still the one sealed with oldNonce: false when it
// changed meanwhile.
func (s *Store) ResealIntegration(ctx context.Context, kind string, oldNonce []byte, v Sealed) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update integration_settings set secret_key_id = $2, secret_nonce = $3, secret_ciphertext = $4
		where kind = $1 and secret_nonce = $5`, kind, v.KeyID, v.Nonce, v.Ciphertext, oldNonce)
	return tag.RowsAffected() == 1, err
}

// TryOrigin lets the sign-in page reach a provider being tried until the
// given time (ADR-0030); those whose time is over are dropped.
func (s *Store) TryOrigin(ctx context.Context, origin string, until time.Time) error {
	_, err := s.Pool.Exec(ctx, `with gone as (delete from oidc_trial_origins where expires_at <= now() and origin <> $1)
		insert into oidc_trial_origins (origin, expires_at) values ($1, $2)
		on conflict (origin) do update set expires_at = greatest(oidc_trial_origins.expires_at, excluded.expires_at)`, origin, until)
	return err
}

// TrialOrigins are the origins of the providers being tried now.
func (s *Store) TrialOrigins(ctx context.Context) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select origin from oidc_trial_origins where expires_at > now() order by origin`)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

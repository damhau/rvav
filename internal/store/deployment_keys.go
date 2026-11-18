package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// DeploymentKey is the key pair rvav made for a deployment (ADR-0039):
// its private key sealed until downloaded once, nil after.
type DeploymentKey struct {
	DeploymentID     string     `db:"deployment_id"`
	Username         string     `db:"username"`
	PublicKey        string     `db:"public_key"`
	Fingerprint      string     `db:"fingerprint"`
	KeyID            *string    `db:"key_id"`
	Nonce            []byte     `db:"nonce"`
	Ciphertext       []byte     `db:"ciphertext"`
	CreatedAt        time.Time  `db:"created_at"`
	DownloadedAt     *time.Time `db:"downloaded_at"`
	DownloadedBy     *string    `db:"downloaded_by"`
	DownloadedByName *string    `db:"downloaded_by_name"`
}

// DeploymentKeyName is what a deployment's private key is sealed under.
func DeploymentKeyName(deploymentID string) string { return "deployment-key:" + deploymentID }

const deploymentKeySelect = `select k.deployment_id, k.username, k.public_key, k.fingerprint, k.key_id, k.nonce, k.ciphertext,
       k.created_at, k.downloaded_at, k.downloaded_by, u.name as downloaded_by_name
from deployment_keys k left join users u on u.id = k.downloaded_by`

// KeepDeploymentKey keeps a deployment's key pair, unless it has one
// already: the one it has is returned.
func (s *Store) KeepDeploymentKey(ctx context.Context, k DeploymentKey) (DeploymentKey, error) {
	if _, err := s.Pool.Exec(ctx, `insert into deployment_keys (deployment_id, username, public_key, fingerprint, key_id, nonce, ciphertext)
		values ($1,$2,$3,$4,$5,$6,$7) on conflict (deployment_id) do nothing`,
		k.DeploymentID, k.Username, k.PublicKey, k.Fingerprint, k.KeyID, k.Nonce, k.Ciphertext); err != nil {
		return k, err
	}
	return s.DeploymentKey(ctx, s.Pool, k.DeploymentID)
}

// DeploymentKey gives a deployment's key pair; for update, in a
// transaction that hands the private key out.
func (s *Store) DeploymentKey(ctx context.Context, db DB, deploymentID string) (DeploymentKey, error) {
	rows, _ := db.Query(ctx, deploymentKeySelect+` where k.deployment_id = $1`, deploymentID)
	k, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[DeploymentKey])
	return k, notFound(err)
}

// LockDeploymentKey reads a deployment's key pair, locked until the
// transaction ends.
func (s *Store) LockDeploymentKey(ctx context.Context, tx pgx.Tx, deploymentID string) (DeploymentKey, error) {
	rows, _ := tx.Query(ctx, deploymentKeySelect+` where k.deployment_id = $1 for update of k`, deploymentID)
	k, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[DeploymentKey])
	return k, notFound(err)
}

// ForgetDeploymentKey drops a deployment's sealed private key: downloaded
// by userID, or nobody when the deployment is deleted.
func (s *Store) ForgetDeploymentKey(ctx context.Context, db DB, deploymentID string, userID *string) error {
	_, err := db.Exec(ctx, `update deployment_keys set key_id = null, nonce = null, ciphertext = null,
		downloaded_at = case when $2::text is null then downloaded_at else now() end,
		downloaded_by = coalesce($2, downloaded_by)
		where deployment_id = $1 and ciphertext is not null`, deploymentID, userID)
	return err
}

// DropDeploymentKey deletes a deployment's key pair: a build that made it
// was rolled back, and no VM of the deployment has it.
func (s *Store) DropDeploymentKey(ctx context.Context, db DB, deploymentID string) error {
	_, err := db.Exec(ctx, `delete from deployment_keys where deployment_id = $1`, deploymentID)
	return err
}

// DeploymentKeysNotWith lists the private keys still kept that were not
// sealed with this key (key rotation).
func (s *Store) DeploymentKeysNotWith(ctx context.Context, keyID string) ([]DeploymentKey, error) {
	rows, _ := s.Pool.Query(ctx, deploymentKeySelect+` where k.ciphertext is not null and k.key_id is distinct from $1`, keyID)
	return pgx.CollectRows(rows, pgx.RowToStructByName[DeploymentKey])
}

// ResealDeploymentKey replaces a private key's sealed value (key
// rotation), if it is still kept and unchanged.
func (s *Store) ResealDeploymentKey(ctx context.Context, deploymentID string, oldNonce []byte, keyID string, nonce, ciphertext []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update deployment_keys set key_id = $3, nonce = $4, ciphertext = $5
		where deployment_id = $1 and nonce = $2`, deploymentID, oldNonce, keyID, nonce, ciphertext)
	return tag.RowsAffected() == 1, err
}

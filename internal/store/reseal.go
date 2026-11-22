package store

import "context"

// ResealVCenter replaces an added vCenter's sealed password, unless it was
// replaced since it was read (nonce); false then.
func (s *Store) ResealVCenter(ctx context.Context, name string, nonce []byte, keyID string, newNonce, ciphertext []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update vcenters set key_id = $3, nonce = $4, ciphertext = $5 where name = $1 and nonce = $2`,
		name, nonce, keyID, newNonce, ciphertext)
	return tag.RowsAffected() == 1, err
}

// ResealAuditDestination replaces a webhook's sealed header value, unless
// it was replaced since it was read (nonce); false then.
func (s *Store) ResealAuditDestination(ctx context.Context, id string, nonce []byte, keyID string, newNonce, ciphertext []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update audit_destinations set auth_key_id = $3, auth_nonce = $4, auth_ciphertext = $5
		where id = $1 and auth_nonce = $2`, id, nonce, keyID, newNonce, ciphertext)
	return tag.RowsAffected() == 1, err
}

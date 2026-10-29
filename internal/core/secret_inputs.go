package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/secretinput"
	"github.com/damhau/rvav/internal/store"
)

// Secret inputs (S14.6): a form field marked secret is "stored encrypted,
// never returned by the API, passed to AWX only at launch". Its answer is
// sealed with the secrets key in request_secrets; the request's inputs and
// plan, and so Temporal's history, hold a marker in its place, which the
// AWX job step opens to launch.

// sealSecretInputs seals the secret inputs of a request and puts markers
// in their place in inputs.
func (s *Service) sealSecretInputs(ctx context.Context, db store.DB, requestID string, uiSchema []byte, inputs map[string]any) error {
	for field := range present.SecretFields(uiSchema) {
		v, ok := inputs[field]
		if !ok || v == nil || secretinput.IsMarker(v) {
			continue
		}
		if s.Secrets == nil {
			return errors.New("rvav has no secrets key (RVAV_SECRETS_KEY) to keep a secret input")
		}
		plain, err := secretinput.Encode(v)
		if err != nil {
			return err
		}
		keyID, nonce, ct, err := s.Secrets.Seal(secretinput.Name(requestID, field), plain)
		if err != nil {
			return fmt.Errorf("seal %s: %w", field, err)
		}
		if err := s.Store.PutRequestSecret(ctx, db, requestID, store.SealedInput{Field: field, KeyID: keyID, Nonce: nonce,
			Ciphertext: ct}); err != nil {
			return err
		}
		inputs[field] = secretinput.Marker(field)
	}
	return nil
}

// openSecretInputs puts the sealed values back in place of their markers,
// to evaluate a request's inputs again (an approver's edits).
func (s *Service) openSecretInputs(ctx context.Context, requestID string, inputs map[string]any) error {
	sealed, err := s.Store.RequestSecrets(ctx, requestID)
	if err != nil || len(sealed) == 0 {
		return err
	}
	return secretinput.Open(s.Secrets, requestID, sealed, inputs)
}

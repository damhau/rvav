// Package secrets encrypts the secrets rvav keeps in its database
// (ADR-0019): AES-256-GCM, a random nonce for each value, and the secret's
// name as additional data, so a value can't be moved to another name.
// Each value records the ID of the key that sealed it; earlier keys still
// open what they sealed until it is sealed again with the current one
// (ADR-0020, rvav secrets rotate).
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
)

// ErrNoKey: RVAV_SECRETS_KEY is not set.
var ErrNoKey = errors.New("RVAV_SECRETS_KEY is not set: rvav can't store secrets")

// ErrUnknownKey: the value was sealed with a key this box doesn't have.
var ErrUnknownKey = errors.New("the secret was sealed with a key that is neither RVAV_SECRETS_KEY nor in RVAV_SECRETS_OLD_KEYS")

// nonceSize is GCM's standard nonce: 96 bits.
const nonceSize = 12

type key struct {
	id   string
	aead cipher.AEAD
}

// Box seals with the current key and opens with it or earlier ones.
type Box struct {
	current key
	old     []key
}

func parseKey(keyB64 string) (key, error) {
	raw, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(raw) != 32 {
		return key{}, fmt.Errorf("secrets keys must be 32 bytes in base64 (openssl rand -base64 32)")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return key{}, err
	}
	// FIPS 140-only mode refuses GCM with nonces the caller chooses
	// (ADR-0060). This one draws its own and writes it before the
	// ciphertext; rvav keeps the two in columns of their own.
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return key{}, err
	}
	sum := sha256.Sum256(raw)
	return key{id: hex.EncodeToString(sum[:4]), aead: aead}, nil
}

// New makes a box from the current base64 key of 32 bytes and earlier
// ones ("" for current gives a nil box, which refuses every call with
// ErrNoKey).
func New(current string, old ...string) (*Box, error) {
	if current == "" {
		return nil, nil
	}
	cur, err := parseKey(current)
	if err != nil {
		return nil, fmt.Errorf("RVAV_SECRETS_KEY: %w", err)
	}
	b := &Box{current: cur}
	for _, o := range old {
		if o == "" {
			continue
		}
		k, err := parseKey(o)
		if err != nil {
			return nil, fmt.Errorf("RVAV_SECRETS_OLD_KEYS: %w", err)
		}
		b.old = append(b.old, k)
	}
	return b, nil
}

// KeyID names the current key (a hash, never the key).
func (b *Box) KeyID() string {
	if b == nil {
		return ""
	}
	return b.current.id
}

// Seal encrypts a secret's value with the current key.
func (b *Box) Seal(name string, value []byte) (keyID string, nonce, ciphertext []byte, err error) {
	if b == nil {
		return "", nil, nil, ErrNoKey
	}
	sealed := b.current.aead.Seal(nil, nil, value, []byte(name))
	return b.current.id, sealed[:nonceSize:nonceSize], sealed[nonceSize:], nil
}

// Open decrypts a secret's value with the key that sealed it (keyID), or
// with each key in turn for values sealed before keys had IDs ("").
func (b *Box) Open(name, keyID string, nonce, ciphertext []byte) ([]byte, error) {
	if b == nil {
		return nil, ErrNoKey
	}
	if len(nonce) != nonceSize {
		return nil, fmt.Errorf("the secret's nonce is %d bytes, not %d", len(nonce), nonceSize)
	}
	sealed := append(slices.Clip(nonce), ciphertext...)
	keys := append([]key{b.current}, b.old...)
	var lastErr error = ErrUnknownKey
	for _, k := range keys {
		if keyID != "" && k.id != keyID {
			continue
		}
		plain, err := k.aead.Open(nil, nil, sealed, []byte(name))
		if err == nil {
			return plain, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

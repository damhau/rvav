// Package secrets encrypts the secrets rvav keeps in its database
// (ADR-0019): AES-256-GCM, a fresh nonce for each value, and the secret's
// name as additional data, so a value can't be moved to another name.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrNoKey: RVAV_SECRETS_KEY is not set.
var ErrNoKey = errors.New("RVAV_SECRETS_KEY is not set: rvav can't store secrets")

// Box seals and opens values with one key.
type Box struct{ aead cipher.AEAD }

// New makes a box from a base64 key of 32 bytes ("" gives a nil box, which
// refuses every call with ErrNoKey).
func New(keyB64 string) (*Box, error) {
	if keyB64 == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("RVAV_SECRETS_KEY must be 32 bytes in base64 (openssl rand -base64 32)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts a secret's value.
func (b *Box) Seal(name string, value []byte) (nonce, ciphertext []byte, err error) {
	if b == nil {
		return nil, nil, ErrNoKey
	}
	nonce = make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, b.aead.Seal(nil, nonce, value, []byte(name)), nil
}

// Open decrypts a secret's value.
func (b *Box) Open(name string, nonce, ciphertext []byte) ([]byte, error) {
	if b == nil {
		return nil, ErrNoKey
	}
	return b.aead.Open(nil, nonce, ciphertext, []byte(name))
}

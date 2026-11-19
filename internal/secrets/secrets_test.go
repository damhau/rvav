package secrets

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/fips/fipstest"
)

func testKey(b byte) string {
	k := make([]byte, 32)
	k[0] = b
	return base64.StdEncoding.EncodeToString(k)
}

func TestSealOpenAndRotate(t *testing.T) {
	b, err := New(testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	id, nonce, ct, err := b.Seal("cmdb", []byte("s3cret"))
	if err != nil || id == "" || id != b.KeyID() {
		t.Fatalf("seal: %q %v", id, err)
	}
	if got, err := b.Open("cmdb", id, nonce, ct); err != nil || string(got) != "s3cret" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := b.Open("other", id, nonce, ct); err == nil {
		t.Fatal("a value opened under another name")
	}

	// A new key: what the old one sealed still opens while it is listed.
	rotated, err := New(testKey(2), testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	if rotated.KeyID() == id {
		t.Fatal("two keys with one ID")
	}
	if got, err := rotated.Open("cmdb", id, nonce, ct); err != nil || string(got) != "s3cret" {
		t.Fatalf("open with the old key = %q, %v", got, err)
	}
	if got, err := rotated.Open("cmdb", "", nonce, ct); err != nil || string(got) != "s3cret" {
		t.Fatalf("open without a key ID = %q, %v", got, err)
	}
	newOnly, err := New(testKey(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newOnly.Open("cmdb", id, nonce, ct); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("without the old key: %v", err)
	}

	if _, err := New("c2hvcnQ="); err == nil {
		t.Fatal("a short key was accepted")
	}
	var none *Box
	if _, _, _, err := none.Seal("x", nil); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key: %v", err)
	}
}

// In FIPS 140-only mode the box seals and opens, and opens what it sealed
// before (ADR-0060), the nonce still kept apart from the ciphertext.
func TestBoxInFIPSOnlyMode(t *testing.T) {
	if !fipstest.In(t, fips.Only) {
		return
	}
	b, err := New(testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	id, nonce, ct, err := b.Seal("cmdb", []byte("s3cret"))
	if err != nil || len(nonce) != 12 {
		t.Fatalf("seal: %x %v", nonce, err)
	}
	if got, err := b.Open("cmdb", id, nonce, ct); err != nil || string(got) != "s3cret" {
		t.Fatalf("open = %q, %v", got, err)
	}
	// Sealed with cipher.NewGCM and the nonce "0123456789ab".
	nonce, _ = hex.DecodeString("303132333435363738396162")
	ct, _ = hex.DecodeString("7f9bb39284992c9247fcfddd0bef0a050c66988c8e69")
	if got, err := b.Open("cmdb", id, nonce, ct); err != nil || string(got) != "s3cret" {
		t.Fatalf("open a value sealed before = %q, %v", got, err)
	}
	if _, err := b.Open("cmdb", id, nonce[:8], ct); err == nil {
		t.Fatal("a short nonce opened")
	}
}

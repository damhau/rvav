package secrets

import (
	"encoding/base64"
	"errors"
	"testing"
)

func TestBox(t *testing.T) {
	b, err := New(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	nonce, ct, err := b.Seal("cmdb", []byte("s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := b.Open("cmdb", nonce, ct); err != nil || string(got) != "s3cret" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := b.Open("other", nonce, ct); err == nil {
		t.Fatal("a value opened under another name")
	}
	if _, err := New("c2hvcnQ="); err == nil {
		t.Fatal("a short key accepted")
	}
	var none *Box
	if _, _, err := none.Seal("x", nil); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key = %v", err)
	}
}

package auth

import (
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	// ADR-0030's parameters, whatever the machine's CPUs.
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=1,p=2$") {
		t.Fatalf("hash %q", h)
	}
	if !CheckPassword("correct horse battery", h) || CheckPassword("correct horse batterY", h) {
		t.Fatal("the hash doesn't check its password")
	}
	if again, _ := HashPassword("correct horse battery"); again == h {
		t.Fatal("two hashes of a password are equal: no salt")
	}
	if CheckPassword("", "not a hash") || CheckPassword("x", "") {
		t.Fatal("a malformed hash matched")
	}
}

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		p := GeneratePassword()
		if len(p) != 24 || strings.Trim(p, passwordAlphabet) != "" {
			t.Fatalf("generated %q", p)
		}
		if seen[p] {
			t.Fatalf("generated %q twice", p)
		}
		seen[p] = true
	}
	if a, b := NewSessionID(), NewSessionID(); len(a) != 43 || a == b {
		t.Fatalf("session IDs %q %q", a, b)
	}
}

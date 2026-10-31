package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func check(t *testing.T, password, hash string) bool {
	t.Helper()
	ok, err := CheckPassword(context.Background(), password, hash)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword(context.Background(), "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	// ADR-0030's parameters, whatever the machine's CPUs.
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=1,p=2$") {
		t.Fatalf("hash %q", h)
	}
	if !check(t, "correct horse battery", h) || check(t, "correct horse batterY", h) {
		t.Fatal("the hash doesn't check its password")
	}
	if again, _ := HashPassword(context.Background(), "correct horse battery"); again == h {
		t.Fatal("two hashes of a password are equal: no salt")
	}
	if check(t, "", "not a hash") || check(t, "x", "") {
		t.Fatal("a malformed hash matched")
	}
}

// TestHashSlotsWaitAsLongAsTheRequest: an attempt waiting for a hash slot
// gives up its place once its client has gone, rather than queueing for
// one of 64 MiB the administrator's sign-in waits behind.
func TestHashSlotsWaitAsLongAsTheRequest(t *testing.T) {
	h, err := HashPassword(context.Background(), "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	release := HoldHashSlots()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	ok, err := CheckPassword(ctx, "correct horse battery", h)
	if ok || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("every slot held: ok=%v err=%v after %s", ok, err, time.Since(start))
	}
	if _, err := HashPassword(ctx, "another"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hashing with every slot held: %v", err)
	}
	release()
	if !check(t, "correct horse battery", h) {
		t.Fatal("the slots released, the password doesn't check")
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

package fips

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Mode{"": "", "off": Off, "on": On, " Only ": Only} {
		if got, err := Parse(in); err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Parse("strict"); err == nil || !strings.Contains(err.Error(), "off, on or only") {
		t.Errorf("Parse(strict) = %v", err)
	}
}

// The environment that starts a process again in the mode asked for adds
// to GODEBUG (its last fips140 wins), and asking again for a mode that
// didn't take is an error, not a loop.
func TestEnvironFor(t *testing.T) {
	base := []string{"PATH=/bin", "GODEBUG=http2debug=1"}
	if env, again, err := environFor(base, "", Off); again || err != nil || !slices.Equal(env, base) {
		t.Errorf("unset: %v %v %v", env, again, err)
	}
	if _, again, err := environFor(base, On, On); again || err != nil {
		t.Errorf("already on: %v %v", again, err)
	}
	env, again, err := environFor(base, Only, Off)
	if !again || err != nil || !slices.Equal(env, []string{"PATH=/bin", "GODEBUG=http2debug=1,fips140=only"}) {
		t.Errorf("off to only: %v %v %v", env, again, err)
	}
	if env, _, _ := environFor([]string{"HOME=/root"}, On, Off); !slices.Equal(env, []string{"HOME=/root", "GODEBUG=fips140=on"}) {
		t.Errorf("no GODEBUG: %v", env)
	}
	if _, again, err := environFor([]string{"GODEBUG=fips140=off,fips140=on"}, On, Off); again || err == nil {
		t.Errorf("asked for and not in effect: %v %v", again, err)
	}
	if lastFIPS140("fips140=only, fips140=on") != "on" || lastFIPS140("x=1") != "" {
		t.Error("lastFIPS140")
	}
}

// RVAV_FIPS starts the process again in its mode: here this test, which
// then runs in FIPS 140-only mode.
func TestApply(t *testing.T) {
	if os.Getenv("FIPSTEST_APPLY") != "" {
		if err := Apply(); err != nil {
			t.Fatal(err)
		}
		t.Logf("mode %s, module %s", Current(), Module())
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestApply$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), "FIPSTEST_APPLY=1", Env+"=only")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "mode only, module ") {
		t.Fatalf("%v\n%s", err, out)
	}
	cmd = exec.Command(os.Args[0], "-test.run=^TestApply$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), "FIPSTEST_APPLY=1", Env+"=strict")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "give off, on or only") {
		t.Fatalf("RVAV_FIPS=strict: %v\n%s", err, out)
	}
}

// In FIPS 140-only mode, Go panics on an HMAC key shorter than 112 bits:
// CheckHMACKey refuses it first.
func TestCheckHMACKey(t *testing.T) {
	if err := CheckHMACKey([]byte("short")); Current() != Only && err != nil {
		t.Fatalf("mode %s: %v", Current(), err)
	}
	if Current() != Only {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCheckHMACKey$", "-test.v", "-test.count=1")
		cmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
		if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "--- PASS") {
			t.Fatalf("in FIPS 140-only mode: %v\n%s", err, out)
		}
		return
	}
	if err := CheckHMACKey([]byte("hook-secret")); !errors.Is(err, ErrShortHMACKey) {
		t.Fatalf("an 11-byte key: %v", err)
	}
	key := []byte("a-14-byte-key!")
	if err := CheckHMACKey(key); err != nil {
		t.Fatalf("a 14-byte key: %v", err)
	}
	hmac.New(sha256.New, key).Sum(nil)
	defer func() {
		if recover() == nil {
			t.Error("Go took an 11-byte HMAC key in FIPS 140-only mode: CheckHMACKey isn't needed any more")
		}
	}()
	hmac.New(sha256.New, []byte("hook-secret")).Sum(nil)
}

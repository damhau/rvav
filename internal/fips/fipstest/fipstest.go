// Package fipstest runs tests in a FIPS 140 mode, in a process of their
// own: Go reads the mode once, as a process starts, so a test binary
// started in another mode runs them again with GODEBUG set.
package fipstest

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/fips"
)

// In reports whether the calling test runs in mode: when the process
// runs in another, it runs the test again in a process in mode, fails
// with that process's output if it fails, and returns false. env adds to
// that process's environment. A top-level test only.
func In(t *testing.T, mode fips.Mode, env ...string) bool {
	t.Helper()
	if fips.Current() == mode {
		return true
	}
	if strings.Contains(t.Name(), "/") {
		t.Fatal("fipstest.In runs a top-level test, not a subtest")
	}
	Run(t, mode, "^"+regexp.QuoteMeta(t.Name())+"$", env...)
	return false
}

// Run runs the tests that match pattern (as go test -run) in a process in
// mode, and fails with its output if they fail or none ran.
func Run(t *testing.T, mode fips.Mode, pattern string, env ...string) {
	t.Helper()
	args := []string{"-test.run=" + pattern, "-test.count=1", "-test.v"}
	if testing.Short() {
		args = append(args, "-test.short")
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(environ(mode), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("in FIPS 140 mode %s: %v\n%s", mode, err, out)
	}
	if !strings.Contains(string(out), "--- PASS") {
		t.Fatalf("in FIPS 140 mode %s, no test matched %s:\n%s", mode, pattern, out)
	}
	if testing.Verbose() {
		var results []string
		for line := range strings.Lines(string(out)) {
			if strings.HasPrefix(strings.TrimSpace(line), "--- ") {
				results = append(results, line)
			}
		}
		t.Logf("in FIPS 140 mode %s:\n%s", mode, strings.Join(results, ""))
	}
}

// environ is this process's environment, with GODEBUG asking for mode
// and RVAV_FIPS leaving it to GODEBUG.
func environ(mode fips.Mode) []string {
	var out []string
	godebug := ""
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "GODEBUG="):
			godebug = strings.TrimPrefix(kv, "GODEBUG=") + ","
		case strings.HasPrefix(kv, fips.Env+"="):
		default:
			out = append(out, kv)
		}
	}
	return append(out, "GODEBUG="+godebug+"fips140="+string(mode))
}

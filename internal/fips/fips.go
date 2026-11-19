// Package fips is rvav's FIPS 140-3 mode (ADR-0060). rvav's images are
// built with Go's FIPS 140-3 module (GOFIPS140); RVAV_FIPS turns the mode
// on when a process starts: "on" keeps to the module's approved
// algorithms where rvav and Go choose, "only" also refuses the others.
package fips

import (
	"crypto/fips140"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Mode is how a process uses the FIPS 140-3 module.
type Mode string

const (
	Off  Mode = "off"
	On   Mode = "on"
	Only Mode = "only"
)

// Env sets the mode of every rvav process.
const Env = "RVAV_FIPS"

// Current is the mode this process runs in.
func Current() Mode {
	switch {
	case fips140.Enforced():
		return Only
	case fips140.Enabled():
		return On
	}
	return Off
}

// Module is the version of the Go Cryptographic Module this binary was
// built with: v1.0.0 in rvav's images, "latest" in a build without
// GOFIPS140, which uses the module of its Go release, not a validated one.
func Module() string { return fips140.Version() }

// Parse reads a value of RVAV_FIPS: "" leaves the mode to GODEBUG.
func Parse(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case "", Off, On, Only:
		return m, nil
	}
	return "", fmt.Errorf("%s=%q: give off, on or only", Env, s)
}

// Apply starts this process again in the mode RVAV_FIPS sets when it runs
// in another: Go reads GODEBUG's fips140 once, as a process starts. Call
// it first in main, before any goroutine starts.
func Apply() error {
	want, err := Parse(os.Getenv(Env))
	if err != nil {
		return err
	}
	env, again, err := environFor(os.Environ(), want, Current())
	if err != nil || !again {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("%s=%s: find this program to start it again: %w", Env, want, err)
	}
	return startAgain(exe, os.Args, env)
}

// environFor is the environment that starts a process in mode want, and
// whether the process must start again to be in it.
func environFor(environ []string, want, current Mode) ([]string, bool, error) {
	if want == "" || want == current {
		return environ, false, nil
	}
	var godebug string
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "GODEBUG="); ok {
			godebug = v
			continue
		}
		out = append(out, kv)
	}
	if lastFIPS140(godebug) == string(want) {
		// Already asked for and not in effect: starting again would loop.
		return nil, false, fmt.Errorf("%s=%s: GODEBUG=%s didn't set the mode; this program runs in mode %s", Env, want, godebug, current)
	}
	setting := "fips140=" + string(want)
	if godebug != "" {
		setting = godebug + "," + setting
	}
	return append(out, "GODEBUG="+setting), true, nil
}

// lastFIPS140 is GODEBUG's fips140 setting: its last one wins.
func lastFIPS140(godebug string) string {
	var v string
	for kv := range strings.SplitSeq(godebug, ",") {
		if s, ok := strings.CutPrefix(strings.TrimSpace(kv), "fips140="); ok {
			v = s
		}
	}
	return v
}

// MinHMACKey is the shortest HMAC key FIPS 140-only mode takes: 112 bits.
const MinHMACKey = 14

// ErrShortHMACKey is an HMAC key FIPS 140-only mode refuses.
var ErrShortHMACKey = errors.New("FIPS 140-only mode refuses HMAC keys shorter than 112 bits: give a secret of 14 bytes or more")

// CheckHMACKey says whether this process may sign with an HMAC key of
// this length; Go panics on a shorter one in FIPS 140-only mode.
func CheckHMACKey(key []byte) error {
	if Current() == Only && len(key) < MinHMACKey {
		return ErrShortHMACKey
	}
	return nil
}

// NotAllowedError is a feature that needs an algorithm FIPS 140-only mode
// refuses.
type NotAllowedError struct {
	What, Algorithm, Instead string
}

func (e *NotAllowedError) Error() string {
	msg := fmt.Sprintf("%s uses %s, which FIPS 140-only mode (%s=only) refuses", e.What, e.Algorithm, Env)
	if e.Instead != "" {
		msg += ": " + e.Instead
	}
	return msg
}

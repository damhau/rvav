package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/auth"
)

func from(ip string) context.Context {
	return auth.WithClient(context.Background(), auth.Client{IP: ip})
}

// TestFailureReportsLimit: one IPv6 /64 is one address, a report the
// total refuses keeps no key, and what the limit keeps stays bounded
// (ADR-0058).
func TestFailureReportsLimit(t *testing.T) {
	s := &Service{}
	for i := range reportsPerMinute {
		if !s.mayRecordFailure(from(fmt.Sprintf("2001:db8:1:2::%x", i+1))) {
			t.Fatalf("report %d from 2001:db8:1:2::/64 refused", i+1)
		}
	}
	if s.mayRecordFailure(from("2001:db8:1:2:ffff:ffff:ffff:ffff")) {
		t.Fatal("an eleventh report from the same /64 kept")
	}
	if !s.mayRecordFailure(from("2001:db8:1:3::1")) || !s.mayRecordFailure(from("::ffff:192.0.2.1")) {
		t.Fatal("a report from another address refused")
	}
	for i := range reportsPerMinute - 1 {
		if !s.mayRecordFailure(from("192.0.2.1")) {
			t.Fatalf("report %d from 192.0.2.1, after one as ::ffff:192.0.2.1, refused", i+2)
		}
	}
	if s.mayRecordFailure(from("192.0.2.1")) {
		t.Fatal("192.0.2.1 and ::ffff:192.0.2.1 kept apart")
	}

	kept := reportsPerMinute*2 + 1
	for i := range 20000 {
		if s.mayRecordFailure(from(fmt.Sprintf("2001:db8:%x::1", 0x1000+i))) {
			kept++
		}
	}
	if kept != allReportsPerMinute {
		t.Errorf("kept %d reports in a minute, want %d", kept, allReportsPerMinute)
	}
	// The addresses kept, and the total.
	if n := s.signInReports.keys(); n > allReportsPerMinute+1 {
		t.Errorf("the limit holds %d keys after the total refused the rest", n)
	}

	var l windowLimit
	now := time.Now()
	for i := range maxLimitKeys + 100 {
		l.allow(fmt.Sprintf("user:%d", i), reportsPerMinute, now)
	}
	if n := l.keys(); n > maxLimitKeys {
		t.Errorf("the limit holds %d keys of one minute", n)
	}
	if !l.allow("user:0", reportsPerMinute, now) || l.allow("user:new", reportsPerMinute, now) {
		t.Error("with the keys of a minute full, a kept key refused or a new one allowed")
	}
	later := now.Add(time.Minute)
	if !l.allow("user:new", reportsPerMinute, later) {
		t.Error("a new key refused a minute later")
	}
	l.allow("user:new", reportsPerMinute, later.Add(time.Minute))
	if n := l.keys(); n > 2 {
		t.Errorf("two minutes later, the limit holds %d keys", n)
	}
}

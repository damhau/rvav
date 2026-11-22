//go:debug fips140=only

// Package fipsonly tests the vSphere client in FIPS 140-only mode set as a
// build's default, where GODEBUG's text says nothing of it.
package fipsonly

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/trust"
)

// TestPinInFIPSOnlyMode: a vCenter whose certificate isn't the pinned one
// is refused as untrusted, without SHA-1 (which panics in this mode), and
// reached nowhere; with its pin, it is reached (ADR-0060).
func TestPinInFIPSOnlyMode(t *testing.T) {
	if fips.Current() != fips.Only {
		t.Skipf("GODEBUG sets FIPS 140 mode %s over this test's only", fips.Current())
	}
	var reached atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Add(1) }))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	other := strings.Repeat("AB:", 31) + "AB"
	_, err := vsphere.New(ctx, "vc", srv.URL+"/sdk", "u", "p", trust.Options{Pin: other})
	if !errors.As(err, new(x509.UnknownAuthorityError)) || reached.Load() != 0 {
		t.Fatalf("another certificate's pin: %v, %d requests reached the server", err, reached.Load())
	}

	_, err = vsphere.New(ctx, "vc", srv.URL+"/sdk", "u", "p", trust.Options{Pin: trust.Fingerprint(srv.Certificate())})
	if reached.Load() == 0 {
		t.Fatalf("with its pin, nothing reached the server: %v", err)
	}
}

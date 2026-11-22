package httpapi

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/fips/fipstest"
)

// In FIPS 140-only mode the console checks the ESXi host's certificate
// with the SHA-256 thumbprint vCenter gives, and says why when it gave
// a SHA-1 one only (ADR-0060).
func TestConsoleThumbprintInFIPSOnlyMode(t *testing.T) {
	if !fipstest.In(t, fips.Only) {
		return
	}
	cert := [][]byte{[]byte("the host's certificate")}
	sum := sha256.Sum256(cert[0])
	sha1Only := "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD"
	if err := checkThumbprint(cert, []string{sha1Only, hex.EncodeToString(sum[:])}); err != nil {
		t.Fatalf("with its SHA-256 thumbprint: %v", err)
	}
	if err := checkThumbprint(cert, []string{sha1Only}); err == nil || !strings.Contains(err.Error(), "SHA-1 thumbprint only") {
		t.Fatalf("with a SHA-1 thumbprint only: %v", err)
	}
	other := sha256.Sum256([]byte("another certificate"))
	if err := checkThumbprint(cert, []string{hex.EncodeToString(other[:])}); err == nil || !strings.Contains(err.Error(), "vouched for") {
		t.Fatalf("another certificate: %v", err)
	}
}

// The SHA-256 thumbprint, when vCenter gives one, is the one checked: a
// certificate whose SHA-1 thumbprint alone matches isn't accepted.
func TestConsoleThumbprintPrefersSHA256(t *testing.T) {
	cert := [][]byte{[]byte("the host's certificate")}
	s1, s256 := sha1.Sum(cert[0]), sha256.Sum256(cert[0])
	other := sha256.Sum256([]byte("another certificate"))
	if err := checkThumbprint(cert, []string{hex.EncodeToString(s1[:]), hex.EncodeToString(other[:])}); err == nil ||
		!strings.Contains(err.Error(), "vouched for") {
		t.Fatalf("its SHA-1 thumbprint with another's SHA-256 one: %v", err)
	}
	if err := checkThumbprint(cert, []string{"00:11", hex.EncodeToString(s256[:])}); err != nil {
		t.Fatalf("its SHA-256 thumbprint: %v", err)
	}
	if err := checkThumbprint(cert, []string{hex.EncodeToString(s1[:])}); err != nil {
		t.Fatalf("its SHA-1 thumbprint alone: %v", err)
	}
}

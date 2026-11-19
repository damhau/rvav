package gitrepo

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"strings"
	"testing"

	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	gossh "golang.org/x/crypto/ssh"

	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/fips/fipstest"
)

// In FIPS 140-only mode rvav reaches a Git server over SSH with the
// algorithms that mode allows, and refuses hashed known_hosts lines, whose
// HMAC-SHA-1 would panic (ADR-0060).
func TestSSHInFIPSOnlyMode(t *testing.T) {
	if !fipstest.In(t, fips.Only) {
		return
	}
	_, hostKey, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, err := gossh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	_, deployKey, _ := ed25519.GenerateKey(rand.Reader)
	block, err := gossh.MarshalPrivateKey(deployKey, "")
	if err != nil {
		t.Fatal(err)
	}
	deployPub, _ := gossh.NewPublicKey(deployKey.Public())

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	server := &gossh.ServerConfig{PublicKeyCallback: func(_ gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
		if string(key.Marshal()) != string(deployPub.Marshal()) {
			return nil, errUnknownKey
		}
		return &gossh.Permissions{}, nil
	}}
	server.AddHostKey(hostSigner)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if sc, chans, reqs, err := gossh.NewServerConn(conn, server); err == nil {
					go gossh.DiscardRequests(reqs)
					go func() {
						for c := range chans {
							_ = c.Reject(gossh.Prohibited, "no shell")
						}
					}()
					_ = sc.Wait()
				}
			}()
		}
	}()

	addr := l.Addr().String()
	host, port, _ := net.SplitHostPort(addr)
	known := "[" + host + "]:" + port + " " + strings.TrimSpace(string(gossh.MarshalAuthorizedKey(hostSigner.PublicKey())))
	r, err := New(Options{URL: "ssh://git@" + addr + "/infra/catalog.git", Credential: string(pem.EncodeToMemory(block)), KnownHosts: known})
	if err != nil {
		t.Fatal(err)
	}
	config, err := r.auth.(gitssh.AuthMethod).ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c, _, _, err := gossh.NewClientConn(conn, addr, config)
	if err != nil {
		t.Fatalf("the SSH handshake in FIPS 140-only mode: %v", err)
	}
	_ = c.Close()

	hashed := "|1|c2FsdHNhbHRzYWx0c2FsdHNhbHQ=|aGFzaGhhc2hoYXNoaGFzaGhhc2g= " + strings.SplitN(known, " ", 2)[1]
	if _, err := New(Options{URL: "ssh://git@" + addr + "/x.git", Credential: string(pem.EncodeToMemory(block)),
		KnownHosts: hashed}); err == nil || !strings.Contains(err.Error(), "unhashed") {
		t.Fatalf("a hashed known_hosts line: %v", err)
	}
}

var errUnknownKey = gossh.ErrNoAuth

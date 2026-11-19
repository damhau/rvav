package store

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/internal/fips/fipstest"
)

// In FIPS 140-only mode a server that asks for MD5 password
// authentication is refused with a reason: pgx would panic computing the
// MD5 (ADR-0060).
func TestMD5RefusedInFIPSOnlyMode(t *testing.T) {
	if !fipstest.In(t, fips.Only) {
		return
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go askMD5(conn)
		}
	}()
	url := "postgres://rvav:rvav@" + l.Addr().String() + "/rvav?sslmode=disable"
	ctx := context.Background()
	if _, err := Open(ctx, url); err == nil || !strings.Contains(err.Error(), "FIPS 140-only mode refuses MD5") {
		t.Fatalf("Open: %v", err)
	}
	if err := Migrate(ctx, url); err == nil || !strings.Contains(err.Error(), "scram-sha-256") {
		t.Fatalf("Migrate: %v", err)
	}
}

// askMD5 is a PostgreSQL server that asks for an MD5 password.
func askMD5(conn net.Conn) {
	defer conn.Close()
	be := pgproto3.NewBackend(conn, conn)
	if _, err := be.ReceiveStartupMessage(); err != nil {
		return
	}
	be.Send(&pgproto3.AuthenticationMD5Password{Salt: [4]byte{1, 2, 3, 4}})
	if be.Flush() != nil {
		return
	}
	_, _ = be.Receive()
}

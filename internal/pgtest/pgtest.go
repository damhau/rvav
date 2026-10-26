// Package pgtest gives tests a PostgreSQL database they may wipe: the one in
// RVAV_TEST_DATABASE_URL, or a throwaway PostgreSQL 16 started with
// embedded-postgres. Its binaries are downloaded once into the user cache
// directory, so later runs need no network.
package pgtest

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// Start returns the URL of a database for tests, and a function that stops
// the server it started, if any. Call it once from TestMain.
func Start() (url string, stop func() error, err error) {
	if url := os.Getenv("RVAV_TEST_DATABASE_URL"); url != "" {
		return url, func() error { return nil }, nil
	}
	port, err := freePort()
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "rvav-pgtest-")
	if err != nil {
		return "", nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", nil, err
	}
	var log io.Writer = io.Discard
	if os.Getenv("RVAV_TEST_VERBOSE") != "" {
		log = os.Stderr
	}
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(port).
		Username("rvav").
		Password("rvav").
		Database("rvav_test").
		RuntimePath(filepath.Join(dir, "run")).
		DataPath(filepath.Join(dir, "data")).
		CachePath(filepath.Join(cache, "rvav", "embedded-postgres")).
		StartTimeout(time.Minute).
		StartParameters(map[string]string{
			// TCP only: some sandboxes forbid Unix sockets, and tests don't use them.
			"unix_socket_directories": "",
			// Durability is not needed for a database that is thrown away.
			"fsync": "off", "synchronous_commit": "off", "full_page_writes": "off",
		}).
		Logger(log))
	if err := pg.Start(); err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("start embedded PostgreSQL: %w", err)
	}
	stop = func() error {
		err := pg.Stop()
		os.RemoveAll(dir)
		return err
	}
	return fmt.Sprintf("postgres://rvav:rvav@127.0.0.1:%d/rvav_test?sslmode=disable", port), stop, nil
}

func freePort() (uint32, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}

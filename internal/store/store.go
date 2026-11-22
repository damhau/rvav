// Package store is the PostgreSQL data access layer. Functions take a
// context and return plain Go structs; SQL stays in this package.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/damhau/rvav/internal/fips"
	"github.com/damhau/rvav/migrations"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrTaken is an external object already recorded for another deployment.
var ErrTaken = errors.New("already recorded for another deployment")

// DB is the subset of pgx used by repository functions, satisfied by both
// *pgxpool.Pool and pgx.Tx so functions work inside or outside a transaction.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

type Store struct {
	Pool *pgxpool.Pool
	// held are WithLock's keys this process holds or waits for, each a
	// channel of one.
	held sync.Map
}

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	refuseMD5(&cfg.ConnConfig.Config)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", explainMD5(err))
	}
	return &Store{Pool: pool}, nil
}

// refuseMD5 turns pgx's MD5 password authentication, which panics in FIPS
// 140-only mode, into a connection error (ADR-0060). SCRAM-SHA-256,
// PostgreSQL's default since version 14, uses PBKDF2 and HMAC-SHA-256.
func refuseMD5(cfg *pgconn.Config) {
	if fips.Current() == fips.Only && cfg.RequireAuth == "" {
		cfg.RequireAuth = "!md5"
	}
}

func explainMD5(err error) error {
	if err != nil && fips.Current() == fips.Only && strings.Contains(err.Error(), "requested md5 authentication") {
		return fmt.Errorf("%w: FIPS 140-only mode refuses MD5; keep rvav's password with SCRAM-SHA-256 "+
			"(password_encryption = scram-sha-256, then set it again) and give its role scram-sha-256 in pg_hba.conf", err)
	}
	return err
}

func (s *Store) Close() { s.Pool.Close() }

// Tx runs fn in a transaction and commits when fn returns nil.
func (s *Store) Tx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.Pool, fn)
}

// Migrate applies all pending goose migrations.
func Migrate(ctx context.Context, url string) error {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return err
	}
	refuseMD5(&cfg.Config)
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	return explainMD5(migrateDB(ctx, db))
}

// migrateDB applies the migrations, one process at a time: every rvav pod
// migrates as it starts (S14.2), and the others wait, then find nothing
// left to do.
func migrateDB(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `select pg_advisory_lock($1)`, LockMigrate); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `select pg_advisory_unlock($1)`, LockMigrate)
	}()
	return goose.UpContext(ctx, db, ".")
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// IsUniqueViolation reports whether err is a unique constraint violation.
func IsUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// IsForeignKeyViolation reports a row other rows still reference.
func IsForeignKeyViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503"
}

// Advisory lock keys.
const (
	LockAWXSync int64 = 0x7276_6176_0001
	// LockAdmins serializes changes that check who is left to administer
	// rvav or a project, so two at once can't leave none.
	LockAdmins int64 = 0x7276_6176_0002
	// LockSubnetSync keeps one EfficientIP subnet sync at a time (S14.1).
	LockSubnetSync int64 = 0x7276_6176_0003
	// LockMigrate keeps one migration at a time (S14.2).
	LockMigrate int64 = 0x7276_6176_0004
	// LockNSXSync keeps one NSX tag sync at a time (ADR-0028).
	LockNSXSync int64 = 0x7276_6176_0005
	// LockPlacement keeps one at a time the placements that found the room
	// they counted on taken by others placed at the same time (ADR-0036).
	LockPlacement int64 = 0x7276_6176_0007
)

// LockConn is a connection of its own for a session advisory lock, outside
// the pool; closing it releases what it holds. Were locks held on pooled
// connections, as many holders as the pool has connections would leave
// their own queries none (#195).
func (s *Store) LockConn(ctx context.Context) (*pgx.Conn, error) {
	return pgx.ConnectConfig(ctx, s.Pool.Config().ConnConfig)
}

// WithLock runs fn holding a PostgreSQL advisory lock, waiting for whoever
// holds it (another process included). The lock is on its own connection
// (LockConn), and fn's queries use the pool.
func (s *Store) WithLock(ctx context.Context, key int64, fn func() error) error {
	// Callers in this process wait their turn without a connection, so that
	// a process opens one connection at most for each key (O1.2).
	turn, _ := s.held.LoadOrStore(key, make(chan struct{}, 1))
	select {
	case turn.(chan struct{}) <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-turn.(chan struct{}) }()
	conn, err := s.LockConn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, `select pg_advisory_lock($1)`, key); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `select pg_advisory_unlock($1)`, key) }()
	return fn()
}

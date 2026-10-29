// Package store is the PostgreSQL data access layer. Functions take a
// context and return plain Go structs; SQL stays in this package.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

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
}

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return &Store{Pool: pool}, nil
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
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	return migrateDB(ctx, db)
}

func migrateDB(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
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

// Advisory lock keys.
const (
	LockAWXSync int64 = 0x7276_6176_0001
	// LockAdmins serializes changes that check who is left to administer
	// rvav or a project, so two at once can't leave none.
	LockAdmins int64 = 0x7276_6176_0002
	// LockSubnetSync keeps one EfficientIP subnet sync at a time (S14.1).
	LockSubnetSync int64 = 0x7276_6176_0003
)

// WithLock runs fn holding a PostgreSQL advisory lock, waiting for whoever
// holds it (another process included).
func (s *Store) WithLock(ctx context.Context, key int64, fn func() error) error {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `select pg_advisory_lock($1)`, key); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `select pg_advisory_unlock($1)`, key) }()
	return fn()
}

package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// rvav's processes, as each records itself (ADR-0030).

type Process struct {
	ID        string    `db:"id"`
	Role      string    `db:"role"`
	Host      string    `db:"host"`
	Version   string    `db:"version"`
	Revision  int64     `db:"revision"`
	State     string    `db:"state"`
	StartedAt time.Time `db:"started_at"`
	SeenAt    time.Time `db:"seen_at"`
}

// SeeProcess records a process as running now, in its state, and forgets
// those not seen since before.
func (s *Store) SeeProcess(ctx context.Context, p Process, before time.Time) error {
	_, err := s.Pool.Exec(ctx, `with gone as (delete from processes where seen_at < $7 and id <> $1)
		insert into processes (id, role, host, version, revision, state) values ($1, $2, $3, $4, $5, $6)
		on conflict (id) do update set revision = excluded.revision, state = excluded.state, seen_at = now()`,
		p.ID, p.Role, p.Host, p.Version, p.Revision, p.State, before)
	return err
}

// ProcessGone forgets a process that exits.
func (s *Store) ProcessGone(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `delete from processes where id = $1`, id)
	return err
}

// Processes are those seen since then, by role and host.
func (s *Store) Processes(ctx context.Context, since time.Time) ([]Process, error) {
	rows, _ := s.Pool.Query(ctx, `select id, role, host, version, revision, state, started_at, seen_at from processes
		where seen_at >= $1 order by role, host, started_at`, since)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Process])
}

// ConnectionRevision is the latest revision of the settings saved for
// other kinds than those read on next use: what a process applies only
// when it starts.
func (s *Store) ConnectionRevision(ctx context.Context, live []string) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx, `select coalesce(max(revision), 0) from integration_settings where kind <> all($1)`, live).Scan(&n)
	return n, err
}

// ClaimDrain marks a process draining unless another of its role is: those
// drain one at a time. False when another is.
func (s *Store) ClaimDrain(ctx context.Context, id, role string, since time.Time) (bool, error) {
	var ok bool
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('rvav.drain.' || $1))`, role); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `update processes set state = 'draining', seen_at = now() where id = $1
			and not exists (select 1 from processes where role = $2 and state = 'draining' and id <> $1 and seen_at >= $3)`,
			id, role, since)
		ok = tag.RowsAffected() == 1
		return err
	})
	return ok, err
}

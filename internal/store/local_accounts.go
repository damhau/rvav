package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// LocalAccount is the local administrator's password and sign-in state
// (ADR-0030).
type LocalAccount struct {
	UserID         string     `db:"user_id"`
	Username       string     `db:"username"`
	PasswordHash   string     `db:"password_hash"`
	MustChange     bool       `db:"must_change"`
	FailedAttempts int        `db:"failed_attempts"`
	LockedUntil    *time.Time `db:"locked_until"`
}

const localCols = `user_id, username, password_hash, must_change, failed_attempts, locked_until`

// CreateLocalAccount makes a local account and its user with the given
// global roles, unless the account exists: created is false then, and
// nothing changes.
func (s *Store) CreateLocalAccount(ctx context.Context, userID, subject, name, username, hash string, roles []string) (created bool, err error) {
	err = s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into users (id, subject, name, global_roles) values ($1, $2, $3, $4)
			on conflict (id) do nothing`, userID, subject, name, roles); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `insert into local_accounts (user_id, username, password_hash) values ($1, $2, $3)
			on conflict do nothing`, userID, username, hash)
		created = tag.RowsAffected() == 1
		return err
	})
	return created, err
}

// LocalAccountByName finds a local account by its username, ignoring case.
func (s *Store) LocalAccountByName(ctx context.Context, username string) (LocalAccount, error) {
	rows, _ := s.Pool.Query(ctx, `select `+localCols+` from local_accounts where lower(username) = lower($1)`, username)
	a, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[LocalAccount])
	return a, notFound(err)
}

// LocalAccountOf is a user's local account.
func (s *Store) LocalAccountOf(ctx context.Context, userID string) (LocalAccount, error) {
	rows, _ := s.Pool.Query(ctx, `select `+localCols+` from local_accounts where user_id = $1`, userID)
	a, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[LocalAccount])
	return a, notFound(err)
}

// LocalSignInFailed counts a failure. From the third in a row, attempts
// wait until locked_until: 1 s, then twice as long each time, at most 15
// minutes.
func (s *Store) LocalSignInFailed(ctx context.Context, userID string, now time.Time) (LocalAccount, error) {
	rows, _ := s.Pool.Query(ctx, `update local_accounts set failed_attempts = failed_attempts + 1,
			locked_until = case when failed_attempts + 1 >= 3
				then $2::timestamptz + make_interval(secs => least(power(2, least(failed_attempts - 2, 10)), 900))
				else locked_until end
		where user_id = $1 returning `+localCols, userID, now)
	a, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[LocalAccount])
	return a, notFound(err)
}

// LocalSignInSucceeded starts the count of failures again.
func (s *Store) LocalSignInSucceeded(ctx context.Context, userID string) error {
	_, err := s.Pool.Exec(ctx, `update local_accounts set failed_attempts = 0, locked_until = null, last_sign_in_at = now()
		where user_id = $1`, userID)
	return err
}

// SetLocalPassword replaces the password; mustChange asks for another at
// the next sign-in (a reset).
func (s *Store) SetLocalPassword(ctx context.Context, userID, hash string, mustChange bool) error {
	tag, err := s.Pool.Exec(ctx, `update local_accounts set password_hash = $2, must_change = $3, password_changed_at = now(),
		failed_attempts = 0, locked_until = null where user_id = $1`, userID, hash, mustChange)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// CreateSession records a local sign-in by its ID's hash, and forgets the
// user's sessions that have ended.
func (s *Store) CreateSession(ctx context.Context, idHash, userID string, now, expires time.Time, idle time.Duration) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `delete from sessions where user_id = $1 and (expires_at <= $2 or last_seen_at <= $3)`,
			userID, now, now.Add(-idle)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into sessions (id_hash, user_id, created_at, last_seen_at, expires_at)
			values ($1, $2, $3, $3, $4)`, idHash, userID, now, expires)
		return err
	})
}

// Session is the user of a session that hasn't ended: used within idle of
// its last request and before it expires. Its last request is noted, at
// most once a minute.
func (s *Store) Session(ctx context.Context, idHash string, now time.Time, idle time.Duration) (string, error) {
	var userID string
	var seen, expires time.Time
	err := s.Pool.QueryRow(ctx, `select user_id, last_seen_at, expires_at from sessions where id_hash = $1`, idHash).
		Scan(&userID, &seen, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !now.Before(expires) || !now.Before(seen.Add(idle)) {
		_, err := s.Pool.Exec(ctx, `delete from sessions where id_hash = $1`, idHash)
		if err != nil {
			return "", err
		}
		return "", ErrNotFound
	}
	if now.Sub(seen) >= time.Minute {
		if _, err := s.Pool.Exec(ctx, `update sessions set last_seen_at = $2 where id_hash = $1`, idHash, now); err != nil {
			return "", err
		}
	}
	return userID, nil
}

// DeleteSession ends a session.
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.Pool.Exec(ctx, `delete from sessions where id_hash = $1`, idHash)
	return err
}

// DeleteSessions ends a user's sessions but the one given ("": all).
func (s *Store) DeleteSessions(ctx context.Context, userID, except string) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `delete from sessions where user_id = $1 and id_hash <> $2`, userID, except)
	return tag.RowsAffected(), err
}

package store

import "context"

// SchemaVersion is the last migration applied (goose's table).
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	var v int64
	err := s.Pool.QueryRow(ctx, `select coalesce(max(version_id), 0) from goose_db_version where is_applied`).Scan(&v)
	return v, err
}

package e2e

import (
	"context"
	"sync"
	"testing"

	"github.com/damhau/rvav/internal/store"
)

// TestMigrateConcurrently: every rvav pod migrates as it starts (S14.2);
// started together on an empty database, one migrates and the others wait,
// then find nothing left to do.
func TestMigrateConcurrently(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Pool.Exec(ctx, `drop schema public cascade; create schema public`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = store.Migrate(ctx, databaseURL)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("migration %d: %v", i, err)
		}
	}
	var version int
	if err := st.Pool.QueryRow(ctx, `select max(version_id) from goose_db_version`).Scan(&version); err != nil || version < 30 {
		t.Fatalf("version %d, %v", version, err)
	}
}

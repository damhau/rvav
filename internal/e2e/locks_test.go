package e2e

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/app"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/seed"
	"github.com/damhau/rvav/internal/store"
)

// withPool is the database's URL with a pool of n connections.
func withPool(url string, n int) string {
	if strings.Contains(url, "?") {
		return fmt.Sprintf("%s&pool_max_conns=%d", url, n)
	}
	return fmt.Sprintf("%s?pool_max_conns=%d", url, n)
}

// TestLocksOutsideThePool: advisory locks are held on connections of their
// own (#195). On a pool of one connection, five jobs of one process hold a
// lock each at once and still run their queries; another process waits for
// a lock held, and takes it once it is released.
func TestLocksOutsideThePool(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	open := func() *store.Store {
		st, err := store.Open(ctx, withPool(databaseURL, 1))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(st.Close)
		return st
	}
	one, other := open(), open()
	query := func(st *store.Store) error {
		var n int
		return st.Pool.QueryRow(ctx, `select 1`).Scan(&n)
	}
	const key int64 = 0x7276_6176_7e00 // keys of this test only

	const jobs = 5
	var holding atomic.Int32
	all := make(chan struct{})
	errs := make(chan error, jobs)
	for i := range int64(jobs) {
		go func() {
			errs <- one.WithLock(ctx, key+i, func() error {
				if err := query(one); err != nil {
					return err
				}
				if holding.Add(1) == jobs {
					close(all)
				}
				select {
				case <-all:
				case <-ctx.Done():
					return fmt.Errorf("%d of %d jobs hold their lock: %w", holding.Load(), jobs, ctx.Err())
				}
				return query(one)
			})
		}()
	}
	for range jobs {
		if err := <-errs; err != nil {
			t.Fatalf("a job holding a lock: %v", err)
		}
	}

	unlock, ok, err := one.TryLock(ctx, key)
	if err != nil || !ok {
		t.Fatalf("TryLock = %v, %v", ok, err)
	}
	if err := query(one); err != nil {
		t.Fatalf("a query while TryLock holds: %v", err)
	}
	if _, ok, err := other.TryLock(ctx, key); err != nil || ok {
		t.Fatalf("the other process's TryLock of a lock held = %v, %v", ok, err)
	}
	var released atomic.Bool
	took := make(chan error, 1)
	go func() {
		took <- other.WithLock(ctx, key, func() error {
			if !released.Load() {
				return errors.New("taken while the first process held it")
			}
			return query(other)
		})
	}()
	select {
	case err := <-took:
		t.Fatalf("the other process didn't wait for the lock: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	released.Store(true)
	unlock()
	if err := <-took; err != nil {
		t.Fatalf("the other process, once the lock was released: %v", err)
	}
}

// TestSyncEveryIntegration: the sync process on a pool of four
// connections, pgx's default up to four CPUs, with vCenter (vcsim),
// EfficientIP, AWX, NSX and the catalog's repository all set. Each of its
// jobs holds an advisory lock while it runs, and their queries still get
// connections: each completes a pass (#195).
func TestSyncEveryIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		st, err := store.Open(ctx, databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if _, err := st.Pool.Exec(ctx, `drop schema public cascade; create schema public`); err != nil {
			t.Fatal(err)
		}
		if err := store.Migrate(ctx, databaseURL); err != nil {
			t.Fatal(err)
		}
		if err := seed.Run(ctx, st); err != nil {
			t.Fatal(err)
		}
		if err := vsphere.Bootstrap(ctx, c, vsphere.DefaultLayout()); err != nil {
			t.Fatal(err)
		}

		// A VM in a security group whose tag NSX no longer has: the tag
		// sync's pass marks it drifted.
		fakeNSX := nsx.NewFakeServer()
		nsxSrv := httptest.NewServer(fakeNSX)
		defer nsxSrv.Close()
		const uuid, membership = "5021b3a1-0195-4c00-9000-000000000195", "res_pool195"
		fakeNSX.AddVM(uuid, "web-dev-pool01")
		var itemID, versionID string
		if err := st.Pool.QueryRow(ctx, `select i.id, v.id from catalog_items i join catalog_item_versions v on v.item_id = i.id
			where i.slug = 'rhel9-server' order by v.created_at limit 1`).Scan(&itemID, &versionID); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertDeployment(ctx, st.Pool, store.Deployment{ID: "dep_pool195", Name: "pool195", Status: "ready",
			ProjectID: "prj_web", Environment: "dev", ItemID: itemID, VersionID: versionID, OwnerID: seed.UserID("lmartin"),
			ExpiryAction: "power_off_then_delete"}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertResource(ctx, st.Pool, store.Resource{ID: membership, DeploymentID: "dep_pool195",
			Type: "nsx.membership", Name: "web-dev-pool01", ExternalID: new("web-dev-pool01/web"),
			Properties: []byte(`{"instance_uuid": "` + uuid + `", "tags": [{"scope": "rvav-group", "tag": "web"}]}`)}); err != nil {
			t.Fatal(err)
		}

		vcsim := *c.URL()
		vcsim.User = nil
		for _, v := range append(config.Variables(), "RVAV_FAKE_AWX", "RVAV_FAKE_EFFICIENTIP", "RVAV_ALLOW_DEV_AUTH", "RVAV_SECRETS_OLD_KEYS") {
			t.Setenv(v, "")
		}
		for k, v := range map[string]string{
			"RVAV_DATABASE_URL": withPool(databaseURL, 4), "RVAV_METRICS_ADDR": "off",
			"RVAV_SECRETS_KEY": base64.StdEncoding.EncodeToString([]byte("e2e-only-secrets-key-32-bytes!!!")),
			"RVAV_VCENTER_URL": vcsim.String(), "RVAV_VCENTER_USERNAME": "user", "RVAV_VCENTER_PASSWORD": "pass", "RVAV_VCENTER_INSECURE": "1",
			"RVAV_FAKE_EFFICIENTIP": "1", "RVAV_FAKE_AWX": "1", "RVAV_AWX_CATALOG_LABELS": "rvav",
			"RVAV_NSX_URL": nsxSrv.URL, "RVAV_NSX_USERNAME": nsx.FakeUser, "RVAV_NSX_PASSWORD": nsx.FakePassword,
			"RVAV_GIT_URL": bareRepo(t), "RVAV_GIT_CACHE": t.TempDir(),
		} {
			t.Setenv(k, v)
		}
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		if os.Getenv("RVAV_TEST_VERBOSE") != "" {
			log = slog.New(slog.NewTextHandler(os.Stderr, nil))
		}
		a, err := app.Open(ctx, cfg, log)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		sctx, stop := context.WithCancel(ctx)
		exited := make(chan struct{})
		var runErr error
		go func() { runErr = a.Run(sctx, "sync", a.RunSync); close(exited) }()
		// Stopped before vcsim is: the inventory's watchers release their
		// collectors.
		defer func() {
			stop()
			<-exited
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				t.Errorf("the sync: %v", runErr)
			}
		}()

		waiting := map[string]string{
			"the inventory sync":            `select count(*) > 0 from inv_clusters`,
			"the EfficientIP subnet sync":   `select count(*) > 0 from network_subnets where efficientip_subnet_id is not null`,
			"the AWX catalog sync":          `select count(*) > 0 from awx_templates`,
			"the NSX tag sync":              `select count(*) > 0 from resources where id = '` + membership + `' and sync_state = 'drifted'`,
			"the catalog's repository sync": `select count(*) > 0 from catalog_git_repo where read_at is not null and error is null`,
		}
		for deadline := time.Now().Add(30 * time.Second); len(waiting) > 0; time.Sleep(100 * time.Millisecond) {
			for job, check := range waiting {
				var passed bool
				if err := st.Pool.QueryRow(ctx, check).Scan(&passed); err != nil {
					t.Fatal(err)
				}
				if passed {
					delete(waiting, job)
				}
			}
			select {
			case <-exited:
				t.Fatalf("the sync exited: %v", runErr)
			default:
			}
			if time.Now().After(deadline) {
				var gitErr *string
				_ = st.Pool.QueryRow(ctx, `select error from catalog_git_repo`).Scan(&gitErr)
				t.Fatalf("no pass within 30s: %s (the repository's error: %q)", strings.Join(slices.Sorted(maps.Keys(waiting)), ", "),
					deref(gitErr))
			}
		}
	})
}

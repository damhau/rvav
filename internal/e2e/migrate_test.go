package e2e

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/migrations"
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

// TestMigrateRoles upgrades a database of today's schema, members and group
// mappings in it, to custom roles (ADR-0034), as the permanent instance
// migrates: everyone holds what they held. Down goes back to one role per
// user and project, the strongest, and up again.
func TestMigrateRoles(t *testing.T) {
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
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	const before = 53
	if err := goose.UpToContext(ctx, db, ".", before); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%v\n%s", err, sql)
		}
	}
	exec(`insert into users (id, subject, name, global_roles) values ('usr_a', 'a', 'A', '{user}'), ('usr_b', 'b', 'B', '{user}'),
		('usr_c', 'c', 'C', '{user,admin}');
		insert into projects (id, code, name) values ('prj_a', 'a1', 'A'), ('prj_b', 'b1', 'B');
		insert into project_members (project_id, user_id, role) values ('prj_a', 'usr_a', 'project-admin'),
			('prj_a', 'usr_b', 'member'), ('prj_b', 'usr_b', 'viewer');
		insert into group_mappings (id, oidc_group, project_id, role) values ('gmp_1', 'admins', null, 'admin'),
			('gmp_2', 'auditors', null, 'auditor'), ('gmp_3', 'b-viewers', 'prj_b', 'viewer'), ('gmp_4', 'odd', null, 'nonsense');
		insert into user_groups (user_id, group_name) values ('usr_a', 'auditors')`)
	if err := store.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	a, err := auth.ForUser(ctx, st, "usr_a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := auth.ForUser(ctx, st, "usr_b")
	if err != nil {
		t.Fatal(err)
	}
	if !a.In(auth.ProjectMembersManage, "prj_a") || !a.Everywhere(auth.AuditRead) || a.Everywhere(auth.AccessManage) ||
		!b.In(auth.RequestSubmit, "prj_a") || b.In(auth.RequestSubmit, "prj_b") || !b.In(auth.ProjectSee, "prj_b") ||
		!slices.Equal(a.Roles, []string{"user", "auditor"}) {
		t.Fatalf("after the upgrade: A %v %v, B %v", a.Roles, a.RolesIn("prj_a"), b.RolesIn("prj_b"))
	}
	var mappings int
	if err := st.Pool.QueryRow(ctx, `select count(*) from group_mappings`).Scan(&mappings); err != nil || mappings != 3 {
		t.Fatalf("group mappings: %d, %v; the one to no role goes", mappings, err)
	}
	// Several roles per user and project, and a custom role, then down.
	exec(`insert into roles (key, name, permissions) values ('vm-operator', 'VM operator', '{project.see,day2.power}');
		insert into project_members (project_id, user_id, role) values ('prj_a', 'usr_b', 'viewer'), ('prj_a', 'usr_b', 'vm-operator'),
			('prj_b', 'usr_a', 'vm-operator');
		insert into group_mappings (id, oidc_group, project_id, role) values ('gmp_5', 'ops', 'prj_a', 'vm-operator');
		insert into user_roles (id, user_id, role) values ('ras_1', 'usr_b', 'vm-operator'), ('ras_2', 'usr_b', 'auditor'),
			('ras_3', 'usr_c', 'admin')`)
	if b, err = auth.ForUser(ctx, st, "usr_b"); err != nil || !b.RunsDay2("power", "reset", "prj_b") ||
		!slices.Equal(b.RolesIn("prj_a"), []string{"member", "viewer", "vm-operator"}) {
		t.Fatalf("several roles and a custom one: %v, %v", b.RolesIn("prj_a"), err)
	}
	if err := goose.DownToContext(ctx, db, ".", before); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.Pool.Query(ctx, `select project_id || ' ' || user_id || ' ' || role from project_members order by 1`)
	members, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || !slices.Equal(members, []string{"prj_a usr_a project-admin", "prj_a usr_b member", "prj_b usr_b viewer"}) {
		t.Fatalf("members after down: %v, %v", members, err)
	}
	rows, _ = st.Pool.Query(ctx, `select id from group_mappings order by id`)
	if ids, err := pgx.CollectRows(rows, pgx.RowTo[string]); err != nil || !slices.Equal(ids, []string{"gmp_1", "gmp_2", "gmp_3"}) {
		t.Fatalf("mappings after down: %v, %v", ids, err)
	}
	// A built-in role given for all of rvav is kept, once.
	rows, _ = st.Pool.Query(ctx, `select id || ' ' || array_to_string(global_roles, ',') from users where id in ('usr_a', 'usr_b', 'usr_c') order by id`)
	if users, err := pgx.CollectRows(rows, pgx.RowTo[string]); err != nil ||
		!slices.Equal(users, []string{"usr_a user", "usr_b user,auditor", "usr_c user,admin"}) {
		t.Fatalf("global roles after down: %v, %v", users, err)
	}
	if err := store.Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// TestMigrateSupervisor: the built-in supervisor (ADR-0041) takes its key
// from a custom role made before, whose holders and day-2 policies keep it
// as supervisor-custom. Down takes the built-in one away, and up again.
func TestMigrateSupervisor(t *testing.T) {
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
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	const before = 70
	if err := goose.UpToContext(ctx, db, ".", before); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `insert into users (id, subject, name) values ('usr_a', 'a', 'A');
		insert into projects (id, code, name) values ('prj_a', 'a1', 'A');
		insert into roles (key, name, permissions) values ('supervisor', 'Our supervisor', '{project.see,day2.power}');
		insert into project_members (project_id, user_id, role) values ('prj_a', 'usr_a', 'supervisor');
		insert into group_mappings (id, oidc_group, project_id, role) values ('gmp_1', 'leads', 'prj_a', 'supervisor');
		insert into day2_policies (id, name, roles, effect) values ('d2p_1', 'Supervisors', '{member,supervisor}', 'allow')`); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	var custom, member, mapping, policy string
	var builtIn bool
	if err := st.Pool.QueryRow(ctx, `select (select name || ' ' || array_to_string(permissions, ',') from roles where key = 'supervisor-custom'),
		(select role from project_members where user_id = 'usr_a'), (select role from group_mappings where id = 'gmp_1'),
		(select array_to_string(roles, ',') from day2_policies where id = 'd2p_1'),
		(select built_in from roles where key = 'supervisor')`).Scan(&custom, &member, &mapping, &policy, &builtIn); err != nil {
		t.Fatal(err)
	}
	if custom != "Our supervisor project.see,day2.power" || member != "supervisor-custom" || mapping != "supervisor-custom" ||
		policy != "member,supervisor-custom" || !builtIn {
		t.Fatalf("after the upgrade: %q, %s, %s, %s, built in %v", custom, member, mapping, policy, builtIn)
	}
	if _, err := st.Pool.Exec(ctx, `insert into project_members (project_id, user_id, role) values ('prj_a', 'usr_a', 'supervisor')`); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownToContext(ctx, db, ".", before); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.Pool.Query(ctx, `select role from project_members order by role`)
	if roles, err := pgx.CollectRows(rows, pgx.RowTo[string]); err != nil || !slices.Equal(roles, []string{"supervisor-custom"}) {
		t.Fatalf("members after down: %v, %v", roles, err)
	}
	if err := store.Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// TestMigrateUpdateRequests: rolling back past updates (00074) is refused
// while update requests exist, which rvav before them can't read; it goes
// down once there are none, and up again.
func TestMigrateUpdateRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		cfg, err := pgx.ParseConfig(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		db := stdlib.OpenDB(*cfg)
		defer db.Close()
		goose.SetBaseFS(migrations.FS)
		if err := goose.SetDialect("postgres"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.Pool.Exec(ctx, `insert into requests (id, number, kind, item_id, version_id, project_id, environment,
			title, status, requester_id, owner_id, inputs)
			select 'req_u1', 'REQ-U1', 'update', i.id, v.id, 'prj_web', 'dev', 'Update', 'succeeded', 'usr_tnguyen', 'usr_tnguyen', '{}'
			from catalog_items i join catalog_item_versions v on v.item_id = i.id where i.slug = 'rhel9-server' limit 1`); err != nil {
			t.Fatal(err)
		}
		const before = 73
		if err := goose.DownToContext(ctx, db, ".", before); err == nil || !strings.Contains(err.Error(), "requests of kind update exist") {
			t.Fatalf("down with an update request: %v", err)
		}
		var kind string
		if err := h.store.Pool.QueryRow(ctx, `select kind from requests where id = 'req_u1'`).Scan(&kind); err != nil || kind != "update" {
			t.Fatalf("the update request after the refusal: %q, %v", kind, err)
		}
		if _, err := h.store.Pool.Exec(ctx, `delete from requests where id = 'req_u1'`); err != nil {
			t.Fatal(err)
		}
		if err := goose.DownToContext(ctx, db, ".", before); err != nil {
			t.Fatalf("down without one: %v", err)
		}
		if err := store.Migrate(ctx, databaseURL); err != nil {
			t.Fatalf("up again: %v", err)
		}
	})
}

// TestMigrateItemSharing: an item shared with a project before G1.2 is
// shared with all its members after, and one shared with every project
// with everyone in each (ADR-0019, Sharing). Down and up again.
func TestMigrateItemSharing(t *testing.T) {
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
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	const before = 75
	if err := goose.UpToContext(ctx, db, ".", before); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `insert into projects (id, code, name) values ('prj_a', 'a1', 'A'), ('prj_b', 'b1', 'B');
		insert into catalog_categories (key, name) values ('compute', 'Compute') on conflict do nothing;
		insert into catalog_items (id, slug, name, summary, category, type) values ('itm_a', 'a', 'A', '', 'compute', 'vm_blueprint');
		insert into catalog_items (id, slug, name, summary, category, type, all_projects)
			values ('itm_all', 'all', 'All', '', 'compute', 'vm_blueprint', true);
		insert into catalog_entitlements (item_id, project_id) values ('itm_a', 'prj_a')`); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	anyone := store.Requester{UserID: "usr_x", Groups: []string{"g"}, Roles: []string{"member"}}
	for _, c := range []struct {
		item, project   string
		inProject, with bool
	}{{"itm_a", "prj_a", true, true}, {"itm_a", "prj_b", false, false}, {"itm_all", "prj_b", true, true}} {
		in, with, err := st.SharedWith(ctx, c.item, c.project, anyone)
		if err != nil || in != c.inProject || with != c.with {
			t.Fatalf("%s in %s: project %v, requester %v, %v", c.item, c.project, in, with, err)
		}
	}
	sh, err := st.ItemSharing(ctx, "itm_a")
	if err != nil || sh.AllProjects || len(sh.Projects) != 1 || !sh.Projects[0].Everyone() || !sh.Everywhere.Everyone() {
		t.Fatalf("itm_a's sharing = %+v, %v", sh, err)
	}
	if err := goose.DownToContext(ctx, db, ".", before); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

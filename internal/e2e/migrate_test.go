package e2e

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

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
		insert into user_roles (id, user_id, role) values ('ras_1', 'usr_b', 'vm-operator')`)
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
	if err := store.Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

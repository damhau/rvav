package store

import "context"

// SetupState is what the setup checklist is read from (ADR-0030).
type SetupState struct {
	Environments, Projects  int
	Items, PublishedItems   int
	AdminGroups             []string
	PasswordChanged, Seeded bool
	Hidden                  bool
}

// SetupState counts what the checklist's steps look for. Seeded: the
// development seed's users are there.
func (s *Store) SetupState(ctx context.Context, localAdmin string) (SetupState, error) {
	var st SetupState
	err := s.Pool.QueryRow(ctx, `select
		(select count(*) from environments),
		(select count(*) from projects where archived_at is null),
		(select count(*) from catalog_items),
		(select count(*) from catalog_items where published_version_id is not null),
		(select coalesce(array_agg(distinct oidc_group order by oidc_group), '{}') from group_mappings
			where project_id is null and role = 'admin'),
		exists (select 1 from local_accounts where username = $1 and not must_change),
		exists (select 1 from users where subject like 'seed:%'),
		coalesce((select hidden from setup_checklist), false)`, localAdmin).
		Scan(&st.Environments, &st.Projects, &st.Items, &st.PublishedItems, &st.AdminGroups,
			&st.PasswordChanged, &st.Seeded, &st.Hidden)
	return st, err
}

// SetSetupHidden hides the checklist, or has it open at sign-in again.
func (s *Store) SetSetupHidden(ctx context.Context, db DB, hidden bool, by string) error {
	_, err := db.Exec(ctx, `insert into setup_checklist (hidden, updated_by) values ($1, $2)
		on conflict (id) do update set hidden = excluded.hidden, updated_at = now(), updated_by = excluded.updated_by`,
		hidden, by)
	return err
}

-- +goose Up
-- The identity provider is set in Administration (docs/adr/0030-settings-and-local-administrator.md):
-- an integration of its own, and the origins of providers being tried, which
-- the sign-in page may reach for ten minutes so that a trial sign-in works
-- before the settings are saved.
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('oidc', 'vcenter', 'awx', 'efficientip', 'nsx', 'git', 'smtp', 'teams', 'slack'));

create table oidc_trial_origins (
    origin     text primary key,
    expires_at timestamptz not null
);

-- +goose Down
drop table oidc_trial_origins;
delete from integration_settings where kind = 'oidc';
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('vcenter', 'awx', 'efficientip', 'nsx', 'git', 'smtp', 'teams', 'slack'));

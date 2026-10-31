-- +goose Up
-- More settings are set in Administration (docs/adr/0030-settings-and-local-administrator.md):
-- the console's account and WebMKS SDK, the vRA the importer reads, and the
-- hosts webhooks and REST value sources may call.
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('oidc', 'vcenter', 'console', 'awx', 'efficientip', 'nsx', 'git', 'vra', 'smtp', 'teams', 'slack',
                    'hosts'));

-- +goose Down
delete from integration_settings where kind in ('console', 'vra', 'hosts');
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('oidc', 'vcenter', 'awx', 'efficientip', 'nsx', 'git', 'smtp', 'teams', 'slack'));

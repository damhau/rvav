-- +goose Up
-- Active Directory (docs/adr/0064-active-directory-computer-objects.md): the
-- directory computer objects are made in, an integration of its own.
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('oidc', 'vcenter', 'console', 'awx', 'efficientip', 'nsx', 'git', 'vra', 'ad', 'smtp', 'teams',
                    'slack', 'hosts', 'trust'));

-- +goose Down
delete from integration_settings where kind = 'ad';
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('oidc', 'vcenter', 'console', 'awx', 'efficientip', 'nsx', 'git', 'vra', 'smtp', 'teams', 'slack',
                    'hosts', 'trust'));
